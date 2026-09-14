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

func TestJSONColumnsRefuseWhatIsNotJSON(t *testing.T) {
	s := testStore(t)
	db := s.DB()
	bad := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err == nil || !strings.Contains(strings.ToUpper(err.Error()), "CHECK") {
			t.Fatalf("a non-JSON value was accepted: %v\n%s", err, query)
		}
	}
	good := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("a JSON value was refused: %v\n%s", err, query)
		}
	}
	const receipt = `INSERT INTO plugin_receipts (receipt_id, plugin_id, operation, target, success, receipt_json, created_at) VALUES (?, 'p', 'kv.put', 'k', 1, ?, '2026-09-10T00:00:00Z')`
	bad(receipt, "r1", "not json at all")
	good(receipt, "r2", `{"host_authored":true}`)
	const mirror = `INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (?, '', '2026-09-10T00:00:00Z', 'test', 3, ?, '', '')`
	bad(mirror, 990001, "{unclosed")
	good(mirror, 990002, `{"id":"x"}`)
}

func TestTheMemoryFunctionsAreProbedAtBoot(t *testing.T) {
	s := testStore(t)
	if err := probeMemoryFunctions(s.DB()); err != nil {
		t.Fatalf("the probe fails on a live store: %v", err)
	}
	// .
	// .
	s2, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}

// .
// .
func TestADatabaseWithoutTheJSONChecksGainsThemAtBoot(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB().Exec(`INSERT INTO plugin_receipts (receipt_id, plugin_id, operation, target, success, receipt_json, created_at) VALUES ('r1', 'p', 'kv.put', 'k', 1, '{"ok":true}', '2026-09-10T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig) VALUES (1, '', '2026-09-10T00:00:00Z', 'test', 3, '{"id":"x"}', '', '')`); err != nil {
		t.Fatal(err)
	}
	real := schemaText(t)
	older := real
	for _, check := range []string{
		" CHECK (json_valid(payload))",
		" CHECK (json_valid(receipt_json))",
		" CHECK (json_valid(envelope_json))",
	} {
		if !strings.Contains(older, check) {
			t.Fatalf("the schema no longer carries %q", check)
		}
		older = strings.ReplaceAll(older, check, "")
	}
	// .
	rep, err := s.reconcileSchema(older)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rebuilt) != 5 {
		t.Fatalf("aging must rebuild the five tables: %+v", rep.Rebuilt)
	}
	if _, err := s.DB().Exec(`INSERT INTO plugin_receipts (receipt_id, plugin_id, operation, target, success, receipt_json, created_at) VALUES ('r2', 'p', 'kv.put', 'k', 1, 'not json', '2026-09-10T00:00:00Z')`); err != nil {
		t.Fatalf("the aged table must accept what the old declaration accepted: %v", err)
	}
	// .
	// .
	if _, err := s.reconcileSchema(real); err == nil || !strings.Contains(err.Error(), "plugin_receipts") {
		t.Fatalf("a non-JSON receipt must refuse the rebuild by name: %v", err)
	}
	if _, err := s.DB().Exec(`DELETE FROM plugin_receipts WHERE receipt_id = 'r2'`); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	rep, err = s.reconcileSchema(real)
	if err != nil {
		t.Fatalf("the next boot: %v", err)
	}
	if len(rep.Rebuilt) == 0 || !strings.Contains(strings.Join(rep.Rebuilt, "\n"), "plugin_receipts") {
		t.Fatalf("the next boot must rebuild plugin_receipts with its check: %+v", rep.Rebuilt)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM plugin_receipts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("receipts carried = %d (%v), want 1", n, err)
	}
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ledger`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("mirror rows carried = %d (%v), want 1", n, err)
	}
	mustConverge(t, s, real)
	if err := s.auditSchema(); err != nil {
		t.Fatalf("audit after the checks landed: %v", err)
	}
}
