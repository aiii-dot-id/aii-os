package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
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
const ContinuityStatusKey = "continuity.status"

// .
const (
	ContinuityOK       = "ok"
	ContinuityFailed   = "failed"
	ContinuityDisabled = "disabled"
	ContinuitySafe     = "safe"
)

// .
// .
// .
// .
const (
	UnencryptedNoKey           = "no_snapshot_key"
	UnencryptedKeyUnusable     = "snapshot_key_unusable"
	UnencryptedNoEscrow        = "no_escrow_checked"
	UnencryptedReceiptUnread   = "escrow_receipt_unreadable"
	UnencryptedReceiptMismatch = "escrow_receipt_mismatch"
	UnencryptedIdentityClosed  = "identity_key_not_open"
)

// .
type ContinuityStatus struct {
	At      string `json:"at"`
	Outcome string `json:"outcome"`
	// .
	// .
	// .
	Detail string `json:"detail,omitempty"`
	// .
	// .
	Snapshot string `json:"snapshot,omitempty"`
	Record   uint64 `json:"record,omitempty"`
	OnDemand bool   `json:"on_demand,omitempty"`
	// .
	// .
	Encrypted   bool   `json:"encrypted,omitempty"`
	Unencrypted string `json:"unencrypted,omitempty"`
	// .
	// .
	// .
	FailingSince string `json:"failing_since,omitempty"`
}

// .
// .
// .
// .
func (s *Store) SetContinuityStatus(st ContinuityStatus) error {
	if st.At == "" {
		st.At = time.Now().UTC().Format(time.RFC3339)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st.FailingSince = ""
	if st.Outcome == ContinuityFailed {
		st.FailingSince = st.At
		if prev, ok, err := s.continuityStatusLocked(); err == nil && ok && prev.Outcome == ContinuityFailed && prev.FailingSince != "" {
			st.FailingSince = prev.FailingSince
		}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.setRuntimeMeta(ContinuityStatusKey, string(raw))
}

// .
// .
// .
func (s *Store) ContinuityStatus() (ContinuityStatus, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.continuityStatusLocked()
}

func (s *Store) continuityStatusLocked() (ContinuityStatus, bool, error) {
	raw, err := s.getRuntimeMeta(ContinuityStatusKey)
	if err != nil || raw == "" {
		return ContinuityStatus{}, false, err
	}
	return ParseContinuityStatus(raw)
}

// .
// .
func ParseContinuityStatus(raw string) (ContinuityStatus, bool, error) {
	var st ContinuityStatus
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return ContinuityStatus{}, false, fmt.Errorf("continuity status does not read: %w", err)
	}
	return st, true, nil
}

// .
func ReadContinuityStatus(db interface {
	QueryRow(query string, args ...any) *sql.Row
}) (ContinuityStatus, bool, error) {
	var raw string
	err := db.QueryRow(`SELECT value FROM runtime_meta WHERE key = ?`, ContinuityStatusKey).Scan(&raw)
	if err == sql.ErrNoRows || (err == nil && raw == "") {
		return ContinuityStatus{}, false, nil
	}
	if err != nil {
		return ContinuityStatus{}, false, err
	}
	return ParseContinuityStatus(raw)
}

// .
// .
// .
// .
func UnencryptedForIdentity(reason string) string {
	switch reason {
	case UnencryptedNoKey:
		return "you have no snapshot key yet; one is made at the next normal boot"
	case UnencryptedKeyUnusable:
		return "the snapshot key on this host does not read as a key; your operator is told"
	case UnencryptedNoEscrow:
		return "no escrow of your keys has been checked, and a snapshot encrypted to a key that exists only on this machine would be lost with it. Checking one is your operator's act — it needs a passphrase only they hold"
	case UnencryptedReceiptUnread:
		return "the record of the escrow check does not read; your operator checks the escrow again"
	case UnencryptedReceiptMismatch:
		return "the snapshot key on this host is not the one whose escrow was checked; your operator makes and checks a new escrow"
	case UnencryptedIdentityClosed:
		return "your signing key is not open in this boot, so the escrow's check cannot be verified"
	}
	return "the reason was not recorded"
}
