package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// .
// .
// .
// .
func TestReconcileSeesAConstraintChangeUnderAnUnchangedName(t *testing.T) {
	const before = `
CREATE TABLE notes (
    id TEXT PRIMARY KEY,
    body TEXT,
    weight INTEGER
);`
	const after = `
CREATE TABLE notes (
    id TEXT PRIMARY KEY,
    body TEXT NOT NULL DEFAULT '',
    weight REAL
);`
	s, _ := driftDB(t, before,
		`INSERT INTO notes VALUES ('n1','kept',3)`,
		`INSERT INTO notes VALUES ('n2','also kept',4)`)
	defer s.db.Close()

	rep, err := s.reconcileSchema(after)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Rebuilt) != 1 {
		t.Fatalf("a changed declaration under an unchanged name was not seen: %+v", rep)
	}
	// .
	line := rep.Rebuilt[0]
	for _, want := range []string{"body", "weight", "not-null", "type"} {
		if !strings.Contains(line, want) {
			t.Errorf("receipt does not name %q: %s", want, line)
		}
	}
	// .
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows carried = %d, want 2 — a shape rebuild lost data", n)
	}
	var body string
	if err := s.db.QueryRow(`SELECT body FROM notes WHERE id='n1'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "kept" {
		t.Fatalf("value not carried: %q", body)
	}
	// .
	// .
	// .
	rep2, err := s.reconcileSchema(after)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if !rep2.Unneeded {
		t.Fatalf("reconcile did not converge — it would rebuild every boot: %+v", rep2)
	}
}

// .
// .
// .
func TestReferenceShapeAgreesWithARealDatabase(t *testing.T) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	want, err := referenceShape(string(raw))
	if err != nil {
		t.Fatalf("reference shape: %v", err)
	}
	if len(want) == 0 {
		t.Fatal("reference shape is empty — the scratch database learned nothing")
	}
	s, err := New(filepath.Join(t.TempDir(), "real.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for table, cols := range want {
		live, err := s.liveShape(table)
		if err != nil {
			t.Fatalf("live shape of %s: %v", table, err)
		}
		if d := shapeDifferences(live, cols); len(d) > 0 {
			t.Errorf("a freshly created %s disagrees with its own declaration: %v", table, d)
		}
	}
}

// .
// .
// .
// .
// .
func TestRebuildLeavesForeignKeysEnforcedOnThePool(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "fk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	// .
	if _, err := s.db.Exec(`ALTER TABLE runtime_meta ADD COLUMN stray TEXT`); err != nil {
		t.Fatalf("seed drift: %v", err)
	}
	if _, err := s.reconcileSchema(string(raw)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// .
	// .
	for i := 0; i < 25; i++ {
		var v int
		if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != 1 {
			t.Fatalf("a pooled connection came back with foreign keys OFF after a rebuild (read %d) — finding 7 all over again", i)
		}
	}
}

// .
// .
// .
func TestReconcileSweepsAStaleShadow(t *testing.T) {
	const before = `CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT);`
	const after = `CREATE TABLE notes (id TEXT PRIMARY KEY, body TEXT, tag TEXT NOT NULL DEFAULT '');`
	s, _ := driftDB(t, before, `INSERT INTO notes VALUES ('n1','kept')`)
	defer s.db.Close()

	// .
	if _, err := s.db.Exec(`CREATE TABLE notes__reconcile_shadow (id TEXT PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.reconcileSchema(after); err != nil {
		t.Fatalf("a stale shadow blocked the rebuild: %v", err)
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE name='notes__reconcile_shadow'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the shadow survived the rebuild — every later boot fails the audit on an undeclared object")
	}
	var body string
	if err := s.db.QueryRow(`SELECT body FROM notes WHERE id='n1'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != "kept" {
		t.Fatalf("value not carried: %q", body)
	}
}

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
func TestPooledPragmaStrandsAConnectionAndPinnedDoesNot(t *testing.T) {
	readAllOn := func(t *testing.T, s *Store) bool {
		t.Helper()
		for i := 0; i < 25; i++ {
			var v int
			if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&v); err != nil {
				t.Fatal(err)
			}
			if v != 1 {
				return false
			}
		}
		return true
	}

	t.Run("pooled pattern strands", func(t *testing.T) {
		s, err := New(filepath.Join(t.TempDir(), "pooled.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		busy, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
		busy.Rollback()

		if readAllOn(t, s) {
			t.Skip("pool did not strand a connection here; the hazard is timing-dependent and the pinned case below is the guarantee")
		}
	})

	t.Run("pinned pattern cannot strand", func(t *testing.T) {
		s, err := New(filepath.Join(t.TempDir(), "pinned.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		conn, err := s.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		busy, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
		busy.Rollback()
		conn.Close()

		if !readAllOn(t, s) {
			t.Fatal("a pinned connection was returned to the pool with foreign keys off — the restore did not reach the connection that was disabled")
		}
	})
}
