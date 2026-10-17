package store

import (
	"crypto/sha256"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"os"
	"path/filepath"
	"testing"
)

func TestInteractionHistoricalReadOnlyWithoutMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	raw := openRawForTest(t, path)
	ddl, err := os.ReadFile("testdata/schema-pre-work-queue.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at) VALUES('old','session','operator','recorded fact',1,'bad date')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := s.RecentTurns(20)
	if err != nil || len(turns) != 1 {
		t.Fatal(turns, err)
	}
	if results, more, err := s.OperatorActResults(t.Context(), 0, nil); err != nil || more || len(results) != 0 {
		t.Fatalf("historical read invented operator acts or demanded migration: %+v %v %v", results, more, err)
	}
	page, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.Rows[0].Kind != interaction.Legacy || page.Rows[0].RecordedAt != "" || page.Rows[0].CreatedAt != "bad date" || page.Availability != "historical_schema" {
		t.Fatalf("invented history: %+v", page)
	}
	oldScope, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1, Filter: interaction.Filter{WorkSession: "old-root", IncludeDescendants: true}})
	if err != nil || oldScope.Availability != "historical_schema" || len(oldScope.Rows) != 0 {
		t.Fatalf("missing ancestry tables: %+v %v", oldScope, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("SAFE read changed bytes", err)
	}
}
