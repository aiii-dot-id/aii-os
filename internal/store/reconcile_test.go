package store

import (
	"database/sql"
	_ "modernc.org/sqlite"
	"path/filepath"
	"testing"
	"time"
)

// .
func driftDB(t *testing.T, oldSchema string, seed ...string) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "drift.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	for _, s := range seed {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed data: %v", err)
		}
	}
	return &Store{db: db}, path
}

const oldWork = `
CREATE TABLE ledger (seq INTEGER PRIMARY KEY, payload TEXT);
CREATE TABLE work_sessions (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_seq INTEGER REFERENCES ledger(seq),
    project_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_ws_status ON work_sessions(status);`

const newWork = `
CREATE TABLE ledger (seq INTEGER PRIMARY KEY, payload TEXT);
CREATE TABLE work_sessions (
    id TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_seq INTEGER REFERENCES ledger(seq),
    project_id TEXT NOT NULL DEFAULT '',
    focus TEXT NOT NULL DEFAULT '',
    plan TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_ws_status ON work_sessions(status);`

// .
func TestReconcileCarriesRowsThroughAnAddedColumn(t *testing.T) {
	s, _ := driftDB(t, oldWork,
		`INSERT INTO ledger VALUES (1,'genesis')`,
		`INSERT INTO work_sessions VALUES ('ws1','find the plans','active',1,'harbour')`,
		`INSERT INTO work_sessions VALUES ('ws2','draft it','delivered',1,'')`)
	defer s.db.Close()

	rep, err := s.reconcileSchema(newWork)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Rebuilt) != 1 {
		t.Fatalf("expected one rebuilt table, got %v", rep.Rebuilt)
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM work_sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows carried = %d, want 2 — the rebuild lost data", n)
	}
	var desc, project, focus string
	if err := s.db.QueryRow(`SELECT description, project_id, focus FROM work_sessions WHERE id='ws1'`).
		Scan(&desc, &project, &focus); err != nil {
		t.Fatal(err)
	}
	if desc != "find the plans" || project != "harbour" {
		t.Fatalf("values not carried: desc=%q project=%q", desc, project)
	}
	if focus != "" {
		t.Fatalf("new column should take its declared DEFAULT, got %q", focus)
	}
}

// .
// .
func TestReconcileRestoresIndexes(t *testing.T) {
	s, _ := driftDB(t, oldWork, `INSERT INTO ledger VALUES (1,'g')`)
	defer s.db.Close()
	if _, err := s.reconcileSchema(newWork); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_ws_status'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("the table's index did not survive the rebuild")
	}
}

// .
// .
func TestReconcileRebuildsATableOthersReference(t *testing.T) {
	oldParent := `
CREATE TABLE ledger (seq INTEGER PRIMARY KEY, payload TEXT);
CREATE TABLE beliefs (id TEXT PRIMARY KEY, first_seq INTEGER NOT NULL REFERENCES ledger(seq));`
	newParent := `
CREATE TABLE ledger (seq INTEGER PRIMARY KEY, payload TEXT, anchor TEXT NOT NULL DEFAULT '');
CREATE TABLE beliefs (id TEXT PRIMARY KEY, first_seq INTEGER NOT NULL REFERENCES ledger(seq));`
	s, _ := driftDB(t, oldParent,
		`INSERT INTO ledger VALUES (1,'genesis')`,
		`INSERT INTO beliefs VALUES ('b1',1)`)
	defer s.db.Close()

	if _, err := s.reconcileSchema(newParent); err != nil {
		t.Fatalf("rebuilding a referenced table failed: %v", err)
	}
	var kids int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM beliefs`).Scan(&kids); err != nil {
		t.Fatal(err)
	}
	if kids != 1 {
		t.Fatalf("child rows lost: %d — the DROP cascaded", kids)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violations after rebuild")
	}
	// .
	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatal("foreign key enforcement was left OFF after reconcile")
	}
}

// .
func TestReconcileRenameCarriesData(t *testing.T) {
	oldIn := `CREATE TABLE inbound (id TEXT PRIMARY KEY, address TEXT NOT NULL, created_ms INTEGER);`
	newIn := "CREATE TABLE inbound (\n id TEXT PRIMARY KEY,\n reach_address TEXT NOT NULL, -- renamed-from: address\n created_ms INTEGER\n);"
	s, _ := driftDB(t, oldIn, `INSERT INTO inbound VALUES ('c1','operator@example.test',1000)`)
	defer s.db.Close()

	rep, err := s.reconcileSchema(newIn)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Renamed) != 1 {
		t.Fatalf("expected one rename, got %v", rep.Renamed)
	}
	var addr string
	if err := s.db.QueryRow(`SELECT reach_address FROM inbound WHERE id='c1'`).Scan(&addr); err != nil {
		t.Fatal(err)
	}
	if addr != "operator@example.test" {
		t.Fatalf("renamed column lost its value: %q", addr)
	}
	// .
	// .
	rep2, err := s.reconcileSchema(newIn)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if !rep2.Unneeded {
		t.Fatalf("reconcile was not idempotent: %+v", rep2)
	}
}

// .
func TestReconcileIsANoOpWhenShapesMatch(t *testing.T) {
	s, _ := driftDB(t, newWork, `INSERT INTO ledger VALUES (1,'g')`)
	defer s.db.Close()
	rep, err := s.reconcileSchema(newWork)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Unneeded {
		t.Fatalf("reconcile acted on a database that already matched: %+v", rep)
	}
}

// .
func TestReconcileRefusesAmbiguousRenames(t *testing.T) {
	oldIn := `CREATE TABLE inbound (id TEXT PRIMARY KEY, address TEXT);`
	bad := "CREATE TABLE inbound (\n id TEXT PRIMARY KEY,\n a TEXT, -- renamed-from: address\n b TEXT -- renamed-from: address\n);"
	s, _ := driftDB(t, oldIn)
	defer s.db.Close()
	if _, err := s.reconcileSchema(bad); err == nil {
		t.Fatal("two columns claiming one source was accepted")
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
func TestReconcileAddsPredictedWithoutInventingAPlanningHistory(t *testing.T) {
	const oldTurnMetrics = `
CREATE TABLE turn_metrics (
    ts_ms    INTEGER PRIMARY KEY,
    calls    INTEGER NOT NULL,
    read_only INTEGER NOT NULL,
    spawned  INTEGER NOT NULL,
    harvested INTEGER NOT NULL
);`
	// .
	// .
	// .
	// .
	const newTurnMetrics = `
CREATE TABLE turn_metrics (
    ts_ms    INTEGER PRIMARY KEY,
    calls    INTEGER NOT NULL,
    read_only INTEGER NOT NULL,
    spawned  INTEGER NOT NULL,
    harvested INTEGER NOT NULL,
    predicted INTEGER NOT NULL DEFAULT 0,
    declared_ordinal INTEGER NOT NULL DEFAULT 0,
    independent INTEGER NOT NULL DEFAULT 0,
    rounds INTEGER NOT NULL DEFAULT 0
);`

	s, _ := driftDB(t, oldTurnMetrics,
		`INSERT INTO turn_metrics VALUES (1000, 127, 47, 0, 7)`,
		`INSERT INTO turn_metrics VALUES (2000, 12, 3, 1, 2)`)
	defer s.db.Close()

	rep, err := s.reconcileSchema(newTurnMetrics)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(rep.Rebuilt) != 1 {
		t.Fatalf("expected turn_metrics rebuilt, got %v", rep.Rebuilt)
	}

	var n, calls, ro, predicted int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM turn_metrics`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows carried = %d, want 2 — the rebuild lost measured history", n)
	}
	if err := s.db.QueryRow(`SELECT calls, read_only, predicted FROM turn_metrics WHERE ts_ms=1000`).
		Scan(&calls, &ro, &predicted); err != nil {
		t.Fatal(err)
	}
	if calls != 127 || ro != 47 {
		t.Fatalf("measured values not carried: calls=%d read_only=%d", calls, ro)
	}
	if predicted != 0 {
		t.Fatalf("predicted = %d on a turn taken before predictions existed, want 0", predicted)
	}

	// .
	plans, _, _, err := s.PlanCalibration(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if plans != 0 {
		t.Fatalf("PlanCalibration found %d plan(s) in a history that contains none", plans)
	}
}
