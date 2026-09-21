package store

import (
	"encoding/json"
	"fmt"
)

// .
// .
const standingOfferKey = "tools.standing_offer"

// .
// .
type StandingSeat struct {
	Name  string `json:"name"`
	Print string `json:"print,omitempty"`
}

// .
// .
// .
// .
func (s *Store) StandingOffer() ([]StandingSeat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, err := s.getRuntimeMeta(standingOfferKey)
	if err != nil || v == "" {
		return nil, err
	}
	var seats []StandingSeat
	if err := json.Unmarshal([]byte(v), &seats); err == nil {
		return seats, nil
	}
	// .
	// .
	var names []string
	if err := json.Unmarshal([]byte(v), &names); err != nil {
		return nil, fmt.Errorf("standing offer: %w", err)
	}
	legacy := make([]StandingSeat, 0, len(names))
	for _, n := range names {
		legacy = append(legacy, StandingSeat{Name: n})
	}
	return legacy, nil
}

// .
// .
func (s *Store) SetStandingOffer(seats []StandingSeat) error {
	if seats == nil {
		seats = []StandingSeat{}
	}
	b, err := json.Marshal(seats)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setRuntimeMeta(standingOfferKey, string(b))
}
