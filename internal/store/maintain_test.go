package store

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// .
// .
// .
// .
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

	// .
	for i := 0; i < 400; i++ {
		if _, err := s.db.Exec(
			`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at)
			 VALUES (?, 'default', 'operator', 'x', ?, '2026-08-29T00:00:00Z')`,
			"c"+strings.Repeat("0", 3)+itoa(i), i); err != nil {
			t.Fatal(err)
		}
	}
	note, err := s.Housekeep()
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

// .
// .
// .
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
	if _, err := s.Housekeep(); err != nil {
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

// .
// .
// .
// .
func TestForeignKeyCheckCatchesWhatQuickCheckMisses(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "fkc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.ForeignKeyCheck(); err != nil {
		t.Fatalf("a fresh database already has orphans: %v", err)
	}

	// .
	// .
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

	// .
	if err := s.QuickCheck(); err != nil {
		t.Fatalf("quick_check unexpectedly reported the orphan: %v", err)
	}
	// .
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

// .
// .
// .
// .
// .
func TestHousekeepBoundsTheAnalyze(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "bound.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20000; i++ {
		if _, err := tx.Exec(
			`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at)
			 VALUES (?, 'default', 'operator', 'x', ?, '2026-08-29T00:00:00Z')`,
			"c"+itoa(i), i); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if _, err := s.Housekeep(); err != nil {
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

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
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

	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	err = ro.Close()
	log.SetOutput(prev)
	if err != nil {
		t.Fatalf("read-only close failed: %v", err)
	}
	if strings.Contains(buf.String(), "optimize on close") {
		t.Fatalf("read-only close attempted an optimize and logged about it: %s", buf.String())
	}
}
