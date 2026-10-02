package store

import (
	"encoding/json"
	"fmt"
	"time"
)

const memoryDecisionsKeep = 5000

func (s *Store) RecordMemoryDecision(d MemoryDecision) error {
	if d.Kind != "salience" && d.Kind != "rhythm" {
		return fmt.Errorf("memory decision: kind %q is not salience or rhythm", d.Kind)
	}
	if d.Facility == "" || d.Decision == "" {
		return fmt.Errorf("memory decision: facility and decision are required")
	}
	record := d.Record
	if record == nil {
		record = map[string]interface{}{}
	}
	b, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("memory decision: record: %w", err)
	}
	at := d.DecidedAt
	if at.IsZero() {
		at = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w().Exec(`INSERT INTO memory_decisions (decided_at, kind, facility, decision, seq, score, record) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		at.UTC().Format(time.RFC3339Nano), d.Kind, d.Facility, d.Decision, d.Seq, d.Score, string(b)); err != nil {
		return err
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_decisions WHERE kind = ?`, d.Kind).Scan(&n); err != nil {
		return err
	}
	if n > memoryDecisionsKeep {
		_, err = s.w().Exec(`DELETE FROM memory_decisions WHERE kind = ? AND id IN (SELECT id FROM memory_decisions WHERE kind = ? ORDER BY id ASC LIMIT ?)`, d.Kind, d.Kind, n-memoryDecisionsKeep)
	}
	return err
}
