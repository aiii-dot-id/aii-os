package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"

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
type PublicNamePayload struct {
	NameID string `json:"name_id"`
	Name   string `json:"name"`
	Zone   string `json:"zone"`
}

// .
type PublicName struct {
	NameID     string
	Name       string
	Zone       string
	ClaimedSeq uint64
	ClaimedAt  string
}

var nameIDRe = regexp.MustCompile(`^[a-z2-7]{26}$`)

// .
// .
// .
func ValidatePublicNamePayload(p PublicNamePayload) error {
	if !nameIDRe.MatchString(p.NameID) {
		return fmt.Errorf("name_id %q is not 26 base32 characters", p.NameID)
	}
	if p.Zone == "" || p.Zone[0] == '.' || p.Zone[len(p.Zone)-1] == '.' {
		return fmt.Errorf("zone %q is not a bare domain", p.Zone)
	}
	if flat, ui := p.NameID+"."+p.Zone, "ui."+p.NameID+"."+p.Zone; p.Name != flat && p.Name != ui {
		return fmt.Errorf("name %q is not %q or %q", p.Name, flat, ui)
	}
	return nil
}

// .
// .
// .
// .
// .
func (s *Store) materializePublicName(evt *ledger.Event) error {
	var p PublicNamePayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse network.name_claimed: %w", err)
	}
	if err := ValidatePublicNamePayload(p); err != nil {
		return fmt.Errorf("network.name_claimed: %w", err)
	}
	res, err := s.h().Exec(
		`INSERT INTO public_name (singleton_id, name_id, name, zone, claimed_seq, claimed_at) VALUES ('current', ?, ?, ?, ?, ?)
		 ON CONFLICT(singleton_id) DO UPDATE SET name_id = excluded.name_id, name = excluded.name, zone = excluded.zone,
		 claimed_seq = excluded.claimed_seq, claimed_at = excluded.claimed_at
		 WHERE public_name.zone != excluded.zone`,
		p.NameID, p.Name, p.Zone, evt.Seq, evt.Timestamp,
	)
	if err != nil {
		return fmt.Errorf("network.name_claimed: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("network.name_claimed: a public name is already claimed under %s; a second claim is admitted only under another zone", p.Zone)
	}
	return nil
}

// .
func (s *Store) PublicName() (PublicName, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n PublicName
	err := s.db.QueryRow(`SELECT name_id, name, zone, claimed_seq, claimed_at FROM public_name WHERE singleton_id = 'current'`).
		Scan(&n.NameID, &n.Name, &n.Zone, &n.ClaimedSeq, &n.ClaimedAt)
	if err == sql.ErrNoRows {
		return PublicName{}, false, nil
	}
	if err != nil {
		return PublicName{}, false, err
	}
	return n, true, nil
}
