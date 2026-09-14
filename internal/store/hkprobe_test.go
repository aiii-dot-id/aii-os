package store

import (
	"context"
	"path/filepath"
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
func TestAnalysisLimitBindsOnlyOnAPinnedConnection(t *testing.T) {
	t.Run("pooled pattern loses the limit", func(t *testing.T) {
		s, err := New(filepath.Join(t.TempDir(), "pooled.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		if _, err := s.db.Exec(`PRAGMA analysis_limit=400`); err != nil {
			t.Fatal(err)
		}
		busy, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer busy.Rollback()

		var limit int
		if err := s.db.QueryRow(`PRAGMA analysis_limit`).Scan(&limit); err != nil {
			t.Fatal(err)
		}
		if limit != 0 {
			t.Skip("pool reused the same connection here; the pinned case below is the guarantee")
		}
	})

	t.Run("pinned pattern keeps it", func(t *testing.T) {
		s, err := New(filepath.Join(t.TempDir(), "pinned.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		conn, err := s.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(context.Background(), `PRAGMA analysis_limit=400`); err != nil {
			t.Fatal(err)
		}
		busy, err := s.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer busy.Rollback()

		var limit int
		if err := conn.QueryRowContext(context.Background(), `PRAGMA analysis_limit`).Scan(&limit); err != nil {
			t.Fatal(err)
		}
		if limit != 400 {
			t.Fatalf("a pinned connection lost its analysis_limit (%d) — the optimize would run unbounded", limit)
		}
	})
}
