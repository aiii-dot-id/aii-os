package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The predecessor's and the successor's declarations of one ephemeral
// table: the successor adds one nullable column and marks it.
const (
	predecessorNotes = "CREATE TABLE IF NOT EXISTS notes (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL\n);"
	successorNotes   = "CREATE TABLE IF NOT EXISTS notes (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL,\n    tally INTEGER -- successor-column: 0.2.1\n);"
	unmarkedNotes    = "CREATE TABLE IF NOT EXISTS notes (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY,\n    body TEXT NOT NULL,\n    tally INTEGER\n);"
	successorTally   = "CREATE TABLE IF NOT EXISTS tallies (\n    -- provenance: ephemeral\n    -- successor-table: 0.2.1\n    id TEXT PRIMARY KEY,\n    n INTEGER\n);"
	unmarkedTally    = "CREATE TABLE IF NOT EXISTS tallies (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY,\n    n INTEGER\n);"
)

func noteRows(t *testing.T, s *Store) string {
	t.Helper()
	rows, err := s.db.Query(`SELECT id, body FROM notes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			t.Fatal(err)
		}
		got = append(got, id+"="+body)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(got, " ")
}

// UPDATE, WRITE, GO BACK, COME FORWARD. The successor's reconcile adds its
// column and writes the mark into the live declaration; it writes values;
// the predecessor drops the column with them, carries every row, and its
// receipt names the column, the rows that held values and the version that
// marked it; the successor then adds it again, empty. Each step converges.
func TestAColumnALaterVersionMarkedIsDroppedByAnEarlierOne(t *testing.T) {
	s, _ := driftDB(t, predecessorNotes, `INSERT INTO notes VALUES ('a', 'first')`, `INSERT INTO notes VALUES ('b', 'second')`)
	defer s.db.Close()

	if _, err := s.reconcileSchema(successorNotes); err != nil {
		t.Fatalf("the successor's reconcile: %v", err)
	}
	if !strings.Contains(liveSQL(t, s, "table", "notes"), "-- successor-column: 0.2.1") {
		t.Fatalf("the successor's mark is not in the live declaration:\n%s", liveSQL(t, s, "table", "notes"))
	}
	mustConverge(t, s, successorNotes)
	if _, err := s.db.Exec(`UPDATE notes SET tally = 7 WHERE id = 'b'`); err != nil {
		t.Fatal(err)
	}

	rep, err := s.reconcileSchema(predecessorNotes)
	if err != nil {
		t.Fatalf("the predecessor refused a column its successor marked droppable: %v", err)
	}
	const want = "notes.tally (1 row(s) held values; marked droppable by 0.2.1, which added it)"
	if len(rep.SuccessorColumns) != 1 || rep.SuccessorColumns[0] != want {
		t.Fatalf("the receipt does not name the dropped column as %q: %+v", want, rep)
	}
	if !strings.Contains(strings.Join(rep.Lines(), "\n"), "RECONCILE: dropped successor column "+want) {
		t.Fatalf("the receipt lines do not say it: %q", rep.Lines())
	}
	if cols, err := s.liveColumns("notes"); err != nil || cols["tally"] || len(cols) != 2 {
		t.Fatalf("the successor's column survived: %v %v", cols, err)
	}
	if got := noteRows(t, s); got != "a=first b=second" {
		t.Fatalf("the rows were not carried whole: %s", got)
	}
	mustConverge(t, s, predecessorNotes)

	if _, err := s.reconcileSchema(successorNotes); err != nil {
		t.Fatalf("coming forward again: %v", err)
	}
	if !strings.Contains(liveSQL(t, s, "table", "notes"), "-- successor-column: 0.2.1") {
		t.Fatal("coming forward again did not write the mark")
	}
	if got := noteRows(t, s); got != "a=first b=second" {
		t.Fatalf("coming forward lost rows: %s", got)
	}
}

// WITHOUT A MARK NOTHING CHANGES: a populated column the predecessor does
// not declare is refused as it always was, the remedy named; and a mark
// the predecessor cannot read as one (no version, or not a version) is no
// mark.
func TestAColumnALaterVersionDidNotMarkIsStillRefused(t *testing.T) {
	for name, successor := range map[string]string{
		"no mark":              unmarkedNotes,
		"a mark with no text":  strings.Replace(successorNotes, "0.2.1", "", 1),
		"a mark of no version": strings.Replace(successorNotes, "0.2.1", "soon", 1),
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := driftDB(t, successor, `INSERT INTO notes VALUES ('a', 'first', 3)`)
			defer s.db.Close()
			_, err := s.reconcileSchema(predecessorNotes)
			var shape *ShapeError
			if !errors.As(err, &shape) || !strings.Contains(err.Error(), "column(s) tally hold data") {
				t.Fatalf("an unmarked populated column was not refused: %v", err)
			}
			if cols, err := s.liveColumns("notes"); err != nil || !cols["tally"] {
				t.Fatalf("a refused rebuild must leave the column: %v %v", cols, err)
			}
		})
	}
}

// A mark is the adder's word about ITS column. A column this binary
// declares is this binary's, marked or not, and stays with its values.
func TestAMarkedColumnThisBinaryDeclaresIsKept(t *testing.T) {
	s, _ := driftDB(t, successorNotes, `INSERT INTO notes VALUES ('a', 'first', 3)`)
	defer s.db.Close()
	rep, err := s.reconcileSchema(unmarkedNotes)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.SuccessorColumns) != 0 {
		t.Fatalf("a declared column was dropped: %+v", rep)
	}
	var n int
	if err := s.db.QueryRow(`SELECT tally FROM notes WHERE id = 'a'`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("the declared column's value: %d, %v", n, err)
	}
}

// A TABLE A LATER VERSION MARKED goes with its rows, counted in the
// receipt; one it did not mark is left as it was (and the audit refuses
// it: a database this binary does not declare).
func TestATableALaterVersionMarkedIsDroppedByAnEarlierOne(t *testing.T) {
	s, _ := driftDB(t, predecessorNotes+"\n"+successorTally, `INSERT INTO tallies VALUES ('x', 1)`, `INSERT INTO tallies VALUES ('y', 2)`)
	defer s.db.Close()
	rep, err := s.reconcileSchema(predecessorNotes)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	const want = "tallies (2 row(s); marked droppable by 0.2.1, which added it)"
	if len(rep.SuccessorTables) != 1 || rep.SuccessorTables[0] != want {
		t.Fatalf("the receipt does not name the dropped table as %q: %+v", want, rep)
	}
	if !strings.Contains(strings.Join(rep.Lines(), "\n"), "RECONCILE: dropped successor table "+want) {
		t.Fatalf("the receipt lines do not say it: %q", rep.Lines())
	}
	if liveSQL(t, s, "table", "tallies") != "" {
		t.Fatal("the successor's table survived")
	}
	mustConverge(t, s, predecessorNotes)

	u, _ := driftDB(t, predecessorNotes+"\n"+unmarkedTally, `INSERT INTO tallies VALUES ('x', 1)`)
	defer u.db.Close()
	rep, err = u.reconcileSchema(predecessorNotes)
	if err != nil || len(rep.SuccessorTables) != 0 || liveSQL(t, u, "table", "tallies") == "" {
		t.Fatalf("an unmarked table was touched: %+v %v", rep, err)
	}
}

// THE WHOLE OPENING: a store written by a successor that marked a column
// of a real ephemeral table and a table of its own opens under this
// binary's schema, the audit passing, every row of the table kept.
func TestAStoreALaterVersionWroteOpensUnderThisOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	ours := string(raw)
	successor := strings.Replace(ours, "    prompt_tokens ", "    cache_hint TEXT, -- successor-column: 0.2.1\n    prompt_tokens ", 1) + "\n" + successorTally + "\n"
	if successor == ours+"\n"+successorTally+"\n" {
		t.Fatal("the successor's declaration did not take: turn_metrics.prompt_tokens moved")
	}
	if _, err := s.reconcileSchema(successor); err != nil {
		t.Fatalf("the successor's reconcile: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO turn_metrics (ts_ms, calls, read_only, spawned, harvested, cache_hint) VALUES (1, 2, 0, 0, 0, 'warm')`,
		`INSERT INTO tallies VALUES ('x', 1)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	back, err := New(path)
	if err != nil {
		t.Fatalf("this binary refused a store its successor wrote with marks: %v", err)
	}
	defer back.Close()
	var calls int
	if err := back.db.QueryRow(`SELECT calls FROM turn_metrics WHERE ts_ms = 1`).Scan(&calls); err != nil || calls != 2 {
		t.Fatalf("the successor's row was not carried: %d, %v", calls, err)
	}
	if cols, err := back.liveColumns("turn_metrics"); err != nil || cols["cache_hint"] {
		t.Fatalf("the marked column survived: %v", err)
	}
	if liveSQL(t, back, "table", "tallies") != "" {
		t.Fatal("the marked table survived")
	}
}

// THE MARKS ARE HELD WHERE THEY ARE WRITTEN: a mark this binary's own
// schema carries must be one a predecessor can act on, or the file is
// refused before anything changes.
func TestTheMarksAreRefusedWhereTheyCannotHold(t *testing.T) {
	for bad, why := range map[string]string{
		strings.Replace(successorNotes, "tally INTEGER", "tally INTEGER NOT NULL DEFAULT 0", 1):                                     "successor column notes.tally must be nullable",
		"CREATE TABLE IF NOT EXISTS notes (\n    -- provenance: ephemeral\n    id TEXT PRIMARY KEY -- successor-column: 0.2.1\n);":  "successor column notes.id must be nullable",
		strings.Replace(successorNotes, "-- provenance: ephemeral", "-- provenance: derived, clear-order 1", 1):                     "successor column notes.tally is in a derived table",
		strings.Replace(successorNotes, "0.2.1", "soon", 1):                                                                         "notes: a successor mark that does not read as one",
		strings.Replace(successorTally, "-- successor-table: 0.2.1", "-- successor-table: 0.2.1\n    -- successor-table: 0.2.2", 1): "tallies: 2 successor-table marks",
	} {
		s, _ := driftDB(t, predecessorNotes)
		_, err := s.reconcileSchema(bad)
		s.db.Close()
		if err == nil || !strings.Contains(err.Error(), why) {
			t.Fatalf("a mark that cannot hold was not refused as %q: %v\n%s", why, err, bad)
		}
	}
}

// A VIEW OVER WHAT A LATER VERSION MARKED GOES WITH IT. A view holds no
// rows, and one this binary does not declare is allowed (code may make
// one); but every view that read before an opening must read after it, so
// a successor's view over its own marked column or table would otherwise
// refuse the very database the marks exist to let go back. A view that
// reads only what stays is kept.
func TestAViewOverWhatALaterVersionMarkedGoesWithIt(t *testing.T) {
	const keeps = "CREATE VIEW IF NOT EXISTS bodies AS SELECT body FROM notes;"
	for name, view := range map[string]string{
		"over a marked column": "CREATE VIEW IF NOT EXISTS tallied AS SELECT tally FROM notes;",
		"over a marked table":  "CREATE VIEW IF NOT EXISTS tallied AS SELECT n FROM tallies;",
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := driftDB(t, predecessorNotes)
			defer s.db.Close()
			successor := successorNotes + "\n" + successorTally + "\n" + view + "\n" + keeps
			if _, err := s.withSchemaReport(t, successor); err != nil {
				t.Fatalf("the successor's opening: %v", err)
			}
			rep, err := s.withSchemaReport(t, predecessorNotes)
			if err != nil {
				t.Fatalf("going back refused a successor's view over what it marked: %v", err)
			}
			if len(rep.SuccessorViews) != 1 || rep.SuccessorViews[0] != "tallied" {
				t.Fatalf("the receipt does not name the view: %+v", rep)
			}
			if !strings.Contains(strings.Join(rep.Lines(), "\n"), "RECONCILE: dropped view over a successor's column or table tallied") {
				t.Fatalf("the receipt lines do not say it: %q", rep.Lines())
			}
			if liveSQL(t, s, "view", "tallied") != "" {
				t.Fatal("the successor's view survived")
			}
			if liveSQL(t, s, "view", "bodies") == "" {
				t.Fatal("a view that reads only what stays was dropped")
			}
		})
	}
}

// withSchemaReport runs one reconcile as an opening does, inside the
// schema operation, whose end checks that every view that read still
// reads.
func (s *Store) withSchemaReport(t *testing.T, schema string) (ReconcileReport, error) {
	t.Helper()
	var rep ReconcileReport
	err := s.withSchema(t.Context(), schema, func() error {
		var e error
		rep, e = s.reconcileSchema(schema)
		return e
	})
	return rep, err
}
