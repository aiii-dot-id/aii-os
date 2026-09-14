package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .

// .
// .
// .
// .
// .
// .
// .
// .
func TestParseDeclarationsUniqueIndex(t *testing.T) {
	got := parseDeclarations(`CREATE TABLE IF NOT EXISTS t1 (id INTEGER);
CREATE UNIQUE INDEX IF NOT EXISTS uq1 ON t1 (id);
CREATE INDEX IF NOT EXISTS ix1 ON t1 (id);
CREATE TRIGGER IF NOT EXISTS tg1 AFTER INSERT ON t1 BEGIN SELECT 1; END;
CREATE VIEW IF NOT EXISTS v1 AS SELECT id FROM t1; -- prose about CREATE INDEX phantom
-- full-line comment: CREATE TABLE phantom_t should not declare
SELECT 1; CREATE TABLE IF NOT EXISTS t2 (id INTEGER);`)
	want := map[string]bool{
		"table:t1": true, "index:uq1": true, "index:ix1": true,
		"trigger:tg1": true, "view:v1": true, "table:t2": true,
	}
	if len(got) != len(want) {
		t.Fatalf("parseDeclarations returned %d keys (%v), want exactly %d (%v)",
			len(got), got, len(want), want)
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing %q; got %v", k, got)
		}
	}
	for k := range got {
		if !want[k] {
			t.Errorf("unexpected %q (phantom declaration); got %v", k, got)
		}
	}
}

func TestAuditAcceptsCleanSchema(t *testing.T) {
	s := testStore(t)
	defer s.Close()
	if err := s.auditSchema(); err != nil {
		t.Fatalf("clean store failed audit: %v", err)
	}
}

func TestAuditRejectsAdHocTable(t *testing.T) {
	s := testStore(t)
	defer s.Close()
	if _, err := s.h().Exec("CREATE TABLE rogue (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	err := s.auditSchema()
	if err == nil {
		t.Fatal("audit accepted a database containing an ad-hoc table — sole ownership is not enforced at runtime")
	}
	if !strings.Contains(err.Error(), "rogue") {
		t.Fatalf("audit error should name the rogue object, got: %v", err)
	}
}

func TestAuditRejectsAdHocIndex(t *testing.T) {
	s := testStore(t)
	defer s.Close()
	if _, err := s.h().Exec("CREATE INDEX rogue_idx ON conversations(session_id)"); err != nil {
		t.Fatal(err)
	}
	err := s.auditSchema()
	if err == nil {
		t.Fatalf("audit accepted an ad-hoc index")
	}
	if !strings.Contains(err.Error(), "rogue_idx") {
		t.Fatalf("audit error should name the rogue index, got: %v", err)
	}
}

// .
// .
// .
// .
// .
func TestAuditAcceptsCodeCreatedView(t *testing.T) {
	s := testStore(t)
	defer s.Close()
	if _, err := s.h().Exec("CREATE VIEW rogue_v AS SELECT session_id FROM conversations"); err != nil {
		t.Fatal(err)
	}
	if err := s.auditSchema(); err != nil {
		t.Fatalf("audit rejected a code-created view — views are the sanctioned exception: %v", err)
	}
}

// .
// .
// .
func TestAuditRejectsAdHocTrigger(t *testing.T) {
	s := testStore(t)
	defer s.Close()
	if _, err := s.h().Exec("CREATE TRIGGER rogue_t AFTER INSERT ON conversations BEGIN SELECT 1; END"); err != nil {
		t.Fatal(err)
	}
	err := s.auditSchema()
	if err == nil {
		t.Fatal("audit accepted an ad-hoc trigger")
	}
	if !strings.Contains(err.Error(), "rogue_t") {
		t.Fatalf("audit error should name the rogue trigger, got: %v", err)
	}
}

// .
// .
// .
// .
func TestNewRejectsDatabaseWithAdHocTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	first, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.h().Exec("CREATE TABLE rogue (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := New(path)
	if err == nil {
		second.Close()
		t.Fatal("New accepted a database containing an ad-hoc table — the boot boundary does not enforce sole ownership")
	}
	if !strings.Contains(err.Error(), "rogue") {
		t.Fatalf("New error should name the rogue object, got: %v", err)
	}
}

// .
// .
// .
// .
func TestOpenReadOnlySkipsAudit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	first, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.h().Exec("CREATE TABLE rogue (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	first.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("read-only mount must tolerate existing rogue objects: %v", err)
	}
	ro.Close()
}
