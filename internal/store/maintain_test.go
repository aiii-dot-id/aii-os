package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// PRAGMA optimize is the operation SQLite documents for long-running
// connections, and it had never run: sqlite_stat1 did not exist on a
// 31 MB live database, so the query planner had never been given a
// single statistic. The pin is that housekeeping produces them.
func TestHousekeepGivesThePlannerStatistics(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var before int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE name='sqlite_stat1'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("a fresh database already had planner statistics (%d) — the premise is wrong", before)
	}

	// optimize analyzes tables worth analyzing; give it one.
	for i := 0; i < 400; i++ {
		if _, err := s.db.Exec(
			`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at)
			 VALUES (?, 'default', 'operator', 'x', ?, '2026-08-29T00:00:00Z')`,
			"c"+strings.Repeat("0", 3)+itoa(i), i); err != nil {
			t.Fatal(err)
		}
	}
	note, err := s.Housekeep(t.Context())
	if err != nil {
		t.Fatalf("housekeep: %v", err)
	}
	if !strings.Contains(note, "optimize ok") {
		t.Fatalf("note does not report the optimize: %q", note)
	}

	var after int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE name='sqlite_stat1'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after == 0 {
		t.Fatal("housekeeping ran and the planner still has no statistics — PRAGMA optimize did nothing")
	}
}

func TestHousekeepTouchesNoIdentityRow(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "rows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.db.Exec(
		`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at)
		 VALUES ('c1','default','operator','the exact words', 1, '2026-08-29T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Housekeep(t.Context()); err != nil {
		t.Fatal(err)
	}
	var content string
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT content FROM conversations WHERE id='c1'`).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if n != 1 || content != "the exact words" {
		t.Fatalf("housekeeping altered identity data: rows=%d content=%q", n, content)
	}
}

// foreign_key_check is quick_check's blind spot: quick_check validates
// page structure and never looks at referential integrity, so an
// orphan passes it in silence. That is exactly the damage silently
// disabled enforcement leaves behind.
func TestForeignKeyCheckCatchesWhatQuickCheckMisses(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "fkc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.ForeignKeyCheck(); err != nil {
		t.Fatalf("a fresh database already has orphans: %v", err)
	}

	// Plant an orphan the way a connection with enforcement off would:
	// insert a child whose parent never existed.
	if _, err := s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO work_sessions (id, description, status, created_seq)
		 VALUES ('orphan','no parent','active', 999999)`); err != nil {
		t.Skipf("schema does not permit this orphan shape here: %v", err)
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}

	// quick_check is content with it — that is the blind spot.
	if err := s.QuickCheck(); err != nil {
		t.Fatalf("quick_check unexpectedly reported the orphan: %v", err)
	}
	// foreign_key_check is not.
	if err := s.ForeignKeyCheck(); err == nil {
		t.Fatal("foreign_key_check passed a row whose parent does not exist — the blind spot is still blind")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// analysis_limit MUST precede optimize. Without it SQLite may scan an
// entire index to gather statistics, and a housekeeping pass that can
// run unbounded is not housekeeping. This pins the bound, not the
// wording: with the limit in force the pass stays quick on a table
// large enough that an unbounded ANALYZE would not.
func TestHousekeepBoundsTheAnalyze(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "bound.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.db.Exec(
		`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM n WHERE i < 19999)
		 INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at)
		 SELECT 'c' || i, 'default', 'operator', 'x', i, '2026-08-29T00:00:00Z' FROM n`); err != nil {
		t.Fatal(err)
	}

	var rows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM conversations").Scan(&rows); err != nil || rows != 20000 {
		t.Fatalf("the bound is claimed on 20k rows, the table holds %d (%v)", rows, err)
	}

	start := time.Now()
	if _, err := s.Housekeep(t.Context()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("housekeeping took %s on 20k rows — the ANALYZE is not bounded", d)
	}

	var limit int
	if err := s.db.QueryRow("PRAGMA analysis_limit").Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if limit == 0 {
		t.Fatal("analysis_limit is 0 (unbounded) after housekeeping — ANALYZE may scan whole indexes")
	}
}

// The write-ahead log must be bounded CONTINUOUSLY, not only on the
// day the maintenance alarm happens to fire. journal_size_limit
// truncates after every checkpoint; without it SQLite leaves the file
// at its high-water mark forever.
func TestWALIsBoundedByTheConnectionItself(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "wal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var limit int64
	if err := s.db.QueryRow("PRAGMA journal_size_limit").Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if limit <= 0 {
		t.Fatalf("journal_size_limit is %d — the WAL grows to its high-water mark and stays there", limit)
	}
}

// Closing a read-only mount must not attempt to optimize. optimize
// runs ANALYZE, ANALYZE writes sqlite_stat1, and query_only(1) refuses
// it — "attempt to write a readonly database (8)". Left unguarded,
// every read-only close logged an error that meant nothing, which is
// how a loud substrate teaches people to stop reading its logs.
func TestReadOnlyCloseDoesNotAttemptToOptimize(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ro.db")
	s, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenReadOnly(p)
	if err != nil {
		t.Fatal(err)
	}
	if !ro.readOnly {
		t.Fatal("a query_only mount is not marked read-only — Close cannot know to skip the write")
	}

	buf := logsink.CaptureForTest(t)
	err = ro.Close()
	if err != nil {
		t.Fatalf("read-only close failed: %v", err)
	}
	if strings.Contains(buf.String(), "optimize on close") {
		t.Fatalf("read-only close attempted an optimize and logged about it: %s", buf.String())
	}
}
