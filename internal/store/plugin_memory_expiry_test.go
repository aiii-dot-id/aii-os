package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type expiryNode struct {
	id, next string
	temp     bool
}

type expiryRow struct {
	rowid                                        int64
	id, plugin, text, project, attribution, next string
	temp                                         int
	created, updated                             string
}

func expiryRows(t *testing.T, s *Store) map[string]expiryRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT rowid,id,plugin_id,text,project,attribution,COALESCE(superseded_by,''),temp,created_at,updated_at FROM plugin_memories ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]expiryRow{}
	for rows.Next() {
		var r expiryRow
		if err := rows.Scan(&r.rowid, &r.id, &r.plugin, &r.text, &r.project, &r.attribution, &r.next, &r.temp, &r.created, &r.updated); err != nil {
			t.Fatal(err)
		}
		out[r.id] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func seedExpiry(t *testing.T, s *Store, nodes []expiryNode) {
	t.Helper()
	for _, n := range nodes {
		if err := s.PluginMemoryAdd(PluginScope{PluginID: "p"}, PluginMemory{ID: n.id, Text: "memory " + n.id, Project: "kept", Temp: n.temp, CreatedAt: time.Unix(1234, 0)}, 0, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordMemoryAccess(time.Unix(1235, 0), MemoryRef{Store: "plugin_memories", ID: n.id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range nodes {
		if n.next != "" {
			// The link itself, as the graph needs it: replacement through
			// PluginMemoryAdd would add its own overlay rows.
			res, err := s.w().Exec(`UPDATE plugin_memories SET superseded_by = ?, updated_at = ?
				WHERE plugin_id = 'p' AND id = ? AND superseded_by IS NULL`, n.next, time.Unix(1236, 0).UTC().Format(time.RFC3339Nano), n.id)
			if err != nil {
				t.Fatal(err)
			}
			if k, _ := res.RowsAffected(); k != 1 {
				t.Fatalf("superseding %s by %s touched %d rows", n.id, n.next, k)
			}
		}
	}
	if err := s.PluginMemoryAdd(PluginScope{PluginID: "q"}, PluginMemory{ID: "other", Text: "another plugin", Temp: true, CreatedAt: time.Unix(1234, 0)}, 0, ""); err != nil {
		t.Fatal(err)
	}
}

// Seed the verbatim historical schema with its historical writer statements.
// Current APIs require current columns and cannot manufacture old-reader data.
func seedHistoricalExpiry(t *testing.T, s *Store, nodes []expiryNode) {
	t.Helper()
	stamp := time.Unix(1234, 0).UTC().Format(time.RFC3339Nano)
	for _, n := range nodes {
		temp := 0
		if n.temp {
			temp = 1
		}
		if _, err := s.db.Exec(`INSERT INTO plugin_memories
			(id,plugin_id,text,project,attribution,superseded_by,temp,created_at,updated_at)
			VALUES (?,'p',?,'kept','plugin',NULL,?,?,?)`, n.id, "memory "+n.id, temp, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordMemoryAccess(time.Unix(1235, 0), MemoryRef{Store: "plugin_memories", ID: n.id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range nodes {
		if n.next != "" {
			if _, err := s.db.Exec(`UPDATE plugin_memories SET superseded_by=?,updated_at=?
				WHERE plugin_id='p' AND id=? AND superseded_by IS NULL`, n.next, stamp, n.id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.db.Exec(`INSERT INTO plugin_memories
		(id,plugin_id,text,project,attribution,superseded_by,temp,created_at,updated_at)
		VALUES ('other','q','another plugin','','plugin',NULL,1,?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func checkExpiryIntegrity(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil || bad {
		t.Fatalf("foreign-key integrity lost: %v", err)
	}
	if failures := checkSidecarsOn(s.db); len(failures) != 0 {
		t.Fatalf("sidecars lost consistency: %v", failures)
	}
}

func TestTemporaryPluginSuccessorExpiresWithoutLosingPersistentMemory(t *testing.T) {
	cases := []struct {
		name  string
		nodes []expiryNode
		next  map[string]string
	}{
		{"leaf", []expiryNode{{"A", "B", false}, {"B", "", true}}, map[string]string{"A": ""}},
		{"temporary_chain", []expiryNode{{"A", "B", false}, {"B", "C", true}, {"C", "", true}}, map[string]string{"A": ""}},
		{"permanent_successor", []expiryNode{{"A", "B", false}, {"B", "C", true}, {"C", "", false}}, map[string]string{"A": "C", "C": ""}},
		{"mixed_chain", []expiryNode{{"A", "B", false}, {"B", "C", true}, {"C", "D", true}, {"D", "", false}}, map[string]string{"A": "D", "D": ""}},
		{"permanent_link", []expiryNode{{"A", "B", false}, {"B", "", false}, {"C", "", true}}, map[string]string{"A": "B", "B": ""}},
	}
	for _, tc := range cases {
		// Exhaust insertion/rowid orders, not a probabilistic scheduling loop.
		var visit func([]expiryNode, int)
		visit = func(order []expiryNode, at int) {
			if at < len(order) {
				for i := at; i < len(order); i++ {
					order[at], order[i] = order[i], order[at]
					visit(order, at+1)
					order[at], order[i] = order[i], order[at]
				}
				return
			}
			ids := ""
			for _, n := range order {
				ids += n.id
			}
			t.Run(tc.name+"/"+ids, func(t *testing.T) {
				s := testStore(t)
				seedExpiry(t, s, order)
				want := expiryRows(t, s)
				for _, n := range tc.nodes {
					if n.temp {
						delete(want, n.id)
					} else {
						r := want[n.id]
						r.next = tc.next[n.id]
						want[n.id] = r
					}
				}
				if err := s.PluginMemoryClearTemp(PluginScope{PluginID: "p"}); err != nil {
					t.Fatalf("activation cleanup failed: %v", err)
				}
				if got := expiryRows(t, s); !maps.Equal(got, want) {
					t.Fatalf("expiry changed surviving data or lost the permanent successor:\ngot=%+v\nwant=%+v", got, want)
				}
				for _, n := range tc.nodes {
					a, found, err := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: n.id})
					if err != nil || found == n.temp || (found && a.Count != 1) {
						t.Fatalf("wrong access lifetime for %s: found=%v count=%d err=%v", n.id, found, a.Count, err)
					}
				}
				checkExpiryIntegrity(t, s)
				if err := s.PluginMemoryClearTemp(PluginScope{PluginID: "p"}); err != nil || !maps.Equal(expiryRows(t, s), want) {
					t.Fatalf("repeated cleanup changed history: %v", err)
				}
			})
		}
		visit(append([]expiryNode(nil), tc.nodes...), 0)
	}
}

func TestPluginExpiryRollsBackInsteadOfRewritingAnotherPluginsReference(t *testing.T) {
	s := testStore(t)
	seedExpiry(t, s, []expiryNode{{"A", "B", false}, {"B", "", true}})
	// Outside the legal API: an inconsistent cross-plugin incoming link must
	// not grant cleanup permission to rewrite the other plugin's history.
	if _, err := s.db.Exec(`UPDATE plugin_memories SET superseded_by='B' WHERE id='other'`); err != nil {
		t.Fatal(err)
	}
	before := expiryRows(t, s)
	access, found, err := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: "B"})
	if err != nil || !found {
		t.Fatal("missing fixture access")
	}
	if err := s.PluginMemoryClearTemp(PluginScope{PluginID: "p"}); err == nil {
		t.Fatal("cleanup rewrote a foreign plugin's link")
	}
	if !maps.Equal(expiryRows(t, s), before) {
		t.Fatal("failed cleanup changed rows or links")
	}
	after, found, err := s.MemoryAccessOf(MemoryRef{Store: "plugin_memories", ID: "B"})
	if err != nil || !found || !reflect.DeepEqual(access, after) {
		t.Fatal("failed cleanup lost access history")
	}
}

func TestPluginExpiryDoesNotMakePermanentSuccessorsDeletable(t *testing.T) {
	s := testStore(t)
	seedExpiry(t, s, []expiryNode{{"A", "B", false}, {"B", "", false}})
	before := expiryRows(t, s)
	if _, err := s.db.Exec(`DELETE FROM plugin_memories WHERE id='B'`); err == nil {
		t.Fatal("expiry rule allowed deletion of a referenced permanent successor")
	}
	if !maps.Equal(expiryRows(t, s), before) {
		t.Fatal("refused permanent deletion changed history")
	}
	checkExpiryIntegrity(t, s)
}

func TestPluginExpiryFailedUpgradeRollsBackAndCanRetry(t *testing.T) {
	old, err := os.ReadFile("testdata/schema-before-plugin-memory-expiry.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "historical.db")
	native, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	d := &writerBoundaryDriver{Driver: native.Driver()}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	name := "expiry-schema-fault-" + rand.Text()
	sql.Register(name, d)
	db, err := sql.Open(name, sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	if _, err := db.Exec(string(old)); err != nil {
		t.Fatal(err)
	}
	seedHistoricalExpiry(t, s, []expiryNode{{"A", "B", false}, {"B", "", true}})
	before := expiryRows(t, s)
	var oldIndex string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='idx_plugin_memories_plugin'`).Scan(&oldIndex); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("injected expiry trigger creation failure")
	indexChanged, injected := false, false
	d.before = func(q string) error {
		if strings.HasPrefix(q, "CREATE INDEX") && strings.Contains(q, "idx_plugin_memories_plugin") {
			indexChanged = true
		}
		if strings.HasPrefix(q, "CREATE TRIGGER") && strings.Contains(q, "plugin_memories_expire_link") {
			injected = true
			return fault
		}
		return nil
	}
	current, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.reconcileSchema(string(current))
	d.before = nil
	if !indexChanged || !injected || !errors.Is(err, fault) {
		t.Fatalf("did not exercise partial upgrade failure: index=%v trigger=%v err=%v", indexChanged, injected, err)
	}
	var index string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='idx_plugin_memories_plugin'`).Scan(&index); err != nil || index != oldIndex {
		t.Fatalf("failed upgrade retained partial index change: %q %v", index, err)
	}
	if !maps.Equal(expiryRows(t, s), before) {
		t.Fatal("failed upgrade changed memories")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatalf("normal retry cannot open historical database: %v", err)
	}
	if err := s.PluginMemoryClearTemp(PluginScope{PluginID: "p"}); err != nil {
		t.Fatalf("normal retry did not install expiry rule: %v", err)
	}
	delete(before, "B")
	a := before["A"]
	a.next = ""
	before["A"] = a
	if !maps.Equal(expiryRows(t, s), before) {
		t.Fatal("retried upgrade lost persistent memories")
	}
	checkExpiryIntegrity(t, s)
}

func TestPluginExpiryUpgradesTheHistoricalSchemaAndRefusesLossyOldReaders(t *testing.T) {
	old, err := os.ReadFile("testdata/schema-before-plugin-memory-expiry.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "historical.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{db: db}
	t.Cleanup(func() {
		if s != nil {
			_ = s.Close()
		}
	})
	if _, err := db.Exec(string(old)); err != nil {
		t.Fatal(err)
	}
	seedHistoricalExpiry(t, s, []expiryNode{{"A", "B", false}, {"B", "C", true}, {"C", "", false}})
	before := expiryRows(t, s)
	// Reproduce the old writer's actual FK refusal, not a current API's
	// missing-column error against an intentionally historical schema.
	if _, err := s.db.Exec(`DELETE FROM plugin_memories WHERE plugin_id='p' AND temp=1`); err == nil {
		t.Fatal("historical fixture no longer reproduces the original FK failure")
	} else if se := new(sqlite.Error); !errors.As(err, &se) || se.Code() != sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		t.Fatalf("historical deletion failed for a different reason: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path) // normal boot, same reconciler as every identity
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(expiryRows(t, s), before) {
		t.Fatal("schema upgrade rewrote runtime memories")
	}
	if err := s.PluginMemoryClearTemp(PluginScope{PluginID: "p"}); err != nil {
		t.Fatal(err)
	}
	delete(before, "B")
	a := before["A"]
	a.next = "C"
	before["A"] = a
	if !maps.Equal(expiryRows(t, s), before) {
		t.Fatal("upgraded cleanup lost surviving rows")
	}
	checkExpiryIntegrity(t, s)
	// Scope and evidence now have explicit values even on migrated legacy
	// notes. A historical reader must refuse to discard them, preserving the
	// surviving rows and the current schema for a compatible restart.
	scopeRows := func() string {
		t.Helper()
		var snapshot string
		if err := s.db.QueryRow(`SELECT json_group_array(json_array(id,activation,overrides,applicability,work_session,source_incarnation,evidence))
			FROM (SELECT * FROM plugin_memories ORDER BY id)`).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	scopesBefore := scopeRows()
	_, err = s.reconcileSchema(string(old))
	var outcome *SchemaFailure
	var shape *ShapeError
	if !errors.As(err, &outcome) || outcome.Outcome != SchemaUnchanged || !errors.As(err, &shape) {
		t.Fatalf("historical reader must refuse the shape without changing it: %v", err)
	}
	if !strings.Contains(err.Error(), "applicability, evidence hold data") {
		t.Fatalf("historical refusal did not name the scoped data it would lose: %v", err)
	}
	if scopeRows() != scopesBefore {
		t.Fatal("refused historical reader changed scope or evidence data")
	}
	if !maps.Equal(expiryRows(t, s), before) {
		t.Fatal("historical reader reconciliation changed memory data")
	}
	checkExpiryIntegrity(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(expiryRows(t, s), before) {
		t.Fatal("current reader restart changed memory data")
	}
	if scopeRows() != scopesBefore {
		t.Fatal("current reader restart changed scope or evidence data")
	}
	var cookie int
	if err := s.db.QueryRow("PRAGMA schema_version").Scan(&cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	var again int
	if err := s.db.QueryRow("PRAGMA schema_version").Scan(&again); err != nil || again != cookie {
		t.Fatalf("repeat boot re-applied DDL: %d/%d %v", cookie, again, err)
	}
}

func TestPluginExpiryLookupUsesBothOwnerAndSuccessorKeys(t *testing.T) {
	s := testStore(t)
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN UPDATE plugin_memories SET superseded_by=? WHERE plugin_id=? AND superseded_by=?`, "C", "p", "B")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// The lookup is a search by the successor's key. Two indexes serve
	// it: the owner's (plugin_id, superseded_by), and the one
	// on superseded_by alone that the table's reference to itself is
	// checked through when a row goes. Which of the two the planner takes
	// is its own choice and a few rows either way; what must not come back
	// is a lookup with no successor key in it, which walks a plugin's
	// history, or the table, for every row that expires.
	if !strings.Contains(plan, "superseded_by=?") {
		t.Fatalf("expiry would rescan a plugin's whole history per row: %s", plan)
	}
}
