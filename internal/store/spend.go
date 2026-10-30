package store

import (
	"database/sql"
	"strings"
)

type SpendKind string

const (
	SpendTurn     SpendKind = "turn"
	SpendFacility SpendKind = "facility"
	SpendSubagent SpendKind = "subagent"
)

type SpendRow struct {
	TsMs                          int64
	Kind                          SpendKind
	Name                          string
	Provider, Model               string
	Calls                         int
	Known, Complete               bool
	Tokens, Input, Output, Cached int
}

func (s *Store) SpendSince(sinceMs int64) ([]SpendRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	columns := `source, provider, model, rounds, total_tokens, prompt_tokens, completion_tokens, cached_prompt_tokens, silent_calls, unknown_attempts`
	if s.historicalTurnMetrics {

		columns = `NULL, NULL, NULL, rounds, NULL, NULL, NULL, NULL, NULL, NULL`
	}
	rows, err := s.db.Query(`SELECT ts_ms, `+columns+` FROM turn_metrics WHERE ts_ms >= ? ORDER BY ts_ms`, sinceMs)
	if err != nil {
		return nil, err
	}
	var out []SpendRow
	for rows.Next() {
		var r SpendRow
		var source, provider, model sql.NullString
		var total, input, output, cached, silent, unknown sql.NullInt64
		if err := rows.Scan(&r.TsMs, &source, &provider, &model, &r.Calls, &total, &input, &output, &cached, &silent, &unknown); err != nil {
			rows.Close()
			return nil, err
		}
		r.Kind = SpendTurn
		if name, ok := strings.CutPrefix(source.String, facilitySourcePrefix); ok {
			r.Kind, r.Name = SpendFacility, name
		}
		r.Provider, r.Model = provider.String, model.String

		r.Known = total.Valid && silent.Valid && unknown.Valid && (total.Int64 > 0 || (silent.Int64 == 0 && unknown.Int64 == 0))
		if r.Known {
			r.Complete = r.Calls > 0 && silent.Int64 == 0 && unknown.Int64 == 0
			r.Tokens, r.Input, r.Output, r.Cached = int(total.Int64), int(input.Int64), int(output.Int64), int(cached.Int64)
		}
		out = append(out, r)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	children, err := s.db.Query(`SELECT ts_ms, role, model, tokens FROM subagent_metrics WHERE ts_ms >= ? ORDER BY ts_ms`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer children.Close()
	for children.Next() {
		r := SpendRow{Kind: SpendSubagent, Known: true}
		if err := children.Scan(&r.TsMs, &r.Name, &r.Model, &r.Tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, children.Err()
}
