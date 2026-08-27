package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The audit's second question. It always asked "is this object there?";
// it did not ask "is it the one the code was written against?" — so a
// table reused under a name the file already declared passed the audit
// and failed at the first query. Found against a live database, 2026-08-24.

func TestTheAuditCatchesATableWithTheWrongColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.db")

	// A database built by the real schema, then one table quietly
	// replaced by an older shape under the same name.
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`ALTER TABLE outbox RENAME COLUMN to_role TO whoever`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	_, err = New(path)
	if err == nil {
		t.Fatal("a table with the wrong columns opened cleanly — the audit only asked for the name")
	}
	msg := err.Error()
	for _, want := range []string{"outbox", "to_role", "whoever"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the failure does not name %q, so the operator cannot act on it: %v", want, err)
		}
	}
}

// The audit must not fail a database it just built. Every table in
// schema.sql is parsed here, so a body this parser cannot read shows up
// as a false accusation rather than lying dormant.
func TestAFreshDatabasePassesTheColumnAudit(t *testing.T) {
	s := testStore(t)
	if problems := s.columnProblems(); len(problems) != 0 {
		t.Fatalf("a database built from schema.sql failed its own column audit: %v", problems)
	}
}

// Table constraints are not columns, however they are written. "UNIQUE(a,
// b)" with no space was read as a column named "unique(a" by the first
// version of this parser, which accused a healthy table.
func TestTableConstraintsAreNotMistakenForColumns(t *testing.T) {
	body := `
		id TEXT PRIMARY KEY,
		from_id TEXT NOT NULL,
		to_id TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('a','b','c')),
		UNIQUE(from_id, to_id),
		PRIMARY KEY (id),
		FOREIGN KEY (from_id) REFERENCES nodes(id)`
	got := columnNames(body)
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
	got := columnNames(`standing TEXT NOT NULL DEFAULT 'unknown' CHECK (standing IN ('unknown','known','chartered'))`)
	if len(got) != 1 || !got["standing"] {
		t.Fatalf("a nested comma list split into %v", got)
	}
}

// Comments may contain parentheses, and the body scan must not derail on
// them — this file's schema is documented heavily inside table bodies.
func TestCommentsInsideATableBodyAreIgnored(t *testing.T) {
	cols := declaredColumns(`
CREATE TABLE IF NOT EXISTS t (
    a TEXT NOT NULL,  -- a note with ( an unbalanced paren
    b INTEGER         -- and CREATE TABLE phantom (x TEXT)
);`)
	got := cols["t"]
	if len(got) != 2 || !got["a"] || !got["b"] {
		t.Fatalf("comments derailed the body scan: %v", got)
	}
	if _, phantom := cols["phantom"]; phantom {
		t.Fatal("a table named in a comment was parsed as a declaration")
	}
}

// The two failures are not the same failure, and the boot treats them
// differently: an undeclared object can only be code doing DDL and kills
// the boot (operator ruling 2026-08-22); wrong columns is damage to a
// mirror the ledger can rebuild, and enters SAFE instead. Erase the
// distinction and a recoverable projection becomes a dead process.
func TestDamageAndDisciplineAreDifferentFailures(t *testing.T) {
	shapeErr := func(t *testing.T, ddl string) error {
		t.Helper()
		path := filepath.Join(t.TempDir(), "aii.db")
		s, err := New(path)
		if err != nil {
			t.Fatal(err)
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
	wrongColumns := shapeErr(t, `ALTER TABLE outbox RENAME COLUMN to_role TO whoever`)
	if !errors.As(wrongColumns, &shape) {
		t.Fatalf("wrong columns did not report as damage, so the boot will die instead of entering SAFE: %v", wrongColumns)
	}

	undeclared := shapeErr(t, `CREATE TABLE rogue (id TEXT PRIMARY KEY)`)
	if errors.As(undeclared, &shape) {
		t.Fatalf("an undeclared table reported as damage — it is a DDL violation and must fail the boot: %v", undeclared)
	}
	if !strings.Contains(undeclared.Error(), "rogue") {
		t.Fatalf("the failure does not name the rogue object: %v", undeclared)
	}
}
