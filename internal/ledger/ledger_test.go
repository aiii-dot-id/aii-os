package ledger

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("injected write failure")
}

func testKeyPair(t *testing.T) *crypto.KeyPair {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keygen failed: %v", err)
	}
	return kp
}

func TestAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, err := New(path)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer l.Close()

	// Append genesis event
	payload := map[string]interface{}{"name": "TestIdentity", "pubkey": kp.PublicKeyB64()}
	evt1, err := l.Append(EventRing0Genesis, kp.Fingerprint(), 0, payload, kp)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	if evt1.Seq != 1 {
		t.Errorf("first event seq = %d, want 1", evt1.Seq)
	}
	if evt1.PrevHash != "" {
		t.Errorf("genesis prev_hash = %q, want empty", evt1.PrevHash)
	}
	if evt1.SigAlg != crypto.SigAlg {
		t.Errorf("sig_alg = %q, want %q", evt1.SigAlg, crypto.SigAlg)
	}
	if evt1.Author != kp.Fingerprint() {
		t.Errorf("author = %q, want %q", evt1.Author, kp.Fingerprint())
	}

	// Append second event
	payload2 := map[string]interface{}{"statement": "I am a test identity", "confidence": 0.9}
	evt2, err := l.Append(EventBeliefUpsert, kp.Fingerprint(), 3, payload2, kp)
	if err != nil {
		t.Fatalf("Append 2 failed: %v", err)
	}

	if evt2.Seq != 2 {
		t.Errorf("second event seq = %d, want 2", evt2.Seq)
	}
	if evt2.PrevHash != evt1.ContentHash {
		t.Errorf("second event prev_hash = %q, want %q", evt2.PrevHash, evt1.ContentHash)
	}

	// Read back
	l.Close()
	events, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("read back %d events, want 2", len(events))
	}

	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Errorf("event order wrong: %d, %d", events[0].Seq, events[1].Seq)
	}

	if events[1].PrevHash != events[0].ContentHash {
		t.Error("chain link broken in readback")
	}
}

func TestChainState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	// Write 3 events
	l, _ := New(path)
	l.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"a": "1"}, kp)
	l.Append(EventBeliefUpsert, kp.Fingerprint(), 3, map[string]string{"b": "2"}, kp)
	evt3, _ := l.Append(EventSelfModelSynthesize, kp.Fingerprint(), 3, map[string]string{"c": "3"}, kp)
	l.Close()

	if l.LastSeq() != 3 {
		t.Errorf("LastSeq = %d, want 3", l.LastSeq())
	}

	// Reopen and check state is restored
	l2, err := New(path)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer l2.Close()

	if l2.LastSeq() != 3 {
		t.Errorf("after reopen LastSeq = %d, want 3", l2.LastSeq())
	}
	if l2.LastHash() != evt3.ContentHash {
		t.Errorf("after reopen LastHash mismatch")
	}
}

func TestVerifyChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, _ := New(path)
	l.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"name": "test"}, kp)
	l.Append(EventBeliefUpsert, kp.Fingerprint(), 3, map[string]string{"x": "y"}, kp)
	l.Append(EventSelfModelSynthesize, kp.Fingerprint(), 3, map[string]string{"z": "w"}, kp)
	l.Close()

	// Verify with correct key
	authorKeys := map[string][]byte{
		kp.Fingerprint(): kp.PublicKey,
	}

	err := VerifyChain(path, authorKeys)
	if err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}

	// Verify with wrong key should fail
	wrongKp, _ := crypto.GenerateKeyPair()
	authorKeysWrong := map[string][]byte{
		kp.Fingerprint(): wrongKp.PublicKey,
	}

	err = VerifyChain(path, authorKeysWrong)
	if err == nil {
		t.Error("VerifyChain should fail with wrong key")
	}
}

func TestTamperDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, _ := New(path)
	l.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"name": "test"}, kp)
	l.Append(EventBeliefUpsert, kp.Fingerprint(), 3, map[string]string{"x": "y"}, kp)
	l.Close()

	// Tamper: append a fake event with bad signature
	data, _ := os.ReadFile(path)
	// Append garbage
	data = append(data, []byte(`{"seq":3,"prev_hash":"bad","type":"belief.upsert","author":"bad","ring":3,"payload":"{}","content_hash":"bad","signature":"bad","sig_alg":"ML-DSA-87","sig_key_id":"bad"}`+"\n")...)
	os.WriteFile(path, data, 0640)

	authorKeys := map[string][]byte{
		kp.Fingerprint(): kp.PublicKey,
	}

	err := VerifyChain(path, authorKeys)
	if err == nil {
		t.Error("VerifyChain should detect tampering")
	}
}

func TestEmptyLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")

	l, err := New(path)
	if err != nil {
		t.Fatalf("New on empty path failed: %v", err)
	}
	defer l.Close()

	if l.LastSeq() != 0 {
		t.Errorf("empty ledger LastSeq = %d, want 0", l.LastSeq())
	}
	if l.LastHash() != "" {
		t.Errorf("empty ledger LastHash = %q, want empty", l.LastHash())
	}

	events, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll on empty ledger failed: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("empty ledger has %d events, want 0", len(events))
	}
}

func TestConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, _ := New(path)
	defer l.Close()

	// Sequential appends from same goroutine — the mutex protects
	// against concurrent access, but we test sequential correctness here
	for i := 0; i < 10; i++ {
		_, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]int{"i": i}, kp)
		if err != nil {
			t.Fatalf("append %d failed: %v", i, err)
		}
	}

	if l.LastSeq() != 10 {
		t.Errorf("LastSeq = %d, want 10", l.LastSeq())
	}

	// Verify chain
	authorKeys := map[string][]byte{kp.Fingerprint(): kp.PublicKey}
	if err := VerifyChain(path, authorKeys); err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}
}

// Model provenance: model_id must be stamped into the payload BEFORE hashing,
// so the signature covers it. Tampering the payload's model_id must break
// the content hash; the envelope mirror must equal the payload stamp.
func TestModelIDStampIsSigned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, err := New(path)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer l.Close()
	l.SetModelID("test-model-v1")

	evt, err := l.Append(EventBeliefUpsert, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b1", "statement": "provenance matters"}, kp)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// 1. Envelope mirror is set
	if evt.ModelID != "test-model-v1" {
		t.Errorf("envelope ModelID = %q, want test-model-v1", evt.ModelID)
	}

	// 2. Payload carries the stamp
	var p map[string]interface{}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		t.Fatalf("payload not JSON object: %v", err)
	}
	if p["model_id"] != "test-model-v1" {
		t.Errorf("payload model_id = %v, want test-model-v1", p["model_id"])
	}

	// 3. Content hash covers the stamp: forge a different model, hash breaks
	forged := make(map[string]interface{})
	for k, v := range p {
		forged[k] = v
	}
	forged["model_id"] = "honest-model-v2"
	fb, _ := json.Marshal(forged)
	if crypto.ContentHash(fb) == evt.ContentHash {
		t.Error("forged model_id produces same content hash — stamp is NOT signed")
	}
	if crypto.ContentHash(evt.Payload) != evt.ContentHash {
		t.Error("payload as-written does not reproduce its own content hash")
	}

	// 4. Signature verifies over the GOLD envelope
	events, _ := ReadAll(path)
	if len(events) != 1 {
		t.Fatalf("ReadAll = %d events, want 1", len(events))
	}
	entrySHA, err := EntrySHA256(&events[0])
	if err != nil {
		t.Fatal(err)
	}
	signMsg := SignatureInputGold(events[0].SigAlg, events[0].SigKeyID, entrySHA)
	if err := crypto.VerifyB64(kp.PublicKeyB64(), signMsg, events[0].Signature); err != nil {
		t.Errorf("signature does not verify: %v", err)
	}
}

// Envelope tampering (finding 1, 2026-08-17 review): rewriting an
// event's TYPE on disk — payload, hashes, and links untouched — must
// fail verification. The old input (prevHash+contentHash only) let
// this through: belief.upsert could become belief.archive silently.
func TestEnvelopeTamperDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, _ := New(path)
	l.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"name": "t"}, kp)
	if _, err := l.Append(EventBeliefUpsert, kp.Fingerprint(), 3, map[string]string{"id": "b1", "statement": "s"}, kp); err != nil {
		t.Fatalf("append: %v", err)
	}
	l.Close()

	// Rewrite event 2's type in place: belief.upsert -> belief.archive.
	// Everything else (payload hash, prev_hash, seq, signature) intact.
	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var evt Event
	if err := json.Unmarshal([]byte(lines[1]), &evt); err != nil {
		t.Fatalf("parse: %v", err)
	}
	evt.Type = EventBeliefArchive
	rewritten, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	lines[1] = string(rewritten)
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0640)

	authorKeys := map[string][]byte{kp.Fingerprint(): kp.PublicKey}
	if err := VerifyChain(path, authorKeys); err == nil {
		t.Fatal("VerifyChain PASSED an envelope type rewrite — the signature does not cover the event type")
	}
}

func TestLedgerReadersRejectUnknownEventField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"name": "t"}, kp); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "}") + `,"unsigned":true}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]string{"note": "x"}, kp); !errors.Is(err, ErrTailIntegrity) {
		t.Fatalf("append accepted an unknown tail field: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAll(path); err == nil {
		t.Fatal("replay accepted an unknown event field")
	}
	if reopened, err := New(path); err == nil {
		_ = reopened.Close()
		t.Fatal("startup accepted an unknown event field")
	}
}

// Without SetModelID, payloads carry no stamp (dormant, not fabricated)
func TestModelIDAbsentWhenUnset(t *testing.T) {
	dir := t.TempDir()
	kp := testKeyPair(t)
	l, err := New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer l.Close()

	evt, err := l.Append(EventBeliefUpsert, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b1", "statement": "x"}, kp)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	var p map[string]interface{}
	json.Unmarshal(evt.Payload, &p)
	if _, ok := p["model_id"]; ok {
		t.Error("model_id stamped without SetModelID — should be absent")
	}
	if evt.ModelID != "" {
		t.Errorf("envelope ModelID = %q, want empty", evt.ModelID)
	}
}

func TestCallerCannotSupplyModelID(t *testing.T) {
	dir := t.TempDir()
	kp := testKeyPair(t)
	l, err := New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "e1", "model_id": "caller-chosen"}, kp); !errors.Is(err, ErrModelIDOwned) {
		t.Fatalf("caller-supplied model_id must be refused, got %v", err)
	}
	if l.LastSeq() != 0 {
		t.Fatalf("refused provenance advanced ledger to %d", l.LastSeq())
	}
}

func TestPayloadMustBeJSONObject(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`"text"`)} {
		if _, err := stampModelID(raw, "configured-model"); err == nil {
			t.Errorf("stamp accepted non-object payload %s", raw)
		}
		if _, _, err := payloadModelID(raw); err == nil {
			t.Errorf("verify accepted non-object payload %s", raw)
		}
	}
}

func TestVerifyChainRejectsModelIDMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	l.SetModelID("payload-model")
	if _, err := l.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]string{"id": "e1"}, kp); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := ReadAll(path)
	if err != nil || len(events) != 1 {
		t.Fatalf("read events: %v (%d)", err, len(events))
	}
	events[0].ModelID = "different-envelope-model"
	entrySHA, err := EntrySHA256(&events[0])
	if err != nil {
		t.Fatal(err)
	}
	events[0].Signature, err = crypto.SignB64(kp, SignatureInputGold(events[0].SigAlg, events[0].SigKeyID, entrySHA))
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChain(path, map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err == nil || !strings.Contains(err.Error(), "model_id") {
		t.Fatalf("signed model_id mismatch must fail verification, got %v", err)
	}
}

// Mid-run tail verification applies the GOLD signature check, not only
// linkage and payload hashes.
func TestTailVerificationRefusesCorruptedSignature(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "l.jsonl")
	lg, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	kp, _ := crypto.GenerateKeyPair()
	for i := 0; i < 3; i++ {
		if _, err := lg.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]string{"n": "x"}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	// Leave linkage and content hashes intact; corrupt only the signature.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var evt Event
	if err := json.Unmarshal([]byte(lines[1]), &evt); err != nil {
		t.Fatal(err)
	}
	evt.Signature = "invalid"
	line, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	lines[1] = string(line)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0640); err != nil {
		t.Fatal(err)
	}

	lg2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg2.Close()
	_, err = lg2.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]string{"n": "y"}, kp)
	if !errors.Is(err, ErrTailIntegrity) {
		t.Fatalf("append onto a corrupted tail must refuse with the SAFE trigger, got %v", err)
	}
	if lg2.frozenReason == "" {
		t.Fatal("signature-corrupted tail did not freeze the ledger")
	}
}

func TestAppendRejectsAuthorKeyMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	lg, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	kp := testKeyPair(t)

	_, err = lg.Append(EventExperienceCreate, "not-the-signing-key", 3, map[string]string{"n": "x"}, kp)
	if !errors.Is(err, ErrAuthorKeyMismatch) {
		t.Fatalf("author/key mismatch must be a typed refusal, got %v", err)
	}
	if lg.LastSeq() != 0 {
		t.Fatal("author/key mismatch reached the ledger")
	}
}

func TestAppendFailureFreezesLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	lg, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.file.Close()
	lg.writer = bufio.NewWriter(failingWriter{})
	kp := testKeyPair(t)

	_, err = lg.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]string{"n": "x"}, kp)
	if !errors.Is(err, ErrAppendUncertain) {
		t.Fatalf("write failure must report uncertain append, got %v", err)
	}
	if lg.frozenReason == "" {
		t.Fatal("uncertain append did not freeze the ledger")
	}
	if _, err := lg.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]string{"n": "y"}, kp); err == nil {
		t.Fatal("ledger accepted another append after an uncertain write")
	}
}
