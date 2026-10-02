package store

import (
	"fmt"
	"strings"
)

func (s *Store) TurnToolCalls(turnIDs []string) (map[string][]ToolCall, error) {
	out := map[string][]ToolCall{}
	var args []interface{}
	for _, id := range turnIDs {
		if _, asked := out[id]; asked || id == "" {
			continue
		}
		out[id] = []ToolCall{}
		args = append(args, id)
	}
	if len(args) == 0 {
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.h().Query(
		`SELECT c.turn_id, c.source_id, c.kind, c.outcome, COALESCE(json_extract(c.details, '$.tool'), '')
		 FROM conversations c
		 WHERE c.source_kind = 'tool' AND c.kind IN ('tool_call', 'tool_result')
		   AND c.turn_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)
		 ORDER BY c.turn_seq ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("turn tool calls: %w", err)
	}
	defer rows.Close()

	at := map[string]int{}
	for rows.Next() {
		var turn, execution, kind, outcome, tool string
		if err := rows.Scan(&turn, &execution, &kind, &outcome, &tool); err != nil {
			return nil, fmt.Errorf("turn tool calls: %w", err)
		}
		key := turn + "\x00" + execution
		i, placed := at[key]
		if !placed {
			out[turn] = append(out[turn], ToolCall{Tool: tool})
			i = len(out[turn]) - 1
			at[key] = i
		}
		if kind == "tool_result" {
			out[turn][i].Outcome = outcome
			if out[turn][i].Tool == "" {
				out[turn][i].Tool = tool
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("turn tool calls: %w", err)
	}
	return out, nil
}
