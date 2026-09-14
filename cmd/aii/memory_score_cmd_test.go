package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
func TestMemoryScoreScoresPoliciesReadOnly(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "aii.db")
	s, err := store.New(db)
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
		exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (?, '', '2026-09-01T00:00:00Z', 'test', 3, '{}', '', '')`, i)
	}
	exec(`INSERT INTO experiences (id, content, category, raw, private, provenance, created_seq, created_at) VALUES ('e1', 'the harbour bell rings at noon', 'observation', 1, 0, 'self', 1, '2026-09-01T00:00:00Z')`)
	exec(`INSERT INTO experiences (id, content, category, raw, private, provenance, created_seq, created_at) VALUES ('e2', 'a quiet day of routine', 'observation', 1, 0, 'self', 2, '2026-09-01T00:00:00Z')`)
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
