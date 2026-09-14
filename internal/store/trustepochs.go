package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

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
type TrustEpochPayload struct {
	Root          string `json:"root"`
	TrustEpoch    int64  `json:"trust_epoch"`
	PayloadSHA256 string `json:"payload_sha256"`
}

// .
// .
func (s *Store) materializeTrustEpochAccepted(evt *ledger.Event) error {
	var p TrustEpochPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse trust.epoch_accepted: %w", err)
	}
	if p.Root == "" || p.TrustEpoch < 1 || p.PayloadSHA256 == "" {
		return fmt.Errorf("trust.epoch_accepted payload missing root/trust_epoch/payload_sha256")
	}
	// .
	// .
	// .
	_, err := s.h().Exec(
		`INSERT INTO trust_epochs (root, trust_epoch, payload_sha256, accepted_at) VALUES (?, ?, ?, ?)`,
		p.Root, p.TrustEpoch, p.PayloadSHA256, evt.Timestamp,
	)
	return err
}

// .
// .
// .
// .
// .
// .
func (s *Store) TrustEpochHighWater(root string) (int64, string, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var epoch int64
	var sha string
	err := s.db.QueryRow(
		`SELECT trust_epoch, payload_sha256 FROM trust_epochs WHERE root = ? ORDER BY id DESC LIMIT 1`,
		root,
	).Scan(&epoch, &sha)
	if err == sql.ErrNoRows {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return epoch, sha, true, nil
}
