package store

import (
	"context"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func (s *Store) OperatorActResults(since uint64, turns []string) ([]interaction.Record, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.mu.RLock()
	defer s.mu.RUnlock()
	where := "c.turn_seq >= ?"
	args := []any{since}
	if len(turns) > 0 {
		where += " OR c.turn_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(turns)), ",") + ")"
		for _, turn := range turns {
			args = append(args, turn)
		}
	}
	args = append(args, interaction.MaxPageRows+1)
	rows, err := s.db.QueryContext(ctx, `SELECT `+interactionSelect+` FROM `+s.interactionTable()+` c
		WHERE c.kind='tool_result' AND c.source_kind='tool' AND json_extract(c.details,'$.actor')='operator'
		AND (`+where+`) ORDER BY c.turn_seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []interaction.Record
	for rows.Next() {
		r, err := scanInteraction(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(out) > interaction.MaxPageRows
	if more {
		out = out[:interaction.MaxPageRows]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, more, nil
}
