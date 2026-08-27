// Package store implements SQLite-backed persistence for AII OS.
//
// The store holds projections of the ledger (beliefs, self-model syntheses, experiences,
// conversations, work_sessions, intentions, edges) and runtime state (alarms,
// identity_lifetime, outbox). All projections are f(ledger) — rebuildable
// from the chain. Runtime state is ephemeral.
//
// SQLite-only in the minimal version. Schema constraints (CHECKs, unique
// edge triples, ledger-seq references) are enforced here; NOTE: entity-id
// endpoints in edges are polymorphic (belief/experience/intention/...) so
// they carry no FK — endpoint validity is enforced at the ENGINE mint
// path (EntityExists), not by the schema. When PG arrives, it's additive.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite" // Pure Go SQLite driver, no CGO
)

//go:embed schema.sql
var schemaFS embed.FS

// Store wraps a SQLite database connection.
type Store struct {
	db       *sql.DB
	mu       sync.RWMutex
	wqFrozen bool // SAFE-mode work-queue freeze (no claims/sweeps/mutations)

	// txh is the open materialization transaction, nil outside one. Every
	// materializer statement routes through h() so ONE code path serves
	// three transactional shapes (canon PROJECTION.md + EVENT_VALIDATION.md
	// step 5): the live per-event mirror+effect pair, replay's
	// all-or-nothing rebuild, and ValidateEvent's rollback-only preflight.
	// Guarded by mu — the transaction lifecycle always holds the write lock.
	txh *sql.Tx

	outboxListeners []func() // outbox push hooks (see outbox.go)

	// activeProject is the current project focus (R62) — runtime
	// attribution for turns and work sessions; never a ledger surface.
	activeProject string
}

// dbi is the statement surface shared by *sql.DB and *sql.Tx — what a
// materializer needs, and nothing a materializer must not have.
type dbi interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// h returns the materializer statement target: the in-flight
// transaction when one is open, else the plain connection. Caller holds
// s.mu (the same lock the transaction lifecycle holds), so the handle
// can never be stale.
func (s *Store) h() dbi {
	if s.txh != nil {
		return s.txh
	}
	return s.db
}

// New opens or creates the SQLite database at the given path and ensures
// the schema is initialized.
func New(path string) (*Store, error) {
	// Ensure parent directory exists
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

	// Initialize the schema. schema.sql is the SOLE owner of every table,
	// index, and component this database contains (operator directive
	// 08-22, corrected from 08-23 — see commit history): no DDL lives in
	// Go source, none is issued ad-hoc at runtime. Evolution means editing
	// schema.sql — additive, with CREATE ... IF NOT EXISTS — before any
	// code that depends on it.
	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema init failed: %w", err)
	}

	// Runtime half of sole ownership: the live database must hold
	// EXACTLY what schema.sql declares — no ad-hoc additions from any
	// source. Fails the boot loudly rather than normalizing silently.
	if err := s.auditSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema audit failed: %w", err)
	}

	// Restore process-scoped runtime state AFTER the schema audit: the
	// audit guarantees runtime_meta exists before anything reads it.
	s.restoreActiveProject()

	return s, nil
}

// OpenReadOnly opens an EXISTING database for display and diagnostics
// only — the boot-SAFE mount (canon SAFE_MODE.md §3.2: "The last
// known-good projection state (identity.db) remains queryable"; local
// R55: no database writes while integrity is unverified). query_only
// rides the DSN so EVERY pooled connection refuses SQL mutations (the
// finding-7 lesson: a pragma executed once reaches one connection).
// No schema init, no migrations — this mount changes nothing, ever.
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
	s := &Store{db: db}
	// Prove the mount actually reads (and fail loudly now, not at the
	// first dashboard query).
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
		db.Close()
		return nil, fmt.Errorf("read-only mount cannot read: %w", err)
	}
	return s, nil
}

// NewMemory opens an empty in-memory store — the boot-SAFE fallback
// when no prior projection database exists. Zero durable footprint; the
// operator surface reads honest zeros. MaxOpenConns(1) because each
// pooled connection would otherwise get its OWN private :memory:
// database and the schema would exist on only one of them.
func NewMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
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
	return s, nil
}

// sqliteDSN builds the connection string with PRAGMAS IN THE DSN — the
// only place they apply to EVERY pooled connection. `PRAGMA foreign_keys`
// is per-connection state: executing it once on a pooled *sql.DB set it
// on ONE connection while later queries landed on fresh connections with
// FKs silently OFF (finding 7, 2026-08-17 review — every FK in the schema
// was advisory-by-accident). busy_timeout (5s) lands here too: concurrent
// readers plus one writer under WAL deserve a queue, not an immediate
// SQLITE_BUSY error.
func sqliteDSN(path string) string {
	return "file:" + path +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)"
}

// initSchema reads and executes the embedded schema.sql.
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

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for direct access (used by other packages).
func (s *Store) DB() *sql.DB {
	return s.db
}

// MaxLedgerSeq is the highest event seq the projection mirror has ever
// materialized — the runtime's own memory of what it acknowledged,
// consulted at boot BEFORE replay wipes and rebuilds the mirror. Zero
// on a fresh database.
func (s *Store) MaxLedgerSeq() (uint64, error) {
	var seq uint64
	err := s.h().QueryRow("SELECT COALESCE(MAX(seq), 0) FROM ledger").Scan(&seq)
	return seq, err
}
