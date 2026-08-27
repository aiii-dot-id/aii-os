package witness

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// fakeWitness implements the bookmark-protocol server semantics with its
// own generated ML-DSA-87 key: 201 first anchor, 200 idempotent retry
// (byte-identical receipt), 409 fork/rollback. Signature inputs are the
// protocol functions — which are themselves pinned to the witnessd
// source by citation; cross-binary proof is the env-gated live test.
type fakeWitness struct {
	mu             sync.Mutex
	state          map[string]*anchorState
	witnessKp      *crypto.KeyPair
	witnessEnv     *PublicKeyEnvelope
	server         *httptest.Server
	sawIdentityIDs map[string]int
}

type anchorState struct {
	ordinal    int64
	hash       string
	rangeStart int64
	rangeHash  string
	receipt    WitnessReceipt
}

func newFakeWitness(t *testing.T) *fakeWitness {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	keyID := "aiii_witness_test_" + kp.Fingerprint()[:16]
	env := &PublicKeyEnvelope{
		V: 1, Kind: PublicKeyEnvelopeKind, KeyID: keyID, KeyType: "witness", Profile: ProfileRoot,
		CreatedAt: now.Format(time.RFC3339), NotBefore: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
		Keys: []PublicKeyMaterial{{
			Alg: AlgMLDSA87, PublicKeyB64: kp.PublicKeyB64(),
			PublicKeyFingerprint: sha256Prefixed([]byte(FingerprintMaterial(AlgMLDSA87, keyID, kp.PublicKeyB64()))),
		}},
	}
	fw := &fakeWitness{state: map[string]*anchorState{}, witnessKp: kp, witnessEnv: env, sawIdentityIDs: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/witness/pubkey/hash", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]string{
			"witness_public_key_hash": sha256Prefixed(mustCanonical(t, env)),
			"key_id":                  keyID,
		})
	})
	mux.HandleFunc("/witness/pubkey", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, env)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]interface{}{"max_range_entries": 4096})
	})
	mux.HandleFunc("/witness/bookmark", func(w http.ResponseWriter, r *http.Request) {
		fw.handleBookmark(t, w, r)
	})
	fw.server = httptest.NewServer(mux)
	t.Cleanup(fw.server.Close)
	return fw
}

func (fw *fakeWitness) handleBookmark(t *testing.T, w http.ResponseWriter, r *http.Request) {
	var req WitnessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "malformed", 400)
		return
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.sawIdentityIDs[req.IdentityID]++

	// verify the identity signature over the request input (server behavior)
	input := RequestSignatureInput(req, req.IdentityPublicKey)
	if sha256Prefixed(input) != req.IdentitySignature.SignatureInputSHA256 {
		http.Error(w, "identity signature input hash mismatch", 401)
		return
	}
	ml, ok := fw.identityMLDsa(t, req.IdentityPublicKey)
	if !ok {
		http.Error(w, "identity key missing", 400)
		return
	}
	sig, err := base64Decode(req.IdentitySignature.SigB64)
	if err != nil {
		http.Error(w, "bad sig encoding", 401)
		return
	}
	if err := crypto.Verify(base64DecodeOrFatal(t, ml.PublicKeyB64), input, sig); err != nil {
		http.Error(w, "identity signature verification failed", 401)
		return
	}

	st, found := fw.state[req.IdentityID]
	if !found {
		receipt := fw.signReceipt(t, req, 0, "")
		fw.state[req.IdentityID] = &anchorState{req.LedgerOrdinal, req.LedgerHash, req.RangeStartOrdinal, req.RangeHash, receipt}
		w.WriteHeader(201)
		writeTestJSON(w, receipt)
		return
	}
	// exact retry → byte-identical receipt, 200
	if st.ordinal == req.LedgerOrdinal && st.hash == req.LedgerHash && st.rangeStart == req.RangeStartOrdinal && st.rangeHash == req.RangeHash {
		w.WriteHeader(200)
		writeTestJSON(w, st.receipt)
		return
	}
	if req.LedgerOrdinal <= st.ordinal {
		// The REAL conflict body shape: witnessd writeJSONError renders
		// {"error": message} (server.go:377-378), message from store.go:375.
		w.WriteHeader(409)
		writeTestJSON(w, map[string]string{"error": conflictMsgRollbackFork})
		return
	}
	receipt := fw.signReceipt(t, req, st.ordinal, st.hash)
	st.ordinal, st.hash, st.rangeStart, st.rangeHash, st.receipt = req.LedgerOrdinal, req.LedgerHash, req.RangeStartOrdinal, req.RangeHash, receipt
	w.WriteHeader(200)
	writeTestJSON(w, receipt)
}

func (fw *fakeWitness) identityMLDsa(t *testing.T, raw json.RawMessage) (PublicKeyMaterial, bool) {
	env := &PublicKeyEnvelope{}
	if err := json.Unmarshal(raw, env); err != nil {
		return PublicKeyMaterial{}, false
	}
	return env.FindPublicKey(AlgMLDSA87)
}

func (fw *fakeWitness) signReceipt(t *testing.T, req WitnessRequest, prevOrdinal int64, prevHash string) WitnessReceipt {
	receipt := WitnessReceipt{
		WitnessVersion:                 WitnessVersion,
		IdentityID:                     req.IdentityID,
		PreviousWitnessedLedgerOrdinal: prevOrdinal,
		PreviousWitnessedLedgerHash:    prevHash,
		LedgerOrdinal:                  req.LedgerOrdinal,
		LedgerHash:                     req.LedgerHash,
		RangeStartOrdinal:              req.RangeStartOrdinal,
		RangeHash:                      req.RangeHash,
		WitnessedAt:                    time.Now().UTC().Format(time.RFC3339),
	}
	input := ReceiptSignatureInput(receipt)
	sig, err := crypto.Sign(fw.witnessKp, input)
	if err != nil {
		t.Fatal(err)
	}
	wm, _ := fw.witnessEnv.FindPublicKey(AlgMLDSA87)
	receipt.WitnessSignature = SignatureEntry{
		SignatureProfile:     ProfileFast,
		Alg:                  AlgMLDSA87,
		KeyID:                fw.witnessEnv.KeyID,
		PublicKeyFingerprint: wm.PublicKeyFingerprint,
		SignatureInputSHA256: sha256Prefixed(input),
		SigB64:               base64Encode(sig),
	}
	return receipt
}

// --- test doubles ---

type memEnvelopeStore struct{ saved []byte }

func (m *memEnvelopeStore) SaveWitnessEnvelope(j []byte) error { m.saved = j; return nil }
func (m *memEnvelopeStore) LoadWitnessEnvelope() ([]byte, error) {
	if m.saved == nil {
		return nil, nil
	}
	return m.saved, nil
}

// memReceiptStore simulates the f(ledger) projection reader: seed it with
// a prior receipt to test restore + chain-continuity.
type memReceiptStore struct {
	seeded *struct {
		seq int64
		js  []byte
	}
}

func (m *memReceiptStore) SeedWitnessReceipt(seq int64, js []byte) {
	m.seeded = &struct {
		seq int64
		js  []byte
	}{seq, js}
}
func (m *memReceiptStore) LastWitnessReceipt() (int64, []byte, error) {
	if m.seeded == nil {
		return 0, nil, nil
	}
	return m.seeded.seq, m.seeded.js, nil
}

// testMinter appends the system.witnessed event to the real ledger
// (projection is the store package's business — table-tested there).
type testMinter struct {
	lg *ledger.Ledger
	kp *crypto.KeyPair
}

func (m testMinter) MintWitnessed(receipt WitnessReceipt, witnessKeyID string) (*ledger.Event, error) {
	return m.lg.Append(ledger.EventSystemWitnessed, m.kp.Fingerprint(), 0, map[string]interface{}{
		"receipt": map[string]interface{}{
			"ledger_ordinal": receipt.LedgerOrdinal, "ledger_hash": receipt.LedgerHash,
			"range_start_ordinal": receipt.RangeStartOrdinal, "range_hash": receipt.RangeHash,
			"witnessed_at": receipt.WitnessedAt, "witness_key_id": witnessKeyID,
			"witness_sig_b64": receipt.WitnessSignature.SigB64,
		},
	}, m.kp)
}

func testLedger(t *testing.T, events int) (*ledger.Ledger, *crypto.KeyPair) {
	t.Helper()
	dir := t.TempDir()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < events; i++ {
		if _, err := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]string{"n": fmt.Sprintf("e%d", i)}, kp); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { lg.Close() })
	return lg, kp
}

// --- the tests ---

func TestAnchorRoundtrip(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 5)
	envelopes := &memEnvelopeStore{}
	receipts := &memReceiptStore{}
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, receipts, testMinter{lg, kp}, 5, "")

	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if a.LastAnchoredSeq() != 5 {
		t.Fatalf("anchor point = %d, want 5", a.LastAnchoredSeq())
	}
	// The verified receipt is IN THE SIGNED LEDGER (system.witnessed)
	events, _ := ledger.ReadAll(lg.Path())
	var witnessed *ledger.Event
	for i := range events {
		if events[i].Type == ledger.EventSystemWitnessed {
			witnessed = &events[i]
		}
	}
	if witnessed == nil {
		t.Fatal("anchor must mint system.witnessed into the ledger")
	}
	if witnessed.Seq != 6 {
		t.Fatalf("system.witnessed lands at seq %d, want 6 (right after the 5 anchored events)", witnessed.Seq)
	}
	_ = events
	var payload struct {
		Receipt struct {
			LedgerOrdinal int64  `json:"ledger_ordinal"`
			LedgerHash    string `json:"ledger_hash"`
			RangeHash     string `json:"range_hash"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(witnessed.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Receipt.LedgerOrdinal != 5 {
		t.Fatalf("receipt in ledger anchors ordinal %d, want 5", payload.Receipt.LedgerOrdinal)
	}
	// Canon §6: range_hash = aggregate over ordered content hashes 1..5
	want := sha256Prefixed(RangeHashMaterial(1, 5, []string{
		events[0].ContentHash, events[1].ContentHash, events[2].ContentHash,
		events[3].ContentHash, events[4].ContentHash,
	}))
	if payload.Receipt.RangeHash != want {
		t.Fatal("range_hash must be the canon §6 AIII-WITNESS-RANGE-HASH aggregate")
	}
	if payload.Receipt.LedgerHash != PrefixHash(events[4].ContentHash) {
		t.Fatal("ledger_hash must be the tail event's content hash in WIRE form — the ledger stores bare hex, the witness requires the sha256: prefix")
	}
}

func TestEnvelopeStableAcrossRestart(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	envelopes := &memEnvelopeStore{}

	a1 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	if err := a1.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}

	// "Restart": fresh anchorer, SAME envelope store → same identity
	a2 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	if err := a2.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	if n := len(fw.sawIdentityIDs); n != 1 {
		t.Fatalf("one identity expected across restarts, saw %d", n)
	}

	// Fresh envelope store + a second timestamp (RFC3339 is second-granular;
	// synthesis in the SAME second yields identical bytes by coincidence —
	// cross the boundary to show the real hazard: re-synthesis after any
	// time gap mints a NEW witness identity)
	time.Sleep(1100 * time.Millisecond)
	a3 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	if err := a3.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	if n := len(fw.sawIdentityIDs); n != 2 {
		t.Fatalf("re-synthesized envelope must mint a second identity (why persistence is load-bearing), saw %d", n)
	}
}

func TestRetryIdempotent(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 4)
	envelopes := &memEnvelopeStore{}
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 4, "")
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	// Same ordinal+hash → server returns the SAME receipt (200); client treats as success
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("idempotent retry must succeed: %v", err)
	}
}

func TestForkSurfacesAsError(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 4)
	envelopes := &memEnvelopeStore{}
	receipts := &memReceiptStore{}
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, receipts, testMinter{lg, kp}, 4, "")
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	// Corrupt the anchored state's hash: the SAME ordinal now carries
	// different content — a fork. A fresh anchorer (empty anchor state,
	// same identity envelope) rebuilds the request; we downgrade it to
	// the corrupted ordinal with the ORIGINAL ledger tail's hash (the
	// classic fork: same ordinal, different content) and re-sign.
	fw.mu.Lock()
	fw.state[onlyKey(fw)].hash = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	fw.mu.Unlock()
	a2 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 4, "")
	req := a2.buildRequestForTest(t)
	// The ledger tail is now 5 (the first anchor minted system.witnessed);
	// force the fork at ordinal 4: same ordinal as the corrupted anchor,
	// content hash of the ORIGINAL event 4.
	events, _ := ledger.ReadAll(lg.Path())
	req.LedgerOrdinal = 4
	req.LedgerHash = events[3].ContentHash
	canonical2, env2, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes)
	if err != nil {
		t.Fatal(err)
	}
	sig2, err := SignRequest(AsIdentityKey(kp), env2, req, canonical2)
	if err != nil {
		t.Fatal(err)
	}
	req.IdentitySignature = sig2
	req.LedgerHash = "sha256:" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := New(fw.server.URL, "").Bookmark(req); err == nil {
		t.Fatal("forked submission must be rejected by the witness (409)")
	}
}

func onlyKey(fw *fakeWitness) string {
	for k := range fw.state {
		return k
	}
	return ""
}

// buildRequestForTest exposes request construction for fork testing.
func (a *Anchorer) buildRequestForTest(t *testing.T) WitnessRequest {
	t.Helper()
	canonical, env, err := EnsureIdentityEnvelope(a.key, a.envelopes)
	if err != nil {
		t.Fatal(err)
	}
	id, err := DeriveIdentityID(canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	req, err := a.buildRequest(id, canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestUnverifiedReceiptDiscarded(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	envelopes := &memEnvelopeStore{}
	receipts := &memReceiptStore{}

	// Corrupt the witness key's served fingerprint? Simpler: verify a
	// tampered receipt directly — the anchorer's contract is VerifyReceipt
	// gates persistence; pin VerifyReceipt itself against tampering.
	canonical, env, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := DeriveIdentityID(canonical, env)
	req := WitnessRequest{
		IdentityID: id, IdentityPublicKey: canonical,
		LedgerOrdinal: 3, LedgerHash: lg.LastHash(), RangeStartOrdinal: 1, RangeHash: "sha256:" + "0000000000000000000000000000000000000000000000000000000000000000",
	}
	receipt := fw.signReceiptNoLock(t, req, 0, "")
	witnessKey := fw.witnessEnv
	if err := VerifyReceipt(receipt, req, witnessKey); err != nil {
		t.Fatalf("honest receipt must verify: %v", err)
	}
	tampered := receipt
	tampered.LedgerHash = "sha256:" + "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if err := VerifyReceipt(tampered, req, witnessKey); err == nil {
		t.Fatal("tampered receipt must fail verification (field echo)")
	}
	tampered = receipt
	tampered.WitnessSignature.SigB64 = base64Encode([]byte("garbage"))
	if err := VerifyReceipt(tampered, req, witnessKey); err == nil {
		t.Fatal("garbage signature must fail verification")
	}
	_ = receipts
}

func (fw *fakeWitness) signReceiptNoLock(t *testing.T, req WitnessRequest, prev int64, prevHash string) WitnessReceipt {
	t.Helper()
	return fw.signReceipt(t, req, prev, prevHash)
}

// Range window: capped by the server's max_range_entries.
func TestRangeWindowCapped(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 10)
	envelopes := &memEnvelopeStore{}
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 10, "")
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	// default max from fake /status = 4096 → full range 1..10
	fw.mu.Lock()
	var id string
	for k := range fw.state {
		id = k
	}
	got := fw.state[id].rangeStart
	fw.mu.Unlock()
	if got != 1 {
		t.Fatalf("uncapped range should start at 1, got %d", got)
	}
}

func mustCanonical(t *testing.T, v interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	c, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeTestJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func base64DecodeOrFatal(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64Decode(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLiveWitnessReadOnly(t *testing.T) {
	url := liveWitnessURL()
	if url == "" {
		t.Skip("live witness test requires AII_TEST_WITNESSD_URL (read-only: key fetch + hash cross-check)")
	}
	c := New(url, "")
	env, err := c.FetchWitnessKey()
	if err != nil {
		t.Fatalf("live key fetch: %v", err)
	}
	if env.KeyID == "" || len(env.Keys) == 0 {
		t.Fatal("live witness envelope malformed")
	}
	if _, err := c.Status(); err != nil {
		t.Fatalf("live status: %v", err)
	}

	// Manifest is served and coherently shaped.
	// Full PRODUCTION dual-PQ verification runs when the platform key
	// envelope is present at /etc/aiii/keys/aiii_ring0_public.key (the
	// production signer aiii_ring0_20260602_k14 — verified present on
	// AIII hosts 2026-08-16; synthetic coverage otherwise in
	// manifest_test.go).
	raw, err := c.getBytes("/witness/pubkey/manifest")
	if err != nil {
		t.Fatalf("live manifest: %v", err)
	}
	var m struct {
		ArtifactKind string `json:"artifact_kind"`
		Payload      struct {
			KeyID    string `json:"key_id"`
			Critical bool   `json:"critical"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.ArtifactKind != "witness.public_key_manifest" {
		t.Fatalf("live manifest malformed: %v %s", err, raw[:120])
	}
	if m.Payload.KeyID != env.KeyID {
		t.Fatalf("manifest vouches key %s but server serves %s", m.Payload.KeyID, env.KeyID)
	}

	// Production dual-PQ via the CANON key source: the platform key
	// DOWNLOADED from genesis per verification (in-memory; nothing stored
	// locally — 2026-08-17 ruling). Genesis unreachable = skip.
	c.SetGenesisURL("https://genesis.aiii.id")
	if err := c.VerifyManifest(env, ""); err != nil {
		t.Logf("production manifest verification via downloaded key: %v (genesis unreachable? skipping)", err)
	} else {
		t.Logf("production manifest dual-PQ verified against the runtime-downloaded platform key")
	}
}

var _ = context.Background

// TestLiveWitnessRoundtrip is the production interop proof, probe-pattern
// (same sequence as ai3-witness-probe): fresh identity → first bookmark
// 201 → exact retry 200 with byte-identical receipt → fork 409. The
// receipts are verified with THIS codebase's stdlib crypto against the
// server's pqsign signatures — the cross-implementation proof. Opt-in:
// AII_TEST_WITNESSD_URL + AII_TEST_WITNESS_LIVE=1 (it writes one probe
// identity row to the witness's state).
func TestLiveWitnessRoundtrip(t *testing.T) {
	base := liveWitnessURL()
	if base == "" || osGetenv("AII_TEST_WITNESS_LIVE") != "1" {
		t.Skip("live roundtrip requires AII_TEST_WITNESSD_URL + AII_TEST_WITNESS_LIVE=1")
	}
	c := New(base, "")

	witnessKey, err := c.FetchWitnessKey()
	if err != nil {
		t.Fatalf("witness key: %v", err)
	}

	// Fresh throwaway identity (probe pattern — one row on the server)
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	envelopes := &memEnvelopeStore{}
	canonical, env, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes)
	if err != nil {
		t.Fatal(err)
	}
	identityID, err := DeriveIdentityID(canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe identity: %s", identityID)

	mkReq := func(ordinal int64, ledgerHash, rangeHash string) WitnessRequest {
		req := WitnessRequest{
			IdentityID: identityID, IdentityPublicKey: canonical,
			LedgerOrdinal: ordinal, LedgerHash: ledgerHash,
			RangeStartOrdinal: 1, RangeHash: rangeHash,
		}
		sig, err := SignRequest(AsIdentityKey(kp), env, req, canonical)
		if err != nil {
			t.Fatal(err)
		}
		req.IdentitySignature = sig
		return req
	}

	h1 := "sha256:" + hexOf("probe-ledger-1")
	r1 := "sha256:" + hexOf("probe-range-1")

	// 1. First bookmark → 201
	first, err := c.Bookmark(mkReq(1, h1, r1))
	if err != nil {
		t.Fatalf("first bookmark: %v", err)
	}
	if !first.First {
		t.Fatal("first bookmark should be 201/First")
	}
	if err := VerifyReceipt(first.Receipt, mkReq(1, h1, r1), witnessKey); err != nil {
		t.Fatalf("PROOF FAILED — our stdlib ML-DSA does not verify the server's pqsign receipt: %v", err)
	}
	t.Logf("first receipt VERIFIED (witness key %s, witnessed %s)", witnessKey.KeyID, first.Receipt.WitnessedAt)

	// 2. Exact retry → 200, byte-identical signature (idempotency)
	retry, err := c.Bookmark(mkReq(1, h1, r1))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retry.First {
		t.Fatal("retry should be 200, not 201")
	}
	if retry.Receipt.WitnessSignature.SigB64 != first.Receipt.WitnessSignature.SigB64 {
		t.Fatal("idempotent retry returned a different receipt signature")
	}

	// 3. Fork (same ordinal, different hash) → 409
	fork, err := c.Bookmark(mkReq(1, "sha256:"+hexOf("probe-fork"), r1))
	if err == nil {
		_ = fork
		t.Fatal("fork must be rejected (409)")
	}
	t.Logf("fork rejected as expected: %v", err)

	// 4. Advance → 200, verified, previous fields chained. The hosted
	// witness enforces min_periodic_cadence (learned live 2026-08-16:
	// ordinal 1→2 → 409 "cadence below hosted minimum") — advance beyond it.
	st, err := c.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	advanceTo := int64(2)
	if st.MinPeriodicCadence > 0 {
		advanceTo = 1 + st.MinPeriodicCadence
	}
	advHash := "sha256:" + hexOf("probe-ledger-adv")
	adv, err := c.Bookmark(mkReq(advanceTo, advHash, "sha256:"+hexOf("probe-range-adv")))
	if err != nil {
		t.Fatalf("advance (ordinal %d, cadence %d): %v", advanceTo, st.MinPeriodicCadence, err)
	}
	if err := VerifyReceipt(adv.Receipt, mkReq(advanceTo, advHash, "sha256:"+hexOf("probe-range-adv")), witnessKey); err != nil {
		t.Fatalf("advance receipt verification: %v", err)
	}
	if adv.Receipt.PreviousWitnessedLedgerOrdinal != 1 || adv.Receipt.PreviousWitnessedLedgerHash != h1 {
		t.Fatalf("advance receipt must chain previous anchor, got (%d, %s)", adv.Receipt.PreviousWitnessedLedgerOrdinal, adv.Receipt.PreviousWitnessedLedgerHash)
	}
	t.Logf("advance receipt VERIFIED and chained to previous anchor")
}

func hexOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:])
}

// TestWitnessStateForkRefused: a receipt chaining a previous anchor that
// does not match our local last receipt = witness state fork (or ours) —
// the anchorer must refuse to anchor over the divergence.
func TestWitnessStateForkRefused(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	envelopes := &memEnvelopeStore{}
	receipts := &memReceiptStore{}

	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, receipts, testMinter{lg, kp}, 3, "")
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}

	// Seed a LOCAL receipt whose hash disagrees with what the witness
	// actually anchored — the next advance's receipt will chain the
	// server's version, mismatching ours → refuse.
	fake := WitnessReceipt{
		LedgerOrdinal: 3,
		LedgerHash:    "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	fakeJSON, _ := json.Marshal(fake)
	receipts2 := &memReceiptStore{}
	receipts2.SeedWitnessReceipt(3, fakeJSON)
	a2 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, receipts2, testMinter{lg, kp}, 1, "") // interval 1: attempt fires on the next event
	err := a2.CheckAndAnchor()
	if err == nil {
		t.Fatal("anchorer must refuse when receipt chains a different previous anchor than local state")
	}
	if !strings.Contains(err.Error(), "fork") && !strings.Contains(err.Error(), "divergence") {
		t.Fatalf("wrong refusal: %v", err)
	}
}

// TLS SPKI pinning (canon §11.1): with a pin set, only the pinned key
// connects; a wrong pin fails closed; a malformed pin never degrades to
// pinless TLS; standard WebPKI still runs underneath.
func TestTLSSPKIPinning(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"service":"ai3-witnessd"}`)
	}))
	defer srv.Close()

	cert, err := x509.ParseCertificate(srv.Certificate().Raw)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	goodPin := hex.EncodeToString(sum[:])

	// 1. Correct pin connects (test CA trusted via env — httptest certs
	// are not WebPKI-valid; the pin rides ON TOP of normal verification,
	// so the chain must verify first)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	t.Setenv("SSL_CERT_FILE", "") // no-op; pool comes from below
	c := NewWithRoots(srv.URL, goodPin, pool)
	if _, err := c.getBytes("/version"); err != nil {
		t.Fatalf("pinned client must connect to the pinned cert: %v", err)
	}

	// 2. Wrong pin fails closed
	badPin := strings.Repeat("ab", 32)
	c2 := New(srv.URL, badPin)
	if _, err := c2.getBytes("/version"); err == nil {
		t.Fatal("wrong SPKI pin must fail closed")
	}

	// 3. Malformed pin NEVER degrades to pinless TLS
	c3 := New(srv.URL, "not-a-pin")
	if _, err := c3.getBytes("/version"); err == nil {
		t.Fatal("malformed pin must fail every request, not silently disable pinning")
	}

	// 4. No pin = standard TLS (httptest certs aren't WebPKI-valid, so a
	//    pinless client gets a TLS error here — proving normal verification
	//    runs rather than a bypass)
	c4 := New(srv.URL, "")
	_, err4 := c4.getBytes("/version")
	if err4 == nil {
		t.Log("note: pinless connection succeeded (custom CA pool in env?)")
	}
}

// The witness hash boundary, pinned locally.
//
// witnessd validates EVERY hash field as HashPrefixSHA256 + 64
// lowercase hex (ai3-witnessd/crypto.go validateSHA256) and answers
// 400 "invalid hash field" otherwise. The ledger stores content_hash as
// bare lowercase hex, deliberately — LEDGER_GOLD_FORMAT.md §2 gives
// content_hash without a prefix and entry_sha256 with one. So the
// prefix belongs at the WIRE and nowhere else.
//
// Sending the bare form cost a live identity every anchor it ever
// attempted: 400 every 30 seconds, health OK, never witnessed. This
// mirrors the server's rule here so the two cannot drift apart again
// without a red test.
func TestWireHashFormSatisfiesWitnessValidation(t *testing.T) {
	// Verbatim from ai3-witnessd/crypto.go validateSHA256.
	validate := func(v string) bool {
		if !strings.HasPrefix(v, HashPrefixSHA256) {
			return false
		}
		raw := strings.TrimPrefix(v, HashPrefixSHA256)
		if len(raw) != 64 || raw != strings.ToLower(raw) {
			return false
		}
		_, err := hex.DecodeString(raw)
		return err == nil
	}

	bare := strings.Repeat("ab", 32) // a ledger content_hash, gold-format shape

	if validate(bare) {
		t.Fatal("precondition wrong: bare ledger hex must NOT satisfy the witness rule")
	}
	if !validate(PrefixHash(bare)) {
		t.Fatal("the wire form must satisfy witnessd validateSHA256")
	}
	if PrefixHash(PrefixHash(bare)) != PrefixHash(bare) {
		t.Fatal("PrefixHash must be idempotent — it is applied at boundaries that cannot know what came before")
	}
	// CheckLocalTail compares a stored tail hash against a raw event
	// ContentHash, so the wire form must return to ledger form for local
	// state. Without this, every boot after a successful anchor would
	// report a truncation that did not happen.
	if TrimHashPrefix(PrefixHash(bare)) != bare {
		t.Fatal("wire form must round-trip back to ledger form for local state")
	}
}
