package store

import (
	"fmt"
	"strings"
)

// maintain.go — the one read the maintenance pass asks of the store.
//
// QuickCheck is a canary, not a repair: the database is f(ledger) and
// fully rebuildable, so the correct response to a failure here is
// delete-and-replay, decided by the operator. Nothing in this file
// writes.

// QuickCheck runs PRAGMA quick_check and reports anything that is not
// literally "ok". Bounded detail: five findings name the shape of the
// damage; five hundred would name nothing more.
func (s *Store) QuickCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query("PRAGMA quick_check")
	if err != nil {
		return fmt.Errorf("quick_check: %w", err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return fmt.Errorf("quick_check scan: %w", err)
		}
		if line != "ok" && len(problems) < 5 {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("quick_check rows: %w", err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("quick_check found damage: %s", strings.Join(problems, "; "))
	}
	return nil
}
