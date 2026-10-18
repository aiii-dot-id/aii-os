package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func liveSQL(t *testing.T, s *Store, kind, name string) string {
	t.Helper()
	text, ok, err := s.liveObjectSQL(kind, name)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return text
}

func mustConverge(t *testing.T, s *Store, schema string) {
	t.Helper()
	rep, err := s.reconcileSchema(schema)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if !rep.Unneeded {
		t.Fatalf("reconcile did not converge — it would act on every boot: %+v", rep)
	}
}

func TestACheckAddedToATableIsAppliedAtBoot(t *testing.T) {
	const before = `CREATE TABLE notes (id TEXT PRIMARY KEY, n INTEGER);`
	const after = "CREATE TABLE IF NOT EXISTS notes (\n    id TEXT PRIMARY KEY,\n    n INTEGER CHECK (n >= 0) -- never negative\n);"
	s, _ := driftDB(t, before, `INSERT INTO notes VALUES ('a', 1)`, `INSERT INTO notes VALUES ('b', 2)`)
	defer s.db.Close()

	rep, err := s.reconcileSchema(after)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Rebuilt) != 1 || !strings.Contains(rep.Rebuilt[0], "[declaration changed]") {
		t.Fatalf("a changed constraint under unchanged columns must rebuild and say why: %+v", rep)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("rows carried = %d (%v), want 2", n, err)
	}
	if _, err := s.db.Exec(`INSERT INTO notes VALUES ('c', -1)`); err == nil {
		t.Fatal("the new CHECK is not enforced on the rebuilt table")
	}
	if canonDDL(liveSQL(t, s, "table", "notes")) != canonDDL(declaredTableSQL(after)["notes"]) {
		t.Fatalf("the rebuilt table does not read as declared:\n%s", liveSQL(t, s, "table", "notes"))
	}
	mustConverge(t, s, after)
}

func TestRowsTheNewDeclarationRefusesAreRefusedForEphemeralAndReplayedForDerived(t *testing.T) {
	const before = `CREATE TABLE ledger (seq INTEGER PRIMARY KEY, payload TEXT);
CREATE TABLE facts (id TEXT PRIMARY KEY, n INTEGER NOT NULL);`
	ephemeral := "CREATE TABLE ledger (seq INTEGER PRIMARY KEY, payload TEXT);\nCREATE TABLE IF NOT EXISTS facts (\n    id TEXT PRIMARY KEY,\n    n INTEGER NOT NULL CHECK (n >= 0)\n);"
	derived := "CREATE TABLE ledger (seq INTEGER PRIMARY KEY, payload TEXT);\nCREATE TABLE IF NOT EXISTS facts (\n    -- provenance: derived, clear-order 1\n    id TEXT PRIMARY KEY,\n    n INTEGER NOT NULL CHECK (n >= 0)\n);"

	s, _ := driftDB(t, before, `INSERT INTO facts VALUES ('a', 1)`, `INSERT INTO facts VALUES ('b', -1)`)
	defer s.db.Close()
	// Ephemeral: nothing can give the rows back, so the boot refuses.
	_, err := s.reconcileSchema(ephemeral)
	if err == nil || !strings.Contains(err.Error(), "facts") || !strings.Contains(err.Error(), "rows stay") {
		t.Fatalf("a row the new declaration refuses must refuse the rebuild by name: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM facts`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("a refused rebuild must leave the table as it was: %d rows (%v)", n, err)
	}
	if strings.Contains(liveSQL(t, s, "table", "facts"), "CHECK") {
		t.Fatal("a refused rebuild changed the table")
	}
	// Derived provenance is not permission to commit an empty table on the
	// promise of later replay. The complete historical-replay proof exercises
	// successful recreation through OpenAndReplay.
	_, err = s.reconcileSchema(derived)
	var replay *ReplayRequiredError
	if !errors.As(err, &replay) {
		t.Fatalf("missing replay requirement: %v", err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM facts").Scan(&n); err != nil || n != 2 {
		t.Fatalf("source-less refusal lost rows: %d %v", n, err)
	}
	if strings.Contains(liveSQL(t, s, "table", "facts"), "CHECK") {
		t.Fatal("source-less refusal changed schema")
	}
}

func TestADerivedTableDroppingAPopulatedColumnIsRecreatedForTheRecord(t *testing.T) {
	const before = `CREATE TABLE facts (id TEXT PRIMARY KEY, n INTEGER NOT NULL, note TEXT);`
	const after = "CREATE TABLE IF NOT EXISTS facts (\n -- provenance: derived, clear-order 1\n id TEXT PRIMARY KEY,\n n INTEGER NOT NULL\n);"
	s, _ := driftDB(t, before, `INSERT INTO facts VALUES ('a',1,'kept')`)
	defer s.db.Close()
	_, err := s.reconcileSchema(after)
	var replay *ReplayRequiredError
	if !errors.As(err, &replay) {
		t.Fatalf("missing replay requirement: %v", err)
	}
	var note string
	if err := s.db.QueryRow("SELECT note FROM facts WHERE id='a'").Scan(&note); err != nil || note != "kept" {
		t.Fatalf("no-source repair lost derived history: %q %v", note, err)
	}
}

func TestStrictIsAppliedAndItsDeclarationIsRead(t *testing.T) {
	const before = `CREATE TABLE notes (id TEXT PRIMARY KEY, n INTEGER);`
	const after = "CREATE TABLE IF NOT EXISTS notes (\n    id TEXT PRIMARY KEY,\n    n INTEGER\n) STRICT;"
	s, _ := driftDB(t, before, `INSERT INTO notes VALUES ('a', 1)`)
	defer s.db.Close()
	if got := declaredTableSQL(after)["notes"]; !strings.HasSuffix(got, ") STRICT") {
		t.Fatalf("the table option is not part of the declaration: %q", got)
	}
	rep, err := s.reconcileSchema(after)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Rebuilt) != 1 {
		t.Fatalf("STRICT must rebuild: %+v", rep)
	}
	if !strings.Contains(liveSQL(t, s, "table", "notes"), "STRICT") {
		t.Fatal("the rebuilt table is not STRICT")
	}
	if _, err := s.db.Exec(`INSERT INTO notes VALUES ('b', 'not a number')`); err == nil {
		t.Fatal("STRICT is not enforced on the rebuilt table")
	}
	mustConverge(t, s, after)

	// The catalog's parser reads a STRICT block too.
	text := "CREATE TABLE IF NOT EXISTS ledger (\n    -- provenance: derived, clear-order 2\n    seq INTEGER PRIMARY KEY\n);\nCREATE TABLE IF NOT EXISTS notes (\n    -- provenance: ephemeral\n    id TEXT\n) STRICT;\n"
	cat, problems := parseProvenance(text)
	if len(problems) != 0 || len(cat) != 2 || cat["notes"].Provenance != Ephemeral {
		t.Fatalf("parseProvenance on a STRICT block: %v %+v", problems, cat)
	}
}

func TestIndexChangesAreAppliedAndUndeclaredIndexesDropped(t *testing.T) {
	const before = `CREATE TABLE notes (id TEXT PRIMARY KEY, a INTEGER, b INTEGER);
CREATE INDEX i ON notes (a);
CREATE INDEX stale ON notes (b);`
	const after = "CREATE TABLE notes (id TEXT PRIMARY KEY, a INTEGER, b INTEGER);\nCREATE INDEX IF NOT EXISTS i ON notes (b) WHERE b > 0;"
	s, _ := driftDB(t, before)
	defer s.db.Close()
	rep, err := s.reconcileSchema(after)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Indexes) != 1 || !strings.HasPrefix(rep.Indexes[0], "i (declaration changed") {
		t.Fatalf("a changed index must be recreated: %+v", rep)
	}
	if len(rep.Dropped) != 1 || rep.Dropped[0] != "index stale" {
		t.Fatalf("an undeclared index must be dropped with a receipt: %+v", rep)
	}
	if !strings.Contains(liveSQL(t, s, "index", "i"), "WHERE b > 0") {
		t.Fatalf("the recreated index lost its WHERE: %s", liveSQL(t, s, "index", "i"))
	}
	if liveSQL(t, s, "index", "stale") != "" {
		t.Fatal("the undeclared index survived")
	}
	mustConverge(t, s, after)
}

// The partial index the real schema declares used to come back whole
// after a rebuild: the index reader dropped its WHERE.
func TestAPartialIndexSurvivesATableRebuildWithItsWhere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`ALTER TABLE work_queue ADD COLUMN stray TEXT`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := liveSQL(t, s2, "index", "wq_dedup_idx"); !strings.Contains(got, "WHERE dedup_key IS NOT NULL") {
		t.Fatalf("the partial index came back without its WHERE: %s", got)
	}
}

func TestTriggerChangesAreAppliedAndUndeclaredTriggersDropped(t *testing.T) {
	const before = `CREATE TABLE notes (id TEXT PRIMARY KEY, n INTEGER);
CREATE TRIGGER t1 AFTER INSERT ON notes BEGIN SELECT 1; END;
CREATE TRIGGER stale AFTER DELETE ON notes BEGIN SELECT 1; END;`
	const after = "CREATE TABLE notes (id TEXT PRIMARY KEY, n INTEGER);\nCREATE TRIGGER IF NOT EXISTS t1 AFTER INSERT ON notes BEGIN\n    UPDATE notes SET n = 2 WHERE id = new.id;\nEND;"
	s, _ := driftDB(t, before)
	defer s.db.Close()
	rep, err := s.reconcileSchema(after)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Triggers) != 1 || !strings.HasPrefix(rep.Triggers[0], "t1 (declaration changed") {
		t.Fatalf("a changed trigger must be recreated: %+v", rep)
	}
	if len(rep.Dropped) != 1 || rep.Dropped[0] != "trigger stale" {
		t.Fatalf("an undeclared trigger must be dropped with a receipt: %+v", rep)
	}
	if _, err := s.db.Exec(`INSERT INTO notes VALUES ('a', 1)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT n FROM notes WHERE id = 'a'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("the recreated trigger does not run its new body: n=%d (%v)", n, err)
	}
	mustConverge(t, s, after)
}

// A view a sidecar reads its rows through, redefined: the view is
// recreated and the sidecar rebuilt over the new definition.
func TestARedefinedViewRebuildsTheSidecarsThatReadIt(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddConversationTurn("participant", "the participant said aardvark"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddConversationTurn("operator", "the operator said aardvark"); err != nil {
		t.Fatal(err)
	}
	const q = `SELECT COUNT(*) FROM conversations_fts WHERE conversations_fts MATCH 'aardvark'`
	if n := countRows(t, s, q); n != 2 {
		t.Fatalf("both turns indexed before the change: %d", n)
	}
	text := schemaText(t)
	const old = "IN ('resident', 'operator', 'participant')"
	if strings.Count(text, old) < 4 {
		t.Fatalf("the test's copy of the role list is stale: %d occurrences", strings.Count(text, old))
	}
	changed := strings.ReplaceAll(text, old, "IN ('resident', 'operator')")
	rep, err := s.reconcileSchema(changed)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Views) != 1 || !strings.HasPrefix(rep.Views[0], "conversations_searchable (declaration changed") {
		t.Fatalf("the redefined view must be recreated: %+v", rep)
	}
	if len(rep.Triggers) != 3 {
		t.Fatalf("the three conversation triggers changed with it: %+v", rep.Triggers)
	}
	if strings.Join(rep.RebuildSidecars, " ") != "conversations_fts conversations_tri" {
		t.Fatalf("the sidecars over the view must be named for rebuild: %v", rep.RebuildSidecars)
	}
	s.ensureSidecars(rep.RebuildSidecars...)
	if n := countRows(t, s, q); n != 1 {
		t.Fatalf("after the rebuild the participant's turn is still indexed: %d", n)
	}
	if failed := checkSidecarsOn(s.db); len(failed) != 0 {
		t.Fatalf("sidecars disagree with the redefined view: %v", failed)
	}
	mustConverge(t, s, changed)
}

func TestARetiredTableIsDroppedAsDeclaredAndAnUndeclaredOneIsNot(t *testing.T) {
	const before = `CREATE TABLE notes (id TEXT PRIMARY KEY);
CREATE TABLE old (id TEXT PRIMARY KEY);`
	const without = "CREATE TABLE notes (id TEXT PRIMARY KEY);"
	const retired = "-- retired-table: old\nCREATE TABLE notes (id TEXT PRIMARY KEY);"
	s, _ := driftDB(t, before, `INSERT INTO old VALUES ('r1')`, `INSERT INTO old VALUES ('r2')`)
	defer s.db.Close()

	rep, err := s.reconcileSchema(without)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Retired) != 0 || liveSQL(t, s, "table", "old") == "" {
		t.Fatalf("a table the file merely stopped declaring must not be dropped: %+v", rep)
	}
	rep, err = s.reconcileSchema(retired)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Retired) != 1 || rep.Retired[0] != "old (2 row(s) discarded, as declared)" {
		t.Fatalf("a retired table is dropped with its rows counted: %+v", rep)
	}
	if liveSQL(t, s, "table", "old") != "" {
		t.Fatal("the retired table survived")
	}
	mustConverge(t, s, retired)
	if _, err := s.reconcileSchema("-- retired-table: notes\nCREATE TABLE notes (id TEXT PRIMARY KEY);"); err == nil {
		t.Fatal("a table both declared and retired was accepted")
	}
}

// A populated column the declaration drops is a decision the file makes.
// Without one the rebuild is refused and the remedy names the
// declaration; with -- retired-column the column goes with its values,
// every row is carried, and the receipt says it was declared. A column
// both declared and retired, or both retired and renamed, is a
// contradiction refused before anything changes.
func TestARetiredColumnIsDroppedAsDeclaredAndAnUndeclaredOneIsRefused(t *testing.T) {
	const before = `CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT NOT NULL, verdict TEXT NOT NULL DEFAULT 'open');`
	const without = "CREATE TABLE IF NOT EXISTS notes (\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL\n);"
	const retired = "CREATE TABLE IF NOT EXISTS notes (\n    -- retired-column: verdict\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL\n);"
	s, _ := driftDB(t, before, `INSERT INTO notes (id, body) VALUES ('a', 'first')`, `INSERT INTO notes VALUES ('b', 'second', 'closed')`)
	defer s.db.Close()

	_, err := s.reconcileSchema(without)
	var shape *ShapeError
	if !errors.As(err, &shape) || !strings.Contains(err.Error(), "column(s) verdict hold data") || !strings.Contains(err.Error(), "-- retired-column: <name>") {
		t.Fatalf("an undeclared drop of a populated column must be refused with the declaration named: %v", err)
	}
	if cols, err := s.liveColumns("notes"); err != nil || !cols["verdict"] {
		t.Fatalf("a refused rebuild must leave the column: %v %v", cols, err)
	}

	rep, err := s.reconcileSchema(retired)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Rebuilt) != 1 || !strings.HasPrefix(rep.Rebuilt[0], "notes (-verdict, 2 row(s) carried) [") ||
		!strings.HasSuffix(rep.Rebuilt[0], "; column(s) verdict retired with their values, as declared]") {
		t.Fatalf("a retired column is dropped with every row carried, and the receipt says it was declared: %+v", rep)
	}
	if cols, err := s.liveColumns("notes"); err != nil || cols["verdict"] || len(cols) != 2 {
		t.Fatalf("the retired column survived: %v %v", cols, err)
	}
	var got []string
	rows, err := s.db.Query(`SELECT id, body FROM notes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			t.Fatal(err)
		}
		got = append(got, id+"="+body)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "a=first b=second" {
		t.Fatalf("the rows were not carried whole: %v", got)
	}
	mustConverge(t, s, retired)

	for contradiction, why := range map[string]string{
		"CREATE TABLE IF NOT EXISTS notes (\n    -- retired-column: body\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL\n);":                       "table notes both declares and retires column body",
		"CREATE TABLE IF NOT EXISTS notes (\n    -- retired-column: body\n    id TEXT PRIMARY KEY,\n    text TEXT NOT NULL -- renamed-from: body\n);": "column notes.body is both retired and renamed to text",
	} {
		if _, err := s.reconcileSchema(contradiction); err == nil || !strings.Contains(err.Error(), why) {
			t.Fatalf("a contradictory retirement was not refused as %q: %v", why, err)
		}
	}
	if cols, err := s.liveColumns("notes"); err != nil || !cols["body"] {
		t.Fatalf("a refused retirement changed the table: %v %v", cols, err)
	}
}

// The real file on a fresh database: nothing to do, and the audit's
// text oracle agrees — the one property that keeps every boot from
// rebuilding the world.
func TestTheRealSchemaConvergesOnAFreshDatabase(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	text := schemaText(t)
	rep, err := s.reconcileSchema(text)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !rep.Unneeded {
		t.Fatalf("a fresh database disagrees with its own file: %+v", rep)
	}
	if problems := s.textProblems(text); len(problems) != 0 {
		t.Fatalf("the text oracle disagrees with a fresh database: %v", problems)
	}
}

// Out-of-band changes to derived objects are repaired at the next boot
// with receipts; the audit's text oracle sees them before that.
func TestOutOfBandChangesToDerivedObjectsAreRepairedAtBoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`DROP TRIGGER experiences_sidecar_au`,
		`CREATE TRIGGER experiences_sidecar_au AFTER UPDATE OF content ON experiences BEGIN SELECT 1; END`,
		`CREATE INDEX rogue_idx ON conversations (session_id)`,
	} {
		if _, err := s.DB().Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	problems := s.textProblems(schemaText(t))
	if len(problems) != 1 || !strings.Contains(problems[0], "experiences_sidecar_au") {
		t.Fatalf("the text oracle must see the altered trigger: %v", problems)
	}
	s.Close()

	s2, err := New(path)
	if err != nil {
		t.Fatalf("the boot must repair derived objects, not refuse: %v", err)
	}
	defer s2.Close()
	if canonDDL(liveSQL(t, s2, "trigger", "experiences_sidecar_au")) != canonDDL(declaredTriggerStatements(schemaText(t))["experiences_sidecar_au"]) {
		t.Fatal("the altered trigger was not restored to its declaration")
	}
	if liveSQL(t, s2, "index", "rogue_idx") != "" {
		t.Fatal("the undeclared index survived the boot")
	}
}

func TestCanonDDLReadsMeaning(t *testing.T) {
	a := "CREATE TABLE IF NOT EXISTS notes (\n    -- the key\n    id TEXT PRIMARY KEY, /* body */ n INTEGER DEFAULT '--' CHECK (n >= 0)\n);"
	b := "CREATE TABLE \"notes\" (id text primary key, n integer default '--' check(n>=0))"
	if canonDDL(a) != canonDDL(b) {
		t.Fatalf("meaning-equal declarations differ:\n%s\n%s", canonDDL(a), canonDDL(b))
	}
	if canonDDL(a) == canonDDL(strings.Replace(a, "n >= 0", "n > 0", 1)) {
		t.Fatal("a changed constraint reads as unchanged")
	}
	if canonDDL("CREATE INDEX i ON t (a) WHERE a > 0") == canonDDL("CREATE INDEX i ON t (a)") {
		t.Fatal("a WHERE reads as nothing")
	}
}

// A table a declared view names is rebuilt while the view stands: the
// promote renames under the legacy alter mode, so the view's reference
// to the table's own name never blocks it (found on the live copy).
func TestATableADeclaredViewReadsIsRebuiltUnderTheView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddConversationTurn("operator", "the view still reads me"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`ALTER TABLE conversations ADD COLUMN stray TEXT`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := New(path)
	if err != nil {
		t.Fatalf("a table under a declared view did not rebuild: %v", err)
	}
	defer s2.Close()
	if n := countRows(t, s2, `SELECT COUNT(*) FROM conversations_searchable`); n != 1 {
		t.Fatalf("the view over the rebuilt table reads %d rows, want 1", n)
	}
	if n := countRows(t, s2, `SELECT COUNT(*) FROM conversations_fts WHERE conversations_fts MATCH 'reads'`); n != 1 {
		t.Fatalf("the sidecar over the rebuilt table reads %d rows, want 1", n)
	}
	if failed := checkSidecarsOn(s2.db); len(failed) != 0 {
		t.Fatalf("sidecars disagree after the rebuild: %v", failed)
	}
	var fk, legacy int
	if err := s2.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys after the rebuild: %d %v", fk, err)
	}
	if err := s2.DB().QueryRow(`PRAGMA legacy_alter_table`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("legacy alter mode left on: %d %v", legacy, err)
	}
}

func TestTheMirrorIsNeverRebuiltBlind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`ALTER TABLE ledger RENAME COLUMN seq TO broken`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reconcileSchema(schemaText(t)); err == nil || !strings.Contains(err.Error(), "blind") {
		t.Fatalf("the reconcile must refuse a blind mirror rebuild: %v", err)
	}
	s.Close()
	_, err = New(path)
	var unreadable *MirrorReadError
	if err == nil || !errors.As(err, &unreadable) || !strings.Contains(err.Error(), "ledger") {
		t.Fatalf("a mirror with no readable head must name its integrity check: %v", err)
	}
}

func TestADerivedTableRecreatedEmptyNamesItsSidecarsForRebuild(t *testing.T) {
	const sidecars = `
-- provenance: sidecar of facts
CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(body, content='facts', content_rowid='rowid');
-- provenance: sidecar of facts
CREATE VIRTUAL TABLE IF NOT EXISTS facts_tri USING fts5(body, content='facts', content_rowid='rowid', tokenize='trigram');
-- provenance: sidecar of others
CREATE VIRTUAL TABLE IF NOT EXISTS others_fts USING fts5(body, content='others', content_rowid='rowid');
`
	const others = "CREATE TABLE IF NOT EXISTS others (\n -- provenance: derived, clear-order 2\n id TEXT PRIMARY KEY,\n body TEXT\n);\n"
	for name, c := range map[string]struct{ before, seed, after, why string }{
		"a populated column the declaration drops": {
			before: `CREATE TABLE facts (id TEXT PRIMARY KEY, body TEXT, note TEXT);`,
			seed:   `INSERT INTO facts VALUES ('a', 'aardvark', 'kept')`,
			after:  "CREATE TABLE IF NOT EXISTS facts (\n -- provenance: derived, clear-order 1\n id TEXT PRIMARY KEY,\n body TEXT\n);\n",
			why:    "held data the declaration drops",
		},
		"rows the new declaration cannot carry": {
			before: `CREATE TABLE facts (id TEXT PRIMARY KEY, body TEXT);`,
			seed:   `INSERT INTO facts VALUES ('a', NULL)`,
			after:  "CREATE TABLE IF NOT EXISTS facts (\n -- provenance: derived, clear-order 1\n id TEXT PRIMARY KEY,\n body TEXT NOT NULL\n);\n",
			why:    "could not be carried",
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := driftDB(t, c.before+"\nCREATE TABLE others (id TEXT PRIMARY KEY, body TEXT);", c.seed, `INSERT INTO others VALUES ('o', 'okapi')`)
			defer s.db.Close()
			// The verified record is at hand, so a derived table may be
			// recreated for it to refill.
			s.schemaReplay = true
			rep, err := s.reconcileSchema(c.after + others + sidecars)
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if len(rep.Recreated) != 1 || !strings.HasPrefix(rep.Recreated[0], "facts (derived;") || !strings.Contains(rep.Recreated[0], c.why) {
				t.Fatalf("facts must be the one table recreated, for %q: %+v", c.why, rep.Recreated)
			}
			if got := strings.Join(rep.RebuildSidecars, " "); got != "facts_fts facts_tri" {
				t.Fatalf("the sidecars named for rebuild are %q; facts has facts_fts and facts_tri, and others was not touched", got)
			}
			if n := countRows(t, s, `SELECT COUNT(*) FROM facts`); n != 0 {
				t.Fatalf("facts was recreated empty, yet holds %d rows", n)
			}
			if n := countRows(t, s, `SELECT COUNT(*) FROM others`); n != 1 {
				t.Fatalf("others was not part of this, yet holds %d rows", n)
			}
		})
	}
}
