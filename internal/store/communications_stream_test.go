package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Decorate actual driver reads, independently of the converter's own claims.
// A source read must be consumed/persisted before another row is requested.
type communicationProbeDriver struct {
	driver.Driver
	pending, read, chunks atomic.Int64
	retained              atomic.Int64
	crashAfter            int64
}

func (d *communicationProbeDriver) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &communicationProbeConn{Conn: c, owner: d}, nil
}

type communicationProbeConn struct {
	driver.Conn
	owner *communicationProbeDriver
}

func (c *communicationProbeConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *communicationProbeConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	r, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
	if err == nil && strings.HasPrefix(q, "INSERT INTO runtime_meta") {
		c.owner.pending.Store(0)
		if n := c.owner.retained.Add(1); c.owner.crashAfter > 0 && n == c.owner.crashAfter {
			os.Exit(83)
		}
	}
	return r, err
}

func TestCommunicationsMigrationCrashKeepsOldOrCommittedState(t *testing.T) {
	if path := os.Getenv("AII_COMMUNICATION_CRASH_PATH"); path != "" {
		if err := compressvfs.Register(); err != nil {
			t.Fatal(err)
		}
		base, err := sql.Open("sqlite", sqliteDSN(path)+"&vfs="+compressvfs.Name)
		if err != nil {
			t.Fatal(err)
		}
		d := &communicationProbeDriver{Driver: base.Driver()}
		base.Close()
		if os.Getenv("AII_COMMUNICATION_CRASH_STAGE") == "row" {
			d.crashAfter = 2
		}
		name := "communication-crash-" + rand.Text()
		sql.Register(name, d)
		db, err := sql.Open(name, sqliteDSN(path)+"&vfs="+compressvfs.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepareSchemaStore(t.Context(), db, &MirrorHead{}, func(func(*ledger.Event) error) error { return nil }, nil, false); err != nil {
			t.Fatal(err)
		}
		os.Exit(83)
	}
	if !hostcap.Can(hostcap.Subprocess).Available {
		t.Skip("process exit proof needs subprocesses")
	}
	for _, compressed := range []bool{false, true} {
		for _, stage := range []string{"row", "commit"} {
			t.Run(fmt.Sprintf("%t/%s", compressed, stage), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "old.db")
				communicationsFixture(t, path, "communications-before-labels.sql")
				if compressed {
					newPath := filepath.Join(t.TempDir(), "compressed.db")
					if ok, err := ConvertDatabase(t.Context(), path, newPath, true); err != nil || !ok {
						t.Fatalf("compression %v %v", ok, err)
					}
					path = newPath
				}
				ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCommunicationsMigrationCrashKeepsOldOrCommittedState$")
				cmd.Env = append(os.Environ(), "AII_COMMUNICATION_CRASH_PATH="+path, "AII_COMMUNICATION_CRASH_STAGE="+stage)
				out, err := cmd.CombinedOutput()
				var exited *exec.ExitError
				if !errors.As(err, &exited) || exited.ExitCode() != 83 {
					t.Fatalf("checkpoint missed: %v\n%s", err, out)
				}
				raw, err := sql.Open("sqlite", sqliteDSN(path)+"&vfs="+compressvfs.Name)
				if err != nil {
					t.Fatal(err)
				}
				var recipient string
				if err := raw.QueryRow("SELECT to_identity FROM outbox WHERE id='pending'").Scan(&recipient); err != nil {
					t.Fatal(err)
				}
				var retained int
				if err := raw.QueryRow("SELECT COUNT(*) FROM runtime_meta WHERE key>=? AND key<?", communicationsBeforeUpgrade+"/", communicationsBeforeUpgrade+"/~").Scan(&retained); err != nil {
					t.Fatal(err)
				}
				if stage == "row" && (recipient != "person-1" || retained != 0) {
					t.Fatalf("partial migration published: %q rows=%d", recipient, retained)
				}
				if stage == "commit" && (recipient != "quinn" || retained != 8) {
					t.Fatalf("committed migration missing: %q rows=%d", recipient, retained)
				}
				raw.Close()
				st, err := New(path)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				contacts, issues, err := st.LegacyContacts()
				if err != nil || len(issues) != 0 || len(contacts) != 2 {
					t.Fatalf("restart did not recover: %v %v %v", contacts, issues, err)
				}
			})
		}
	}
}
func (c *communicationProbeConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(q, "SELECT substr(CAST(value AS BLOB)") {
		if args[1].Value.(int64) > 64*1024 {
			return nil, fmt.Errorf("image reader requested an unbounded chunk")
		}
		c.owner.chunks.Add(1)
	}
	r, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(q, "SELECT rowid,* FROM \"reach\"") {
		return &communicationProbeRows{Rows: r, owner: c.owner}, nil
	}
	return r, nil
}

type communicationProbeRows struct {
	driver.Rows
	owner *communicationProbeDriver
}

func (r *communicationProbeRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	if err == nil {
		r.owner.read.Add(1)
		if r.owner.pending.Add(1) > 1 {
			return fmt.Errorf("source rows accumulated before retention")
		}
	}
	return err
}

func TestCommunicationsMigrationRetainsEachRowBeforeReadingAnother(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprint(compressed), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.db")
			communicationsFixture(t, path, "communications-before-labels.sql")
			raw := openRawForTest(t, path)
			if _, err := raw.Exec("WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<1200) INSERT INTO reach SELECT 'many_'||i,'person-1','channel_'||i,'address_'||i,i,0,i FROM n"); err != nil {
				t.Fatal(err)
			}
			raw.Close()
			base, err := New(filepath.Join(t.TempDir(), "driver.db"))
			if err != nil {
				t.Fatal(err)
			}
			d := &communicationProbeDriver{Driver: base.db.Driver()}
			base.Close()
			if compressed {
				converted := filepath.Join(t.TempDir(), "compressed.db")
				if ok, err := ConvertDatabase(t.Context(), path, converted, true); err != nil || !ok {
					t.Fatalf("conversion %v %v", ok, err)
				}
				path = converted
			}
			name := "communication-probe-" + rand.Text()
			sql.Register(name, d)
			db, err := sql.Open(name, sqliteDSN(path)+"&vfs="+compressvfs.Name)
			if err != nil {
				t.Fatal(err)
			}
			st, err := prepareSchemaStore(t.Context(), db, nil, nil, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if d.read.Load() != 1202 {
				t.Fatalf("not all source rows read: %d", d.read.Load())
			}
			count := 0
			if err := st.visitCommunicationArchive("reach", func(communicationRow) error { count++; return nil }); err != nil {
				t.Fatal(err)
			}
			if count != 1202 {
				t.Fatalf("retained %d rows", count)
			}
			if err := st.visitCommunicationArchive("inbound", func(row communicationRow) error {
				if _, duplicated := row["content"]; duplicated {
					t.Fatal("already copied inbox body was duplicated in retention")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCommunicationsPreviousSingleImageReadsInBoundedChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prior.db")
	st, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	base := st.db.Driver()
	cell := func(v any) communicationValue {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return communicationValue{JSON: raw}
	}
	source := communicationSource{
		"correspondents": {{"id": cell("person"), "label": cell("quinn")}},
		"inbound":        {{"id": cell("large"), "content": cell(strings.Repeat("λ", 200000))}},
		"reach":          {{"correspondent_id": cell("person"), "channel": cell("signal"), "address": cell("+1555"), "rank": cell(0), "created_ms": cell(1), "wake": cell(1)}},
	}
	image, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := setRuntimeMeta(st.w(), communicationsBeforeUpgrade, string(image)); err != nil {
		t.Fatal(err)
	}
	st.Close()
	d := &communicationProbeDriver{Driver: base}
	name := "communication-image-" + rand.Text()
	sql.Register(name, d)
	db, err := sql.Open(name, sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	opened := &Store{db: db}
	contacts, issues, err := opened.LegacyContacts()
	if err != nil || len(issues) != 0 || len(contacts) != 1 || contacts[0].Name != "quinn" || !contacts[0].Wake {
		t.Fatalf("prior image lost: %+v %v %v", contacts, issues, err)
	}
	if d.chunks.Load() < 4 {
		t.Fatalf("large prior image was not streamed: %d", d.chunks.Load())
	}
}
