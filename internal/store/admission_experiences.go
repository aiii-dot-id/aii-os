package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func (s *Store) validateExperienceLocked(provenance string, sourceTurn uint64, sourceURL string) error {
	switch provenance {
	case "", "self", "dream", "system":

	case "operator":
		if sourceTurn == 0 {
			return fmt.Errorf("operator provenance requires source_turn — testimony cites its turn")
		}
	case "external":
		if sourceURL == "" {
			return fmt.Errorf("external provenance requires source_url — foreign text cites where it came from")
		}
	default:
		return fmt.Errorf("unknown provenance %q — sanctioned: self, dream, system, operator, external", provenance)
	}
	return nil
}

func (s *Store) runOutputsPrecedeLocked(evt *ledger.Event, outputs []uint64) error {
	for _, seq := range outputs {
		if seq >= evt.Seq {
			return fmt.Errorf("%s cites output seq %d at or after itself (seq %d) — outputs precede their marker", evt.Type, seq, evt.Seq)
		}
		var one int
		if err := s.h().QueryRow(`SELECT 1 FROM ledger WHERE seq = ?`, seq).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%s cites output seq %d — no such event: fabricated provenance fails closed", evt.Type, seq)
			}
			return fmt.Errorf("verify output seq %d: %w", seq, err)
		}
	}
	return nil
}
