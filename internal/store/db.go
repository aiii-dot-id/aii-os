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
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// .
type Store struct {
	db *sql.DB
	mu sync.RWMutex
	// .
	// .
	workObserver func(WorkEvent)
	wqFrozen     bool
	// .
	// .
	operatorASCII atomic.Bool
	// .
	// .
	// .
	claimLimits map[string]int
	// .
	// .
	readOnly bool
	// .
	// .
	// .
	mirrorHead uint64

	// .
	// .
	// .
	// .
	// .
	// .
	txh *sql.Tx

	outboxListeners []func()

	// .
	// .
	activeProject string
}

// .
// .
type dbi interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// .
// .
// .
// .
func (s *Store) h() dbi {
	if s.txh != nil {
		return s.txh
	}
	return s.db
}

// .
// .
func New(path string) (*Store, error) {
	// .
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return nil, fmt.Errorf("cannot create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("cannot open database: %w", err)
	}

	s := &Store{db: db}

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
	if raw, rerr := schemaFS.ReadFile("schema.sql"); rerr == nil {
		notes, _ := s.applyDeclaredReplacements(string(raw))
		for _, line := range notes {
			log.Print(line)
		}
	}

	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema init failed: %w", err)
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
	var forcedSidecars []string
	if raw, rerr := schemaFS.ReadFile("schema.sql"); rerr == nil {
		rep, err := s.reconcileSchema(string(raw))
		forcedSidecars = rep.RebuildSidecars
		for _, line := range rep.Lines() {
			log.Print(line)
		}
		if err != nil {
			// .
			// .
			// .
			log.Printf("RECONCILE: could not complete — %v", err)
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := s.auditSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema audit failed: %w", err)
	}

	// .
	// .
	if err := probeMemoryFunctions(db); err != nil {
		db.Close()
		return nil, err
	}

	// .
	// .
	// .
	// .
	// .
	for _, line := range s.ensureSidecars(forcedSidecars...) {
		log.Print(line)
	}

	// .
	// .
	s.restoreActiveProject()

	return s, nil
}

// .
// .
// .
// .
// .
// .
// .
func OpenReadOnly(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no database to mount read-only: %w", err)
	}
	dsn := "file:" + path +
		"?_pragma=query_only(1)" +
		"&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("cannot open database read-only: %w", err)
	}
	s := &Store{db: db, readOnly: true}
	// .
	// .
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
		db.Close()
		return nil, fmt.Errorf("read-only mount cannot read: %w", err)
	}
	return s, nil
}

// .
// .
// .
// .
// .
func NewMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)&_pragma=recursive_triggers(1)")
	if err != nil {
		return nil, fmt.Errorf("cannot open memory database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory schema init failed: %w", err)
	}
	if err := s.auditSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory schema audit failed: %w", err)
	}
	if err := probeMemoryFunctions(db); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func sqliteDSN(path string) string {
	return "file:" + path +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		// .
		// .
		// .
		// .
		// .
		"&_pragma=recursive_triggers(1)" +
		"&_pragma=journal_mode(WAL)" +
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		"&_pragma=journal_size_limit(8388608)"
}

// .
func (s *Store) initSchema() error {
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("cannot read embedded schema: %w", err)
	}

	if _, err := s.db.Exec(string(schemaBytes)); err != nil {
		return fmt.Errorf("schema execution failed: %w", err)
	}

	return nil
}

// .
func (s *Store) Close() error {
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
	// .
	if s.readOnly {
		return s.db.Close()
	}
	// .
	// .
	// .
	// .
	if conn, cerr := s.db.Conn(context.Background()); cerr != nil {
		log.Printf("store: optimize on close skipped: %v", cerr)
	} else {
		if _, err := conn.ExecContext(context.Background(), "PRAGMA analysis_limit=400"); err != nil {
			log.Printf("store: optimize on close skipped: %v", err)
		} else if _, err := conn.ExecContext(context.Background(), "PRAGMA optimize"); err != nil {
			log.Printf("store: optimize on close skipped: %v", err)
		}
		conn.Close()
	}
	return s.db.Close()
}

// .
func (s *Store) DB() *sql.DB {
	return s.db
}

// .
// .
// .
// .
func (s *Store) MaxLedgerSeq() (uint64, error) {
	var seq uint64
	err := s.h().QueryRow("SELECT COALESCE(MAX(seq), 0) FROM ledger").Scan(&seq)
	if err == nil && s.mirrorHead > seq {
		seq = s.mirrorHead
	}
	return seq, err
}
