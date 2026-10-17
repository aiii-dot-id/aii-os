package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestOperatorActSearchUpgradeIsLosslessAndMaintained(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	add := func(turn, actor, text string) {
		t.Helper()
		if err := s.RecordToolStart(turn, 1, actor, "", "", "probe", "{}"); err != nil {
			t.Fatal(err)
		}
		if err := recordToolDone(s, turn, 1, "probe", "{}", text, interaction.Succeeded, false); err != nil {
			t.Fatal(err)
		}
	}
	add("approved", "operator", "scarletkingfisher")
	add("ordinary", "main", "scarletkingfisher")
	before := startupQueryRows(t, s, `SELECT rowid,* FROM conversations ORDER BY rowid`)
	old, err := os.ReadFile("testdata/operator-act-search-before.sql")
	if err != nil {
		t.Fatal(err)
	}
	installOld := func() {
		t.Helper()
		if _, err := s.db.Exec(`DROP TRIGGER conversations_sidecar_ai; DROP TRIGGER conversations_sidecar_ad; DROP TRIGGER conversations_sidecar_au; DROP VIEW conversations_searchable;` + string(old)); err != nil {
			t.Fatal(err)
		}
		if err := rebuildSidecars(s, "conversations_fts", "conversations_tri"); err != nil {
			t.Fatal(err)
		}
	}
	installOld()
	const query = `SELECT COUNT(*) FROM conversations_fts WHERE conversations_fts MATCH 'scarletkingfisher'`
	if got := countRows(t, s, query); got != 0 {
		t.Fatalf("historical fixture includes results: %d", got)
	}
	for cycle := 0; cycle < 2; cycle++ {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = New(path)
		if err != nil {
			t.Fatalf("normal reopen stranded the store: %v", err)
		}
		if got := countRows(t, s, query); got != 1 {
			t.Fatalf("upgrade did not reindex exactly the operator result: %d", got)
		}
		if after := startupQueryRows(t, s, `SELECT rowid,* FROM conversations ORDER BY rowid`); !reflect.DeepEqual(before, after) {
			t.Fatal("view repair changed stored history")
		}
		if bad := checkSidecarsOn(s.db); len(bad) != 0 {
			t.Fatal(bad)
		}
		if cycle == 0 {
			installOld()
		} // old-view recovery, then return to current
	}
	add("fresh", "operator", "freshkingfisher")
	if got := countRows(t, s, `SELECT COUNT(*) FROM conversations_fts WHERE conversations_fts MATCH 'freshkingfisher'`); got != 1 {
		t.Fatal("insert trigger missed a fresh result")
	}
	if _, err := s.db.Exec(`UPDATE conversations SET details=json_set(details,'$.actor','main') WHERE kind='tool_result' AND turn_id='fresh'`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, s, `SELECT COUNT(*) FROM conversations_fts WHERE conversations_fts MATCH 'freshkingfisher'`); got != 0 {
		t.Fatal("actor change retained an ineligible result")
	}
	if _, err := s.db.Exec(`UPDATE conversations SET details=json_set(details,'$.actor','operator') WHERE kind='tool_result' AND turn_id='fresh'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM conversations WHERE kind='tool_result' AND turn_id='fresh'`); err != nil {
		t.Fatal(err)
	}
	if bad := checkSidecarsOn(s.db); len(bad) != 0 {
		t.Fatal(bad)
	}
}
