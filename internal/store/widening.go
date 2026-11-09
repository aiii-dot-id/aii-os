package store

import (
	"encoding/json"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

type WideningCompletion struct {
	ID, Description, Result string
	Seq                     uint64
	At                      string

	Audited bool
}

func (s *Store) LastWideningSeq(counterpart string) (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var seq uint64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM ledger WHERE type = ? AND json_extract(payload,'$.id') = ?
		AND COALESCE(json_extract(payload,'$.approval_digest'),'') != ''`, string(ledger.EventRelationshipUpsert), counterpart).Scan(&seq)
	return seq, err
}

func (s *Store) WideningCounts(counterpart string, since uint64) (completed []WideningCompletion, abandonedUnrepaired int, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT id, description FROM commitments WHERE counterpart_id = ?`, counterpart)
	if err != nil {
		return nil, 0, err
	}
	theirs := map[string]string{}
	for rows.Next() {
		var id, description string
		if err := rows.Scan(&id, &description); err != nil {
			rows.Close()
			return nil, 0, err
		}
		theirs[id] = description
	}
	rows.Close()
	if len(theirs) == 0 {
		return nil, 0, nil
	}
	changes, err := s.db.Query(`SELECT seq, ts, payload FROM ledger WHERE type = ? AND seq > ? ORDER BY seq`, string(ledger.EventCommitmentStateChange), since)
	if err != nil {
		return nil, 0, err
	}
	defer changes.Close()
	abandoned := map[string]bool{}
	for changes.Next() {
		var seq uint64
		var ts, raw string
		if err := changes.Scan(&seq, &ts, &raw); err != nil {
			return nil, 0, err
		}
		var p struct {
			ID     string `json:"id"`
			State  string `json:"state"`
			Result string `json:"result"`
		}
		if json.Unmarshal([]byte(raw), &p) != nil {
			continue
		}
		description, ok := theirs[p.ID]
		if !ok {
			continue
		}
		switch p.State {
		case "completed":
			audited, err := s.completionAudited(p.ID)
			if err != nil {
				return nil, 0, err
			}
			completed = append(completed, WideningCompletion{ID: p.ID, Description: description, Result: p.Result, Seq: seq, At: ts, Audited: audited})
			delete(abandoned, p.ID)
		case "abandoned":
			abandoned[p.ID] = true
		case "repaired":
			delete(abandoned, p.ID)
		}
	}
	return completed, len(abandoned), changes.Err()
}
