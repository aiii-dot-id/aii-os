package store

import (
	"database/sql"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func emptyStartupRecord(func(*ledger.Event) error) error { return nil }

func startupRawStore(t *testing.T, path string) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// A prospective scoped writer's shape, not a shipped migration. The current
// recovery reader must retain persistent values and expire only T0 rows.
func startupScopedFixture(t *testing.T) (string, map[string]expiryRow, []any) {
	t.Helper()
	s := testStore(t)
	var path string
	if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(PluginScope{PluginID: "p"}, "persistent", "kept", false, 10, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.PluginKVPut(PluginScope{PluginID: "p"}, "working", "old", true, 10, 1000); err != nil {
		t.Fatal(err)
	}
	kv := startupQueryRows(t, s, `SELECT rowid,plugin_id,key,value,temp,updated_at FROM plugin_kv WHERE key='persistent'`)[0]
	seedExpiry(t, s, []expiryNode{{"A", "B", false}, {"B", "C", true}, {"C", "", false}})
	want := expiryRows(t, s)
	delete(want, "B")
	delete(want, "other")
	a := want["A"]
	a.next = "C"
	want["A"] = a
	future := schemaText(t)
	for _, table := range []string{"plugin_kv", "plugin_memories"} {
		header := "CREATE TABLE IF NOT EXISTS " + table + " (\n    -- provenance: ephemeral\n"
		future = strings.Replace(future, header, header+"    future_scope TEXT NOT NULL DEFAULT '',\n", 1)
	}
	future = strings.Replace(future, "PRIMARY KEY (plugin_id, activation, key)", "PRIMARY KEY (plugin_id, activation, future_scope, key)", 1)
	if _, err := s.reconcileSchema(future); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE plugin_kv SET future_scope='old' WHERE temp=1`,
		`INSERT INTO plugin_kv(plugin_id,future_scope,key,value,temp,updated_at) VALUES ('p','candidate','working','new',1,'unchanged stamp')`,
		`UPDATE plugin_memories SET future_scope='old' WHERE temp=1`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path, want, kv
}

func startupQueryRows(t *testing.T, s *Store, query string) [][]any {
	t.Helper()
	rows, err := s.db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func startupSnapshot(t *testing.T, path string) [][][]any {
	t.Helper()
	s, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out [][][]any
	for _, table := range []string{"plugin_kv", "plugin_memories", "memory_access"} {
		out = append(out, startupQueryRows(t, s, "SELECT rowid,* FROM "+table+" ORDER BY rowid"))
	}
	out = append(out, startupQueryRows(t, s, `SELECT type,name,sql FROM sqlite_schema WHERE tbl_name IN ('plugin_kv','plugin_memories') ORDER BY type,name`))
	return out
}

func TestOnlyExclusiveStartupCanRetireScopedTemporaryRows(t *testing.T) {
	path, want, kv := startupScopedFixture(t)
	before := startupSnapshot(t, path) // read-only inspection changes nothing
	for _, open := range []func() (*Store, error){
		func() (*Store, error) { return New(path) },
		func() (*Store, error) { return OpenAndReplay(t.Context(), path, MirrorHead{}, emptyStartupRecord) },
	} {
		s, err := open()
		if s != nil {
			s.Close()
		}
		var shape *ShapeError
		if !errors.As(err, &shape) {
			t.Fatalf("general reader gained startup deletion authority: %v", err)
		}
		if !reflect.DeepEqual(startupSnapshot(t, path), before) {
			t.Fatal("general open changed data or schema")
		}
	}
	s, err := OpenForStartup(t.Context(), path, MirrorHead{}, emptyStartupRecord)
	if err != nil {
		t.Fatalf("exclusive recovery startup failed: %v", err)
	}
	defer s.Close()
	if !maps.Equal(expiryRows(t, s), want) {
		t.Fatal("startup lost persistent memory or its surviving successor")
	}
	if got := startupQueryRows(t, s, `SELECT rowid,plugin_id,key,value,temp,updated_at FROM plugin_kv`); len(got) != 1 || !reflect.DeepEqual(got[0], kv) {
		t.Fatalf("persistent KV changed: %+v, want %+v", got, kv)
	}
	for _, id := range []string{"A", "C", "B"} {
		a, found, err := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: id})
		if err != nil || found != (id != "B") || (found && a.Count != 1) {
			t.Fatalf("memory access lifetime changed: %s %+v %v %v", id, a, found, err)
		}
	}
	checkExpiryIntegrity(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	plain, err := New(path)
	if err != nil {
		t.Fatalf("ordinary reopen cannot read repaired database: %v", err)
	}
	defer plain.Close()
	if !maps.Equal(expiryRows(t, plain), want) {
		t.Fatal("ordinary reopen changed memories")
	}
}

func TestStartupPreservesUnknownColumnsOnNonTemporaryRows(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"persistent_kv", `UPDATE plugin_kv SET future_scope='must keep' WHERE temp=0`},
		{"persistent_memory", `UPDATE plugin_memories SET future_scope='must keep' WHERE temp=0`},
		{"unknown_lifetime", `UPDATE plugin_kv SET temp=2 WHERE temp=1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _, _ := startupScopedFixture(t)
			s := startupRawStore(t, path)
			if _, err := s.db.Exec(tc.query); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			before := startupSnapshot(t, path)
			opened, err := OpenForStartup(t.Context(), path, MirrorHead{}, emptyStartupRecord)
			if opened != nil {
				opened.Close()
			}
			var shape *ShapeError
			if !errors.As(err, &shape) {
				t.Fatalf("unproved data discarded: %v", err)
			}
			if !reflect.DeepEqual(startupSnapshot(t, path), before) {
				t.Fatal("refused startup changed persistent or temporary data")
			}
		})
	}
}

func TestStartupRetirementRollsBackWithReplayFailure(t *testing.T) {
	path, _, _ := startupScopedFixture(t)
	before := startupSnapshot(t, path)
	fault := errors.New("injected verified-source read failure")
	s, err := OpenForStartup(t.Context(), path, MirrorHead{}, func(func(*ledger.Event) error) error { return fault })
	if s != nil {
		s.Close()
	}
	if !errors.Is(err, fault) {
		t.Fatalf("did not reach replay after schema recovery: %v", err)
	}
	if !reflect.DeepEqual(startupSnapshot(t, path), before) {
		t.Fatal("failed replay committed partial expiry/schema changes")
	}
	s, err = OpenForStartup(t.Context(), path, MirrorHead{}, emptyStartupRecord)
	if err != nil {
		t.Fatalf("normal startup retry failed: %v", err)
	}
	s.Close()
}

func TestStartupRetirementPermissionDoesNotEscapeOpening(t *testing.T) {
	path, _, _ := startupScopedFixture(t)
	s, err := OpenForStartup(t.Context(), path, MirrorHead{}, emptyStartupRecord)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.PluginKVPut(PluginScope{PluginID: "p"}, "live", "keep", true, 10, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE plugin_kv ADD COLUMN future TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE plugin_kv SET future='live owner' WHERE temp=1`); err != nil {
		t.Fatal(err)
	}
	_, err = s.reconcileSchema(schemaText(t))
	var shape *ShapeError
	if !errors.As(err, &shape) {
		t.Fatalf("live reconciliation retained startup expiry permission: %v", err)
	}
	if value, found, err := s.PluginKVGet(PluginScope{PluginID: "p"}, "live"); err != nil || !found || value != "keep" {
		t.Fatalf("live temporary state lost: %q %v %v", value, found, err)
	}
}

func TestStartupCannotExpireUnrelatedRuntimeDataOrAnUnverifiedOpen(t *testing.T) {
	path, _, _ := startupScopedFixture(t)
	before := startupSnapshot(t, path)
	opened, err := OpenForStartup(t.Context(), path, MirrorHead{}, nil)
	if opened != nil {
		opened.Close()
	}
	var schema *SchemaError
	if !errors.As(err, &schema) || schema.Phase != "source" {
		t.Fatalf("startup accepted no verified source: %v", err)
	}
	if !reflect.DeepEqual(startupSnapshot(t, path), before) {
		t.Fatal("unverified open changed data")
	}
	s := startupRawStore(t, path)
	for _, query := range []string{
		`ALTER TABLE runtime_meta ADD COLUMN temp INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE runtime_meta ADD COLUMN future TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO runtime_meta(key,value,updated_at,future) VALUES ('keep','unrelated recorded data','original stamp','must survive')`,
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	meta := startupQueryRows(t, s, `SELECT rowid,* FROM runtime_meta ORDER BY rowid`)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err = OpenForStartup(t.Context(), path, MirrorHead{}, emptyStartupRecord)
	if opened != nil {
		opened.Close()
	}
	var shape *ShapeError
	if !errors.As(err, &shape) {
		t.Fatalf("a data column granted expiry to unrelated runtime state: %v", err)
	}
	s = startupRawStore(t, path)
	if !reflect.DeepEqual(startupQueryRows(t, s, `SELECT rowid,* FROM runtime_meta ORDER BY rowid`), meta) {
		t.Fatal("unrelated runtime data changed")
	}
	if !reflect.DeepEqual(startupSnapshot(t, path), before) {
		t.Fatal("refusal committed earlier temporary cleanup")
	}
}
