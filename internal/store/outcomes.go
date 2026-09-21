package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strconv"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

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
// .
// .

// .
const OutcomeCursorKey = "consolidate.outcomes_considered_through"

// .
// .
// .
const MaxOutcomesPerIntake = 8

// .
// .
type Outcome struct {
	Seq       uint64
	EntryHash string
	Kind      string
	ID        string
	State     string
	Said      string
	Was       string
}

// .
// .
type OutcomeBatch struct {
	From     uint64
	Through  uint64
	Outcomes []Outcome
}

// .
// .
// .
var ErrCursorMoved = errors.New("the cursor moved since this pass read it")

// .
func (s *Store) OutcomeCursor() (uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.outcomeCursorLocked()
}

func (s *Store) outcomeCursorLocked() (uint64, error) {
	v, err := s.getRuntimeMeta(OutcomeCursorKey)
	if err != nil {
		return 0, fmt.Errorf("outcome cursor: %w", err)
	}
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		// .
		return 0, nil
	}
	// .
	// .
	var head sql.NullInt64
	if err := s.h().QueryRow(`SELECT MAX(seq) FROM ledger`).Scan(&head); err != nil {
		return 0, fmt.Errorf("outcome cursor: %w", err)
	}
	if !head.Valid || n > uint64(head.Int64) {
		return 0, nil
	}
	return n, nil
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) NextOutcomes(window int) (OutcomeBatch, error) {
	if window <= 0 {
		return OutcomeBatch{}, fmt.Errorf("outcome window must be positive")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	from, err := s.outcomeCursorLocked()
	if err != nil {
		return OutcomeBatch{}, err
	}
	batch := OutcomeBatch{From: from, Through: from}
	self, err := s.ownFingerprintLocked()
	if err != nil {
		return OutcomeBatch{}, fmt.Errorf("outcomes: %w", err)
	}
	rows, err := s.h().Query(
		`SELECT seq, prev, ts, type, ring, content, payload FROM ledger WHERE seq > ? ORDER BY seq ASC LIMIT ?`, from, window)
	if err != nil {
		return OutcomeBatch{}, fmt.Errorf("outcomes: %w", err)
	}
	var found []Outcome
	for rows.Next() {
		var evt ledger.Event
		var typ string
		var ring sql.NullInt64
		var payload []byte
		if err := rows.Scan(&evt.Seq, &evt.Prev, &evt.Timestamp, &typ, &ring, &evt.Content, &payload); err != nil {
			rows.Close()
			return OutcomeBatch{}, fmt.Errorf("outcomes: %w", err)
		}
		batch.Through = evt.Seq
		if typ != string(ledger.EventIntentionStateChange) && typ != string(ledger.EventCommitmentStateChange) {
			continue
		}
		var p struct {
			ID          string `json:"id"`
			State       string `json:"state"`
			Outcome     string `json:"outcome"`
			Result      string `json:"result"`
			RepairState string `json:"repair_state"`
			Note        string `json:"note"`
		}
		if err := json.Unmarshal(payload, &p); err != nil || !ring.Valid {
			// .
			// .
			// .
			// .
			logsink.Warn("store.error", "outcome record %d (%s) cannot be read from the mirror (payload: %v, ring present: %v) — passed over, not observed",
				evt.Seq, typ, err, ring.Valid)
			continue
		}
		o := Outcome{Seq: evt.Seq, ID: p.ID, State: p.State}
		switch {
		case typ == string(ledger.EventIntentionStateChange) && (p.State == "completed" || p.State == "abandoned"):
			o.Kind, o.Said = "intention", p.Outcome
		case typ == string(ledger.EventCommitmentStateChange) && (p.State == "completed" || p.State == "abandoned" || p.State == "repaired"):
			// .
			// .
			o.Kind = "commitment"
			var said []string
			if p.Result != "" {
				said = append(said, p.Result)
			}
			if p.RepairState != "" {
				said = append(said, "repair: "+p.RepairState)
			}
			if p.Note != "" {
				said = append(said, p.Note)
			}
			o.Said = strings.Join(said, " — ")
		default:
			continue
		}
		evt.Type, evt.Ring = ledger.EventType(typ), int(ring.Int64)
		o.EntryHash = evt.EntryHash()
		found = append(found, o)
		if len(found) == MaxOutcomesPerIntake {
			// .
			// .
			break
		}
	}
	if err := rows.Close(); err != nil {
		return OutcomeBatch{}, fmt.Errorf("outcomes: %w", err)
	}
	if err := rows.Err(); err != nil {
		return OutcomeBatch{}, fmt.Errorf("outcomes: %w", err)
	}
	for _, o := range found {
		cited, err := s.outcomeAlreadyObservedLocked(self, o.Seq)
		if err != nil {
			return OutcomeBatch{}, err
		}
		if cited {
			continue
		}
		table, col := "intentions", "statement"
		if o.Kind == "commitment" {
			table, col = "commitments", "description"
		}
		if err := s.h().QueryRow(`SELECT `+col+` FROM `+table+` WHERE id = ?`, o.ID).Scan(&o.Was); err != nil && err != sql.ErrNoRows {
			return OutcomeBatch{}, fmt.Errorf("outcomes: %s %s: %w", o.Kind, o.ID, err)
		}
		batch.Outcomes = append(batch.Outcomes, o)
	}
	return batch, nil
}

// .
// .
// .
// .
// .
// .
func (s *Store) outcomeAlreadyObservedLocked(self string, seq uint64) (bool, error) {
	if self == "" {
		return false, nil
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var one int
	err := s.h().QueryRow(
		`SELECT 1 FROM ledger l, json_each(l.payload, '$.cites') c
		 WHERE l.seq > ? AND l.type = 'experience.create'
		   AND json_type(l.payload, '$.cites') = 'array'
		   AND CASE WHEN c.type = 'object' THEN json_extract(c.value, '$.identity') END = ?
		   AND CASE WHEN c.type = 'object' THEN json_extract(c.value, '$.seq') END = ?
		 LIMIT 1`, seq, self, seq).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("outcomes: already observed: %w", err)
	}
	return true, nil
}

// .
// .
// .
func (s *Store) PublishOutcomeCursor(from, through uint64) error {
	if through < from {
		return fmt.Errorf("outcome cursor: %d is behind %d — the cursor never goes back", through, from)
	}
	if through == from {
		return nil
	}
	// .
	// .
	s.mu.Lock()
	defer s.mu.Unlock()
	now, err := s.outcomeCursorLocked()
	if err != nil {
		return err
	}
	if now != from {
		return ErrCursorMoved
	}
	if err := s.setRuntimeMeta(OutcomeCursorKey, strconv.FormatUint(through, 10)); err != nil {
		return fmt.Errorf("outcome cursor: %w", err)
	}
	return nil
}

// .
// .
func (s *Store) OwnFingerprint() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ownFingerprintLocked()
}
