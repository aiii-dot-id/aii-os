package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// This is the subagent_metrics declaration before emitted requests were
// measured. Keeping turn_metrics unchanged isolates the child's downgrade.
const subagentMetricsBeforeEmissions = `CREATE TABLE IF NOT EXISTS subagent_metrics (
    -- provenance: ephemeral
    session_id TEXT PRIMARY KEY,
    ts_ms      INTEGER NOT NULL,
    calls      INTEGER NOT NULL,
    tokens     INTEGER NOT NULL,
    wall_ms    INTEGER NOT NULL,
    failed     INTEGER NOT NULL,
    role       TEXT NOT NULL DEFAULT '',
    model      TEXT NOT NULL DEFAULT '',
    legs       INTEGER NOT NULL DEFAULT 1
) STRICT;`

func subagentSchemaBeforeEmissions(t *testing.T) string {
	t.Helper()
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	const head, tail = "CREATE TABLE IF NOT EXISTS subagent_metrics (", ") STRICT;"
	start := strings.Index(s, head)
	end := strings.Index(s[max(start, 0):], tail)
	if start < 0 || end < 0 {
		t.Fatal("schema.sql no longer declares subagent_metrics")
	}
	return s[:start] + subagentMetricsBeforeEmissions + s[start+end+len(tail):]
}

func TestSubagentEmissionDowngradeRefusesEachPopulatedColumn(t *testing.T) {
	for _, column := range []string{"emitted_calls", "emission_coverage_complete"} {
		t.Run(column, func(t *testing.T) {
			s, err := NewMemory()
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.InsertSubagentMetric(SubagentMetric{SessionID: "child", Calls: 19, Legs: 2}); err != nil {
				t.Fatal(err)
			}
			// Zero is measured data too. Leave the other new column NULL,
			// so neither it nor any parent metric can cause this refusal.
			if _, err := s.DB().Exec(`UPDATE subagent_metrics SET ` + column + ` = 0 WHERE session_id = 'child'`); err != nil {
				t.Fatal(err)
			}
			var turns int
			if err := s.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics`).Scan(&turns); err != nil || turns != 0 {
				t.Fatalf("fixture has parent metrics: %d (%v)", turns, err)
			}
			older := subagentSchemaBeforeEmissions(t)
			err = s.withSchema(t.Context(), older, func() error {
				_, err := s.reconcileSchema(older)
				return err
			})
			var shape *ShapeError
			if !errors.As(err, &shape) || !strings.Contains(err.Error(), "refusing to rebuild subagent_metrics: column(s) "+column+" hold data") {
				t.Fatalf("the child's measured column was not refused: %v", err)
			}
			var calls, legs int
			var emitted, complete sql.NullInt64
			if err := s.DB().QueryRow(`SELECT calls, legs, emitted_calls, emission_coverage_complete FROM subagent_metrics WHERE session_id = 'child'`).Scan(&calls, &legs, &emitted, &complete); err != nil {
				t.Fatal(err)
			}
			if calls != 19 || legs != 2 || emitted.Valid != (column == "emitted_calls") || complete.Valid != (column == "emission_coverage_complete") || emitted.Int64 != 0 || complete.Int64 != 0 {
				t.Fatalf("refusal changed the child's retained row: calls=%d legs=%d emitted=%+v complete=%+v", calls, legs, emitted, complete)
			}
		})
	}
}

func TestSubagentEmissionUpgradePreservesHistoricalUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	for _, query := range []string{
		`DROP TABLE subagent_metrics`,
		subagentMetricsBeforeEmissions,
		`INSERT INTO subagent_metrics VALUES ('child', 123, 19, 700, 80, 1, 'reviewer', 'fixture-model', 2)`,
	} {
		if _, err := s.DB().Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	var ts, calls, tokens, wall, failed, legs int
	var role, model string
	var emitted, complete sql.NullInt64
	if err := s.DB().QueryRow(`SELECT ts_ms, calls, tokens, wall_ms, failed, role, model, legs, emitted_calls, emission_coverage_complete FROM subagent_metrics WHERE session_id = 'child'`).Scan(&ts, &calls, &tokens, &wall, &failed, &role, &model, &legs, &emitted, &complete); err != nil {
		t.Fatal(err)
	}
	if ts != 123 || calls != 19 || tokens != 700 || wall != 80 || failed != 1 || role != "reviewer" || model != "fixture-model" || legs != 2 || emitted.Valid || complete.Valid {
		t.Fatalf("upgrade changed historical child data or invented a measurement: %d %d %d %d %d %q %q %d %+v %+v", ts, calls, tokens, wall, failed, role, model, legs, emitted, complete)
	}
	var turns int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics`).Scan(&turns); err != nil || turns != 0 {
		t.Fatalf("fixture has parent metrics: %d (%v)", turns, err)
	}
	// NULLs contain no new observation: going back remains lossless.
	older := subagentSchemaBeforeEmissions(t)
	if err := s.withSchema(t.Context(), older, func() error {
		_, err := s.reconcileSchema(older)
		return err
	}); err != nil {
		t.Fatalf("historical unknown emissions prevented lossless downgrade: %v", err)
	}
	if err := s.DB().QueryRow(`SELECT calls, legs FROM subagent_metrics WHERE session_id = 'child'`).Scan(&calls, &legs); err != nil || calls != 19 || legs != 2 {
		t.Fatalf("lossless downgrade changed historical calls/legs: %d/%d (%v)", calls, legs, err)
	}
}
