package ledger

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const (
	vecPayload        = `{"content":"gold vector — café ✓","id":"exp_vector_1","n":9007199254740993,"nested":{"a":[true,null,1.5],"z":1}}`
	vecPayloadRaw     = `{"n":9007199254740993,"id":"exp_vector_1","content":"gold vector — café ✓","nested":{"z":1,"a":[true,null,1.5]}}`
	vecPrev           = "1f6a76673b2571ff380cbdbd267ed5da257290ba4c8f8be3aeaf7ba1a34cba55"
	vecContentHash    = "a2385272909d5729b3b320ad6548f1bfd96144c9de0731277dedfa3a08055967"
	vecEntryBytes     = `{"content":"a2385272909d5729b3b320ad6548f1bfd96144c9de0731277dedfa3a08055967","prev":"1f6a76673b2571ff380cbdbd267ed5da257290ba4c8f8be3aeaf7ba1a34cba55","ring":3,"seq":3,"ts":"2026-08-17T12:00:00.000000000Z","type":"experience.create"}`
	vecEntryHash      = "dd71f319ffc1b8b6b1c44ae07ebf6e9a54d74bbcb44bb50801aab31a604763ce"
	vecSignatureInput = "AII-LEDGER-LINE-SIGNATURE-GOLD\n" +
		"artifact_kind:aii.ledger.line\n" +
		"canonicalization:aii-canonical-json\n" +
		"suite_id:aii-pq-mldsa87\n" +
		"role:identity\n" +
		"alg:ML-DSA-87\n" +
		"key_id:vector_fingerprint_0000000000000000\n" +
		"entry_sha256:dd71f319ffc1b8b6b1c44ae07ebf6e9a54d74bbcb44bb50801aab31a604763ce\n"
	vecLine = `{"entry":` + vecEntryBytes + `,"payload":` + vecPayload + `}`
)

func vectorEvent() *Event {
	return &Event{
		Seq: 3, Prev: vecPrev,
		Timestamp: "2026-08-17T12:00:00.000000000Z",
		Type:      EventExperienceCreate,
		Ring:      3, Payload: json.RawMessage(vecPayload),
		Content: vecContentHash,
	}
}

func acceptReplay(string) error { return nil }

// .
// .
func genesisPayload(kp *crypto.KeyPair, name string) map[string]interface{} {
	return map[string]interface{}{"name": name, "public_key": kp.PublicKeyB64(), "fingerprint": kp.Fingerprint()}
}

func TestGoldFormatVectors(t *testing.T) {
	evt := vectorEvent()

	// .
	canon, err := canonicaljson.CanonicalizeV1([]byte(vecPayloadRaw))
	if err != nil {
		t.Fatal(err)
	}
	if string(canon) != vecPayload {
		t.Fatalf("canonical payload drifted — this is a CHAIN SPLIT with any conforming implementation:\n got %s\nwant %s", canon, vecPayload)
	}
	if got := crypto.ContentHash(evt.Payload); got != vecContentHash {
		t.Fatalf("content hash drifted:\n got %s\nwant %s", got, vecContentHash)
	}
	if got := string(evt.EntryBytes()); got != vecEntryBytes {
		t.Fatalf("entry bytes drifted:\n got %s\nwant %s", got, vecEntryBytes)
	}
	if got := evt.EntryHash(); got != vecEntryHash {
		t.Fatalf("entry hash drifted:\n got %s\nwant %s", got, vecEntryHash)
	}
	if got := string(SignatureInputGold("ML-DSA-87", "vector_fingerprint_0000000000000000", evt.EntryHash())); got != vecSignatureInput {
		t.Fatalf("signature input drifted:\n got %q\nwant %q", got, vecSignatureInput)
	}
	line, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != vecLine {
		t.Fatalf("record line drifted:\n got %s\nwant %s", line, vecLine)
	}

	// .
	// .
	data, err := os.ReadFile(filepath.Join("testdata", "gold_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vf struct {
		PayloadRaw     string `json:"payload_as_given"`
		Payload        string `json:"payload"`
		ContentHash    string `json:"content"`
		EntryBytes     string `json:"entry_bytes"`
		EntryHash      string `json:"entry_hash"`
		SignatureInput string `json:"signature_input"`
		Line           string `json:"line"`
	}
	if err := json.Unmarshal(data, &vf); err != nil {
		t.Fatal(err)
	}
	if vf.PayloadRaw != vecPayloadRaw || vf.Payload != vecPayload || vf.ContentHash != vecContentHash ||
		vf.EntryBytes != vecEntryBytes || vf.EntryHash != vecEntryHash ||
		vf.SignatureInput != vecSignatureInput || vf.Line != vecLine {
		t.Fatal("testdata/gold_vectors.json disagrees with the pinned constants — regenerate BOTH together, never one")
	}
}

// .
// .
// .
// .
// .
func TestRewrapRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	lg, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(EventRing0Genesis, kp.Fingerprint(), 0, genesisPayload(kp, "rewrapped"), kp); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := lg.Append(EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]interface{}{"id": "exp_rw", "content": "event", "i": i}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	// .
	raw, _ := os.ReadFile(path)
	scrambled := strings.ReplaceAll(string(raw), `"sig":"`, `"sig":"XX`)
	if err := os.WriteFile(path, []byte(scrambled), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err == nil {
		t.Fatal("scrambled chain must not verify (precondition)")
	}

	n, err := Rewrap(path, kp, "", acceptReplay)
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if n != 4 {
		t.Fatalf("rewrapped %d events, want 4", n)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err != nil {
		t.Fatalf("rewrapped chain must verify: %v", err)
	}

	// .
	// .
	// .
	otherKP, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rewrap(path, otherKP, "", acceptReplay); err == nil {
		t.Fatal("rewrap must refuse a ledger signed by a different key")
	}

	// .
	raw, _ = os.ReadFile(path)
	lines := strings.Split(string(raw), "\n")
	i := strings.Index(lines[0], `{"content":"`)
	j := i + len(`{"content":"`)
	flip := byte('0')
	if lines[0][j] == '0' {
		flip = '1'
	}
	lines[0] = lines[0][:j] + string(flip) + lines[0][j+1:]
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Rewrap(path, kp, "", acceptReplay); err == nil {
		t.Fatal("rewrap must refuse a corrupted chain — it re-signs history, never repairs it")
	}
}

func TestRewrapRefusesOpenLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	if _, err := lg.Append(EventRing0Genesis, kp.Fingerprint(), 0, genesisPayload(kp, "locked"), kp); err != nil {
		t.Fatal(err)
	}

	if _, err := Rewrap(path, kp, "", acceptReplay); !errors.Is(err, ErrLedgerInUse) {
		t.Fatalf("rewrap must not replace a live ledger, got %v", err)
	}
}

func TestRewrapRefusesOpenOutputLedger(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.jsonl")
	outputPath := filepath.Join(dir, "output.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	source, err := New(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Append(EventRing0Genesis, kp.Fingerprint(), 0, genesisPayload(kp, "source"), kp); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := New(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })

	if _, err := Rewrap(sourcePath, kp, outputPath, acceptReplay); !errors.Is(err, ErrLedgerInUse) {
		t.Fatalf("rewrap must not replace a live output ledger, got %v", err)
	}
}

func TestRewrapPublishesNewOutput(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.jsonl")
	outputPath := filepath.Join(dir, "output.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := New(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(EventRing0Genesis, kp.Fingerprint(), 0, genesisPayload(kp, "source"), kp); err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Rewrap(sourcePath, kp, outputPath, acceptReplay); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyChain(outputPath, kp.PublicKeyBytes(), nil); err != nil {
		t.Fatalf("published output does not verify: %v", err)
	}
}

func TestRewrapRefusesNoncanonicalRing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(EventRing0Genesis, kp.Fingerprint(), 0, genesisPayload(kp, "ring"), kp); err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(EventExperienceCreate, kp.Fingerprint(), 3, map[string]string{"id": "exp", "content": "x"}, kp); err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	events[1].Ring = 2
	first, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(events[1])
	if err != nil {
		t.Fatal(err)
	}
	invalid := append(append(append(first, '\n'), second...), '\n')
	if err := os.WriteFile(path, invalid, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := Rewrap(path, kp, "", acceptReplay); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("rewrap must refuse a noncanonical ring, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(invalid) {
		t.Fatal("refused rewrap changed the ledger")
	}
}

// .
// .
// .
// .
func TestCanonicalRingsCoverVocabulary(t *testing.T) {
	for _, et := range AllEventTypes() {
		if len(CanonicalRings(et)) == 0 {
			t.Errorf("%s has no canonical ring — every type in the vocabulary needs its authority entry", et)
		}
	}
	if CanonicalRings(EventType("made.up")) != nil {
		t.Error("unknown types must have NO legal ring (fail closed)")
	}
}

// .
// .
// .
// .
// .
func TestRewrapReadsThePriorShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	prev := ""
	for i := 1; i <= 3; i++ {
		payload := `{"id":"exp_prior","content":"written before the record","i":` + strconv.Itoa(i) + `,"model_id":"glm-5.2"}`
		ch := crypto.ContentHash([]byte(payload))
		line := `{"seq":` + strconv.Itoa(i) + `,"prev_hash":"` + prev + `","timestamp":"2026-08-20T00:00:0` + strconv.Itoa(i) + `Z","type":"experience.create","author":"` + kp.Fingerprint() +
			`","ring":3,"payload":` + payload + `,"content_hash":"` + ch + `","signature":"prior","sig_alg":"ML-DSA-87","sig_key_id":"` + kp.Fingerprint() + `","model_id":"glm-5.2"}`
		lines = append(lines, line)
		prev = ch
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAll(path); err == nil {
		t.Fatal("the prior shape must not read as the record (precondition)")
	}

	n, err := Rewrap(path, kp, "", acceptReplay)
	if err != nil {
		t.Fatalf("rewrap of the prior shape: %v", err)
	}
	if n != 3 {
		t.Fatalf("rewrapped %d events, want 3", n)
	}
	if _, err := VerifyChain(path, kp.PublicKeyBytes(), nil); err != nil {
		t.Fatalf("rewrapped chain must verify: %v", err)
	}
	events, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(events[1].Payload), `{"content":"written before the record","i":2,"id":"exp_prior","model_id":"glm-5.2"}`) {
		t.Fatalf("payload not carried in canonical form: %s", events[1].Payload)
	}
	if events[1].Prev != events[0].EntryHash() {
		t.Fatal("rewrap did not relink the chain over entry hashes")
	}

	// .
	other, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Rewrap(path, other, "", acceptReplay); err == nil {
		t.Fatal("rewrap adopted another key's prior-shape history")
	}
}
