package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// --- Trust-epoch acceptances (f(ledger) projection of
// trust.epoch_accepted events — PLUGIN_REVOCATION_DESIGN §2.3) ---
// The runtime never writes this table directly: acceptances enter
// through materializeTrustEpochAccepted when the epoch-guard adapter
// mints the signed event, exactly the witness-receipt pattern.

// TrustEpochPayload is the closed trust.epoch_accepted event payload:
// which root accepted which snapshot epoch, and the snapshot's own
// canonical payload digest — recorded so an equal-epoch snapshot with
// different content is detectable as a fork, not interchangeable.
type TrustEpochPayload struct {
	Root          string `json:"root"`
	TrustEpoch    int64  `json:"trust_epoch"`
	PayloadSHA256 string `json:"payload_sha256"`
}

// materializeTrustEpochAccepted lands one acceptance into the
// trust_epochs projection. Caller holds s.mu.
func (s *Store) materializeTrustEpochAccepted(evt *ledger.Event) error {
	var p TrustEpochPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse trust.epoch_accepted: %w", err)
	}
	if p.Root == "" || p.TrustEpoch < 1 || p.PayloadSHA256 == "" {
		return fmt.Errorf("trust.epoch_accepted payload missing root/trust_epoch/payload_sha256")
	}
	// s.h(), not s.db: materializer writes ride whatever transaction is
	// open (live per-event pair, replay's all-or-nothing rebuild, the
	// rollback-only preflight) — see db.go h().
	_, err := s.h().Exec(
		`INSERT INTO trust_epochs (root, trust_epoch, payload_sha256, accepted_at) VALUES (?, ?, ?, ?)`,
		p.Root, p.TrustEpoch, p.PayloadSHA256, evt.Timestamp,
	)
	return err
}

// TrustEpochHighWater returns the most recently accepted snapshot epoch
// and payload digest for root, or ok=false when no acceptance was ever
// ledgered. The newest row is the high-water mark by construction:
// acceptance only ever happens at or above it (packagefmt.EpochGuard),
// so rows are monotonic per root — the same startup-restore shape as
// LastWitnessReceipt.
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
