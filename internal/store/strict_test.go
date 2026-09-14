package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
func TestEveryTableIsStrict(t *testing.T) {
	s := testStore(t)
	rows, err := s.DB().Query(`SELECT name, sql FROM sqlite_master WHERE type = 'table' AND sql IS NOT NULL AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	checked := 0
	for rows.Next() {
		var name, sql string
		if err := rows.Scan(&name, &sql); err != nil {
			t.Fatal(err)
		}
		if isSidecarObject(name) {
			continue
		}
		checked++
		if !strings.HasSuffix(strings.TrimSpace(sql), "STRICT") {
			t.Errorf("table %s is not STRICT: %s", name, sql)
		}
	}
	if checked < 30 {
		t.Fatalf("checked only %d tables", checked)
	}
	// .
	if _, err := s.DB().Exec(`INSERT INTO memory_access (store, id, count, last_at) VALUES ('experiences', 'x', 'not a number', '2026-09-10T00:00:00Z')`); err == nil {
		t.Fatal("STRICT must refuse a text where an integer is declared")
	}
}

func TestAnUnstrictDatabaseBecomesStrictAtBoot(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB().Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (1, '', '2026-09-10T00:00:00Z', 'test', 3, '{}', '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO inbound (id, channel, address, body, received_ms) VALUES ('in1', 'sms', 'x', 'kept', 1)`); err != nil {
		t.Fatal(err)
	}
	real := schemaText(t)
	older := strings.ReplaceAll(real, ") STRICT;", ");")
	if older == real {
		t.Fatal("the schema carries no STRICT to remove")
	}
	rep, err := s.reconcileSchema(older)
	if err != nil {
		t.Fatalf("aging to the unstrict shape: %v", err)
	}
	if len(rep.Rebuilt) < 30 {
		t.Fatalf("aging must rebuild every table: %d", len(rep.Rebuilt))
	}
	rep, err = s.reconcileSchema(real)
	if err != nil {
		t.Fatalf("the next boot: %v", err)
	}
	if len(rep.Rebuilt) < 30 {
		t.Fatalf("the boot must rebuild every table to STRICT: %d", len(rep.Rebuilt))
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM inbound WHERE body = 'kept'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows carried = %d (%v)", n, err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("mirror rows carried = %d (%v)", n, err)
	}
	mustConverge(t, s, real)
	if err := s.auditSchema(); err != nil {
		t.Fatalf("audit after STRICT: %v", err)
	}
}
