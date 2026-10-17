package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"modernc.org/sqlite"
)

// Decorate the real driver at one failure boundary. No alternative schema
// engine: all statements, transactions and connection reuse remain real SQLite.
type schemaCleanupDriver struct {
	driver.Driver
	armed         atomic.Bool
	opens         atomic.Int32
	fault         error
	reopenErr     error
	commitFault   string
	rollbackFault bool
}

func (d *schemaCleanupDriver) Open(name string) (driver.Conn, error) {
	if d.opens.Load() > 0 && d.reopenErr != nil {
		return nil, d.reopenErr
	}
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	d.opens.Add(1)
	return &schemaCleanupConn{Conn: c, owner: d}, nil
}

type schemaCleanupConn struct {
	driver.Conn
	owner *schemaCleanupDriver
}

func (c *schemaCleanupConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &schemaOutcomeTx{Tx: tx, owner: c.owner}, nil
}

type schemaOutcomeTx struct {
	driver.Tx
	owner *schemaCleanupDriver
}

func (t *schemaOutcomeTx) Commit() error {
	switch t.owner.commitFault {
	case "before":
		if err := t.Tx.Rollback(); err != nil {
			return err
		}
		return t.owner.fault
	case "after":
		if err := t.Tx.Commit(); err != nil {
			return err
		}
		return t.owner.fault
	default:
		return t.Tx.Commit()
	}
}
func (t *schemaOutcomeTx) Rollback() error {
	if err := t.Tx.Rollback(); err != nil {
		return err
	}
	if t.owner.rollbackFault {
		return t.owner.fault
	}
	return nil
}

func (c *schemaCleanupConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if query == "PRAGMA foreign_keys=1" && c.owner.armed.CompareAndSwap(true, false) {
		return nil, c.owner.fault
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func TestSchemaFailedConnectionRestoreDiscardsTheConnection(t *testing.T) {
	d := &schemaCleanupDriver{Driver: &sqlite.Driver{}, fault: errors.New("synthetic foreign-key restoration failure")}
	name := "schema-cleanup-" + rand.Text()
	sql.Register(name, d)
	db, err := sql.Open(name, sqliteDSN(filepath.Join(t.TempDir(), "source.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE facts(id TEXT PRIMARY KEY); INSERT INTO facts VALUES ('kept');`); err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	d.armed.Store(true)
	_, err = s.reconcileSchema(`CREATE TABLE facts(id TEXT PRIMARY KEY,tag TEXT DEFAULT 'new');`)
	var failure *SchemaError
	if !errors.Is(err, d.fault) || !errors.As(err, &failure) || failure.Phase != "connection cleanup" {
		t.Fatalf("cleanup failure was not reported: %v", err)
	}
	var outcome *SchemaFailure
	if !errors.As(err, &outcome) || outcome.Outcome != SchemaCommitted {
		t.Fatalf("committed cleanup failure misclassified: %v", err)
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("unsafe connection reused: fk=%d %v", fk, err)
	}
	if d.opens.Load() != 2 {
		t.Fatalf("failed connection was not discarded: %d opens", d.opens.Load())
	}
	// SQL committed before cleanup failed. Do not falsely claim rollback.
	var value string
	if err := db.QueryRow("SELECT id||':'||tag FROM facts").Scan(&value); err != nil || value != "kept:new" {
		t.Fatalf("committed state misreported/lost: %q %v", value, err)
	}
}

func TestSchemaOpeningRecoversOnlyVerifiedCommittedCleanup(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(fmt.Sprint(refuse), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.db")
			st, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.db.Exec("ALTER TABLE outbox ADD COLUMN empty_old_field TEXT"); err != nil {
				t.Fatal(err)
			}
			baseDriver := st.db.Driver()
			st.Close()
			d := &schemaCleanupDriver{Driver: baseDriver, fault: errors.New("injected cleanup failure")}
			if refuse {
				d.reopenErr = errors.New("injected fresh connection failure")
			}
			name := "schema-opening-" + rand.Text()
			sql.Register(name, d)
			db, err := sql.Open(name, sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			d.armed.Store(true)
			calls := 0
			opened, err := prepareSchemaStore(t.Context(), db, &MirrorHead{}, func(func(*ledger.Event) error) error { calls++; return nil }, nil, false)
			if calls != 1 {
				t.Fatalf("recovery source calls: %d, opening error: %v", calls, err)
			}
			if refuse {
				var outcome *SchemaFailure
				if opened != nil || !errors.Is(err, d.reopenErr) || !errors.As(err, &outcome) || outcome.Outcome != SchemaCommitted {
					t.Fatalf("unverified readback published or lost outcome: %v %v", opened, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			if d.opens.Load() != 2 {
				t.Fatalf("recovery did not use fresh connection: %d", d.opens.Load())
			}
			if err := opened.AddOutboxMessage("after", "operator", "", "still writable", nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSchemaFailureOutcomeDoesNotGuessCommitOrRollback(t *testing.T) {
	for _, mode := range []string{"apply", "before", "after", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			d := &schemaCleanupDriver{Driver: &sqlite.Driver{}, fault: errors.New("injected transaction failure")}
			name := "schema-outcome-" + rand.Text()
			sql.Register(name, d)
			db, err := sql.Open(name, sqliteDSN(filepath.Join(t.TempDir(), "source.db")))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec("CREATE TABLE facts(id TEXT PRIMARY KEY)"); err != nil {
				t.Fatal(err)
			}
			if mode == "before" || mode == "after" {
				d.commitFault = mode
			}
			d.rollbackFault = mode == "rollback"
			s := &Store{db: db}
			err = s.withSchema(t.Context(), "CREATE TABLE facts(id TEXT PRIMARY KEY)", func() error {
				if _, err := s.h().Exec("INSERT INTO facts VALUES('new')"); err != nil {
					return err
				}
				if mode == "apply" || mode == "rollback" {
					return d.fault
				}
				return nil
			})
			var outcome *SchemaFailure
			want := SchemaUnknown
			if mode == "apply" {
				want = SchemaUnchanged
			}
			if !errors.As(err, &outcome) || outcome.Outcome != want {
				t.Fatalf("outcome=%v want %s", err, want)
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM facts").Scan(&count); err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			if mode == "after" {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("fault fixture differs: %d want %d", count, wantCount)
			}
		})
	}
}

func TestSchemaCommittedReadbackReadsTheProjectFocusBack(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetActiveProject("proj-kept"); err != nil {
		t.Fatal(err)
	}
	// What the Store holds in memory is gone, as after a failed opening;
	// the database still has it.
	s.activeProject = ""
	if err := s.verifyCommittedSchema(t.Context(), nil); err != nil {
		t.Fatalf("read-back of a sound database: %v", err)
	}
	if got := s.ActiveProjectID(); got != "proj-kept" {
		t.Fatalf("the focus after the read-back is %q; the database holds proj-kept", got)
	}
	// And a database that holds none reads back as none.
	if err := s.SetActiveProject(""); err != nil {
		t.Fatal(err)
	}
	s.activeProject = "stale"
	if err := s.verifyCommittedSchema(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got := s.ActiveProjectID(); got != "" {
		t.Fatalf("the focus after the read-back is %q; the database holds none", got)
	}
}

func TestSchemaCommittedReadbackHoldsVerifiedBoundary(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.verifyCommittedSchema(t.Context(), &MirrorHead{Seq: 1, Hash: "not present"}); err == nil {
		t.Fatal("unverified record boundary admitted")
	}
}
