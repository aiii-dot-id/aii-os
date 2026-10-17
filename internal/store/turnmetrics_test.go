package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestTurnMetricRoundTrip pins the rhythm-line source: one row per
// completed turn, written at the TURN_SUMMARY seam, aggregated by
// RhythmStats over a wall-clock window.
func TestTurnMetricRoundTrip(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.InsertTurnMetric(TurnMetric{TsMs: time.Now().UnixMilli() - time.Hour.Milliseconds(), Calls: 10, ReadOnly: 7, Spawned: 1, Harvested: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTurnMetric(TurnMetric{TsMs: time.Now().UnixMilli(), Calls: 5, ReadOnly: 5, Spawned: 0, Harvested: 0}); err != nil {
		t.Fatal(err)
	}
	turns, calls, roPct, spawns, harvests, _, err := s.RhythmStats(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if turns != 2 || calls != 15 || roPct != 80 || spawns != 1 || harvests != 2 {
		t.Fatalf("rhythm stats wrong: turns=%d calls=%d ro%%=%d spawns=%d harvests=%d", turns, calls, roPct, spawns, harvests)
	}
}

// Nothing gives a turn a millisecond of its own, and the key is the
// millisecond. The older write (INSERT OR REPLACE) let the second turn
// replace the first without a word; now the second lands at the next free
// millisecond, a third at the one after, and every reader counts them all.
func TestTurnMetricsKeepTwoTurnsEndingInOneMillisecond(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := time.Now().UTC().UnixMilli()
	for _, m := range []TurnMetric{{TsMs: ts, Calls: 3, ReadOnly: 1}, {TsMs: ts, Calls: 5, ReadOnly: 5}, {TsMs: ts, Calls: 2, ReadOnly: 0}} {
		if err := s.InsertTurnMetric(m); err != nil {
			t.Fatalf("a turn ending in a taken millisecond: %v", err)
		}
	}
	var got []int64
	rows, err := s.DB().Query(`SELECT ts_ms, calls FROM turn_metrics ORDER BY ts_ms`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var at, calls int64
		if err := rows.Scan(&at, &calls); err != nil {
			t.Fatal(err)
		}
		got = append(got, at-ts, calls)
	}
	if want := []int64{0, 3, 1, 5, 2, 2}; !slices.Equal(got, want) {
		t.Fatalf("rows (ms after the first, calls) = %v, want %v — each turn at the next free millisecond, none lost", got, want)
	}
	turns, calls, roPct, _, _, _, err := s.RhythmStats(48 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if turns != 3 || calls != 10 || roPct != 60 {
		t.Fatalf("rhythm read turns=%d calls=%d ro%%=%d, want 3, 10, 60", turns, calls, roPct)
	}
}

const olderTurnMetrics = `CREATE TABLE IF NOT EXISTS turn_metrics (
    -- provenance: ephemeral
    ts_ms    INTEGER PRIMARY KEY,
    calls    INTEGER NOT NULL,
    read_only INTEGER NOT NULL,
    spawned  INTEGER NOT NULL,
    harvested INTEGER NOT NULL,
    predicted INTEGER NOT NULL DEFAULT 0,
    declared_ordinal INTEGER NOT NULL DEFAULT 0,
    independent INTEGER NOT NULL DEFAULT 0,
    rounds INTEGER NOT NULL DEFAULT 0
) STRICT;`

// turnUsageColumns are the columns per-turn usage added, all nullable.
var turnUsageColumns = []string{"source", "provider", "model", "turn_id", "emitted_calls", "emission_coverage_complete",
	"prompt_tokens", "completion_tokens", "total_tokens", "cached_prompt_tokens",
	"cache_write_tokens", "cache_write_5m_tokens", "cache_write_1h_tokens",
	"cache_read_reports", "cache_write_reports", "unknown_attempts", "silent_calls"}

// olderSchema is schema.sql as the older binary carries it: this file with
// turn_metrics declared the older way.
func olderSchema(t *testing.T) string {
	t.Helper()
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	const head, tail = "CREATE TABLE IF NOT EXISTS turn_metrics (", ") STRICT;"
	start := strings.Index(s, head)
	end := strings.Index(s[max(start, 0):], tail)
	if start < 0 || end < 0 {
		t.Fatal("schema.sql no longer declares turn_metrics")
	}
	return s[:start] + olderTurnMetrics + s[start+end+len(tail):]
}

// olderReconcile runs the older binary's schema step on this database as
// its startup does: the older file, reconciled in one transaction that a
// refusal rolls back.
func olderReconcile(t *testing.T, s *Store) error {
	t.Helper()
	older := olderSchema(t)
	return s.withSchema(t.Context(), older, func() error {
		_, err := s.reconcileSchema(older)
		return err
	})
}

// olderRows seeds turn_metrics in the older shape with rows the readers
// can tell apart, as a database the older binary left. The Store is open.
func olderRows(t *testing.T, s *Store, now int64) {
	t.Helper()
	for _, q := range []string{`DROP TABLE turn_metrics`, olderTurnMetrics} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range [][9]int64{
		// ts_ms, calls, read_only, spawned, harvested, predicted, declared_ordinal, independent, rounds
		{now - 5000, 47, 20, 1, 0, 6, 2, 0, 12},
		{now - 4000, 130, 100, 0, 2, 0, 0, 0, 40},    // deep, unplanned
		{now - 3000, 12, 12, 0, 1, 10, 4, 2, 0},      // before rounds were counted
		{now - 2000, 150, 30, 2, 0, 25, 87, 0, 60},   // deep, declared late
		{now - 72*3600*1000, 9, 9, 0, 0, 3, 1, 0, 3}, // outside the window
	} {
		if _, err := s.DB().Exec(`INSERT INTO turn_metrics VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], r[8]); err != nil {
			t.Fatal(err)
		}
	}
}

// olderColumns is what the older declaration keeps of every row, in key
// order: the history a rollback must not lose.
func olderColumns(t *testing.T, s *Store) string {
	t.Helper()
	var out string
	if err := s.DB().QueryRow(`SELECT COALESCE(group_concat(r, ';'), '') FROM (SELECT ts_ms||','||calls||','||read_only||','||spawned||','||harvested||','||predicted||','||declared_ordinal||','||independent||','||rounds AS r FROM turn_metrics ORDER BY ts_ms)`).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A DATABASE THE OLDER BINARY LEFT. The engine rebuilds turn_metrics with
// the new columns, carrying every row under its own key. The new columns
// read NULL — unknown usage, never a zero that would call those turns
// free — and every reader the identity's own prompt is built from answers
// exactly what it answered before. "Before" is read as the SAFE mount
// reads that database, reconciling nothing: a table with no source column
// is all turns, and the readers still answer.
func TestTurnMetricsUpgradeCarriesRowsAsUnknownUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	olderRows(t, s, now)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	type readings struct{ turns, calls, ro, spawns, harvests, batch, plans, predicted, actual, unmeasured, deep int }
	read := func(s *Store) readings {
		t.Helper()
		var r readings
		var err error
		if r.turns, r.calls, r.ro, r.spawns, r.harvests, r.batch, err = s.RhythmStats(48 * time.Hour); err != nil {
			t.Fatal(err)
		}
		calibration, err := s.PlanCalibration(48 * time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		r.plans, r.predicted, r.actual, r.unmeasured = calibration.Plans, calibration.Predicted, calibration.Actual, calibration.Unmeasured
		if r.deep, err = s.BetaUnplannedDeepCount(now - 48*3600*1000); err != nil {
			t.Fatal(err)
		}
		return r
	}
	older, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	before, history := read(older), olderColumns(t, older)
	if before.turns != 4 || before.deep != 2 || before.plans != 0 || before.unmeasured != 3 {
		t.Fatalf("fixture reads %+v; it is not exercising the readers", before)
	}
	if err := older.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = New(path)
	if err != nil {
		t.Fatalf("the engine did not open a database the older binary left: %v", err)
	}
	defer s.Close()
	if after := read(s); after != before {
		t.Fatalf("the readers changed their answer across the upgrade:\nbefore %+v\n after %+v", before, after)
	}
	if got := olderColumns(t, s); got != history {
		t.Fatalf("carried rows changed:\nbefore %s\n after %s", history, got)
	}
	unknown := "1"
	for _, c := range turnUsageColumns {
		unknown += " AND " + c + " IS NULL"
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics WHERE ` + unknown).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("%d of 5 carried rows read as unknown usage", n)
	}
}

func TestFacilityRowsAreNotTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	olderRows(t, s, now) // rows from before sources were kept …
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = New(path); err != nil { // … carried across the upgrade
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	if err := s.InsertTurnMetric(TurnMetric{TsMs: now - 1000, Calls: 8, ReadOnly: 8, Rounds: 3, Predicted: 6, DeclaredOrdinal: 1, Source: "operator",
		Usage: &TurnUsage{PromptTokens: 30, CompletionTokens: 3, TotalTokens: 33}, Emissions: &ToolCallEmissions{Count: 8, Complete: true}}); err != nil {
		t.Fatal(err)
	}
	type readings struct {
		turns, calls, ro, spawns, harvests, batch, plans, predicted, actual, unmeasured, deep int
		measure                                                                               MeasurementReadout
	}
	read := func() readings {
		t.Helper()
		var r readings
		var err error
		if r.turns, r.calls, r.ro, r.spawns, r.harvests, r.batch, err = s.RhythmStats(48 * time.Hour); err != nil {
			t.Fatal(err)
		}
		calibration, err := s.PlanCalibration(48 * time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		r.plans, r.predicted, r.actual, r.unmeasured = calibration.Plans, calibration.Predicted, calibration.Actual, calibration.Unmeasured
		if r.deep, err = s.BetaUnplannedDeepCount(now - 48*3600*1000); err != nil {
			t.Fatal(err)
		}
		if r.measure, err = s.Measure(48 * time.Hour); err != nil {
			t.Fatal(err)
		}
		return r
	}
	before := read()
	if before.turns != 5 || before.deep != 2 || before.plans != 1 || before.unmeasured != 3 || before.measure.Metrics[0].Denominator != 5 {
		t.Fatalf("fixture reads %+v; it is not exercising the readers", before)
	}
	for i, name := range []string{"dream", "consolidate", "selfmodel"} {
		if err := s.InsertTurnMetric(TurnMetric{TsMs: now - 500 + int64(i), Calls: 150, ReadOnly: 150, Spawned: 1, Harvested: 1,
			Predicted: 10, DeclaredOrdinal: 3, Independent: 2, Rounds: 5, Source: FacilitySource(name), Model: "m",
			Usage: &TurnUsage{PromptTokens: 7, CompletionTokens: 1, TotalTokens: 8}, Emissions: &ToolCallEmissions{Count: 150, Complete: true}}); err != nil {
			t.Fatal(err)
		}
	}
	var facility int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics WHERE source LIKE 'facility:%'`).Scan(&facility); err != nil || facility != 3 {
		t.Fatalf("fixture: %d facility rows (%v), want 3", facility, err)
	}
	if after := read(); !reflect.DeepEqual(after, before) {
		t.Fatalf("facility rows changed what the turn readers answer:\nbefore %+v\n after %+v", before, after)
	}
}

// GOING BACK, simulated with the older binary's own engine and schema.
// (a) Upgraded but no turn recorded since: every new column is NULL, and
// the older reconcile drops them without loss — that binary would start.
// (b) Once a turn's usage is written, it refuses, naming each column that
// holds data. (c) Deleting the facility rows, then dropping each named
// column — lets it reconcile, the turns' history is still there, and the
// older binary's readers, which know no source, count turns only: a
// facility row left behind would read to them as a turn.
func TestTurnMetricsRollbackSimulation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	olderRows(t, s, time.Now().UTC().UnixMilli())
	history := olderColumns(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = New(path); err != nil { // the upgrade
		t.Fatal(err)
	}
	defer func() { s.Close() }()

	// (a)
	if err := olderReconcile(t, s); err != nil {
		t.Fatalf("(a) the older engine refused a database with no usage recorded: %v", err)
	}
	if got := olderColumns(t, s); got != history {
		t.Fatalf("(a) rollback lost history:\nbefore %s\n after %s", history, got)
	}
	if cols, err := s.liveColumns("turn_metrics"); err != nil || cols["source"] {
		t.Fatalf("(a) the older shape was not restored: %v %v", cols, err)
	}

	// (b) upgrade again, record one turn, go back again.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = New(path); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 1, Calls: 2, Rounds: 1, Source: "operator", Model: "m", TurnID: "turn_1",
		Usage: &TurnUsage{PromptTokens: 7, CompletionTokens: 1, TotalTokens: 8}}); err != nil {
		t.Fatal(err)
	}
	history = olderColumns(t, s)
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 2, Rounds: 1, Source: FacilitySource("dream"), Model: "m",
		Usage: &TurnUsage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}}); err != nil {
		t.Fatal(err)
	}
	err = olderReconcile(t, s)
	var shape *ShapeError
	if !errors.As(err, &shape) {
		t.Fatalf("(b) the older engine did not refuse: %v", err)
	}
	named := []string{"cache_read_reports", "cache_write_1h_tokens", "cache_write_5m_tokens", "cache_write_reports",
		"cache_write_tokens", "cached_prompt_tokens", "completion_tokens", "model", "prompt_tokens",
		"silent_calls", "source", "total_tokens", "turn_id", "unknown_attempts"} // provider is NULL: not named
	want := "refusing to rebuild turn_metrics: column(s) " + strings.Join(named, ", ") + " hold data and are not declared"
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "drop them deliberately") {
		t.Fatalf("(b) refusal does not name the columns and the route:\n%v\nwant %q", err, want)
	}

	// (c)
	if _, err := s.DB().Exec(`DELETE FROM turn_metrics WHERE source LIKE 'facility:%';`); err != nil {
		t.Fatalf("(c) the documented route failed at the facility rows: %v", err)
	}
	for _, c := range named {
		if _, err := s.DB().Exec(`ALTER TABLE turn_metrics DROP COLUMN ` + c); err != nil {
			t.Fatalf("(c) the documented route failed at %s: %v", c, err)
		}
	}
	if err := olderReconcile(t, s); err != nil {
		t.Fatalf("(c) with the named columns dropped the older engine still refused: %v", err)
	}
	if got := olderColumns(t, s); got != history {
		t.Fatalf("(c) the route lost history, or left a facility row for the older binary to count as a turn:\nbefore %s\n after %s", history, got)
	}
}

// previousSchema is schema.sql as the releases before turn_id carry it
// (0.1.14 through 0.1.17): this file without that one column.
func previousSchema(t *testing.T) string {
	t.Helper()
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	const first, last = "    turn_id  TEXT,\n", "    turn_id  TEXT,\n"
	start, end := strings.Index(s, first), strings.Index(s, last)
	if start < 0 || end < start {
		t.Fatal("schema.sql no longer declares turn_metrics.turn_id with its comment")
	}
	return s[:start] + s[end+len(last):]
}

// GOING BACK ONE RELEASE, to a binary that knows every usage column but the
// turn's id. (a) Upgraded, and only a facility's call recorded since: the id
// column is all NULL and the previous engine drops it without loss. (b) Once
// one turn is recorded it refuses, naming turn_id and nothing else.
// (c) One DROP COLUMN lets it reconcile with every row, the facility's
// included, and its usage still there.
func TestGoingBackOneReleaseMeetsOnlyTheTurnID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	previous := previousSchema(t)
	back := func() error {
		return s.withSchema(t.Context(), previous, func() error {
			_, err := s.reconcileSchema(previous)
			return err
		})
	}
	rows := func() string {
		var got string
		if err := s.DB().QueryRow(`SELECT COALESCE(group_concat(ts_ms||':'||COALESCE(source,'')||':'||COALESCE(total_tokens,-1), ';'), '') FROM (SELECT * FROM turn_metrics ORDER BY ts_ms)`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// (a)
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 1, Rounds: 1, Source: FacilitySource("dream"), Model: "m",
		Usage: &TurnUsage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}}); err != nil {
		t.Fatal(err)
	}
	history := rows()
	if err := back(); err != nil {
		t.Fatalf("(a) the previous engine refused a database with no turn recorded: %v", err)
	}
	if cols, err := s.liveColumns("turn_metrics"); err != nil || cols["turn_id"] || !cols["source"] {
		t.Fatalf("(a) the previous shape was not restored: %v %v", cols, err)
	}
	if got := rows(); got != history {
		t.Fatalf("(a) going back lost usage:\nbefore %s\n after %s", history, got)
	}

	// (b) upgrade again, record one turn, go back again.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = New(path); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 2, Calls: 2, Rounds: 1, Source: "operator", Provider: "p", Model: "m", TurnID: "turn_1",
		Usage: &TurnUsage{PromptTokens: 7, CompletionTokens: 1, TotalTokens: 8}}); err != nil {
		t.Fatal(err)
	}
	history = rows()
	err = back()
	var shape *ShapeError
	if !errors.As(err, &shape) {
		t.Fatalf("(b) the previous engine did not refuse: %v", err)
	}
	if want := "refusing to rebuild turn_metrics: column(s) turn_id hold data and are not declared"; !strings.Contains(err.Error(), want) {
		t.Fatalf("(b) the refusal does not name turn_id alone:\n%v\nwant %q", err, want)
	}

	// (c)
	if _, err := s.DB().Exec(`ALTER TABLE turn_metrics DROP COLUMN turn_id;`); err != nil {
		t.Fatalf("(c) the documented route failed: %v", err)
	}
	if err := back(); err != nil {
		t.Fatalf("(c) with turn_id dropped the previous engine still refused: %v", err)
	}
	if got := rows(); got != history || !strings.Contains(got, "facility:dream") {
		t.Fatalf("(c) the route lost a row or its usage, the facility's included:\nbefore %s\n after %s", history, got)
	}
}

// A turn's row carries the turn's id and a facility's call carries none:
// what a turn cost is joined to the turn by an exact id, never by a time.
func TestATurnsRowCarriesItsTurnIDAndAFacilitysCarriesNone(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 10, Calls: 1, Rounds: 1, Source: "operator", Model: "m", TurnID: "turn_a",
		Usage: &TurnUsage{PromptTokens: 7, CompletionTokens: 1, TotalTokens: 8}}); err != nil {
		t.Fatal(err)
	}
	// The same millisecond: the row takes the next slot and keeps its own id.
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 10, Calls: 3, Rounds: 2, Source: "operator", Model: "m", TurnID: "turn_b",
		Usage: &TurnUsage{PromptTokens: 20, CompletionTokens: 2, TotalTokens: 22}}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertTurnMetric(TurnMetric{TsMs: 12, Rounds: 1, Source: FacilitySource("dream"), Model: "m",
		Usage: &TurnUsage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"turn_a": 8, "turn_b": 22} {
		var rows, total int
		if err := s.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(total_tokens), 0) FROM turn_metrics WHERE turn_id = ?`, id).Scan(&rows, &total); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || total != want {
			t.Fatalf("turn %s: %d row(s), %d tokens; want one row of %d", id, rows, total, want)
		}
	}
	var unnamed int
	var source string
	if err := s.DB().QueryRow(`SELECT COUNT(*), MAX(source) FROM turn_metrics WHERE turn_id IS NULL`).Scan(&unnamed, &source); err != nil {
		t.Fatal(err)
	}
	if unnamed != 1 || source != FacilitySource("dream") {
		t.Fatalf("%d row(s) without a turn id (%s); want only the facility's call", unnamed, source)
	}
}
