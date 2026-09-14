package store

import (
	"database/sql"
	"strings"
	"time"
)

// .
// .
// .
// .
// .
// .
type CuriosityCue struct {
	Subject string
	Why     string
	Pointer string
	Kind    string
	SetAt   string
}

// .
// .
func (s *Store) SetCuriosityCue(subject, why, pointer, kind string) error {
	if strings.TrimSpace(kind) == "" {
		kind = "invitation"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO curiosity_cue (singleton_id, subject, why, pointer, kind, set_at)
		 VALUES ('current', ?, ?, ?, ?, ?)
		 ON CONFLICT(singleton_id) DO UPDATE SET
		   subject=excluded.subject, why=excluded.why, pointer=excluded.pointer,
		   kind=excluded.kind, set_at=excluded.set_at`,
		subject, why, pointer, kind, time.Now().UTC().Format(time.RFC3339))
	return err
}

// .
// .
func (s *Store) ClearCuriosityCue() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM curiosity_cue WHERE singleton_id='current'`)
	return err
}

// .
// .
func (s *Store) CuriosityCue() (*CuriosityCue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var c CuriosityCue
	err := s.db.QueryRow(
		`SELECT subject, COALESCE(why,''), COALESCE(pointer,''), COALESCE(kind,'invitation'), set_at
		 FROM curiosity_cue WHERE singleton_id='current'`,
	).Scan(&c.Subject, &c.Why, &c.Pointer, &c.Kind, &c.SetAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// .
// .
// .
func RenderCuriosityCue(c CuriosityCue) string {
	if strings.TrimSpace(c.Subject) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Curiosity — an invitation, not a task")
	b.WriteString("\n" + c.Subject)
	if strings.TrimSpace(c.Why) != "" {
		b.WriteString("\nwhy it caught you: " + c.Why)
	}
	if strings.TrimSpace(c.Pointer) != "" {
		b.WriteString("\nwhere you left it: " + c.Pointer)
	}
	b.WriteString("\nA thread you chose — here if you want to pull it, owed to no one, and it closes nothing.")
	return b.String()
}
