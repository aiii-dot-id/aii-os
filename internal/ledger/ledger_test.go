package ledger

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
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
	payload := map[string]any{"name": "TestIdentity", "pubkey": kp.PublicKeyB64()}
	evt1, err := l.Append(EventRing0Genesis, 0, payload, kp)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	if evt1.Seq != 1 {
		t.Errorf("first event seq = %d, want 1", evt1.Seq)
	}
	if evt1.Prev != "" {
		t.Errorf("genesis prev = %q, want empty", evt1.Prev)
	}
	if evt1.Sig == "" {
		t.Error("a tail record carries its proof")
	}

	// Append second event
	payload2 := map[string]any{"statement": "I am a test identity", "confidence": 0.9}
	evt2, err := l.Append(EventBeliefUpsert, 3, payload2, kp)
	if err != nil {
		t.Fatalf("Append 2 failed: %v", err)
	}

	if evt2.Seq != 2 {
		t.Errorf("second event seq = %d, want 2", evt2.Seq)
	}
	if evt2.Prev != evt1.EntryHash() {
		t.Errorf("second event prev = %q, want the first record's entry hash %q", evt2.Prev, evt1.EntryHash())
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

	if events[1].Prev != events[0].EntryHash() {
		t.Error("chain link broken in readback")
	}
}

func TestChainState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	// Write 3 events
	l, _ := New(path)
	l.Append(EventRing0Genesis, 0, map[string]string{"a": "1"}, kp)
	l.Append(EventBeliefUpsert, 3, map[string]string{"b": "2"}, kp)
	evt3, _ := l.Append(EventSelfModelSynthesize, 3, map[string]string{"c": "3"}, kp)
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
	if l2.LastHash() != evt3.EntryHash() {
		t.Errorf("after reopen LastHash mismatch")
	}
}

func TestVerifyChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, _ := New(path)
	l.Append(EventRing0Genesis, 0, map[string]string{"name": "test"}, kp)
	l.Append(EventBeliefUpsert, 3, map[string]string{"x": "y"}, kp)
	l.Append(EventSelfModelSynthesize, 3, map[string]string{"z": "w"}, kp)
	l.Close()

	// Verify with correct key
	_, err := VerifyChain(path, kp.PublicKey, nil)
	if err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}

	// Verify with wrong key should fail
	wrongKp, _ := crypto.GenerateKeyPair()
	_, err = VerifyChain(path, wrongKp.PublicKey, nil)
	if err == nil {
		t.Error("VerifyChain should fail with wrong key")
	}
}

func TestTamperDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, _ := New(path)
	l.Append(EventRing0Genesis, 0, map[string]string{"name": "test"}, kp)
	l.Append(EventBeliefUpsert, 3, map[string]string{"x": "y"}, kp)
	l.Close()

	// Tamper: append a fake event with bad signature
	data, _ := os.ReadFile(path)
	// Append garbage
	data = append(data, []byte(`{"seq":3,"prev_hash":"bad","type":"belief.upsert","author":"bad","ring":3,"payload":"{}","content_hash":"bad","signature":"bad","sig_alg":"ML-DSA-87","sig_key_id":"bad"}`+"\n")...)
	os.WriteFile(path, data, 0640)

	_, err := VerifyChain(path, kp.PublicKey, nil)
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
		_, err := l.Append(EventExperienceCreate, 3, map[string]int{"i": i}, kp)
		if err != nil {
			t.Fatalf("append %d failed: %v", i, err)
		}
	}

	if l.LastSeq() != 10 {
		t.Errorf("LastSeq = %d, want 10", l.LastSeq())
	}

	// Verify chain
	if _, err := VerifyChain(path, kp.PublicKey, nil); err != nil {
		t.Fatalf("VerifyChain failed: %v", err)
	}
}

// Model provenance: model_id is stamped into the payload BEFORE hashing,
// so the proof covers it through content. Tampering the payload's
// model_id must break the content hash. Provenance lives in the payload
// only; the record carries no copy of it.
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

	evt, err := l.Append(EventBeliefUpsert, 3,
		map[string]any{"id": "b1", "statement": "provenance matters"}, kp)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// 1. Payload carries the stamp
	var p map[string]any
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		t.Fatalf("payload not JSON object: %v", err)
	}
	if p["model_id"] != "test-model-v1" {
		t.Errorf("payload model_id = %v, want test-model-v1", p["model_id"])
	}

	// 3. Content hash covers the stamp: forge a different model, hash breaks
	forged := make(map[string]any)
	for k, v := range p {
		forged[k] = v
	}
	forged["model_id"] = "honest-model-v2"
	fb, _ := json.Marshal(forged)
	if crypto.ContentHash(fb) == evt.Content {
		t.Error("forged model_id produces same content hash — stamp is NOT signed")
	}
	if crypto.ContentHash(evt.Payload) != evt.Content {
		t.Error("payload as-written does not reproduce its own content hash")
	}

	// 3. The proof verifies over the GOLD envelope under the chain's key
	events, _ := ReadAll(path)
	if len(events) != 1 {
		t.Fatalf("ReadAll = %d events, want 1", len(events))
	}
	signMsg := SignatureInputGold(crypto.SigAlg, kp.Fingerprint(), events[0].EntryHash())
	sig, err := base64.StdEncoding.DecodeString(events[0].Sig)
	if err != nil {
		t.Fatalf("the proof is not base64: %v", err)
	}
	if err := crypto.Verify(kp.PublicKey, signMsg, sig); err != nil {
		t.Errorf("signature does not verify: %v", err)
	}
}

func TestEnvelopeTamperDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp := testKeyPair(t)

	l, _ := New(path)
	l.Append(EventRing0Genesis, 0, map[string]string{"name": "t"}, kp)
	if _, err := l.Append(EventBeliefUpsert, 3, map[string]string{"id": "b1", "statement": "s"}, kp); err != nil {
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

	if _, err := VerifyChain(path, kp.PublicKey, nil); err == nil {
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
	if _, err := l.Append(EventRing0Genesis, 0, map[string]string{"name": "t"}, kp); err != nil {
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
	if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"note": "x"}, kp); !errors.Is(err, ErrTailIntegrity) {
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

	evt, err := l.Append(EventBeliefUpsert, 3,
		map[string]any{"id": "b1", "statement": "x"}, kp)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	var p map[string]any
	json.Unmarshal(evt.Payload, &p)
	if _, ok := p["model_id"]; ok {
		t.Error("model_id stamped without SetModelID — should be absent")
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
	if _, err := l.Append(EventExperienceCreate, 3,
		map[string]any{"id": "e1", "model_id": "caller-chosen"}, kp); !errors.Is(err, ErrModelIDOwned) {
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
		if _, err := lg.Append(EventExperienceCreate, 3, map[string]string{"n": "x"}, kp); err != nil {
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
	evt.Sig = "invalid"
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
	_, err = lg2.Append(EventExperienceCreate, 3, map[string]string{"n": "y"}, kp)
	if !errors.Is(err, ErrTailIntegrity) {
		t.Fatalf("append onto a corrupted tail must refuse with the SAFE trigger, got %v", err)
	}
	if lg2.frozenReason == "" {
		t.Fatal("signature-corrupted tail did not freeze the ledger")
	}
}

func TestAppendFailureFreezesLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	lg, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	// The whole ledger, not only its file: the directory lock is a handle
	// too, and Windows cannot remove the directory while it is open. Close
	// reports the failing writer's flush and closes everything regardless.
	defer func() { _ = lg.Close() }()
	lg.writer = bufio.NewWriter(failingWriter{})
	kp := testKeyPair(t)

	_, err = lg.Append(EventExperienceCreate, 3, map[string]string{"n": "x"}, kp)
	if !errors.Is(err, ErrAppendUncertain) {
		t.Fatalf("write failure must report uncertain append, got %v", err)
	}
	if lg.frozenReason == "" {
		t.Fatal("uncertain append did not freeze the ledger")
	}
	if _, err := lg.Append(EventExperienceCreate, 3, map[string]string{"n": "y"}, kp); err == nil {
		t.Fatal("ledger accepted another append after an uncertain write")
	}
}

func TestTheChainBindsWholeEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"id": "e", "n": strings.Repeat("x", i+1)}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var evt Event
	if err := json.Unmarshal([]byte(lines[1]), &evt); err != nil {
		t.Fatal(err)
	}
	evt.Ring = 2 // the key holder rewrites one field of history...
	evt.Sig, err = crypto.SignB64(kp, SignatureInputGold(crypto.SigAlg, kp.Fingerprint(), evt.EntryHash()))
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(evt) // ...and re-signs it with the real key.
	if err != nil {
		t.Fatal(err)
	}
	lines[1] = string(line)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyChain(path, kp.PublicKey, nil)
	if err == nil {
		t.Fatal("a re-signed rewrite of an interior field verified — the chain does not bind entries")
	}
	// Record 3, by its seq. This used to read "event 2": the verifier
	// wrote its zero-based count of records already verified and called
	// it the event.
	var failure *VerifyFailure
	if !errors.As(err, &failure) || failure.Seq != 3 || !strings.Contains(err.Error(), "record 3 (ledger.jsonl): prev mismatch") {
		t.Fatalf("the break must be the CHAIN at the next record, named by its seq, got: %v", err)
	}
	// And the rewritten record 2 is PROVED: the key holder re-signed it,
	// and a proof that verifies proves its prefix. That is what the key
	// can do, and why a witness exists.
	if failure.Proved.Seq != 2 || failure.Traversed.Seq != 2 {
		t.Fatalf("proved %d, traversed %d, want both 2", failure.Proved.Seq, failure.Traversed.Seq)
	}
}

// The bytes at rest are the canonical form: a payload with a space, or
// an entry with its members out of order, is refused by every reader.
func TestStoredBytesAreTheCanonicalForm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"id": "e", "b": "2", "a": "1"}, kp); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.Contains(line, `"payload":{"a":"1","b":"2","id":"e"}`) {
		t.Fatalf("the door did not store the canonical payload form: %s", line)
	}
	cases := map[string]string{
		"payload with whitespace":    strings.Replace(line, `"a":"1"`, `"a": "1"`, 1),
		"entry members out of order": strings.Replace(line, `{"content":`, `{"seq":1,"content":`, 1),
	}
	for name, bad := range cases {
		if err := os.WriteFile(path, []byte(bad+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadAll(path); err == nil {
			t.Errorf("%s: read accepted bytes that are not the stored form", name)
		}
	}
}

// The entry bytes this package writes by hand are exactly what the
// canonicalizer produces, so the chain hash has one definition and a
// foreign verifier may use either.
func TestEntryBytesAreCanonical(t *testing.T) {
	evt := &Event{Seq: 1042, Prev: strings.Repeat("ab", 32), Timestamp: "2026-09-03T14:02:11.482913Z",
		Type: EventBeliefUpsert, Ring: 3, Content: strings.Repeat("cd", 32)}
	want := `{"content":"` + strings.Repeat("cd", 32) + `","prev":"` + strings.Repeat("ab", 32) +
		`","ring":3,"seq":1042,"ts":"2026-09-03T14:02:11.482913Z","type":"belief.upsert"}`
	if got := string(evt.EntryBytes()); got != want {
		t.Fatalf("entry bytes:\n got %s\nwant %s", got, want)
	}
	canon, err := canonicaljson.CanonicalizeV1(evt.EntryBytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(canon) != want {
		t.Fatalf("the canonicalizer disagrees with EntryBytes:\n got %s\nwant %s", canon, want)
	}
}

// A record without its proof is refused by the chain verifier: the only
// place a proof may be absent is inside a sealed segment, whose head
// vouches for it, and that rule belongs to the container reader, not
// here. Dropping the refusal would let an unsigned line pass as history.
func TestAnUnsignedRecordIsRefusedByTheVerifier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp := testKeyPair(t)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := l.Append(EventExperienceCreate, 3, map[string]string{"id": "e", "i": strings.Repeat("x", i+1)}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var evt Event
	if err := json.Unmarshal([]byte(lines[1]), &evt); err != nil {
		t.Fatal(err)
	}
	evt.Sig = ""
	line, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	lines[1] = string(line)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = VerifyChain(path, kp.PublicKey, nil)
	if !errors.Is(err, ErrUnsignedRecord) {
		t.Fatalf("an unsigned tail record must be refused with ErrUnsignedRecord, got %v", err)
	}
}
