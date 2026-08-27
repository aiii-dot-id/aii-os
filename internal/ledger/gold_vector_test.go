package ledger

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// The gold-format interop vectors (James's 2026-08-17 ruling: ONE
// canonical ledger format for the Go and C stacks; Go is the reference,
// C conforms). The C implementation must reproduce these EXACT bytes —
// the canonicalization is consensus-critical, and a divergence is a
// chain split between the stacks. The same values live in
// testdata/gold_vectors.json for the C test suite to consume
// (docs/LEDGER_GOLD_FORMAT.md).
//
// The payload is chosen adversarially: a >2^53 integer (float64
// round-tripping would corrupt it — the L2 class), raw UTF-8 outside
// ASCII, nested objects with unsorted keys, an array mixing types.
const (
	vecPayload  = `{"n":9007199254740993,"id":"exp_vector_1","content":"gold vector — café ✓","nested":{"z":1,"a":[true,null,1.5]}}`
	vecPrevHash = "1f6a76673b2571ff380cbdbd267ed5da257290ba4c8f8be3aeaf7ba1a34cba55"

	vecContentHash    = "9697ffb8b1a7d6ddbac64c8b9a19023f837dabca0dd7df94f7f905c37c9c919e"
	vecCanonicalEntry = `{"author":"vector_fingerprint_0000000000000000","content_hash":"9697ffb8b1a7d6ddbac64c8b9a19023f837dabca0dd7df94f7f905c37c9c919e","model_id":"glm-5.2","payload":{"content":"gold vector — café ✓","id":"exp_vector_1","n":9007199254740993,"nested":{"a":[true,null,1.5],"z":1}},"prev_hash":"1f6a76673b2571ff380cbdbd267ed5da257290ba4c8f8be3aeaf7ba1a34cba55","ring":3,"seq":3,"timestamp":"2026-08-17T12:00:00.000000000Z","type":"experience.create"}`
	vecEntrySHA256    = "sha256:f1222c068cba934cf8da3888413c36fa201bee0809e1c2598fb562a934b06153"
	vecSignatureInput = "AII-LEDGER-LINE-SIGNATURE-GOLD\n" +
		"artifact_kind:aii.ledger.line\n" +
		"canonicalization:aii-canonical-json-v1\n" +
		"suite_id:aii-pq-mldsa87\n" +
		"role:identity\n" +
		"alg:ML-DSA-87\n" +
		"key_id:vector_fingerprint_0000000000000000\n" +
		"entry_sha256:sha256:f1222c068cba934cf8da3888413c36fa201bee0809e1c2598fb562a934b06153\n"
)

func vectorEvent() *Event {
	return &Event{
		Seq: 3, PrevHash: vecPrevHash,
		Timestamp: "2026-08-17T12:00:00.000000000Z",
		Type:      EventExperienceCreate,
		Author:    "vector_fingerprint_0000000000000000",
		Ring:      3, Payload: json.RawMessage(vecPayload),
		ContentHash: vecContentHash,
		ModelID:     "glm-5.2",
	}
}

func acceptReplay([]Event) error { return nil }

func TestGoldFormatVectors(t *testing.T) {
	evt := vectorEvent()

	if got := crypto.ContentHash(evt.Payload); got != vecContentHash {
		t.Fatalf("content_hash drifted:\n got %s\nwant %s", got, vecContentHash)
	}

	raw, err := entryForSigningJSON(evt)
	if err != nil {
		t.Fatal(err)
	}
	canon, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(canon) != vecCanonicalEntry {
		t.Fatalf("canonical entry drifted — this is a CHAIN SPLIT with any conforming implementation:\n got %s\nwant %s", canon, vecCanonicalEntry)
	}

	sha, err := EntrySHA256(evt)
	if err != nil {
		t.Fatal(err)
	}
	if sha != vecEntrySHA256 {
		t.Fatalf("entry_sha256 drifted:\n got %s\nwant %s", sha, vecEntrySHA256)
	}

	if got := string(SignatureInputGold("ML-DSA-87", evt.Author, sha)); got != vecSignatureInput {
		t.Fatalf("signature input drifted:\n got %q\nwant %q", got, vecSignatureInput)
	}

	// The checked-in vector file (what the C suite consumes) must agree
	// with the constants this suite enforces — one set of vectors, two
	// implementations.
	data, err := os.ReadFile(filepath.Join("testdata", "gold_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vf struct {
		Payload        string `json:"payload"`
		ContentHash    string `json:"content_hash"`
		CanonicalEntry string `json:"canonical_entry"`
		EntrySHA256    string `json:"entry_sha256"`
		SignatureInput string `json:"signature_input"`
	}
	if err := json.Unmarshal(data, &vf); err != nil {
		t.Fatal(err)
	}
	if vf.Payload != vecPayload || vf.ContentHash != vecContentHash ||
		vf.CanonicalEntry != vecCanonicalEntry || vf.EntrySHA256 != vecEntrySHA256 ||
		vf.SignatureInput != vecSignatureInput {
		t.Fatal("testdata/gold_vectors.json disagrees with the pinned constants — regenerate BOTH together, never one")
	}
}

// Rewrap: the routine pre-gold format transition (no eras, no
// migration). A ledger whose signatures are garbage — the exact state
// of any test ledger after a format change — rewraps into a fully
// verifying gold chain; a ledger whose HISTORY is corrupted is refused
// untouched.
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
	for i := 0; i < 3; i++ {
		if _, err := lg.Append(EventExperienceCreate, kp.Fingerprint(), 3,
			map[string]interface{}{"id": "exp_rw", "content": "event", "i": i}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a superseded format: scramble every signature.
	raw, _ := os.ReadFile(path)
	scrambled := strings.ReplaceAll(string(raw), `"signature":"`, `"signature":"XX`)
	if err := os.WriteFile(path, []byte(scrambled), 0600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChain(path, map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err == nil {
		t.Fatal("scrambled chain must not verify (precondition)")
	}

	n, err := Rewrap(path, kp, "", acceptReplay)
	if err != nil {
		t.Fatalf("rewrap: %v", err)
	}
	if n != 3 {
		t.Fatalf("rewrapped %d events, want 3", n)
	}
	if err := VerifyChain(path, map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err != nil {
		t.Fatalf("rewrapped chain must verify: %v", err)
	}

	// A foreign key is refused while the history is otherwise valid:
	// someone else's record cannot be adopted by re-signing it.
	otherKP, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Rewrap(path, otherKP, "", acceptReplay); err == nil {
		t.Fatal("rewrap must refuse a ledger signed by a different key")
	}

	// Corrupted HISTORY is refused, original untouched.
	raw, _ = os.ReadFile(path)
	lines := strings.Split(string(raw), "\n")
	i := strings.Index(lines[0], `"content_hash":"`)
	j := i + len(`"content_hash":"`)
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
	if _, err := lg.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"name": "locked"}, kp); err != nil {
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
	if _, err := source.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"name": "source"}, kp); err != nil {
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
	if _, err := lg.Append(EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"name": "source"}, kp); err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Rewrap(sourcePath, kp, outputPath, acceptReplay); err != nil {
		t.Fatal(err)
	}
	if err := VerifyChain(outputPath, map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err != nil {
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
	events[0].Ring = 2
	invalid, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	invalid = append(invalid, '\n')
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

// Ring authority is a closed table beside the closed vocabulary (canon
// §11, 2026-08-18 ring enforcement): every event type has canonical
// rings; an unknown type has none. Drift-gated like the materializer
// table — adding a type without its authority entry fails here.
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
