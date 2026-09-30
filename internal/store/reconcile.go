package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
)

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

	RebuildSidecars []string
}

func (r ReconcileReport) empty() bool {
	return len(r.Renamed) == 0 && len(r.Rebuilt) == 0 && len(r.Recreated) == 0 && len(r.Created) == 0 &&
		len(r.Indexes) == 0 && len(r.Triggers) == 0 && len(r.Views) == 0 && len(r.Sidecars) == 0 &&
		len(r.Retired) == 0 && len(r.Dropped) == 0
}

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

func (s *Store) applyDeclaredReplacements(schemaText string) ([]string, error) {
	var notes []string
	declared := declaredTableSQL(schemaText)
	for _, table := range sortedKeys(declaredReplacements(schemaText)) {
		marker := declaredReplacements(schemaText)[table]
		has, err := s.tableHasColumn(table, marker)
		if err != nil {
			return nil, err
		}
		if !has {
			continue
		}
		if table != "tool_events" {
			return nil, fmt.Errorf("no conversion owner for legacy table %s", table)
		}
		var before int64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(table)).Scan(&before); err != nil {
			return nil, err
		}
		rows, err := s.h().Query(`SELECT st.ts_ms, st.call_id, st.tool, st.args FROM tool_events st
   WHERE st.phase='started' AND NOT EXISTS (
    SELECT 1 FROM tool_events d WHERE d.call_id=st.call_id AND d.phase!='started' AND d.rowid>st.rowid)
   ORDER BY st.rowid`)
		if err != nil {
			return nil, err
		}
		var starts []LegacyStart
		for rows.Next() {
			var item LegacyStart
			if err := rows.Scan(&item.TsMs, &item.CallID, &item.Tool, &item.Args); err != nil {
				rows.Close()
				return nil, err
			}
			starts = append(starts, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if _, err := s.h().Exec("DROP TABLE " + quoteIdentifier(table)); err != nil {
			return nil, err
		}
		if _, err := s.h().Exec(declared[table]); err != nil {
			return nil, err
		}
		for i, item := range starts {
			id := "tex_" + uuid.New().String()
			if _, err := s.h().Exec(`INSERT INTO tool_events
    (execution_id,turn_id,ordinal,actor,model,provider_call_id,tool,args_record,state,started_ms)
    VALUES (?,'legacy_v1',?,'legacy','',?,?,?,'started',?)`,
				id, i+1, item.CallID, item.Tool, metaRecord(item.Args), item.TsMs); err != nil {
				return nil, err
			}
			var call, tool, args, state string
			var at int64
			if err := s.h().QueryRow("SELECT provider_call_id,tool,args_record,state,started_ms FROM tool_events WHERE execution_id=?", id).Scan(&call, &tool, &args, &state, &at); err != nil {
				return nil, err
			}
			if call != item.CallID || tool != item.Tool || args != metaRecord(item.Args) || state != "started" || at != item.TsMs {
				return nil, fmt.Errorf("legacy tool conversion failed its preservation check")
			}
		}
		var after int64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM tool_events").Scan(&after); err != nil {
			return nil, err
		}
		if after != int64(len(starts)) {
			return nil, fmt.Errorf("legacy tool conversion count differs")
		}
		if s.schemaConversions == nil {
			s.schemaConversions = map[string]RuntimeConversion{}
		}
		s.schemaConversions[table] = RuntimeConversion{Before: before, After: after, Reason: "legacy phase records to unfinished executions"}
		notes = append(notes, fmt.Sprintf("RECONCILE: %s converted its declared legacy shape; %d unfinished call(s) carried for the boot warning", table, len(starts)))
	}
	for target, previous := range declaredDerivedPredecessors(schemaText) {
		if lenientProvenance(schemaText)[target] != Derived || target == previous {
			return nil, fmt.Errorf("invalid derived predecessor %s -> %s", previous, target)
		}
		if _, exists, err := s.liveObjectSQL("table", previous); err != nil {
			return nil, err
		} else if !exists {
			continue
		}
		var count int64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(previous)).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 && !s.schemaReplay {
			return nil, &ReplayRequiredError{Table: previous}
		}
		if _, err := s.h().Exec("DROP TABLE " + quoteIdentifier(previous)); err != nil {
			return nil, err
		}
		notes = append(notes, fmt.Sprintf("RECONCILE: derived predecessor %s replaced by %s through verified replay", previous, target))
	}
	return notes, nil
}

func (s *Store) tableHasColumn(table, column string) (bool, error) {
	rows, err := s.h().Query("PRAGMA table_info(" + quoteIdentifier(table) + ")")
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

func (s *Store) reconcileSchema(schemaText string) (rep ReconcileReport, err error) {
	if s.txh == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		err = s.withSchema(context.Background(), schemaText, func() error { var e error; rep, e = s.reconcileSchema(schemaText); return e })
		return rep, err
	}
	if _, err := referenceSchema(schemaText); err != nil {
		return rep, err
	}
	if err := s.reconcileCommunications(schemaText, &rep); err != nil {
		return rep, err
	}
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

			if prev, dup := seen[oldCol]; dup {
				return fmt.Errorf("table %s: columns %s and %s both declare renamed-from: %s", table, prev, newCol, oldCol)
			}
			seen[oldCol] = newCol

			if !live[oldCol] || live[newCol] {
				continue
			}
			stmt := fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", table, oldCol, newCol)
			if _, err := s.h().Exec(stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
			rep.Renamed = append(rep.Renamed, fmt.Sprintf("%s.%s -> %s.%s", table, oldCol, table, newCol))
		}
	}
	return nil
}

func (s *Store) rebuildTable(table, declaredSQL string, live, want map[string]bool, indexes []string, triggers []string) (carried int64, retErr error) {

	if _, err := s.h().Exec("SAVEPOINT schema_copy"); err != nil {
		return 0, err
	}
	defer func() {
		if retErr != nil {
			if _, err := s.h().Exec("ROLLBACK TO schema_copy"); err != nil {
				retErr = errors.Join(retErr, err)
			}
		}
		if _, err := s.h().Exec("RELEASE schema_copy"); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	shadow := table + "__reconcile_shadow"
	if _, err := s.h().Exec("DROP TABLE IF EXISTS " + quoteIdentifier(shadow)); err != nil {
		return 0, err
	}
	shadowSQL, err := tableSQLNamed(declaredSQL, shadow)
	if err != nil {
		return 0, err
	}
	if _, err := s.h().Exec(shadowSQL); err != nil {
		return 0, fmt.Errorf("create shadow: %w", err)
	}
	before, err := s.liveShape(table)
	if err != nil {
		return 0, err
	}
	after, err := s.liveShape(shadow)
	if err != nil {
		return 0, err
	}
	var shared []string
	for name, c := range after {
		if _, ok := before[name]; ok && c.Hidden == 0 {
			shared = append(shared, name)
		}
	}
	sort.Strings(shared)
	if len(shared) == 0 {
		return 0, fmt.Errorf("no writable columns in common for %s", table)
	}
	tables, err := describeTables(s.h())
	if err != nil {
		return 0, err
	}
	sourceCols, targetCols := []string{}, []string{}

	if !tables[table].WithoutRowID && !tables[shadow].WithoutRowID {
		src, dst := accessibleRowID(before), accessibleRowID(after)
		if src == "" || dst == "" {
			return 0, fmt.Errorf("cannot preserve hidden rowid of %s", table)
		}
		sourceCols = append(sourceCols, quoteIdentifier(src))
		targetCols = append(targetCols, quoteIdentifier(dst))
	}
	for _, name := range shared {
		sourceCols = append(sourceCols, quoteIdentifier(name))
		targetCols = append(targetCols, quoteIdentifier(name))
	}
	from, to := strings.Join(sourceCols, ","), strings.Join(targetCols, ",")
	if _, err := s.h().Exec("INSERT INTO " + quoteIdentifier(shadow) + " (" + to + ") SELECT " + from + " FROM " + quoteIdentifier(table)); err != nil {
		return 0, &CopyRowsError{Cause: err}
	}

	for _, name := range sortedKeys(after) {
		if original, exists := before[name]; exists && original.Hidden == 0 && after[name].Hidden != 0 {
			sourceCols = append(sourceCols, quoteIdentifier(name))
			targetCols = append(targetCols, quoteIdentifier(name))
		}
	}
	if err := verifyCopiedValues(s.h(), table, shadow, sourceCols, targetCols); err != nil {
		return 0, err
	}
	if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(shadow)).Scan(&carried); err != nil {
		return 0, err
	}
	if _, err := s.h().Exec("DROP TABLE " + quoteIdentifier(table)); err != nil {
		return 0, err
	}
	if _, err := s.h().Exec("ALTER TABLE " + quoteIdentifier(shadow) + " RENAME TO " + quoteIdentifier(table)); err != nil {
		return 0, err
	}
	for _, stmt := range append(append([]string{}, indexes...), triggers...) {
		if _, err := s.h().Exec(stmt); err != nil {
			return 0, err
		}
	}
	return carried, nil
}

var createTableHead = regexp.MustCompile("(?is)^CREATE\\s+TABLE\\s+(?:IF\\s+NOT\\s+EXISTS\\s+)?(\"(?:[^\"]|\"\")*\"|" + "`(?:[^`]|``)*`" + "|\\[[^\\]]*\\]|[^\\s(]+)")

func tableSQLNamed(statement, name string) (string, error) {
	loc := createTableHead.FindStringIndex(statement)
	if loc == nil {
		return "", fmt.Errorf("cannot locate target table identifier")
	}
	return "CREATE TABLE " + quoteIdentifier(name) + statement[loc[1]:], nil
}

func accessibleRowID(shape map[string]columnShape) string {
	for _, name := range []string{"_rowid_", "rowid", "oid"} {
		if _, shadowed := shape[name]; !shadowed {
			return name
		}
	}
	return ""
}

func (s *Store) liveColumns(table string) (map[string]bool, error) {
	shape, err := s.liveShape(table)
	if err != nil {
		return nil, err
	}
	return columnNameSet(shape), nil
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

func (s *Store) populatedDroppedColumns(table string, live, want map[string]bool) ([]string, error) {
	shape, err := s.liveShape(table)
	if err != nil {
		return nil, err
	}
	var candidates []string
	for c := range live {
		if !want[c] && shape[c].Hidden != 2 && shape[c].Hidden != 3 {
			candidates = append(candidates, c)
		}
	}
	sort.Strings(candidates)

	var populated []string
	for _, c := range candidates {
		var n int
		q := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL AND %s != ''", quoteIdentifier(table), quoteIdentifier(c), quoteIdentifier(c))
		if err := s.h().QueryRow(q).Scan(&n); err != nil {

			populated = append(populated, c)
			continue
		}
		if n > 0 {
			populated = append(populated, c)
		}
	}
	return populated, nil
}

func (s *Store) liveObjects(kind string) (map[string]string, error) {
	rows, err := s.h().Query(`SELECT name, COALESCE(sql, '') FROM sqlite_master WHERE type = ? AND name NOT GLOB 'sqlite_*'`, kind)
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
		out[sqliteName(name)] = text
	}
	return out, rows.Err()
}

func (s *Store) liveObjectSQL(kind, name string) (string, bool, error) {
	var text sql.NullString
	err := s.h().QueryRow(`SELECT sql FROM sqlite_master WHERE type = ? AND name = ? COLLATE NOCASE`, kind, name).Scan(&text)
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

func (s *Store) reconcileTables(schemaText string, rep *ReconcileReport) error {
	declared := declaredTableSQL(schemaText)
	indexes := declaredIndexSQL(schemaText)
	triggers := declaredTriggerSQL(schemaText)
	prov := lenientProvenance(schemaText)
	sidecars := sidecarsIn(schemaText)

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
			if _, err := s.h().Exec(declared[table]); err != nil {
				return fmt.Errorf("create table %s: %w", table, err)
			}
			rep.Created = append(rep.Created, table)
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

		if table == "ledger" && !live["seq"] {
			return &MirrorReadError{Cause: fmt.Errorf("refusing blind ledger mirror rebuild: no readable seq column")}
		}

		lost, err := s.populatedDroppedColumns(table, live, want)
		if err != nil {
			return err
		}

		if len(lost) > 0 && !derived && s.schemaStartup && live["temp"] && (table == "plugin_kv" || table == "plugin_memories") {
			if table == "plugin_kv" {
				err = clearPluginKVTemp(s.h(), nil)
			} else {
				err = clearPluginMemoryTemp(s.h(), nil)
			}
			if err != nil {
				return &SchemaError{Phase: "expired plugin state", Cause: err}
			}

			lost, err = s.populatedDroppedColumns(table, live, want)
			if err != nil {
				return err
			}
			if len(lost) == 0 {
				why += "; expired T0 rows retired at startup"
			}
		}
		if len(lost) > 0 && !derived {
			return &ShapeError{Problems: []string{fmt.Sprintf(
				"refusing to rebuild %s: column(s) %s hold data and are not declared — "+
					"if they were renamed, say so in schema.sql (-- renamed-from: <old>); "+
					"if they are meant to go, drop them deliberately",
				table, strings.Join(lost, ", ")) + "; " + why}}
		}
		if len(lost) > 0 {
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
			var valueError *CopyValueError
			var copyError *CopyRowsError
			var constraint interface{ Code() int }
			canReplay := errors.As(err, &valueError) || (errors.As(err, &copyError) && errors.As(copyError.Cause, &constraint) && constraint.Code()&255 == 19)
			if !canReplay {
				return fmt.Errorf("rebuild %s: %w — the rows stay as they are; repair them or declare the change", table, err)
			}
			if !derived {
				return &RowsRefusedError{Table: table, Cause: err}
			}
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

type RowsRefusedError struct {
	Table string
	Cause error
}

func (e *RowsRefusedError) Error() string {
	return fmt.Sprintf("rebuild %s: %v — the rows stay as they are; repair them or declare the change", e.Table, e.Cause)
}
func (e *RowsRefusedError) Unwrap() error { return e.Cause }

func (s *Store) recreateEmpty(table, declaredSQL string, indexes, triggers []string) error {
	if !s.schemaReplay {
		var count int64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(table)).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return &ReplayRequiredError{Table: table}
		}
	}
	if _, err := s.h().Exec("DROP TABLE IF EXISTS " + quoteIdentifier(table+"__reconcile_shadow")); err != nil {
		return err
	}
	if _, err := s.h().Exec("DROP TABLE " + quoteIdentifier(table)); err != nil {
		return err
	}
	if _, err := s.h().Exec(declaredSQL); err != nil {
		return err
	}
	for _, stmt := range append(append([]string{}, indexes...), triggers...) {
		if _, err := s.h().Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) reconcileIndexes(schemaText string, rep *ReconcileReport) error {
	declared := declaredIndexStatements(schemaText)
	live, err := s.liveObjects("index")
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(declared) {
		liveSQL, ok := live[name]
		if !ok {
			if _, err := s.h().Exec(declared[name].SQL); err != nil {
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
		if _, err := s.h().Exec("DROP INDEX " + quoteIdentifier(name)); err != nil {
			return fmt.Errorf("drop undeclared index %s: %w", name, err)
		}
		rep.Dropped = append(rep.Dropped, "index "+name)
	}
	return nil
}

func (s *Store) reconcileTriggers(schemaText string, rep *ReconcileReport) error {
	declared := declaredTriggerStatements(schemaText)
	live, err := s.liveObjects("trigger")
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(declared) {
		liveSQL, ok := live[name]
		if !ok {
			if _, err := s.h().Exec(declared[name]); err != nil {
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
		if _, err := s.h().Exec("DROP TRIGGER " + quoteIdentifier(name)); err != nil {
			return fmt.Errorf("drop undeclared trigger %s: %w", name, err)
		}
		rep.Dropped = append(rep.Dropped, "trigger "+name)
	}
	return nil
}

func (s *Store) reconcileViews(schemaText string, rep *ReconcileReport) error {
	declared := declaredViewSQL(schemaText)
	sidecars := sidecarsIn(schemaText)
	for _, name := range sortedKeys(declared) {
		liveSQL, exists, err := s.liveObjectSQL("view", name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.h().Exec(declared[name]); err != nil {
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
		if err := s.h().QueryRow("SELECT COUNT(*) FROM " + quoteIdentifier(name)).Scan(&n); err != nil {
			return fmt.Errorf("count %s before retiring it: %w", name, err)
		}
		if _, err := s.h().Exec("DROP TABLE " + quoteIdentifier(name)); err != nil {
			return fmt.Errorf("retire %s: %w", name, err)
		}
		if c, preserved := s.schemaConversions[name]; preserved {
			rep.Retired = append(rep.Retired, fmt.Sprintf("%s (%d row(s) %s)", name, n, c.Reason))
		} else {
			rep.Retired = append(rep.Retired, fmt.Sprintf("%s (%d row(s) discarded, as declared)", name, n))
		}
	}
	return nil
}

func (s *Store) dropAndCreate(kind, name, declaredSQL string) error {
	if _, err := s.h().Exec("DROP " + kind + " " + quoteIdentifier(name)); err != nil {
		return err
	}
	_, err := s.h().Exec(declaredSQL)
	return err
}

func (s *Store) reconcileSidecars(schemaText string, rep *ReconcileReport) error {
	declared := declaredVirtualSQL(schemaText)
	live, err := s.liveObjects("table")
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(declared) {
		liveSQL, exists := live[name]
		if !exists {
			if _, err := s.h().Exec(declared[name]); err != nil {
				return err
			}
			rep.Sidecars = append(rep.Sidecars, name+" (created)")
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
		if _, err := s.h().Exec("DROP TABLE " + quoteIdentifier(name)); err != nil {
			return fmt.Errorf("drop undeclared sidecar %s: %w", name, err)
		}
		rep.Dropped = append(rep.Dropped, "sidecar "+name)
	}
	return nil
}
