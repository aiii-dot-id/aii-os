package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .
// .

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func addPluginMemory(t *testing.T, s *Store, id, text string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().Exec(
		`INSERT INTO plugin_memories (id, plugin_id, text, created_at, updated_at) VALUES (?, 'id.test', ?, ?, ?)`,
		id, text, now, now); err != nil {
		t.Fatal(err)
	}
}

const (
	pmFTS = `SELECT COUNT(*) FROM plugin_memories_fts f JOIN plugin_memories b ON b.rowid = f.rowid WHERE plugin_memories_fts MATCH ?`
	pmTri = `SELECT COUNT(*) FROM plugin_memories_tri f JOIN plugin_memories b ON b.rowid = f.rowid WHERE plugin_memories_tri MATCH ?`
)

func TestSidecarsFollowTheirBaseThroughEveryWrite(t *testing.T) {
	s := testStore(t)
	addPluginMemory(t, s, "m1", "The café serves résumé-quality espresso")

	if n := countRows(t, s, pmFTS, "cafe"); n != 1 {
		t.Fatalf("exact-words sidecar must fold diacritics and find the row: %d", n)
	}
	if n := countRows(t, s, pmTri, "spress"); n != 1 {
		t.Fatalf("trigram sidecar must find a substring: %d", n)
	}
	if _, err := s.DB().Exec(`UPDATE plugin_memories SET text = 'a different memory entirely' WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, pmFTS, "cafe"); n != 0 {
		t.Fatalf("the old text must leave the index on update: %d", n)
	}
	if n := countRows(t, s, pmFTS, "different"); n != 1 {
		t.Fatalf("the new text must enter the index on update: %d", n)
	}
	if _, err := s.DB().Exec(`DELETE FROM plugin_memories WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, pmFTS, "different"); n != 0 {
		t.Fatalf("a deleted row must leave the index: %d", n)
	}
	if failed := s.CheckSidecars(); len(failed) != 0 {
		t.Fatalf("after insert, update and delete the sidecars disagree with their base: %v", failed)
	}
	states, err := s.SidecarStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != len(Sidecars) {
		t.Fatalf("SidecarStates reported %d of %d sidecars", len(states), len(Sidecars))
	}
	for _, st := range states {
		if !st.Ready() {
			t.Errorf("%s: index %d rows, content %d", st.Name, st.Indexed, st.Rows)
		}
	}
}

// .
// .
func TestSystemTurnsNeverEnterTheConversationSidecar(t *testing.T) {
	s := testStore(t)
	if err := s.AddConversationTurn("system", "tool output mentioning espresso"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddConversationTurn("operator", "an espresso, please"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddConversationTurn("resident", "the espresso conversation, remembered"); err != nil {
		t.Fatal(err)
	}
	const q = `SELECT COUNT(*) FROM conversations_fts f JOIN conversations b ON b.rowid = f.rowid WHERE conversations_fts MATCH ? AND b.role = ?`
	if n := countRows(t, s, q, "espresso", "system"); n != 0 {
		t.Fatalf("a system turn is in the sidecar: %d", n)
	}
	if n := countRows(t, s, q, "espresso", "operator"); n != 1 {
		t.Fatalf("the operator's turn is not in the sidecar: %d", n)
	}
	if n := countRows(t, s, q, "espresso", "resident"); n != 1 {
		t.Fatalf("the resident's turn is not in the sidecar: %d", n)
	}
	states, err := s.SidecarStates()
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range states {
		if st.Name == "conversations_fts" && (st.Rows != 2 || st.Indexed != 2) {
			t.Fatalf("conversations_fts: index %d, content %d; want 2 and 2 (the system turn excluded)", st.Indexed, st.Rows)
		}
	}
	if failed := s.CheckSidecars(); len(failed) != 0 {
		t.Fatalf("sidecars disagree with the view: %v", failed)
	}
}

func replayFixture(t *testing.T) (dbPath, ledgerPath string, s *Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "aii.db")
	ledgerPath = filepath.Join(dir, "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	evt1, err := lg.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0, map[string]string{"name": "test"}, kp)
	if err != nil {
		t.Fatal(err)
	}
	evt2, err := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]string{"id": "e1", "content": "the lighthouse keeper kept a ledger of every ship", "category": "observation"}, kp)
	if err != nil {
		t.Fatal(err)
	}
	lg.Close()
	s, err = New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, evt := range []*ledger.Event{evt1, evt2} {
		if err := s.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	return dbPath, ledgerPath, s
}

const expFTS = `SELECT COUNT(*) FROM experiences_fts f JOIN experiences b ON b.rowid = f.rowid WHERE experiences_fts MATCH ?`

// .
// .
// .
// .
func TestReplayRebuildsTheSidecarsOfDerivedTablesAndLeavesAccessAlone(t *testing.T) {
	_, ledgerPath, s := replayFixture(t)
	defer s.Close()
	if n := countRows(t, s, expFTS, "lighthouse"); n != 1 {
		t.Fatalf("the materialized experience is not indexed: %d", n)
	}
	when := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	if err := s.RecordMemoryAccess(when, MemoryRef{Store: "experiences", ID: "e1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplayFromFile(ledgerPath); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, expFTS, "lighthouse"); n != 1 {
		t.Fatalf("after replay the experience is indexed %d times, want 1", n)
	}
	if failed := s.CheckSidecars(); len(failed) != 0 {
		t.Fatalf("after replay the sidecars disagree with their bases: %v", failed)
	}
	a, ok, err := s.MemoryAccessOf(MemoryRef{Store: "experiences", ID: "e1"})
	if err != nil || !ok || a.Count != 1 {
		t.Fatalf("replay touched the access record: %+v ok=%v err=%v", a, ok, err)
	}
}

// .
// .
// .
// .
// .
func TestInsertOrReplaceKeepsTheSidecarHonest(t *testing.T) {
	_, _, s := replayFixture(t)
	defer s.Close()
	if _, err := s.DB().Exec(
		`INSERT OR REPLACE INTO experiences (id, content, category, raw, private, provenance, created_seq, created_at)
		 VALUES ('e1', 'the harbour master replaced the lighthouse keeper', NULL, 1, 0, 'self', 2, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, expFTS, "ship"); n != 0 {
		t.Fatalf("the replaced row's old text is still indexed: %d", n)
	}
	if n := countRows(t, s, expFTS, "harbour"); n != 1 {
		t.Fatalf("the replacing row is not indexed: %d", n)
	}
	if failed := s.CheckSidecars(); len(failed) != 0 {
		t.Fatalf("after INSERT OR REPLACE the sidecars disagree: %v", failed)
	}
}

// .
// .
func TestBootRebuildsASidecarOlderThanItsBase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"first memory of the sea", "second memory of the mountain", "third memory of the sea again"} {
		addPluginMemory(t, s, "m"+string(rune('1'+i)), text)
	}
	// .
	if _, err := s.DB().Exec(`DROP TABLE plugin_memories_fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DROP TABLE plugin_memories_tri`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := New(path)
	if err != nil {
		t.Fatalf("a database older than its sidecars must boot: %v", err)
	}
	defer s2.Close()
	if n := countRows(t, s2, pmFTS, "sea"); n != 2 {
		t.Fatalf("readiness did not rebuild the sidecar: %d rows match, want 2", n)
	}
	if n := countRows(t, s2, pmTri, "mountai"); n != 1 {
		t.Fatalf("readiness did not rebuild the trigram sidecar: %d", n)
	}
	if failed := s2.CheckSidecars(); len(failed) != 0 {
		t.Fatalf("after the rebuild the sidecars disagree: %v", failed)
	}
}

// .
// .
// .
func TestHousekeepRepairsASidecarThatDriftedPastItsTriggers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addPluginMemory(t, s, "m1", "the original text")
	if _, err := s.DB().Exec(`DROP TRIGGER plugin_memories_sidecar_au`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE plugin_memories SET text = 'drifted text the index never saw' WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	failed := s.CheckSidecars()
	if strings.Join(failed, " ") != "plugin_memories_fts plugin_memories_tri" {
		t.Fatalf("the check must name exactly the drifted sidecars: %v", failed)
	}
	report, err := s.Housekeep()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, "sidecars rebuilt after a failed integrity check: plugin_memories_fts plugin_memories_tri") {
		t.Fatalf("Housekeep did not report the repair: %q", report)
	}
	if failed := s.CheckSidecars(); len(failed) != 0 {
		t.Fatalf("after Housekeep the sidecars still disagree: %v", failed)
	}
	if n := countRows(t, s, pmFTS, "drifted"); n != 1 {
		t.Fatalf("the repaired index does not hold the drifted text: %d", n)
	}
	// .
	report, err = s.Housekeep()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report, "sidecar") {
		t.Fatalf("a clean check must stay silent: %q", report)
	}
}

// .
// .
// .
// .
func TestReconcileRestoresTriggersAndRowidsWhenItRebuildsABase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	addPluginMemory(t, s, "m1", "alpha memory")
	addPluginMemory(t, s, "m2", "beta memory")
	addPluginMemory(t, s, "m3", "gamma memory")
	// .
	if _, err := s.DB().Exec(`DELETE FROM plugin_memories WHERE id = 'm2'`); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	if _, err := s.DB().Exec(`ALTER TABLE plugin_memories DROP COLUMN project`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := New(path)
	if err != nil {
		t.Fatalf("the rebuilt base must pass the audit: %v", err)
	}
	defer s2.Close()
	if n := countRows(t, s2, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'plugin_memories'`); n != 3 {
		t.Fatalf("the rebuilt base has %d triggers, want 3", n)
	}
	if failed := s2.CheckSidecars(); len(failed) != 0 {
		t.Fatalf("the rebuild renumbered the rows under the index: %v", failed)
	}
	if n := countRows(t, s2, pmFTS, "gamma"); n != 1 {
		t.Fatalf("gamma memory lost to the rebuild: %d", n)
	}
	addPluginMemory(t, s2, "m4", "delta memory")
	if n := countRows(t, s2, pmFTS, "delta"); n != 1 {
		t.Fatalf("a write after the rebuild is not indexed — the triggers were not restored: %d", n)
	}
}

// .
// .
// .
// .
func TestAChangedSidecarDeclarationIsRecreatedAndRefilled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	addPluginMemory(t, s, "m1", "Sensitive text")
	addPluginMemory(t, s, "m2", "another row")

	text := schemaText(t)
	const oldDecl = "CREATE VIRTUAL TABLE IF NOT EXISTS plugin_memories_tri USING fts5(text, content='plugin_memories', content_rowid='rowid', tokenize='trigram');"
	const newDecl = "CREATE VIRTUAL TABLE IF NOT EXISTS plugin_memories_tri USING fts5(text, content='plugin_memories', content_rowid='rowid', tokenize='trigram case_sensitive 1');"
	if strings.Count(text, oldDecl) != 1 {
		t.Fatalf("the test's copy of the declaration is stale; schema.sql no longer holds %q", oldDecl)
	}
	changed := strings.Replace(text, oldDecl, newDecl, 1)

	rep, err := s.reconcileSchema(changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Sidecars) != 1 || !strings.HasPrefix(rep.Sidecars[0], "plugin_memories_tri ") {
		t.Fatalf("reconcile must report the one recreated sidecar: %+v", rep)
	}
	if rep.Unneeded {
		t.Fatal("a reconcile that recreated a sidecar is not unneeded")
	}
	var live string
	if err := s.DB().QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'plugin_memories_tri'`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(live, "case_sensitive 1") {
		t.Fatalf("the live declaration was not replaced: %s", live)
	}
	notes := s.ensureSidecars()
	if len(notes) != 1 || !strings.Contains(notes[0], "rebuilt sidecar plugin_memories_tri (index held 0 of 2 rows)") {
		t.Fatalf("readiness must rebuild exactly the recreated sidecar: %v", notes)
	}
	if n := countRows(t, s, pmTri, "Sensit"); n != 1 {
		t.Fatalf("the recreated sidecar does not serve the new declaration: %d", n)
	}
	if n := countRows(t, s, pmTri, "sensit"); n != 0 {
		t.Fatalf("case_sensitive 1 was declared but the index folds case: %d", n)
	}
	again, err := s.reconcileSchema(changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Sidecars) != 0 {
		t.Fatalf("re-running the reconcile must be a no-op: %+v", again.Sidecars)
	}
}

// .
// .
func TestSidecarsAreOutsideReplayAndTheCarry(t *testing.T) {
	for _, name := range append(DerivedTables(), EphemeralTables()...) {
		if _, isSidecar := Sidecars[name]; isSidecar {
			t.Errorf("%s is both a catalogued table and a sidecar", name)
		}
	}
	for name := range Sidecars {
		if _, listed := Catalog[name]; listed {
			t.Errorf("sidecar %s is in the table catalog", name)
		}
	}
}
