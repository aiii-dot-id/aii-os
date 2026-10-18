package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// A scoped observation at the real driver's write boundary, following the
// schema cleanup fault-driver idiom. There is no alternate store or SQL engine.
type writerBoundaryDriver struct {
	driver.Driver
	before func(string) error // installed before the observed call; no concurrent mutation
}

func (d *writerBoundaryDriver) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &writerBoundaryConn{Conn: c, owner: d}, nil
}

type writerBoundaryConn struct {
	driver.Conn
	owner *writerBoundaryDriver
}

func (c *writerBoundaryConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *writerBoundaryConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.owner.before != nil {
		if err := c.owner.before(query); err != nil {
			return nil, err
		}
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func TestRuntimeWritersCannotBypassTheStoreTransactionOwner(t *testing.T) {
	for _, tc := range []struct {
		name, table string
		write       func(*Store) error
	}{
		{"speech", "speech_usage", func(s *Store) error {
			return s.AddSpeechUse(SpeechUse{Provider: "fixture", Direction: "tts", Period: "2026-09", Requests: 1, Characters: 5})
		}},
		{"turn", "turn_metrics", func(s *Store) error {
			return s.InsertTurnMetric(TurnMetric{TsMs: 1, Calls: 1})
		}},
		{"subagent", "subagent_metrics", func(s *Store) error {
			return s.InsertSubagentMetric(SubagentMetric{SessionID: "fixture", TsMs: 1, Calls: 1})
		}},
		{"proposal", "skill_proposals", func(s *Store) error {
			_, err := s.ProposeSkill("fixture", "a proposed change", "disposable evidence")
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := compressvfs.Register(); err != nil {
				t.Fatal(err)
			}
			dsn := sqliteDSN(filepath.Join(t.TempDir(), "identity.db")) + "&vfs=" + compressvfs.Name
			// Reuse the registered production driver, including the memory
			// scalar functions; a new sqlite.Driver would omit those hooks.
			native, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			d := &writerBoundaryDriver{Driver: native.Driver()}
			if err := native.Close(); err != nil {
				t.Fatal(err)
			}
			name := "writer-boundary-" + rand.Text()
			sql.Register(name, d)
			db, err := sql.Open(name, dsn)
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxIdleConns(storeIdleConnections)
			s, err := prepareSchemaStore(t.Context(), db, nil, nil, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			var journal string
			if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "wal" {
				t.Fatalf("fixture must use the production WAL shape: %q %v", journal, err)
			}
			var competing *sql.Tx
			t.Cleanup(func() {
				if competing != nil {
					_ = competing.Rollback()
					s.mu.Unlock()
				}
			})
			visits := 0
			d.before = func(query string) error {
				if !strings.Contains(query, tc.table) {
					return nil
				}
				visits++
				// Only this test's writer can hold the mutex here. TryRLock
				// distinguishes exclusive writer ownership from a read lock.
				if !s.mu.TryRLock() {
					return nil
				}
				s.mu.RUnlock()
				if !s.mu.TryLock() {
					return errors.New("writer holds only a read lock")
				}
				// An outsider reached SQL before taking the store owner.
				// Admit the store's read-first snapshot now, while that
				// outsider has not committed yet. No scheduling race.
				competing, err = s.w().Begin()
				if err != nil {
					s.mu.Unlock()
					return err
				}
				var seq int64
				return competing.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM ledger`).Scan(&seq)
			}
			writeErr := tc.write(s)
			d.before = nil
			if competing != nil {
				_, conflict := competing.Exec(`INSERT INTO runtime_meta(key,value,updated_at) VALUES ('writer_control','kept','2026-09-27T00:00:00Z')`)
				var se *sqlite.Error
				if !errors.As(conflict, &se) || se.Code() != sqlite3.SQLITE_BUSY_SNAPSHOT {
					t.Fatalf("bypass did not exercise the snapshot conflict: %v (writer: %v)", conflict, writeErr)
				}
				t.Fatalf("runtime writer bypassed Store.mu and invalidated its admitted snapshot: %v", conflict)
			}
			if writeErr != nil || visits != 1 {
				t.Fatalf("writer did not reach SQL once under its owner: visits=%d err=%v", visits, writeErr)
			}
			if !s.mu.TryLock() {
				t.Fatal("successful write left the store owner locked")
			}
			s.mu.Unlock()
			var rows int
			if err := db.QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(tc.table)).Scan(&rows); err != nil || rows != 1 {
				t.Fatalf("owned write did not persist its row: rows=%d err=%v", rows, err)
			}
			s.SetFrozen(true, "writer ownership fixture")
			var frozen *FrozenError
			if err := tc.write(s); !errors.As(err, &frozen) {
				t.Fatalf("writer bypassed the existing SAFE refusal: %v", err)
			}
			if !s.mu.TryLock() {
				t.Fatal("refused write left the store owner locked")
			}
			s.mu.Unlock()
			s.SetFrozen(false, "")
			// With the normal writer finished, the next owned read-first
			// transaction still commits; the repair did not disable writes.
			s.mu.Lock()
			defer s.mu.Unlock()
			tx, err := s.w().Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var seq int64
			if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM ledger`).Scan(&seq); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO runtime_meta(key,value,updated_at) VALUES ('writer_control',?,'2026-09-27T00:00:00Z')`, fmt.Sprint(seq)); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
