package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
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
// .
// .
// .
type ReconcileReport struct {
	Renamed   []string
	Rebuilt   []string
	Recreated []string
	Created   []string
	Indexes   []string
	Triggers  []string
	Views     []string
	Sidecars  []string
	Retired   []string
	Dropped   []string
	Unneeded  bool
	// .
	// .
	// .
	RebuildSidecars []string
}

func (r ReconcileReport) empty() bool {
	return len(r.Renamed) == 0 && len(r.Rebuilt) == 0 && len(r.Recreated) == 0 && len(r.Created) == 0 &&
		len(r.Indexes) == 0 && len(r.Triggers) == 0 && len(r.Views) == 0 && len(r.Sidecars) == 0 &&
		len(r.Retired) == 0 && len(r.Dropped) == 0
}

// .
func (r ReconcileReport) Lines() []string {
	var out []string
	add := func(verb string, items []string) {
		for _, s := range items {
			out = append(out, "RECONCILE: "+verb+" "+s)
		}
	}
	add("renamed column", r.Renamed)
	add("retired table", r.Retired)
	add("rebuilt table", r.Rebuilt)
	add("recreated derived table", r.Recreated)
	add("created table", r.Created)
	add("index", r.Indexes)
	add("trigger", r.Triggers)
	add("view", r.Views)
	add("recreated sidecar", r.Sidecars)
	add("dropped undeclared", r.Dropped)
	return out
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
func (s *Store) applyDeclaredReplacements(schemaText string) ([]string, []LegacyStart) {
	var notes []string
	declared := declaredTableSQL(schemaText)
	for table, marker := range declaredReplacements(schemaText) {
		has, err := s.tableHasColumn(table, marker)
		if err != nil || !has {
			continue
		}
		createSQL, ok := declared[table]
		if !ok {
			notes = append(notes, fmt.Sprintf("RECONCILE: %s declares a replacement but schema.sql holds no CREATE for it — NOT dropping (audit will judge)", table))
			continue
		}
		// .
		// .
		// .
		// .
		// .
		// .
		tx, terr := s.db.Begin()
		if terr != nil {
			notes = append(notes, fmt.Sprintf("RECONCILE: replacement of %s could not begin — %v", table, terr))
			continue
		}
		var salvaged []LegacyStart
		bad := ""
		if table == "tool_events" {
			rows, qerr := tx.Query(`SELECT st.ts_ms, st.call_id, st.tool, st.args FROM tool_events st
				WHERE st.phase = 'started' AND NOT EXISTS (
					SELECT 1 FROM tool_events d WHERE d.call_id = st.call_id AND d.phase != 'started' AND d.rowid > st.rowid)`)
			if qerr != nil {
				bad = qerr.Error()
			} else {
				for rows.Next() {
					var ls LegacyStart
					if err := rows.Scan(&ls.TsMs, &ls.CallID, &ls.Tool, &ls.Args); err != nil {
						bad = err.Error()
						break
					}
					salvaged = append(salvaged, ls)
				}
				if rerr := rows.Err(); rerr != nil && bad == "" {
					bad = rerr.Error()
				}
				rows.Close()
			}
		}
		if bad != "" {
			tx.Rollback()
			notes = append(notes, fmt.Sprintf("RECONCILE: legacy unfinished-call scan failed on %s — %s; NOT dropping (audit will judge)", table, bad))
			continue
		}
		if _, err := tx.Exec("DROP TABLE " + table); err != nil {
			tx.Rollback()
			notes = append(notes, fmt.Sprintf("RECONCILE: declared replacement of %s could not drop the legacy shape — %v (audit will judge)", table, err))
			continue
		}
		if _, err := tx.Exec(createSQL); err != nil {
			tx.Rollback()
			notes = append(notes, fmt.Sprintf("RECONCILE: declared replacement of %s could not create the v2 shape — %v; legacy table left standing", table, err))
			continue
		}
		insertFailed := false
		for i, r := range salvaged {
			if _, err := tx.Exec(`INSERT INTO tool_events
				(execution_id, turn_id, ordinal, actor, model, provider_call_id, tool, args_record, state, started_ms)
				VALUES (?, 'legacy_v1', ?, 'legacy', '', ?, ?, ?, 'started', ?)`,
				"tex_"+uuid.New().String(), i+1, r.CallID, r.Tool, metaRecord(r.Args), r.TsMs); err != nil {
				tx.Rollback()
				notes = append(notes, fmt.Sprintf("RECONCILE: replacement of %s could not carry %d unfinished call(s) — %v; legacy table left standing", table, len(salvaged), err))
				insertFailed = true
				break
			}
		}
		if insertFailed {
			continue
		}
		if err := tx.Commit(); err != nil {
			notes = append(notes, fmt.Sprintf("RECONCILE: replacement of %s did not commit — %v; legacy table left standing", table, err))
			continue
		}
		note := fmt.Sprintf("RECONCILE: %s carried its declared legacy shape (marker column %q) — replaced atomically; rebuildable runtime state", table, marker)
		if n := len(salvaged); n > 0 {
			note += fmt.Sprintf("; %d unfinished call(s) carried for the boot warning", n)
		}
		notes = append(notes, note)
	}
	return notes, nil
}

// .
func (s *Store) tableHasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// .
// .
// .
func (s *Store) reconcileSchema(schemaText string) (ReconcileReport, error) {
	var rep ReconcileReport
	if err := s.applyDeclaredRenames(schemaText, &rep); err != nil {
		return rep, err
	}
	if err := s.reconcileRetired(schemaText, &rep); err != nil {
		return rep, err
	}
	if err := s.reconcileTables(schemaText, &rep); err != nil {
		return rep, err
	}
	if err := s.reconcileIndexes(schemaText, &rep); err != nil {
		return rep, err
	}
	if err := s.reconcileTriggers(schemaText, &rep); err != nil {
		return rep, err
	}
	if err := s.reconcileViews(schemaText, &rep); err != nil {
		return rep, err
	}
	if err := s.reconcileSidecars(schemaText, &rep); err != nil {
		return rep, err
	}
	rep.Unneeded = rep.empty()
	return rep, nil
}

// .
// .
// .
// .
func (s *Store) applyDeclaredRenames(schemaText string, rep *ReconcileReport) error {
	renames := declaredRenames(schemaText)
	tables := make([]string, 0, len(renames))
	for t := range renames {
		tables = append(tables, t)
	}
	sort.Strings(tables)

	for _, table := range tables {
		live, err := s.liveColumns(table)
		if err != nil {
			return err
		}
		if len(live) == 0 {
			continue
		}
		cols := make([]string, 0, len(renames[table]))
		for c := range renames[table] {
			cols = append(cols, c)
		}
		sort.Strings(cols)

		seen := map[string]string{}
		for _, newCol := range cols {
			oldCol := renames[table][newCol]
			// .
			// .
			if prev, dup := seen[oldCol]; dup {
				return fmt.Errorf("table %s: columns %s and %s both declare renamed-from: %s", table, prev, newCol, oldCol)
			}
			seen[oldCol] = newCol

			if !live[oldCol] || live[newCol] {
				continue
			}
			stmt := fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", table, oldCol, newCol)
			if _, err := s.db.Exec(stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
			rep.Renamed = append(rep.Renamed, fmt.Sprintf("%s.%s -> %s.%s", table, oldCol, table, newCol))
		}
	}
	return nil
}

// .
// .
func (s *Store) rebuildTable(table, declaredSQL string, live, want map[string]bool, indexes []string, triggers []string) (int64, error) {
	shadow := table + "__reconcile_shadow"

	// .
	// .
	// .
	var shared []string
	for c := range want {
		if live[c] {
			shared = append(shared, c)
		}
	}
	sort.Strings(shared)
	if len(shared) == 0 {
		return 0, fmt.Errorf("no columns in common — refusing to rebuild %s into an empty table", table)
	}
	cols := strings.Join(shared, ", ")

	// .
	// .
	// .
	// .
	// .
	before, err := s.liveShape(table)
	if err != nil {
		return 0, err
	}
	rowidAliased := false
	pkCols := 0
	for _, c := range before {
		if c.PK > 0 {
			pkCols++
			if c.Type == "INTEGER" {
				rowidAliased = true
			}
		}
	}
	rowidAliased = rowidAliased && pkCols == 1
	copyCols := cols
	if !rowidAliased {
		copyCols = "rowid, " + cols
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
	// .
	// .
	// .
	// .
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return 0, fmt.Errorf("pin connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
		return 0, fmt.Errorf("suspend foreign keys: %w", err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if _, err := conn.ExecContext(context.Background(), `PRAGMA legacy_alter_table=ON`); err != nil {
		return 0, fmt.Errorf("legacy alter mode: %w", err)
	}
	defer func() {
		if _, rerr := conn.ExecContext(context.Background(), `PRAGMA legacy_alter_table=OFF`); rerr != nil {
			logsink.Error("store.error", "legacy alter mode could NOT be restored on this connection after a table rebuild (%v)", rerr)
		}
		// .
		// .
		// .
		if _, rerr := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); rerr != nil {
			logsink.Error("store.error", "foreign keys could NOT be restored on this connection after a table rebuild (%v) — the process should be restarted", rerr)
		}
	}()

	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// .
	// .
	// .
	// .
	// .
	if _, err := tx.Exec("DROP TABLE IF EXISTS " + shadow); err != nil {
		return 0, fmt.Errorf("clear stale shadow: %w", err)
	}
	shadowSQL := strings.Replace(declaredSQL, "CREATE TABLE "+table+" (", "CREATE TABLE "+shadow+" (", 1)
	if _, err := tx.Exec(shadowSQL); err != nil {
		return 0, fmt.Errorf("create shadow: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", shadow, copyCols, copyCols, table)); err != nil {
		return 0, fmt.Errorf("carry rows: %w", err)
	}
	var carried int64
	if err := tx.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", shadow)).Scan(&carried); err != nil {
		return 0, fmt.Errorf("count carried rows: %w", err)
	}
	if _, err := tx.Exec("DROP TABLE " + table); err != nil {
		return 0, fmt.Errorf("drop original: %w", err)
	}
	if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", shadow, table)); err != nil {
		return 0, fmt.Errorf("promote shadow: %w", err)
	}
	// .
	// .
	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return 0, fmt.Errorf("restore index: %w", err)
		}
	}
	// .
	// .
	// .
	for _, trg := range triggers {
		if _, err := tx.Exec(trg); err != nil {
			return 0, fmt.Errorf("restore trigger: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return carried, nil
}

// .
// .
func (s *Store) liveColumns(table string) (map[string]bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, fmt.Errorf("read shape of %s: %w", table, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scan shape of %s: %w", table, err)
		}
		out[strings.ToLower(name)] = true
	}
	return out, rows.Err()
}

func describeRebuild(table string, live, want map[string]bool, carried int64) string {
	var added, dropped []string
	for c := range want {
		if !live[c] {
			added = append(added, c)
		}
	}
	for c := range live {
		if !want[c] {
			dropped = append(dropped, c)
		}
	}
	sort.Strings(added)
	sort.Strings(dropped)
	parts := []string{}
	if len(added) > 0 {
		parts = append(parts, "+"+strings.Join(added, " +"))
	}
	if len(dropped) > 0 {
		parts = append(parts, "-"+strings.Join(dropped, " -"))
	}
	return fmt.Sprintf("%s (%s, %d row(s) carried)", table, strings.Join(parts, " "), carried)
}

// .
// .
// .
// .
// .
func (s *Store) populatedDroppedColumns(table string, live, want map[string]bool) ([]string, error) {
	var candidates []string
	for c := range live {
		if !want[c] {
			candidates = append(candidates, c)
		}
	}
	sort.Strings(candidates)

	var populated []string
	for _, c := range candidates {
		var n int
		q := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL AND %s != ''", table, c, c)
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			// .
			// .
			// .
			populated = append(populated, c)
			continue
		}
		if n > 0 {
			populated = append(populated, c)
		}
	}
	return populated, nil
}

// .
var mirrorDeclarationRe = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ledger \(.*?\n\)[^\n;]*;`)

// .
// .
// .
// .
// .
// .
// .
// .
func recreateMirror(db *sql.DB, schemaText string) error {
	ddl := mirrorDeclarationRe.FindString(schemaText)
	if ddl == "" {
		return fmt.Errorf("schema.sql declares no ledger mirror")
	}
	if _, err := db.Exec("DROP TABLE IF EXISTS ledger"); err != nil {
		return fmt.Errorf("drop the mirror: %w", err)
	}
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("recreate the mirror: %w", err)
	}
	return nil
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
// .
// .

// .
// .
func (s *Store) liveObjects(kind string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type = ? AND name NOT LIKE 'sqlite_%'`, kind)
	if err != nil {
		return nil, fmt.Errorf("read live %ss: %w", kind, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, text string
		if err := rows.Scan(&name, &text); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = text
	}
	return out, rows.Err()
}

// .
func (s *Store) liveObjectSQL(kind, name string) (string, bool, error) {
	var text sql.NullString
	err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = ? AND name = ?`, kind, name).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return text.String, true, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) reconcileTables(schemaText string, rep *ReconcileReport) error {
	declared := declaredTableSQL(schemaText)
	indexes := declaredIndexSQL(schemaText)
	triggers := declaredTriggerSQL(schemaText)
	prov := lenientProvenance(schemaText)
	sidecars := sidecarsIn(schemaText)
	// .
	// .
	// .
	wantShape, err := referenceShape(schemaText)
	if err != nil {
		return fmt.Errorf("reference shape: %w", err)
	}
	sidecarsOf := func(table string) []string {
		var out []string
		for name, e := range sidecars {
			if e.Base == table {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}

	liveTables, err := s.liveObjects("table")
	if err != nil {
		return err
	}
	for _, table := range sortedKeys(declared) {
		liveSQL, exists := liveTables[table]
		if !exists {
			continue
		}
		if canonDDL(liveSQL) == canonDDL(declared[table]) {
			continue
		}
		liveCols, err := s.liveShape(table)
		if err != nil {
			return err
		}
		wantCols := wantShape[table]
		diffs := shapeDifferences(liveCols, wantCols)
		live := columnNameSet(liveCols)
		want := columnNameSet(wantCols)
		why := "declaration changed"
		if len(diffs) > 0 {
			why = "shape: " + strings.Join(diffs, "; ")
		}
		derived := prov[table] == Derived
		// .
		// .
		// .
		// .
		// .
		// .
		if table == "ledger" && !live["seq"] {
			return fmt.Errorf("refusing to rebuild the ledger mirror blind: its acknowledged head cannot be read (no seq column) — the torn-tail check needs it; the carry-across (rewrap -db) rebuilds the projection deliberately")
		}

		// .
		// .
		// .
		// .
		// .
		// .
		lost, err := s.populatedDroppedColumns(table, live, want)
		if err != nil {
			return err
		}
		if len(lost) > 0 && !derived {
			return fmt.Errorf(
				"refusing to rebuild %s: column(s) %s hold data and are not declared — "+
					"if they were renamed, say so in schema.sql (-- renamed-from: <old>); "+
					"if they are meant to go, drop them deliberately",
				table, strings.Join(lost, ", "))
		}
		if len(lost) > 0 {
			s.noteMirrorHead(table, live)
			if err := s.recreateEmpty(table, declared[table], indexes[table], triggers[table]); err != nil {
				return fmt.Errorf("recreate %s: %w", table, err)
			}
			rep.Recreated = append(rep.Recreated, fmt.Sprintf("%s (derived; column(s) %s held data the declaration drops — recreated empty, the record refills it at replay) [%s]",
				table, strings.Join(lost, ", "), why))
			rep.RebuildSidecars = append(rep.RebuildSidecars, sidecarsOf(table)...)
			continue
		}
		carried, err := s.rebuildTable(table, declared[table], live, want, indexes[table], triggers[table])
		if err != nil {
			if !derived {
				return fmt.Errorf("rebuild %s: %w — the rows stay as they are; repair them or declare the change", table, err)
			}
			s.noteMirrorHead(table, live)
			if err2 := s.recreateEmpty(table, declared[table], indexes[table], triggers[table]); err2 != nil {
				return fmt.Errorf("rebuild %s: %v; recreate empty: %w", table, err, err2)
			}
			rep.Recreated = append(rep.Recreated, fmt.Sprintf("%s (derived; its rows could not be carried into the new declaration: %v — recreated empty, the record refills it at replay) [%s]",
				table, err, why))
			rep.RebuildSidecars = append(rep.RebuildSidecars, sidecarsOf(table)...)
			continue
		}
		rep.Rebuilt = append(rep.Rebuilt, describeRebuild(table, live, want, carried)+" ["+why+"]")
	}
	return nil
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) noteMirrorHead(table string, live map[string]bool) {
	if table != "ledger" || !live["seq"] {
		return
	}
	var head uint64
	if err := s.db.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM ledger").Scan(&head); err == nil && head > s.mirrorHead {
		s.mirrorHead = head
	}
}

// .
// .
// .
// .
// .
func (s *Store) recreateEmpty(table, declaredSQL string, indexes, triggers []string) error {
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("pin connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("suspend foreign keys: %w", err)
	}
	defer func() {
		if _, rerr := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); rerr != nil {
			logsink.Error("store.error", "foreign keys could NOT be restored on this connection after recreating an empty table (%v) — the process should be restarted", rerr)
		}
	}()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DROP TABLE IF EXISTS " + table + "__reconcile_shadow"); err != nil {
		return fmt.Errorf("clear stale shadow: %w", err)
	}
	if _, err := tx.Exec("DROP TABLE " + table); err != nil {
		return fmt.Errorf("drop: %w", err)
	}
	if _, err := tx.Exec(declaredSQL); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	for _, idx := range indexes {
		if _, err := tx.Exec(idx); err != nil {
			return fmt.Errorf("restore index: %w", err)
		}
	}
	for _, trg := range triggers {
		if _, err := tx.Exec(trg); err != nil {
			return fmt.Errorf("restore trigger: %w", err)
		}
	}
	return tx.Commit()
}

// .
// .
// .
func (s *Store) reconcileIndexes(schemaText string, rep *ReconcileReport) error {
	declared := declaredIndexStatements(schemaText)
	live, err := s.liveObjects("index")
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(declared) {
		liveSQL, ok := live[name]
		if !ok {
			if _, err := s.db.Exec(declared[name].SQL); err != nil {
				return fmt.Errorf("create index %s: %w", name, err)
			}
			rep.Indexes = append(rep.Indexes, name+" (created)")
			continue
		}
		if canonDDL(liveSQL) == canonDDL(declared[name].SQL) {
			continue
		}
		if err := s.dropAndCreate("INDEX", name, declared[name].SQL); err != nil {
			return err
		}
		rep.Indexes = append(rep.Indexes, name+" (declaration changed; recreated)")
	}
	for _, name := range sortedKeys(live) {
		if _, ok := declared[name]; ok || live[name] == "" {
			continue
		}
		if _, err := s.db.Exec("DROP INDEX " + name); err != nil {
			return fmt.Errorf("drop undeclared index %s: %w", name, err)
		}
		rep.Dropped = append(rep.Dropped, "index "+name)
	}
	return nil
}

// .
// .
func (s *Store) reconcileTriggers(schemaText string, rep *ReconcileReport) error {
	declared := declaredTriggerStatements(schemaText)
	live, err := s.liveObjects("trigger")
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(declared) {
		liveSQL, ok := live[name]
		if !ok {
			if _, err := s.db.Exec(declared[name]); err != nil {
				return fmt.Errorf("create trigger %s: %w", name, err)
			}
			rep.Triggers = append(rep.Triggers, name+" (created)")
			continue
		}
		if canonDDL(liveSQL) == canonDDL(declared[name]) {
			continue
		}
		if err := s.dropAndCreate("TRIGGER", name, declared[name]); err != nil {
			return err
		}
		rep.Triggers = append(rep.Triggers, name+" (declaration changed; recreated)")
	}
	for _, name := range sortedKeys(live) {
		if _, ok := declared[name]; ok {
			continue
		}
		if _, err := s.db.Exec("DROP TRIGGER " + name); err != nil {
			return fmt.Errorf("drop undeclared trigger %s: %w", name, err)
		}
		rep.Dropped = append(rep.Dropped, "trigger "+name)
	}
	return nil
}

// .
// .
// .
// .
// .
// .
func (s *Store) reconcileViews(schemaText string, rep *ReconcileReport) error {
	declared := declaredViewSQL(schemaText)
	sidecars := sidecarsIn(schemaText)
	for _, name := range sortedKeys(declared) {
		liveSQL, exists, err := s.liveObjectSQL("view", name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.Exec(declared[name]); err != nil {
				return fmt.Errorf("create view %s: %w", name, err)
			}
			rep.Views = append(rep.Views, name+" (created)")
			continue
		}
		if canonDDL(liveSQL) == canonDDL(declared[name]) {
			continue
		}
		if err := s.dropAndCreate("VIEW", name, declared[name]); err != nil {
			return err
		}
		rep.Views = append(rep.Views, name+" (declaration changed; recreated)")
		for sc, e := range sidecars {
			if e.Content == name {
				rep.RebuildSidecars = append(rep.RebuildSidecars, sc)
			}
		}
	}
	sort.Strings(rep.RebuildSidecars)
	return nil
}

// .
// .
// .
func (s *Store) reconcileRetired(schemaText string, rep *ReconcileReport) error {
	declared := declaredTableSQL(schemaText)
	for _, name := range declaredRetiredTables(schemaText) {
		if _, still := declared[name]; still {
			return fmt.Errorf("schema.sql both declares and retires table %s", name)
		}
		_, exists, err := s.liveObjectSQL("table", name)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		var n int64
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + name).Scan(&n); err != nil {
			return fmt.Errorf("count %s before retiring it: %w", name, err)
		}
		conn, err := s.db.Conn(context.Background())
		if err != nil {
			return fmt.Errorf("pin connection: %w", err)
		}
		if _, err := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=OFF`); err != nil {
			conn.Close()
			return fmt.Errorf("suspend foreign keys: %w", err)
		}
		_, derr := conn.ExecContext(context.Background(), "DROP TABLE "+name)
		if _, rerr := conn.ExecContext(context.Background(), `PRAGMA foreign_keys=ON`); rerr != nil {
			logsink.Error("store.error", "foreign keys could NOT be restored on this connection after retiring a table (%v) — the process should be restarted", rerr)
		}
		conn.Close()
		if derr != nil {
			return fmt.Errorf("retire %s: %w", name, derr)
		}
		rep.Retired = append(rep.Retired, fmt.Sprintf("%s (%d row(s) discarded, as declared)", name, n))
	}
	return nil
}

// .
func (s *Store) dropAndCreate(kind, name, declaredSQL string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DROP " + kind + " " + name); err != nil {
		return fmt.Errorf("drop %s %s: %w", strings.ToLower(kind), name, err)
	}
	if _, err := tx.Exec(declaredSQL); err != nil {
		return fmt.Errorf("recreate %s %s: %w", strings.ToLower(kind), name, err)
	}
	return tx.Commit()
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) reconcileSidecars(schemaText string, rep *ReconcileReport) error {
	declared := declaredVirtualSQL(schemaText)
	live, err := s.liveObjects("table")
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(declared) {
		liveSQL, exists := live[name]
		if !exists {
			continue
		}
		if canonDDL(liveSQL) == canonDDL(declared[name]) {
			continue
		}
		if err := s.dropAndCreate("TABLE", name, declared[name]); err != nil {
			return fmt.Errorf("sidecar %s: %w", name, err)
		}
		rep.Sidecars = append(rep.Sidecars, name+" (declaration changed; rebuilt at readiness)")
	}
	for _, name := range sortedKeys(live) {
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(live[name])), "CREATE VIRTUAL TABLE") {
			continue
		}
		if _, ok := declared[name]; ok {
			continue
		}
		if _, err := s.db.Exec("DROP TABLE " + name); err != nil {
			return fmt.Errorf("drop undeclared sidecar %s: %w", name, err)
		}
		rep.Dropped = append(rep.Dropped, "sidecar "+name)
	}
	return nil
}
