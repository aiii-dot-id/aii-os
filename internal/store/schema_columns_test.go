package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .

func TestTheAuditCatchesATableWithTheWrongColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.db")

	// .
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
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

// .
// .
// .
func TestAFreshDatabasePassesTheColumnAudit(t *testing.T) {
	s := testStore(t)
	if problems := s.columnProblems(); len(problems) != 0 {
		t.Fatalf("a database built from schema.sql failed its own column audit: %v", problems)
	}
}

// .
// .
// .
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

// .
// .
func TestACheckListDoesNotBecomeThreeColumns(t *testing.T) {
	got := columnNames(`standing TEXT NOT NULL DEFAULT 'unknown' CHECK (standing IN ('unknown','known','chartered'))`)
	if len(got) != 1 || !got["standing"] {
		t.Fatalf("a nested comma list split into %v", got)
	}
}

// .
// .
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

// .
// .
// .
// .
// .
func TestDamageAndDisciplineAreDifferentFailures(t *testing.T) {
	// .
	// .
	// .
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

	// .
	// .
	// .
	undeclared := shapeErr(t, "", `CREATE TABLE rogue (id TEXT PRIMARY KEY)`)
	if errors.As(undeclared, &shape) {
		t.Fatalf("an undeclared table reported as damage — it is a DDL violation and must fail the boot: %v", undeclared)
	}
	if !strings.Contains(undeclared.Error(), "rogue") {
		t.Fatalf("the failure does not name the rogue object: %v", undeclared)
	}
}
