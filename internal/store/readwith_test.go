package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

func TestAWriterReturnsWhileAReadIsOpen(t *testing.T) {
	for _, format := range []string{compressvfs.FormatSQLite, compressvfs.FormatZstd} {
		t.Run(format, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "aii.db")
			s, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			activation, err := s.ActivateFormatAtStartup(t.Context(), path, format)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			s = activation.Opened
			defer s.Close()
			if err := s.AddConversationTurn("operator", "before the read"); err != nil {
				t.Fatal(err)
			}

			holding, release := make(chan struct{}), make(chan struct{})
			read := make(chan error, 1)
			go func() {
				read <- s.ReadWith(t.Context(), func(db Reader) error {
					var n int
					if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM conversations`).Scan(&n); err != nil {
						return err
					}
					close(holding)
					<-release
					return nil
				})
			}()
			<-holding
			wrote := make(chan error, 1)
			go func() {
				err := s.AddConversationTurn("resident", "written during the read")
				_ = s.ValidateEvent("experience.recorded", 3, []byte(`{}`)) // the mint's preflight: its verdict is not the question, its wait is
				wrote <- err
			}()
			select {
			case err := <-wrote:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("a write waited on an open read")
			}
			close(release)
			if err := <-read; err != nil {
				t.Fatal(err)
			}

			// Control: the read lock ReadWith once held blocks the same writer.
			s.mu.RLock()
			blocked := make(chan error, 1)
			go func() { blocked <- s.AddConversationTurn("resident", "written behind the lock") }()
			select {
			case <-blocked:
				s.mu.RUnlock()
				t.Fatal("the writer did not wait for a held read lock; the probe cannot see a block")
			case <-time.After(200 * time.Millisecond):
			}
			s.mu.RUnlock()
			if err := <-blocked; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A READ CANNOT WRITE. The snapshot is query_only, so a statement that
// would change the store is refused by SQLite itself, whatever surface
// reaches it.
func TestAReadCannotWrite(t *testing.T) {
	s := testStore(t)
	err := s.ReadWith(context.Background(), func(db Reader) error {
		var key string
		return db.QueryRowContext(context.Background(), `INSERT INTO runtime_meta(key, value) VALUES ('t27', 'x') RETURNING key`).Scan(&key)
	})
	if err == nil {
		t.Fatal("a write inside a read was accepted")
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM runtime_meta WHERE key = 't27'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the refused write landed: %d %v", n, err)
	}
}
