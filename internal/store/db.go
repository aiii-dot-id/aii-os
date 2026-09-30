package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

const storeIdleConnections = 2

type Store struct {
	db *sql.DB

	frozen frozenState

	mu sync.RWMutex

	workObserver func(WorkEvent)
	wqFrozen     bool

	operatorASCII atomic.Bool

	claimLimits map[string]int

	readOnly               bool
	historicalInteractions bool
	historicalProject      bool
	historicalAnnotations  bool

	historicalTurnMetrics bool

	txh               *sql.Tx
	schemaContext     context.Context
	schemaReplay      bool
	schemaStartup     bool
	schemaConversions map[string]RuntimeConversion

	interactionOnce     sync.Once
	interactionInstance string
	interactionObserver func()
	outboxListeners     []func()

	activeProject string
}

type dbi interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

func (s *Store) h() dbi {
	if s.txh != nil {
		if s.schemaContext != nil {
			return boundStatements{contextQueries: s.txh, ctx: s.schemaContext}
		}

		return s.txh
	}
	return s.w()
}

func New(path string) (*Store, error) {
	return openSchema(context.Background(), path, nil, nil, nil, false)
}

func OpenAndReplay(ctx context.Context, path string, head MirrorHead, source EventSource) (*Store, error) {
	if source == nil {
		return nil, &SchemaError{"source", fmt.Errorf("verified event source is required")}
	}
	return openSchema(ctx, path, &head, source, nil, false)
}

func OpenForStartup(ctx context.Context, path string, head MirrorHead, source EventSource) (*Store, error) {
	if source == nil {
		return nil, &SchemaError{"source", fmt.Errorf("verified event source is required")}
	}
	return openSchema(ctx, path, &head, source, nil, true)
}

func openSchema(ctx context.Context, path string, head *MirrorHead, source EventSource, carry *CarryReport, startup bool) (*Store, error) {
	if err := compressvfs.Register(); err != nil {
		return nil, err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", sqliteDSN(path)+"&vfs="+compressvfs.Name)
	if err != nil {
		return nil, err
	}
	db.SetMaxIdleConns(storeIdleConnections)
	return prepareSchemaStore(ctx, db, head, source, carry, startup)
}

func prepareSchemaStore(ctx context.Context, db *sql.DB, head *MirrorHead, source EventSource, carry *CarryReport, startup bool) (*Store, error) {
	s := &Store{db: db, schemaReplay: source != nil, schemaStartup: startup}
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		db.Close()
		return nil, err
	}
	var notes []string
	err = s.withSchema(ctx, string(raw), func() error {
		var objects int
		if err := s.h().QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'").Scan(&objects); err != nil {
			return err
		}
		var before map[string]int64
		if carry != nil {
			var e error
			before, e = s.catalogCounts()
			if e != nil {
				return e
			}
		}
		if head != nil {
			seq, e := s.acknowledgedSequence(string(raw))
			if e != nil {
				return e
			}
			if seq > head.Seq {
				return &MirrorAheadError{Mirror: seq, Record: head.Seq}
			}
		}
		var e error
		notes, e = s.applyDeclaredReplacements(string(raw))
		if e != nil {
			return e
		}
		rep, e := s.reconcileSchema(string(raw))
		if e != nil {
			return e
		}
		notes = append(notes, rep.Lines()...)

		notes = append(notes, s.ensureSidecars(rep.RebuildSidecars...)...)
		if e := s.upgradeInteractionHistory(); e != nil {
			return &SchemaError{Phase: "interaction history", Cause: e}
		}
		if e := s.upgradeStandingOffer(); e != nil {
			return &SchemaError{Phase: "standing offer", Cause: e}
		}

		if startup {
			if e := clearPluginKVTemp(s.h(), nil); e != nil {
				return &SchemaError{Phase: "expired plugin state", Cause: e}
			}
			if e := clearPluginMemoryTemp(s.h(), nil); e != nil {
				return &SchemaError{Phase: "expired plugin state", Cause: e}
			}
		}
		if objects == 0 {
			notes = nil
		}
		if e := probeMemoryFunctions(s.h()); e != nil {
			return e
		}
		if source != nil {
			if e := s.replayInTransaction(source, head); e != nil {
				return e
			}
		}
		if e := s.auditSchema(); e != nil {
			return e
		}
		if carry != nil {
			if e := s.checkCarry(before, carry); e != nil {
				return e
			}
		}
		var e2 error
		s.activeProject, e2 = s.getRuntimeMeta("active_project")
		return e2
	})
	s.schemaReplay = false
	s.schemaStartup = false
	if err != nil {
		var outcome *SchemaFailure
		if errors.As(err, &outcome) && outcome.Outcome == SchemaCommitted {
			if checkErr := s.verifyCommittedSchema(ctx, head); checkErr == nil {
				logsink.Warn("store.decision", "schema committed; failed connection discarded and database verified on a fresh connection: %v", err)
				err = nil
			} else {
				err = errors.Join(err, &SchemaError{Phase: "committed readback", Cause: checkErr})
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("open database: %w", errors.Join(err, db.Close()))
	}
	for _, note := range notes {
		logsink.Info("store.decision", "%s", note)
	}
	return s, nil
}

type MirrorAheadError struct{ Mirror, Record uint64 }

func (e *MirrorAheadError) Error() string {
	return fmt.Sprintf("ledger ends at seq %d but the projection mirror acknowledged seq %d", e.Record, e.Mirror)
}

type MirrorReadError struct{ Cause error }

func (e *MirrorReadError) Error() string {
	return "read acknowledged mirror before replay: " + e.Cause.Error()
}
func (e *MirrorReadError) Unwrap() error { return e.Cause }

func openPreparedStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", sqliteDSN(path)+"&vfs="+compressvfs.Name)
	if err != nil {
		return nil, err
	}
	db.SetMaxIdleConns(storeIdleConnections)
	s := &Store{db: db}
	if err := s.auditSchema(); err != nil {
		db.Close()
		return nil, err
	}
	if err := probeMemoryFunctions(s.h()); err != nil {
		db.Close()
		return nil, err
	}
	s.activeProject, err = s.getRuntimeMeta("active_project")
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func OpenReadOnly(path string) (*Store, error) {
	if err := compressvfs.Register(); err != nil {
		return nil, fmt.Errorf("register database VFS: %w", err)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no database to mount read-only: %w", err)
	}
	dsn := databaseURI(path) +
		"?vfs=" + compressvfs.Name + "&mode=ro&_pragma=query_only(1)" +
		"&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("cannot open database read-only: %w", err)
	}
	s := &Store{db: db, readOnly: true}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master`).Scan(&n); err != nil {
		db.Close()
		return nil, fmt.Errorf("read-only mount cannot read: %w", err)
	}
	var interactionColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('conversations') WHERE name='kind'`).Scan(&interactionColumns); err != nil {
		db.Close()
		return nil, err
	}
	s.historicalInteractions = interactionColumns == 0
	var sourceColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('turn_metrics') WHERE name='source'`).Scan(&sourceColumns); err != nil {
		db.Close()
		return nil, err
	}
	s.historicalTurnMetrics = sourceColumns == 0
	if s.historicalInteractions {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('conversations') WHERE name='project_id'`).Scan(&n); err != nil {
			db.Close()
			return nil, err
		}
		s.historicalProject = n > 0
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='turn_annotations' AND type='table'`).Scan(&n); err != nil {
			db.Close()
			return nil, err
		}
		s.historicalAnnotations = n > 0
	}
	return s, nil
}

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

func sqliteDSN(path string) string {
	return databaseURI(path) +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)" +

		"&_pragma=recursive_triggers(1)" +
		"&_pragma=journal_mode(WAL)" +

		"&_pragma=journal_size_limit(8388608)"
}

func databaseURI(path string) string {
	escaped := strings.ReplaceAll(url.PathEscape(filepath.ToSlash(path)), "%2F", "/")
	if strings.HasPrefix(escaped, "//") {
		return "file://" + escaped
	}
	return "file:" + escaped
}

func (s *Store) initSchema() error {
	schemaBytes, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("cannot read embedded schema: %w", err)
	}

	if _, err := s.w().Exec(string(schemaBytes)); err != nil {
		return fmt.Errorf("schema execution failed: %w", err)
	}

	return nil
}

func (s *Store) Close() error {

	if s.readOnly {
		return s.db.Close()
	}

	if conn, cerr := s.db.Conn(context.Background()); cerr != nil {
		logsink.Debug("store.end", "optimize on close skipped: no connection (%v)", cerr)
	} else {
		if _, err := conn.ExecContext(context.Background(), "PRAGMA analysis_limit=400"); err != nil {
			logsink.Debug("store.end", "optimize on close skipped: analysis_limit refused (%v)", err)
		} else if _, err := conn.ExecContext(context.Background(), "PRAGMA optimize"); err != nil {
			logsink.Debug("store.end", "optimize on close skipped: optimize refused (%v)", err)
		}
		conn.Close()
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) MaxLedgerSeq() (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var seq uint64
	err := s.h().QueryRow("SELECT COALESCE(MAX(seq), 0) FROM ledger").Scan(&seq)
	return seq, err
}
