package store

import (
	"encoding/json"
	"fmt"
)

const standingOfferKey = "tools.standing_offer"

type StandingSeat struct {
	Name  string `json:"name"`
	Print string `json:"print,omitempty"`
}

func (s *Store) StandingOffer() ([]StandingSeat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, err := s.getRuntimeMeta(standingOfferKey)
	if err != nil || v == "" {
		return nil, err
	}
	var seats []StandingSeat
	if err := json.Unmarshal([]byte(v), &seats); err != nil {
		return nil, fmt.Errorf("standing offer: %w", err)
	}
	return seats, nil
}

func (s *Store) upgradeStandingOffer() error {
	v, err := s.getRuntimeMeta(standingOfferKey)
	if err != nil || v == "" {
		return err
	}
	var current []StandingSeat
	if json.Unmarshal([]byte(v), &current) == nil {
		return nil
	}
	var names []string
	if json.Unmarshal([]byte(v), &names) != nil {
		return nil
	}
	seats := make([]StandingSeat, 0, len(names))
	for _, n := range names {
		seats = append(seats, StandingSeat{Name: n})
	}
	raw, err := json.Marshal(seats)
	if err != nil {
		return err
	}
	if _, err := s.h().Exec(`UPDATE runtime_meta SET value=? WHERE key=?`, string(raw), standingOfferKey); err != nil {
		return err
	}
	if got, err := s.getRuntimeMeta(standingOfferKey); err != nil {
		return err
	} else if got != string(raw) {
		return fmt.Errorf("converted standing offer did not read back exactly")
	}
	var count int64
	if err := s.h().QueryRow(`SELECT COUNT(*) FROM runtime_meta`).Scan(&count); err != nil {
		return err
	}
	if s.schemaConversions == nil {
		s.schemaConversions = map[string]RuntimeConversion{}
	}
	change, exists := s.schemaConversions["runtime_meta"]
	if !exists {
		change = RuntimeConversion{Before: count, After: count}
	} else {
		change.Reason += "; "
	}
	change.Reason += "standing offer names upgraded to seats without fingerprints"
	s.schemaConversions["runtime_meta"] = change
	return nil
}

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
