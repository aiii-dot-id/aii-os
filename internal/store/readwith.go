package store

import "database/sql"

// .
// .
// .
// .
// .
// .
// .
func (s *Store) ReadWith(fn func(db *sql.DB) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(s.db)
}
