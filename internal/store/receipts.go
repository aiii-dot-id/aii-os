package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func (s *Store) LastWitnessReceipt() (int64, []byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var seq int64
	var js string
	err := s.db.QueryRow(`SELECT anchored_seq, receipt_json FROM witness_receipts ORDER BY id DESC LIMIT 1`).Scan(&seq, &js)
	if err == sql.ErrNoRows {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	return seq, []byte(js), nil
}

func (s *Store) WitnessEnrollment() (ids []string, born, firstWitnessed time.Time, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ts string
	err = s.db.QueryRow(`SELECT ts FROM ledger WHERE type = ? ORDER BY seq LIMIT 1`, string(ledger.EventRing0Genesis)).Scan(&ts)
	switch {
	case err == sql.ErrNoRows:
		return nil, time.Time{}, time.Time{}, nil
	case err != nil:
		return nil, time.Time{}, time.Time{}, fmt.Errorf("read the genesis record's time: %w", err)
	}
	if born, err = time.Parse(time.RFC3339Nano, ts); err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("the genesis record's time %q: %w", ts, err)
	}
	rows, err := s.db.Query(`SELECT ts, COALESCE(json_extract(payload, '$.receipt.identity_id'), json_extract(payload, '$.receipt_before_rewrap.identity_id'), '')
		FROM ledger WHERE type = ? ORDER BY seq`, string(ledger.EventSystemWitnessed))
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("read the record's witness receipts: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var at, id string
		if err := rows.Scan(&at, &id); err != nil {
			return nil, time.Time{}, time.Time{}, err
		}
		if firstWitnessed.IsZero() {
			if firstWitnessed, err = time.Parse(time.RFC3339Nano, at); err != nil {
				return nil, time.Time{}, time.Time{}, fmt.Errorf("a system.witnessed record's time %q: %w", at, err)
			}
		}
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	return ids, born, firstWitnessed, nil
}
