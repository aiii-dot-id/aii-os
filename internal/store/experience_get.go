package store

import (
	"errors"
	"fmt"
)

var ErrExperienceNotFound = errors.New("experience not found")

func (s *Store) GetExperience(id string) (*Experience, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT `+s.experienceColumns()+`
		 FROM experiences WHERE id = ?`, id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	got, err := scanExperiences(rows)
	if err != nil {
		return nil, err
	}
	if len(got) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrExperienceNotFound, id)
	}
	return &got[0], nil
}
