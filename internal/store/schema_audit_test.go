package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The runtime half of sole ownership: auditSchema holds the live
// database to exactly what schema.sql declares. These tests prove the
// audit has teeth in both directions, using the sanctioned exemption —
// test code may issue DDL directly.

func TestSchemaComponentsUniqueIndex(t *testing.T) {
	got, err := schemaComponents(`CREATE TABLE IF NOT EXISTS t1 (id INTEGER);
CREATE UNIQUE INDEX IF NOT EXISTS uq1 ON t1 (id);
CREATE INDEX IF NOT EXISTS ix1 ON t1 (id);
CREATE TRIGGER IF NOT EXISTS tg1 AFTER INSERT ON t1 BEGIN SELECT 1; END;
CREATE VIEW IF NOT EXISTS v1 AS SELECT id FROM t1; -- prose about CREATE INDEX phantom
-- full-line comment: CREATE TABLE phantom_t should not declare
SELECT 1; CREATE TABLE IF NOT EXISTS t2 (id INTEGER);`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"table:t1": true, "index:uq1": true, "index:ix1": true,
		"trigger:tg1": true, "view:v1": true, "table:t2": true,
	}
	if len(got) != len(want) {
		t.Fatalf("schemaComponents returned %d keys (%v), want exactly %d (%v)",
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

	var undeclared *UndeclaredObjectError
	if !errors.As(err, &undeclared) || len(undeclared.Objects) != 1 || undeclared.Objects[0] != "table:rogue" {
		t.Fatalf("audit refusal is not the typed undeclared-object error naming table:rogue: %#v", err)
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

// TestAuditRejectsAdHocIndex (above) and TestAuditRejectsAdHocTable
// pin the other side: everything that is not a view is held to the
// file, without exception.
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

// TestNewRejectsDatabaseWithAdHocTable proves the wiring: a database
// that gained a rogue object (any means — bug, debug session, hand-typed
// SQL) FAILS the next writable boot instead of carrying the object
// silently. This is the directive made mechanical at the boot boundary.
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

// TestOpenReadOnlySkipsAudit pins the design boundary: the SAFE mount
// displays state when integrity is already in question — it must never
// add its own failure modes. It mounts the rogue-containing database
// read-only and succeeds.
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
