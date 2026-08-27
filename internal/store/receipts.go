package store

import (
	"database/sql"
)

// --- Witness receipts (f(ledger) projection of system.witnessed events) ---
// The runtime never writes this table: receipts enter through
// materializeSystemWitnessed when the anchorer mints the signed event.

// LastWitnessReceipt returns the most recent receipt's anchored seq and
// raw JSON, or (0, nil, nil) when none exist. Startup restores anchoring
// state from this so the unanchored count is honest across restarts.
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
