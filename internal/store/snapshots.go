package store

import (
	"database/sql"

	"time"
)

// --- Ring snapshots (durable facility output) ---

// RingSnapshot is one persisted ring section.
type RingSnapshot struct {
	RingLevel int
	Section   string
	Content   string
}

// SaveRingSection persists (or updates) one ring section snapshot.
// Best-effort durability for facility-authored content: the in-memory ring
// manager is the live view; this is what a restart restores from.
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

// RingSnapshots returns all persisted ring sections (for startup restore).
func (s *Store) RingSnapshots() ([]RingSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// The brief is NOT a ring section. It is stored in this table under
	// ring_level 0 as a single-slot sentinel (SaveBrief), which means
	// callers restoring ring content would install the morning brief as
	// Ring 0 — the constitution's level — unless every one of them
	// remembers to skip it. One caller does remember. The protection was
	// a single `if` in app.go, and a second consumer would not have known
	// to write it.
	//
	// Excluded HERE instead, so the function that returns ring sections
	// cannot return the thing that is not one. GetBrief remains the way
	// to read it.
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

// SaveBrief persists the morning brief (single-slot snapshot).
func (s *Store) SaveBrief(content string) error {
	return s.SaveRingSection(0, briefSection, content)
}

// GetBrief returns the persisted morning brief, or "".
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

// EntityExists reports whether id resolves to any known entity (belief,
// experience, intention, commitment, relationship). Edge endpoints must
// exist before edges mint — an evidence graph with ghost nodes certifies
// nothing (honesty review B1).

// briefSection is the single-slot key the morning brief occupies in the
// ring_snapshots table. It is named rather than repeated as a literal in
// three places, and RingSnapshots excludes it so no caller can restore
// the brief as ring content.
const briefSection = "__brief__"
