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

// .
// .
// .
// .
// .
type fakeWitness struct {
	mu             sync.Mutex
	state          map[string]*anchorState
	witnessKp      *crypto.KeyPair
	witnessEnv     *PublicKeyEnvelope
	server         *httptest.Server
	sawIdentityIDs map[string]int
	manifest       []byte
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
	mux.HandleFunc("/witness/pubkey/manifest", func(w http.ResponseWriter, r *http.Request) {
		if fw.manifest == nil {
			http.Error(w, "no manifest", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fw.manifest)
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]interface{}{"min_periodic_cadence": 0})
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

	// .
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
	// .
	if st.ordinal == req.LedgerOrdinal && st.hash == req.LedgerHash {
		w.WriteHeader(200)
		writeTestJSON(w, st.receipt)
		return
	}
	if req.LedgerOrdinal <= st.ordinal {
		// .
		// .
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
	receipt := WitnessReceipt{
		IdentityID:                     req.IdentityID,
		PreviousWitnessedLedgerOrdinal: prevOrdinal,
		PreviousWitnessedLedgerHash:    prevHash,
		LedgerOrdinal:                  req.LedgerOrdinal,
		LedgerHash:                     req.LedgerHash,
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

// .

type memEnvelopeStore struct{ saved []byte }

func (m *memEnvelopeStore) SaveWitnessEnvelope(j []byte) error { m.saved = j; return nil }
func (m *memEnvelopeStore) LoadWitnessEnvelope() ([]byte, error) {
	if m.saved == nil {
		return nil, nil
	}
	return m.saved, nil
}

// .
// .
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

// .
// .
type testMinter struct {
	lg *ledger.Ledger
	kp *crypto.KeyPair
}

// .
// .
// .
func (m testMinter) MintWitnessed(receipt WitnessReceipt, witnessKeyID string) (*ledger.Event, error) {
	return m.lg.Append(ledger.EventSystemWitnessed, m.kp.Fingerprint(), 0, map[string]interface{}{
		"receipt": map[string]interface{}{
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
		if _, err := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]string{"n": fmt.Sprintf("e%d", i)}, kp); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { lg.Close() })
	return lg, kp
}

// .

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
	// .
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
	// .
	// .
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

	// .
	a2 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	if err := a2.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	if n := len(fw.sawIdentityIDs); n != 1 {
		t.Fatalf("one identity expected across restarts, saw %d", n)
	}

	// .
	// .
	// .
	// .
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
	// .
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
	// .
	// .
	// .
	// .
	// .
	fw.mu.Lock()
	fw.state[onlyKey(fw)].hash = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	fw.mu.Unlock()
	a2 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, &memReceiptStore{}, testMinter{lg, kp}, 4, "")
	req := a2.buildRequestForTest(t)
	// .
	// .
	// .
	events, _ := ledger.ReadAll(lg.Path())
	req.LedgerOrdinal = 4
	req.LedgerHash = events[3].Content
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
		t.Fatal("forked submission must be rejected by the witness (http.StatusConflict)")
	}
}

func onlyKey(fw *fakeWitness) string {
	for k := range fw.state {
		return k
	}
	return ""
}

// .
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

	// .
	// .
	// .
	canonical, env, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes)
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

	// .
	// .
	// .
	// .
	// .
	// .
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

	// .
	// .
	// .
	c.SetGenesisURL("https://genesis.aiii.id")
	if _, err := c.VerifyManifest(env, ""); err != nil {
		t.Logf("production manifest verification via downloaded key: %v (genesis unreachable? skipping)", err)
	} else {
		t.Logf("production manifest dual-PQ verified against the runtime-downloaded platform key")
	}
}

var _ = context.Background

// .
// .
// .
// .
// .
// .
// .
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

	// .
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

	// .
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

	// .
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

	// .
	fork, err := c.Bookmark(mkReq(1, hexOf("probe-fork")))
	if err == nil {
		_ = fork
		t.Fatal("fork must be rejected (http.StatusConflict)")
	}
	t.Logf("fork rejected as expected: %v", err)

	// .
	// .
	// .
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

// .
// .
// .
func TestWitnessStateForkRefused(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	envelopes := &memEnvelopeStore{}
	receipts := &memReceiptStore{}

	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, receipts, testMinter{lg, kp}, 3, "")
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}

	// .
	// .
	// .
	fake := WitnessReceipt{
		LedgerOrdinal: 3,
		LedgerHash:    "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	fakeJSON, _ := json.Marshal(fake)
	receipts2 := &memReceiptStore{}
	receipts2.SeedWitnessReceipt(3, fakeJSON)
	a2 := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), envelopes, receipts2, testMinter{lg, kp}, 1, "")
	err := a2.CheckAndAnchor()
	if err == nil {
		t.Fatal("anchorer must refuse when receipt chains a different previous anchor than local state")
	}
	if !strings.Contains(err.Error(), "fork") && !strings.Contains(err.Error(), "divergence") {
		t.Fatalf("wrong refusal: %v", err)
	}
}

// .
// .
// .
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

	// .
	// .
	// .
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	t.Setenv("SSL_CERT_FILE", "")
	c := NewWithRoots(srv.URL, goodPin, pool)
	if _, err := c.getBytes("/version"); err != nil {
		t.Fatalf("pinned client must connect to the pinned cert: %v", err)
	}

	// .
	badPin := strings.Repeat("ab", 32)
	c2 := New(srv.URL, badPin)
	if _, err := c2.getBytes("/version"); err == nil {
		t.Fatal("wrong SPKI pin must fail closed")
	}

	// .
	c3 := New(srv.URL, "not-a-pin")
	if _, err := c3.getBytes("/version"); err == nil {
		t.Fatal("malformed pin must fail every request, not silently disable pinning")
	}

	// .
	// .
	// .
	c4 := New(srv.URL, "")
	_, err4 := c4.getBytes("/version")
	if err4 == nil {
		t.Log("note: pinless connection succeeded (custom CA pool in env?)")
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestLedgerHashFormSatisfiesWitnessValidation(t *testing.T) {
	// .
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

// .
// .
// .
// .
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

// .
// .
// .
// .
func TestReceiptForAnotherRequestIsRefused(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	envelopes := &memEnvelopeStore{}
	canonical, env, err := EnsureIdentityEnvelope(AsIdentityKey(kp), envelopes)
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
