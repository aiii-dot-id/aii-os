package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The harness scores the policies against fixtures and the record,
// read-only, and reports a miss by name.
func TestMemoryScoreScoresPoliciesReadOnly(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "aii.db")
	s, err := store.NewForTest(db)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	for i := 1; i <= 2; i++ {
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', '2026-09-01T00:00:00Z', 'test', 3, '{}', '')`, i)
	}
	exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES ('e1', 'the harbour bell rings at noon', 'observation', 1, 0, 'self', 1, '2026-09-01T00:00:00Z')`)
	exec(`INSERT INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at) VALUES ('e2', 'a quiet day of routine', 'observation', 1, 0, 'self', 2, '2026-09-01T00:00:00Z')`)
	if err := s.RecordMemoryAccess(time.Now(), store.MemoryRef{Store: "experiences", ID: "e1"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	fixtures := filepath.Join(dir, "fixtures.json")
	if err := os.WriteFile(fixtures, []byte(`[{"query":"harbour bell","expect":["experiences/e1"]},{"query":"routine","expect":["beliefs/none"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if rc := runMemoryScore([]string{"-db", db, "-fixtures", fixtures, "-k", "3"}, &out, &errb); rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, errb.String())
	}
	text := out.String()
	for _, want := range []string{"policy carrd — fixtures 2: hit@3 50%", "policy actr", "policy none", "miss \"routine\"", "experiences", "once", "never", "read-only"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
	if rc := runMemoryScore([]string{}, &out, &errb); rc != 2 {
		t.Fatalf("no -db must be a usage error, got %d", rc)
	}
	if rc := runMemoryScore([]string{"-db", filepath.Join(dir, "missing.db")}, &out, &errb); rc != 1 {
		t.Fatalf("a missing database must fail, got %d", rc)
	}
}

// With -eval the verb scores a frozen corpus through the facility and
// holds the run to the manifest: words alone fail the meaning threshold
// and say so with exit 3; a corpus that moved is refused with exit 1.
func TestMemoryScoreEvaluatesAFrozenCorpus(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(body))
		return hex.EncodeToString(sum[:])
	}
	corpus := write("corpus.jsonl", `{"id":"m1","lang":"en","kind":"short","text":"the harbour bell rings at noon"}`+"\n"+`{"id":"m2","lang":"en","kind":"short","text":"a quiet day of routine"}`+"\n")
	cues := write("cues.jsonl", `{"id":"c1","lang":"en","type":"meaning","text":"the chime by the water","expect":["m1"]}`+"\n")
	write("manifest.json", `{"corpus":"test","frozen":"2026-10-05","files":{"corpus.jsonl":"`+corpus+`","cues.jsonl":"`+cues+`"},"thresholds":{"k":3,"meaning_hit_at_k_min":0.8,"meaning_hit_at_k_over_words_min":0.25}}`)
	var out, errb bytes.Buffer
	if rc := runMemoryScore([]string{"-eval", dir}, &out, &errb); rc != 3 {
		t.Fatalf("words alone must not meet the meaning threshold: rc=%d\n%s%s", rc, out.String(), errb.String())
	}
	for _, want := range []string{"memory-eval — corpus \"test\" frozen 2026-10-05, manifest ", "basis words only", "NOT MET: meaning cues: hit@3 0.000 is below 0.80"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("report lacks %q:\n%s", want, out.String())
		}
	}
	write("cues.jsonl", `{"id":"c1","lang":"en","type":"meaning","text":"harbour bell","expect":["m1"]}`+"\n")
	out.Reset()
	if rc := runMemoryScore([]string{"-eval", dir}, &out, &errb); rc != 1 || !strings.Contains(errb.String(), "not the ones the manifest froze") {
		t.Fatalf("a cue rewritten after the freeze must be refused: rc=%d stderr=%s", rc, errb.String())
	}
}
