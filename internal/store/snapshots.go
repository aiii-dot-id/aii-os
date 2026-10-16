package store

import (
	"database/sql"
	"errors"

	"time"
)

type RingSnapshot struct {
	RingLevel int
	Section   string
	Content   string

	UpdatedAt string
}

func (s *Store) SaveRingSection(level int, name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w().Exec(
		`INSERT INTO ring_snapshots (ring_level, section, content, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(ring_level, section) DO UPDATE SET
		   content = excluded.content,
		   updated_at = excluded.updated_at`,
		level, name, content, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) RingSnapshots() ([]RingSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT ring_level, section, content, updated_at FROM ring_snapshots WHERE section <> ?`, briefSection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RingSnapshot
	for rows.Next() {
		var rs RingSnapshot
		if err := rows.Scan(&rs.RingLevel, &rs.Section, &rs.Content, &rs.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, rs)
	}
	return out, rows.Err()
}

func (s *Store) SaveBrief(content string) error {
	return s.SaveRingSection(0, briefSection, content)
}

func (s *Store) GetBrief() (string, error) {
	content, _, err := s.Brief()
	return content, err
}

func (s *Store) Brief() (content string, written time.Time, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var at string
	err = s.db.QueryRow(`SELECT content, updated_at FROM ring_snapshots WHERE section = ?`, briefSection).Scan(&content, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	written, _ = time.Parse(time.RFC3339Nano, at)
	return content, written, nil
}

const briefSection = "__brief__"
