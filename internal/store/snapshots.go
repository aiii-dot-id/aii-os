package store

import (
	"database/sql"

	"time"
)

// .

// .
type RingSnapshot struct {
	RingLevel int
	Section   string
	Content   string
}

// .
// .
// .
func (s *Store) SaveRingSection(level int, name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO ring_snapshots (ring_level, section, content, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(ring_level, section) DO UPDATE SET
		   content = excluded.content,
		   updated_at = excluded.updated_at`,
		level, name, content, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// .
func (s *Store) RingSnapshots() ([]RingSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
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
	rows, err := s.db.Query(
		`SELECT ring_level, section, content FROM ring_snapshots WHERE section <> ?`, briefSection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RingSnapshot
	for rows.Next() {
		var rs RingSnapshot
		if err := rows.Scan(&rs.RingLevel, &rs.Section, &rs.Content); err != nil {
			return nil, err
		}
		out = append(out, rs)
	}
	return out, rows.Err()
}

// .
func (s *Store) SaveBrief(content string) error {
	return s.SaveRingSection(0, briefSection, content)
}

// .
func (s *Store) GetBrief() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var content string
	err := s.db.QueryRow(`SELECT content FROM ring_snapshots WHERE section = ?`, briefSection).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return content, err
}

// .
// .
// .
// .

// .
// .
// .
// .
const briefSection = "__brief__"
