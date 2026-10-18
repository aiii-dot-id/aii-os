package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheAuditCatchesATableWithTheWrongColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.db")

	// A database built by the real schema, then one table quietly
	// replaced by an older shape under the same name — WITH A ROW IN IT.
	// The row is the point: reconcile repairs a drifted shape when
	// nothing is lost, so damage is what it cannot repair without
	// discarding something. An empty table would simply be fixed.
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(
		`INSERT INTO outbox (id, to_role, content, delivered, created_ms) VALUES ('o1','operator','a message',0,1)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`ALTER TABLE outbox RENAME COLUMN to_role TO whoever`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, err = New(path)
	if err == nil {
		t.Fatal("a table whose wrong column HOLDS DATA opened cleanly — the operator never got to decide")
	}
	msg := err.Error()
	for _, want := range []string{"outbox", "to_role", "whoever"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the failure does not name %q, so the operator cannot act on it: %v", want, err)
		}
	}
}

// The other half of the same line: the SAME drift with nothing in the
// table is repaired rather than escalated. Stranding an operator over
// an empty table would be alarm without a reason, and the receipt keeps
// it from being silent.
func TestAnEmptyDriftedTableIsRepairedNotEscalated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`ALTER TABLE outbox RENAME COLUMN to_role TO whoever`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := New(path)
	if err != nil {
		t.Fatalf("an empty drifted table should be repaired, not escalated: %v", err)
	}
	defer s2.Close()

	var n int
	if err := s2.DB().QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('outbox') WHERE name='to_role'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("the declared column was not restored")
	}
}

// The audit must not fail a database it just built. Every table in
// schema.sql is read here, so a body the reader cannot read shows up
// as a false accusation rather than lying dormant.
func TestAFreshDatabasePassesTheColumnAudit(t *testing.T) {
	s := testStore(t)
	if problems := s.columnProblems(); len(problems) != 0 {
		t.Fatalf("a database built from schema.sql failed its own column audit: %v", problems)
	}
}

// referenceColumns is the column set the audit reads for one table of a
// schema text: SQLite's own reading (referenceShape), never a Go parse.
func referenceColumns(t *testing.T, text, table string) map[string]bool {
	t.Helper()
	shapes, err := referenceShape(text)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for name := range shapes[table] {
		got[name] = true
	}
	return got
}

// Table constraints are not columns, however they are written. "UNIQUE(a,
// b)" with no space was read as a column named "unique(a" by the first
// version of the column parser, which accused a healthy table.
func TestTableConstraintsAreNotMistakenForColumns(t *testing.T) {
	got := referenceColumns(t, `CREATE TABLE nodes (id TEXT PRIMARY KEY);
	CREATE TABLE edges (
		id TEXT,
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('a','b','c')),
		UNIQUE(from_id, to_id),
		PRIMARY KEY (id),
		FOREIGN KEY (from_id) REFERENCES nodes(id))`, "edges")
	want := []string{"id", "from_id", "to_id", "kind"}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want exactly %v", got, want)
	}
	for _, c := range want {
		if !got[c] {
			t.Fatalf("column %q was not parsed: %v", c, got)
		}
	}
}

// A CHECK list contains commas, and splitting on them naively turns one
// column into several phantoms.
func TestACheckListDoesNotBecomeThreeColumns(t *testing.T) {
	got := referenceColumns(t, `CREATE TABLE seats (standing TEXT NOT NULL DEFAULT 'unknown' CHECK (standing IN ('unknown','known','chartered')))`, "seats")
	if len(got) != 1 || !got["standing"] {
		t.Fatalf("a nested comma list split into %v", got)
	}
}

// Comments may contain parentheses, and the body read must not derail on
// them — this file's schema is documented heavily inside table bodies.
func TestCommentsInsideATableBodyAreIgnored(t *testing.T) {
	text := `
CREATE TABLE IF NOT EXISTS t (
    a TEXT NOT NULL,  -- a note with ( an unbalanced paren
    b INTEGER         -- and CREATE TABLE phantom (x TEXT)
);`
	got := referenceColumns(t, text, "t")
	if len(got) != 2 || !got["a"] || !got["b"] {
		t.Fatalf("comments derailed the body read: %v", got)
	}
	if phantom := referenceColumns(t, text, "phantom"); len(phantom) != 0 {
		t.Fatal("a table named in a comment was read as a declaration")
	}
}

func TestDamageAndDisciplineAreDifferentFailures(t *testing.T) {
	// seed is applied before the damaging DDL, so a column that is about
	// to be orphaned actually holds something. Reconcile repairs what it
	// can lose nothing by repairing; damage is what it must refuse.
	shapeErr := func(t *testing.T, seed, ddl string) error {
		t.Helper()
		path := filepath.Join(t.TempDir(), "aii.db")
		s, err := New(path)
		if err != nil {
			t.Fatal(err)
		}
		if seed != "" {
			if _, err := s.DB().Exec(seed); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.DB().Exec(ddl); err != nil {
			t.Fatal(err)
		}
		s.Close()
		_, err = New(path)
		if err == nil {
			t.Fatal("a damaged database opened cleanly")
		}
		return err
	}

	var shape *ShapeError
	wrongColumns := shapeErr(t,
		`INSERT INTO outbox (id, to_role, content, delivered, created_ms) VALUES ('o1','operator','a message',0,1)`,
		`ALTER TABLE outbox RENAME COLUMN to_role TO whoever`)
	if !errors.As(wrongColumns, &shape) {
		t.Fatalf("wrong columns did not report as damage, so the boot will die instead of entering SAFE: %v", wrongColumns)
	}

	// An undeclared object is not damage and reconcile must not touch
	// it: nothing declares it, so nothing can reshape it into
	// legitimacy. It stays a DDL violation that fails the boot.
	undeclared := shapeErr(t, "", `CREATE TABLE rogue (id TEXT PRIMARY KEY)`)
	if errors.As(undeclared, &shape) {
		t.Fatalf("an undeclared table reported as damage — it is a DDL violation and must fail the boot: %v", undeclared)
	}
	if !strings.Contains(undeclared.Error(), "rogue") {
		t.Fatalf("the failure does not name the rogue object: %v", undeclared)
	}
}
