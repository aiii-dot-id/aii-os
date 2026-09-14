package store

import (
	"database/sql"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
type TurnMetric struct {
	TsMs      int64
	Calls     int
	ReadOnly  int
	Spawned   int
	Harvested int
	// .
	// .
	// .
	// .
	// .
	Predicted int
	// .
	// .
	// .
	DeclaredOrdinal int
	// .
	// .
	// .
	// .
	Independent int
	// .
	// .
	// .
	Rounds int
}

// .
// .
func (s *Store) InsertTurnMetric(m TurnMetric) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO turn_metrics (ts_ms, calls, read_only, spawned, harvested, predicted, declared_ordinal, independent, rounds)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.TsMs, m.Calls, m.ReadOnly, m.Spawned, m.Harvested, m.Predicted, m.DeclaredOrdinal, m.Independent, m.Rounds)
	return err
}

// .
type SubagentMetric struct {
	SessionID string
	TsMs      int64
	Calls     int
	Tokens    int
	WallMs    int64
	Failed    bool
	Role      string
	Model     string
	// .
	// .
	Legs int
}

// .
// .
func (s *Store) InsertSubagentMetric(m SubagentMetric) error {
	failed := 0
	if m.Failed {
		failed = 1
	}
	legs := m.Legs
	if legs < 1 {
		legs = 1
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO subagent_metrics (session_id, ts_ms, calls, tokens, wall_ms, failed, role, model, legs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.SessionID, m.TsMs, m.Calls, m.Tokens, m.WallMs, failed, m.Role, m.Model, legs)
	return err
}

// .
// .
// .
func (s *Store) SubagentStats(window time.Duration) (runs, calls, tokens, failed int, err error) {
	cutoff := time.Now().UTC().Add(-window).UnixMilli()
	row := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(calls),0), COALESCE(SUM(tokens),0), COALESCE(SUM(failed),0)
		FROM subagent_metrics WHERE ts_ms >= ?`, cutoff)
	if e := row.Scan(&runs, &calls, &tokens, &failed); e != nil {
		if e == sql.ErrNoRows {
			return 0, 0, 0, 0, nil
		}
		return 0, 0, 0, 0, e
	}
	return runs, calls, tokens, failed, nil
}

// .
// .
// .
// .
const timelyDeclarationMax = 20

// .
// .
// .
// .
func (s *Store) BetaUnplannedDeepCount(sinceMs int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM turn_metrics
		WHERE ts_ms >= ? AND calls > 100
		  AND (predicted = 0 OR declared_ordinal = 0 OR declared_ordinal > ?)`,
		sinceMs, timelyDeclarationMax).Scan(&n)
	return n, err
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
func (s *Store) PlanCalibration(window time.Duration) (plans int, predicted int, actual int, err error) {
	cutoff := time.Now().UTC().Add(-window).UnixMilli()
	// .
	// .
	// .
	// .
	row := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(predicted),0), COALESCE(SUM(calls),0)
		FROM turn_metrics WHERE ts_ms >= ? AND predicted > 0
		  AND declared_ordinal BETWEEN 1 AND ?`, cutoff, timelyDeclarationMax)
	if e := row.Scan(&plans, &predicted, &actual); e != nil {
		if e == sql.ErrNoRows {
			return 0, 0, 0, nil
		}
		return 0, 0, 0, e
	}
	return plans, predicted, actual, nil
}

// .
// .
// .
func (s *Store) RhythmStats(window time.Duration) (turns int, calls int, readOnlyPct int, spawns int, harvests int, rounds int, err error) {
	cutoff := time.Now().UTC().Add(-window).UnixMilli()
	// .
	// .
	// .
	// .
	row := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(calls),0), COALESCE(SUM(read_only),0), COALESCE(SUM(spawned),0), COALESCE(SUM(harvested),0),
		COALESCE(SUM(CASE WHEN rounds > 0 THEN rounds ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN rounds > 0 THEN calls ELSE 0 END),0)
		FROM turn_metrics WHERE ts_ms >= ?`, cutoff)
	var ro, roundsSum, roundedCalls int64
	if e := row.Scan(&turns, &calls, &ro, &spawns, &harvests, &roundsSum, &roundedCalls); e != nil {
		if e == sql.ErrNoRows {
			return 0, 0, 0, 0, 0, 0, nil
		}
		return 0, 0, 0, 0, 0, 0, e
	}
	if calls > 0 {
		readOnlyPct = int(100 * ro / int64(calls))
	}
	// .
	// .
	if roundsSum > 0 {
		rounds = int(100 * roundedCalls / roundsSum)
	}
	return turns, calls, readOnlyPct, spawns, harvests, rounds, nil
}
