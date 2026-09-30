package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type TurnMetric struct {
	TsMs      int64
	Calls     int
	ReadOnly  int
	Spawned   int
	Harvested int

	Predicted int

	DeclaredOrdinal int

	Independent int

	Rounds int

	Source   string
	Provider string
	Model    string

	Usage *TurnUsage
}

type TurnUsage struct {
	PromptTokens       int
	CompletionTokens   int
	TotalTokens        int
	CachedPromptTokens int
	CacheWriteTokens   int
	CacheWrite5mTokens int
	CacheWrite1hTokens int
	CacheReadReports   int
	CacheWriteReports  int
	UnknownAttempts    int
	Silent             int
}

const turnMetricSlots = 1000

const facilitySourcePrefix = "facility:"

func FacilitySource(name string) string { return facilitySourcePrefix + name }

const TimerWakeSource = "timer"

func (s *Store) mainTurns() string {
	if s.historicalTurnMetrics {
		return "1"
	}
	return `(source IS NULL OR source NOT LIKE '` + facilitySourcePrefix + `%')`
}

func (s *Store) InsertTurnMetric(m TurnMetric) error {
	usage := make([]any, 11)
	if u := m.Usage; u != nil {
		usage = []any{u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.CachedPromptTokens,
			u.CacheWriteTokens, u.CacheWrite5mTokens, u.CacheWrite1hTokens,
			u.CacheReadReports, u.CacheWriteReports, u.UnknownAttempts, u.Silent}
	}
	args := append([]any{m.TsMs, m.Calls, m.ReadOnly, m.Spawned, m.Harvested, m.Predicted, m.DeclaredOrdinal, m.Independent, m.Rounds,
		nullable(m.Source), nullable(m.Provider), nullable(m.Model)}, usage...)
	s.mu.Lock()
	defer s.mu.Unlock()
	for slot := int64(0); slot < turnMetricSlots; slot++ {
		args[0] = m.TsMs + slot
		_, err := s.w().Exec(`INSERT INTO turn_metrics (ts_ms, calls, read_only, spawned, harvested, predicted, declared_ordinal, independent, rounds,
			source, provider, model,
			prompt_tokens, completion_tokens, total_tokens, cached_prompt_tokens,
			cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens,
			cache_read_reports, cache_write_reports, unknown_attempts, silent_calls)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args...)
		var taken *sqlite.Error
		if !errors.As(err, &taken) || taken.Code() != sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY {
			return err
		}
	}
	return fmt.Errorf("turn metric at %d: every millisecond up to %d later is taken", m.TsMs, turnMetricSlots)
}

type SubagentMetric struct {
	SessionID string
	TsMs      int64
	Calls     int
	Tokens    int
	WallMs    int64
	Failed    bool
	Role      string
	Model     string

	Legs int
}

func (s *Store) InsertSubagentMetric(m SubagentMetric) error {
	failed := 0
	if m.Failed {
		failed = 1
	}
	legs := m.Legs
	if legs < 1 {
		legs = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w().Exec(`INSERT OR REPLACE INTO subagent_metrics (session_id, ts_ms, calls, tokens, wall_ms, failed, role, model, legs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.SessionID, m.TsMs, m.Calls, m.Tokens, m.WallMs, failed, m.Role, m.Model, legs)
	return err
}

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

const timelyDeclarationMax = 20

func (s *Store) BetaUnplannedDeepCount(sinceMs int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM turn_metrics
		WHERE ts_ms >= ? AND calls > 100
		  AND (predicted = 0 OR declared_ordinal = 0 OR declared_ordinal > ?)
		  AND `+s.mainTurns(),
		sinceMs, timelyDeclarationMax).Scan(&n)
	return n, err
}

func (s *Store) PlanCalibration(window time.Duration) (plans int, predicted int, actual int, err error) {
	cutoff := time.Now().UTC().Add(-window).UnixMilli()

	row := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(predicted),0), COALESCE(SUM(calls),0)
		FROM turn_metrics WHERE ts_ms >= ? AND predicted > 0
		  AND declared_ordinal BETWEEN 1 AND ? AND `+s.mainTurns(), cutoff, timelyDeclarationMax)
	if e := row.Scan(&plans, &predicted, &actual); e != nil {
		if e == sql.ErrNoRows {
			return 0, 0, 0, nil
		}
		return 0, 0, 0, e
	}
	return plans, predicted, actual, nil
}

func (s *Store) RhythmStats(window time.Duration) (turns int, calls int, readOnlyPct int, spawns int, harvests int, rounds int, err error) {
	cutoff := time.Now().UTC().Add(-window).UnixMilli()

	row := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(calls),0), COALESCE(SUM(read_only),0), COALESCE(SUM(spawned),0), COALESCE(SUM(harvested),0),
		COALESCE(SUM(CASE WHEN rounds > 0 THEN rounds ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN rounds > 0 THEN calls ELSE 0 END),0)
		FROM turn_metrics WHERE ts_ms >= ? AND `+s.mainTurns(), cutoff)
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

	if roundsSum > 0 {
		rounds = int(100 * roundedCalls / roundsSum)
	}
	return turns, calls, readOnlyPct, spawns, harvests, rounds, nil
}

type WakeUsage struct {
	Complete, Min, Median, Max int
	Floors, LargestFloor       int
	Unknown                    int
}

func (s *Store) TimerWakeUsage(window time.Duration) (WakeUsage, error) {
	var u WakeUsage
	if s.historicalTurnMetrics {
		return u, nil
	}
	cutoff := time.Now().UTC().Add(-window).UnixMilli()
	rows, err := s.db.Query(`SELECT rounds, total_tokens, silent_calls, unknown_attempts
		FROM turn_metrics WHERE ts_ms >= ? AND source = ?`, cutoff, TimerWakeSource)
	if err != nil {
		return WakeUsage{}, err
	}
	defer rows.Close()
	var exact []int
	for rows.Next() {
		var rounds int
		var total, silent, unknown sql.NullInt64
		if err := rows.Scan(&rounds, &total, &silent, &unknown); err != nil {
			return WakeUsage{}, err
		}
		switch {
		case !total.Valid || !silent.Valid || !unknown.Valid:
			u.Unknown++
		case rounds > 0 && silent.Int64 == 0 && unknown.Int64 == 0:
			exact = append(exact, int(total.Int64))
		case total.Int64 > 0:
			u.Floors++
			u.LargestFloor = max(u.LargestFloor, int(total.Int64))
		default:
			u.Unknown++
		}
	}
	if err := rows.Err(); err != nil {
		return WakeUsage{}, err
	}
	if u.Complete = len(exact); u.Complete > 0 {
		slices.Sort(exact)
		u.Min, u.Median, u.Max = exact[0], exact[(u.Complete-1)/2], exact[u.Complete-1]
	}
	return u, nil
}
