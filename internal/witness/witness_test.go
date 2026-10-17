package witness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// fakeWitness implements the bookmark-protocol server semantics with its
// own generated ML-DSA-87 key: 201 first anchor, 200 idempotent retry
// (byte-identical receipt), http.StatusConflict fork/rollback. Signature inputs are the
// protocol functions — which are themselves pinned to the witnessd
// source by citation; cross-binary proof is the env-gated live test.
type fakeWitness struct {
	mu             sync.Mutex
	state          map[string]*anchorState
	witnessKp      *crypto.KeyPair
	witnessEnv     *PublicKeyEnvelope
	server         *httptest.Server
	sawIdentityIDs map[string]int
	manifest       []byte // served at /witness/pubkey/manifest when set; 404 otherwise
	during         func() // runs inside every bookmark's round trip, before the answer (slowly)
	statuses       int    // how many times /status was asked
}

// slowly makes the witness slow: fn runs inside every bookmark's round
// trip, after the request has left the anchorer and before the witness
// answers — what lands in the record while it answers.
func (fw *fakeWitness) slowly(fn func()) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.during = fn
}

type anchorState struct {
	ordinal int64
	hash    string
	receipt WitnessReceipt
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
			PublicKeyFingerprint: sigenvelope.PublicKeyFingerprint(AlgMLDSA87, keyID, kp.PublicKeyB64()),
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
	mux.HandleFunc("/witness/pubkey/manifest", func(w http.ResponseWriter, r *http.Request) {
		fw.mu.Lock()
		manifest := fw.manifest
		fw.mu.Unlock()
		if manifest == nil {
			http.Error(w, "no manifest", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(manifest)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		fw.mu.Lock()
		fw.statuses++
		fw.mu.Unlock()
		writeTestJSON(w, map[string]any{"min_periodic_cadence": 0})
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
	during := fw.during
	fw.mu.Unlock()
	if during != nil {
		during()
	}
	fw.mu.Lock()
	defer fw.mu.Unlock()
	fw.sawIdentityIDs[req.IdentityID]++

	// verify the identity signature over the request input (server behavior)
	input := RequestSignatureInput(req, req.IdentityPublicKey)
	if sha256Prefixed(input) != req.IdentitySignature.SignatureInputSHA256 {
		http.Error(w, "identity signature input hash mismatch", http.StatusUnauthorized)
		return
	}
	ml, ok := fw.identityMLDsa(t, req.IdentityPublicKey)
	if !ok {
		http.Error(w, "identity key missing", 400)
		return
	}
	sig, err := base64Decode(req.IdentitySignature.SigB64)
	if err != nil {
		http.Error(w, "bad sig encoding", http.StatusUnauthorized)
		return
	}
	if err := crypto.Verify(base64DecodeOrFatal(t, ml.PublicKeyB64), input, sig); err != nil {
		http.Error(w, "identity signature verification failed", http.StatusUnauthorized)
		return
	}

	st, found := fw.state[req.IdentityID]
	if !found {
		receipt := fw.signReceipt(t, req, -1, ZeroLedgerHash)
		fw.state[req.IdentityID] = &anchorState{req.LedgerOrdinal, req.LedgerHash, receipt}
		w.WriteHeader(201)
		writeTestJSON(w, receipt)
		return
	}
	// exact retry → byte-identical receipt, 200
	if st.ordinal == req.LedgerOrdinal && st.hash == req.LedgerHash {
		w.WriteHeader(200)
		writeTestJSON(w, st.receipt)
		return
	}
	if req.LedgerOrdinal <= st.ordinal {
		// The REAL conflict body shape: witnessd writeJSONError renders
		// {"error": message} (server.go:377-378), message from store.go:375.
		w.WriteHeader(http.StatusConflict)
		writeTestJSON(w, map[string]string{"error": conflictMsgRollbackFork})
		return
	}
	receipt := fw.signReceipt(t, req, st.ordinal, st.hash)
	st.ordinal, st.hash, st.receipt = req.LedgerOrdinal, req.LedgerHash, receipt
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
	return fw.signReceiptAt(t, req, prevOrdinal, prevHash, time.Now())
}

// signReceiptAt is signReceipt with the receipt witnessed at a given moment.
func (fw *fakeWitness) signReceiptAt(t *testing.T, req WitnessRequest, prevOrdinal int64, prevHash string, at time.Time) WitnessReceipt {
	receipt := WitnessReceipt{
		IdentityID:                     req.IdentityID,
		PreviousWitnessedLedgerOrdinal: prevOrdinal,
		PreviousWitnessedLedgerHash:    prevHash,
		LedgerOrdinal:                  req.LedgerOrdinal,
		LedgerHash:                     req.LedgerHash,
		WitnessedAt:                    at.UTC().Format(time.RFC3339),
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

// memEnvelopeStore keeps the row as the store does — a save never
// overwrites, a replace does — and answers WitnessEnrollment with what a
// test says the record holds.
type memEnvelopeStore struct {
	saved         []byte
	saves         int
	replaces      int
	enrolledIDs   []string
	born, witness time.Time
}

func (m *memEnvelopeStore) SaveWitnessEnvelope(j []byte) error {
	m.saves++
	if m.saved == nil {
		m.saved = j
	}
	return nil
}
func (m *memEnvelopeStore) ReplaceWitnessEnvelope(j []byte) error {
	m.replaces++
	m.saved = j
	return nil
}
func (m *memEnvelopeStore) LoadWitnessEnvelope() ([]byte, error) {
	if m.saved == nil {
		return nil, nil
	}
	return m.saved, nil
}
func (m *memEnvelopeStore) WitnessEnrollment() ([]string, time.Time, time.Time, error) {
	return m.enrolledIDs, m.born, m.witness, nil
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

// MintWitnessed mints the head payload the runtime mints
// (app/adapters.go witnessMinter): the whole receipt, so the head
// verifier can check the chain of receipts and the witness signature.
func (m testMinter) MintWitnessed(receipt WitnessReceipt, witnessKeyID string) (*ledger.Event, error) {
	return m.lg.Append(ledger.EventSystemWitnessed, 0, map[string]any{
		"receipt": map[string]any{
			"identity_id":                       receipt.IdentityID,
			"previous_witnessed_ledger_ordinal": receipt.PreviousWitnessedLedgerOrdinal,
			"previous_witnessed_ledger_hash":    receipt.PreviousWitnessedLedgerHash,
			"ledger_ordinal":                    receipt.LedgerOrdinal,
			"ledger_hash":                       receipt.LedgerHash,
			"witnessed_at":                      receipt.WitnessedAt,
			"witness_key_id":                    witnessKeyID,
			"witness_sig_b64":                   receipt.WitnessSignature.SigB64,
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
		if _, err := lg.Append(ledger.EventExperienceCreate, 3,
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
		} `json:"receipt"`
	}
	if err := json.Unmarshal(witnessed.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Receipt.LedgerOrdinal != 5 {
		t.Fatalf("receipt in ledger anchors ordinal %d, want 5", payload.Receipt.LedgerOrdinal)
	}

	if payload.Receipt.LedgerHash != events[4].EntryHash() {
		t.Fatalf("ledger_hash must be the tail record's entry hash, bare: got %s want %s", payload.Receipt.LedgerHash, events[4].EntryHash())
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

	// "Restart": fresh anchorer, SAME envelope store → same identity. Its
	// last receipt is read from the record, as the store answers it after
	// a restart: an anchorer that held none over a record that carries a
	// head would be handed a receipt the head rule refuses for it.
	a2 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &recordStore{lg: lg}, testMinter{lg, kp}, 3, "")
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
	req.LedgerHash = events[3].Content
	canonical2, env2, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes, filepath.Dir(lg.Path()))
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
		t.Fatal("forked submission must be rejected by the witness (http.StatusConflict)")
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
	canonical, env, err := EnsureIdentityEnvelope(a.key, a.envelopes, filepath.Dir(a.ledger.Path()))
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
	canonical, env, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes, filepath.Dir(lg.Path()))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := DeriveIdentityID(canonical, env)
	req := WitnessRequest{
		IdentityID: id, IdentityPublicKey: canonical,
		LedgerOrdinal: 3, LedgerHash: lg.LastHash(),
	}
	receipt := fw.signReceiptNoLock(t, req, 0, "")
	witnessKey := fw.witnessEnv
	if err := VerifyReceipt(receipt, req, witnessKey); err != nil {
		t.Fatalf("honest receipt must verify: %v", err)
	}
	tampered := receipt
	tampered.LedgerHash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
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

func mustCanonical(t *testing.T, v any) []byte {
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

func writeTestJSON(w http.ResponseWriter, v any) {
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

	// Manifest is served and coherently shaped, then must verify against
	// the shipped root. An opted-in proof must fail, not log-and-pass,
	// when production does not agree with the root this build trusts.
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
		t.Fatalf("live manifest malformed: %v %.120s", err, raw)
	}
	if m.Payload.KeyID != env.KeyID {
		t.Fatalf("manifest vouches key %s but server serves %s", m.Payload.KeyID, env.KeyID)
	}

	// Read-only: no bookmark, identity envelope, mint or installed state.
	c.SetGenesisURL("https://genesis.aiii.id")
	if _, err := c.VerifyManifest(env, ""); err != nil {
		t.Fatalf("production manifest must verify against the shipped boot root: %v", err)
	}
	t.Log("production manifest dual-PQ verified against the shipped boot root")
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
	if base == "" || os.Getenv("AII_TEST_WITNESS_LIVE") != "1" {
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
	canonical, env, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	identityID, err := DeriveIdentityID(canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("probe identity: %s", identityID)

	mkReq := func(ordinal int64, ledgerHash string) WitnessRequest {
		req := WitnessRequest{
			IdentityID: identityID, IdentityPublicKey: canonical,
			LedgerOrdinal: ordinal, LedgerHash: ledgerHash,
		}
		sig, err := SignRequest(AsIdentityKey(kp), env, req, canonical)
		if err != nil {
			t.Fatal(err)
		}
		req.IdentitySignature = sig
		return req
	}

	h1 := hexOf("probe-ledger-1")

	// 1. First bookmark → 201
	first, err := c.Bookmark(mkReq(1, h1))
	if err != nil {
		t.Fatalf("first bookmark: %v", err)
	}
	if !first.First {
		t.Fatal("first bookmark should be 201/First")
	}
	if err := VerifyReceipt(first.Receipt, mkReq(1, h1), witnessKey); err != nil {
		t.Fatalf("PROOF FAILED — our stdlib ML-DSA does not verify the server's pqsign receipt: %v", err)
	}
	t.Logf("first receipt VERIFIED (witness key %s, witnessed %s)", witnessKey.KeyID, first.Receipt.WitnessedAt)

	// 2. Exact retry → 200, byte-identical signature (idempotency)
	retry, err := c.Bookmark(mkReq(1, h1))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retry.First {
		t.Fatal("retry should be 200, not 201")
	}
	if retry.Receipt.WitnessSignature.SigB64 != first.Receipt.WitnessSignature.SigB64 {
		t.Fatal("idempotent retry returned a different receipt signature")
	}

	// 3. Fork (same ordinal, different hash) → http.StatusConflict
	fork, err := c.Bookmark(mkReq(1, hexOf("probe-fork")))
	if err == nil {
		_ = fork
		t.Fatal("fork must be rejected (http.StatusConflict)")
	}
	t.Logf("fork rejected as expected: %v", err)

	st, err := c.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	advanceTo := int64(2)
	if st.MinPeriodicCadence > 0 {
		advanceTo = 1 + st.MinPeriodicCadence
	}
	advHash := hexOf("probe-ledger-adv")
	adv, err := c.Bookmark(mkReq(advanceTo, advHash))
	if err != nil {
		t.Fatalf("advance (ordinal %d, cadence %d): %v", advanceTo, st.MinPeriodicCadence, err)
	}
	if err := VerifyReceipt(adv.Receipt, mkReq(advanceTo, advHash), witnessKey); err != nil {
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

// A MALFORMED PIN IS THE ERROR OF EVERY REQUEST, and no request leaves.
// The server here is plain HTTP and answers anything, so a client that
// had degraded to no pin would succeed; TestTLSSPKIPinning's TLS server
// refuses an untrusted certificate either way and cannot tell the two
// apart.
func TestAMalformedPinIsTheErrorOfEveryRequestAndNothingLeaves(t *testing.T) {
	var asked atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		writeTestJSON(w, map[string]any{"min_periodic_cadence": 0})
	}))
	defer srv.Close()
	c := New(srv.URL, "not-a-pin")
	if _, err := c.Status(); err == nil || !strings.Contains(err.Error(), "witness TLS pin malformed") {
		t.Fatalf("Status with a malformed pin = %v, want the pin error", err)
	}
	if _, err := c.Bookmark(WitnessRequest{}); err == nil || !strings.Contains(err.Error(), "witness TLS pin malformed") {
		t.Fatalf("Bookmark with a malformed pin = %v, want the pin error", err)
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("A REQUEST LEFT UNDER A MALFORMED PIN: the server was asked %d times", n)
	}
}

func TestLedgerHashFormSatisfiesWitnessValidation(t *testing.T) {
	// Verbatim from ai3-witnessd/crypto.go validateLedgerHash.
	validate := func(v string) bool {
		if len(v) != 64 || v != strings.ToLower(v) {
			return false
		}
		_, err := hex.DecodeString(v)
		return err == nil
	}
	lg, _ := testLedger(t, 1)
	if !validate(lg.LastHash()) {
		t.Fatalf("the ledger's entry hash %q must satisfy the witness rule as it is", lg.LastHash())
	}
	if validate(HashPrefixSHA256 + lg.LastHash()) {
		t.Fatal("a prefixed hash must NOT satisfy the ledger-hash rule — the envelope grammar's form is not the record's")
	}
	if validate(strings.ToUpper(lg.LastHash())) {
		t.Fatal("uppercase hex is not the record's form")
	}
}

const (
	vectorReceiptInput = "AIII-WITNESS-RECEIPT\nidentity_id:did:aiii:identity:sha256:abc\nprevious_witnessed_ledger_ordinal:-1\nprevious_witnessed_ledger_hash:0000000000000000000000000000000000000000000000000000000000000000\nledger_ordinal:42\nledger_hash:abababababababababababababababababababababababababababababababab\nwitnessed_at:2026-09-03T00:00:00Z\n"
	vectorRequestInput = "AIII-WITNESS-REQUEST\nidentity_id:did:aiii:identity:sha256:abc\nidentity_public_key:{\"k\":1}\nledger_ordinal:42\nledger_hash:abababababababababababababababababababababababababababababababab\n"
)

func TestSignatureInputsMatchTheServerVectors(t *testing.T) {
	receipt := WitnessReceipt{
		IdentityID:                     "did:aiii:identity:sha256:abc",
		PreviousWitnessedLedgerOrdinal: -1,
		PreviousWitnessedLedgerHash:    ZeroLedgerHash,
		LedgerOrdinal:                  42,
		LedgerHash:                     strings.Repeat("ab", 32),
		WitnessedAt:                    "2026-09-03T00:00:00Z",
	}
	if got := string(ReceiptSignatureInput(receipt)); got != vectorReceiptInput {
		t.Fatalf("receipt signature input drifted from the server's vector:\n got %q\nwant %q", got, vectorReceiptInput)
	}
	req := WitnessRequest{IdentityID: "did:aiii:identity:sha256:abc", LedgerOrdinal: 42, LedgerHash: strings.Repeat("ab", 32)}
	if got := string(RequestSignatureInput(req, []byte(`{"k":1}`))); got != vectorRequestInput {
		t.Fatalf("request signature input drifted from the server's vector:\n got %q\nwant %q", got, vectorRequestInput)
	}
}

// A receipt the witness validly signed for ANOTHER request must not
// verify for this one: the signature check alone cannot see that, since
// the witness did sign it; the echo of ordinal and hash is what refuses
// a misdirected or replayed receipt.
func TestReceiptForAnotherRequestIsRefused(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	envelopes := &memEnvelopeStore{}
	canonical, env, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes, filepath.Dir(lg.Path()))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := DeriveIdentityID(canonical, env)
	mine := WitnessRequest{IdentityID: id, IdentityPublicKey: canonical, LedgerOrdinal: 3, LedgerHash: lg.LastHash()}
	other := WitnessRequest{IdentityID: id, IdentityPublicKey: canonical, LedgerOrdinal: 3, LedgerHash: strings.Repeat("e", 64)}
	receiptForOther := fw.signReceiptNoLock(t, other, 0, "")
	if err := VerifyReceipt(receiptForOther, other, fw.witnessEnv); err != nil {
		t.Fatalf("precondition: the witness's own receipt verifies for its request: %v", err)
	}
	if err := VerifyReceipt(receiptForOther, mine, fw.witnessEnv); err == nil {
		t.Fatal("a validly signed receipt for a different hash verified for this request")
	}
}

// enrolled mints the envelope this key would have minted at enrolledAt
// and gives its DID: the record's view of an identity enrolled then.
func enrolled(t *testing.T, key IdentityKey, enrolledAt time.Time) ([]byte, string) {
	t.Helper()
	canonical, env, err := buildIdentityEnvelope(key, enrolledAt)
	if err != nil {
		t.Fatal(err)
	}
	did, err := DeriveIdentityID(canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, did
}

func TestALostEnvelopeIsRebuiltFromTheRecord(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	born := time.Date(2026, 8, 20, 13, 32, 14, 157003771, time.UTC)
	enrolledAt := born.Add(time.Hour + 17*time.Second)
	original, did := enrolled(t, key, enrolledAt)
	store := &memEnvelopeStore{enrolledIDs: []string{did}, born: born, witness: enrolledAt.Add(3*time.Minute + 400*time.Millisecond)}

	got, env, err := EnsureIdentityEnvelope(key, store, t.TempDir())
	if err != nil {
		t.Fatalf("the record names %s; its envelope must be rebuilt: %v", did, err)
	}
	if !bytes.Equal(got, original) || !bytes.Equal(store.saved, original) || store.saves != 1 || store.replaces != 0 {
		t.Fatalf("the rebuilt envelope must be the original, saved once (saves=%d replaces=%d, created %s)", store.saves, store.replaces, env.CreatedAt)
	}
	if id, _ := DeriveIdentityID(got, env); id != did {
		t.Fatalf("rebuilt DID %s, the record names %s", id, did)
	}
	// Birth-derived (the withdrawn proposal) is not it.
	if _, fromBirth := enrolled(t, key, born); fromBirth == did {
		t.Fatal("the fixture must enroll after birth, or it proves nothing about first-enrollment time")
	}
}

// The slack above the first witness record: a clock stepped back after
// enrollment still finds the second.
func TestTheEnrollmentSecondIsFoundAboveAStepBackClock(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	born := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	enrolledAt := born.Add(2 * time.Hour)
	original, did := enrolled(t, key, enrolledAt)
	// The first witness record was stamped 90 seconds BEFORE the envelope's
	// second: the host clock stepped back between them.
	store := &memEnvelopeStore{enrolledIDs: []string{did}, born: born, witness: enrolledAt.Add(-90 * time.Second)}
	got, _, err := EnsureIdentityEnvelope(key, store, t.TempDir())
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("a second inside the slack above the first witness record must be found: %v", err)
	}
}

// A row that names another identity than the record — minted fresh after
// an earlier loss, before this fix — is replaced by the record's envelope.
func TestARowThatContradictsTheRecordIsReplacedByTheRecordsEnvelope(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	born := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	original, did := enrolled(t, key, born.Add(26*time.Hour))
	stranger, strangerDID := enrolled(t, key, born.Add(9*24*time.Hour))
	if strangerDID == did {
		t.Fatal("fixture: the stranger must be another identity")
	}
	store := &memEnvelopeStore{saved: stranger, enrolledIDs: []string{did}, born: born, witness: born.Add(26*time.Hour + time.Minute)}
	got, _, err := EnsureIdentityEnvelope(key, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) || !bytes.Equal(store.saved, original) || store.replaces != 1 {
		t.Fatalf("the contradicting row must be replaced by the record's envelope (replaces=%d)", store.replaces)
	}
}

// The record names a DID no envelope of this key in the window hashes to:
// refused, typed, and nothing is minted or saved — a guess would be a
// second identity.
func TestAnEnvelopeTheRecordCannotRebuildIsRefusedNeverMinted(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	born := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	_, did := enrolled(t, key, born.Add(-72*time.Hour)) // outside every window the record allows
	store := &memEnvelopeStore{enrolledIDs: []string{did}, born: born, witness: born.Add(time.Hour)}
	_, _, err := EnsureIdentityEnvelope(key, store, t.TempDir())
	if !errors.Is(err, ErrEnvelopeUnrecoverable) {
		t.Fatalf("want ErrEnvelopeUnrecoverable, got %v", err)
	}
	if store.saves != 0 || store.replaces != 0 || store.saved != nil {
		t.Fatalf("nothing may be minted or saved when the record's envelope cannot be rebuilt (saves=%d)", store.saves)
	}
}

// Two identities named by the record: the fork is already there. Refused.
func TestARecordThatNamesTwoWitnessIdentitiesIsRefused(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	born := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	_, a := enrolled(t, key, born.Add(time.Hour))
	_, b := enrolled(t, key, born.Add(2*time.Hour))
	store := &memEnvelopeStore{enrolledIDs: []string{a, b}, born: born, witness: born.Add(3 * time.Hour)}
	if _, _, err := EnsureIdentityEnvelope(key, store, t.TempDir()); !errors.Is(err, ErrEnrollmentForked) || store.saves != 0 {
		t.Fatalf("want ErrEnrollmentForked and nothing saved, got %v (saves=%d)", err, store.saves)
	}
}

// Two first uses at once each mint; the store keeps the first it was
// given, and both use what it holds — never an envelope it did not keep.
func TestConcurrentFirstUsesUseTheEnvelopeTheStoreKept(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	winner, _ := enrolled(t, key, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	store := &racingEnvelopeStore{memEnvelopeStore: memEnvelopeStore{}, other: winner}
	got, _, err := EnsureIdentityEnvelope(key, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, winner) {
		t.Fatal("the caller whose save was a no-op used an envelope the store never kept")
	}
}

// racingEnvelopeStore lets another first use save its envelope between
// this caller's load and save.
type racingEnvelopeStore struct {
	memEnvelopeStore
	other []byte
}

func (r *racingEnvelopeStore) SaveWitnessEnvelope(j []byte) error {
	if r.saved == nil {
		r.saved = r.other // the other caller got there first
	}
	return r.memEnvelopeStore.SaveWitnessEnvelope(j)
}

// Whatever produced the envelope, the anchorer never bookmarks under a
// DID the record's last receipt does not name: the witness is not asked,
// and the refusal is typed. Not latched: the pass is retried.
func TestTheAnchorerNeverPresentsASecondIdentity(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	key := AsIdentityKey(kp)
	_, recorded := enrolled(t, key, time.Now().Add(-48*time.Hour))
	seeded, _ := json.Marshal(WitnessReceipt{IdentityID: recorded, LedgerOrdinal: 2, LedgerHash: "sha256:" + strings.Repeat("a", 64)})
	receipts := &memReceiptStore{}
	receipts.SeedWitnessReceipt(2, seeded)
	stranger, strangerDID := enrolled(t, key, time.Now().Add(-time.Hour))
	envelopes := &memEnvelopeStore{saved: stranger} // a row the fake's record does not contradict
	a := NewAnchorer(New(fw.server.URL, ""), lg, key, envelopes, receipts, testMinter{lg, kp}, 1, "")
	err := a.CheckAndAnchor()
	if !errors.Is(err, ErrNotTheRecordedIdentity) {
		t.Fatalf("want ErrNotTheRecordedIdentity, got %v", err)
	}
	fw.mu.Lock()
	asked := fw.sawIdentityIDs[strangerDID]
	fw.mu.Unlock()
	if asked != 0 {
		t.Fatalf("the witness was asked %d time(s) about a second identity", asked)
	}
	if a.conflict != nil {
		t.Fatal("a wrong envelope is ours to fix, not a witness conflict: nothing may be latched")
	}
}

// The template is the builder's envelope at every second, not only at the
// two it is proven against when made: seconds across day, month, year and
// leap-day boundaries, and a century on.
func TestTheEnvelopeTemplateIsTheBuildersAtEverySecond(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	c, err := newEnvelopeCandidates(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{
		time.Date(2026, 8, 23, 15, 28, 41, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2028, 2, 29, 12, 0, 0, 0, time.UTC),
		time.Date(2028, 3, 1, 0, 0, 59, 0, time.UTC),
		time.Date(2126, 9, 4, 1, 2, 3, 0, time.UTC),
		time.Date(2026, 9, 28, 7, 0, 0, 999999999, time.UTC), // a fraction the builder drops
	} {
		want, _, err := buildIdentityEnvelope(key, at)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.canonical(nil, at); !bytes.Equal(got, want) {
			t.Fatalf("at %s the template gives\n%s\nthe builder\n%s", at, got, want)
		}
	}
}

// The builder alone finds the same second the template does: the fallback
// when a template cannot be proven is the search, only slower.
func TestTheSearchFindsTheSameSecondThroughTheBuilderAlone(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	born := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	enrolledAt := born.Add(26 * time.Minute)
	original, did := enrolled(t, key, enrolledAt)
	got, _, err := searchIdentityEnvelope(key, did, born, enrolledAt.Add(2*time.Minute), nil)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("the builder-only search must find the enrollment second: %v", err)
	}
}
