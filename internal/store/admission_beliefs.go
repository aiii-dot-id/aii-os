package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
)

func normalizedBeliefStatement(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func (s *Store) validateActiveBeliefGroundingLocked(payload []byte) error {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	var target, edgeType string
	if err := s.h().QueryRow(`SELECT to_id, edge_type FROM edges WHERE id = ?`, p.ID).Scan(&target, &edgeType); err != nil {
		return err
	}
	if edgeType != "SUPPORTS" && edgeType != "DERIVED_FROM" && edgeType != "REINFORCED_BY" {
		return nil
	}
	var one int
	err := s.h().QueryRow(`SELECT 1 FROM beliefs WHERE id = ? AND archived = 0 AND superseded_by IS NULL`, target).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var remaining int
	if err := s.h().QueryRow(`SELECT COUNT(*) FROM edges e WHERE e.to_id = ? AND e.from_id != ? AND e.archived = 0
		AND e.edge_type IN ('SUPPORTS','DERIVED_FROM','REINFORCED_BY') AND
		((EXISTS (SELECT 1 FROM experiences x WHERE x.id=e.from_id) AND NOT EXISTS (SELECT 1 FROM beliefs x WHERE x.id=e.from_id))
		OR (EXISTS (SELECT 1 FROM beliefs x WHERE x.id=e.from_id) AND NOT EXISTS (SELECT 1 FROM experiences x WHERE x.id=e.from_id)))`, target, target).Scan(&remaining); err != nil {
		return err
	}
	if remaining == 0 {
		return fmt.Errorf("edge.archive would leave active belief %q without an evidence source; archive or supersede the belief first", target)
	}
	return nil
}

func (s *Store) validateBeliefAdmissionLocked(eventType ledger.EventType, payload []byte) error {
	if eventType != ledger.EventBeliefUpsert && eventType != ledger.EventWorkingStyleUpsert {
		return nil
	}
	var p struct {
		ID           string         `json:"id"`
		Statement    string         `json:"statement"`
		Content      string         `json:"content"`
		OriginRefs   []beliefOrigin `json:"origin_refs"`
		DistinctFrom []string       `json:"distinct_from"`
		Distinction  string         `json:"distinction"`
		DuplicateOK  bool           `json:"duplicate_ok"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("decode belief admission: %w", err)
	}
	if p.DuplicateOK {
		return fmt.Errorf("duplicate_ok cannot bypass belief admission")
	}

	statement := p.Statement
	if eventType == ledger.EventWorkingStyleUpsert || statement == "" {
		statement = p.Content
	}
	norm := normalizedBeliefStatement(statement)
	if norm == "" {
		if eventType == ledger.EventWorkingStyleUpsert {
			return fmt.Errorf("working_style.upsert requires content")
		}
		return fmt.Errorf("belief.upsert requires statement or content")
	}
	if eventType == ledger.EventWorkingStyleUpsert && strings.TrimSpace(p.Content) == "" {
		return fmt.Errorf("working_style.upsert requires content")
	}
	if len(p.OriginRefs) == 0 {
		return fmt.Errorf("%s requires at least one real source and its incoming evidence edge", eventType)
	}
	rows, err := s.h().Query(`SELECT id, statement FROM beliefs WHERE archived = 0 AND superseded_by IS NULL`)
	if err != nil {
		return fmt.Errorf("read active beliefs for duplicate admission: %w", err)
	}
	defer rows.Close()
	distinct := map[string]bool{}
	for _, id := range p.DistinctFrom {
		if id == "" || id == p.ID || distinct[id] {
			return fmt.Errorf("distinct_from must name each different active candidate once")
		}
		distinct[id] = true
	}
	activeIDs := map[string]bool{}
	like := trigram.Against(statement)
	for rows.Next() {
		var id, current string
		if err := rows.Scan(&id, &current); err != nil {
			return fmt.Errorf("read duplicate candidate: %w", err)
		}
		activeIDs[id] = true
		if id == p.ID {
			if normalizedBeliefStatement(current) != norm {
				return fmt.Errorf("belief %q cannot change its statement under the same id; mint a successor and supersede", id)
			}
			continue
		}
		if normalizedBeliefStatement(current) == norm {
			return fmt.Errorf("active belief %q already states this; add evidence to it instead of minting a duplicate", id)
		}
		if like(current) >= 0.75 {
			if !distinct[id] || strings.TrimSpace(p.Distinction) == "" {
				return fmt.Errorf("belief %q may already express this claim; cite it in distinct_from[] and explain the difference, or add evidence to it", id)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id := range distinct {
		if !activeIDs[id] {
			return fmt.Errorf("distinct_from %q does not name an active belief", id)
		}
	}
	return nil
}

type beliefOrigin struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

func (s *Store) validateRing2TextLocked(eventType ledger.EventType, payload []byte) error {
	if eventType != ledger.EventBeliefUpsert && eventType != ledger.EventWorkingStyleUpsert {
		return nil
	}
	var p struct {
		ID, Statement, Content string
	}
	if json.Unmarshal(payload, &p) != nil || p.ID == "" {
		return nil
	}
	text := p.Statement
	if eventType == ledger.EventWorkingStyleUpsert || text == "" {
		text = p.Content
	}
	var previous string
	var ring int
	err := s.h().QueryRow(`SELECT statement, ring FROM beliefs WHERE id = ?`, p.ID).Scan(&previous, &ring)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read belief %q before Ring 2 text admission: %w", p.ID, err)
	}
	if ring == 2 && text != "" && text != previous {
		return fmt.Errorf("%w: belief %q retains its words; mint a new belief and supersede this id", ErrRing2TextChange, p.ID)
	}
	return nil
}

func (s *Store) validateRetiredBeliefLocked(eventType ledger.EventType, payload []byte) error {
	switch eventType {
	case ledger.EventBeliefUpsert, ledger.EventWorkingStyleUpsert, ledger.EventBeliefPromote:
	default:
		return nil
	}
	var p struct{ ID string }
	if json.Unmarshal(payload, &p) != nil || p.ID == "" {
		return nil
	}
	var archived int
	var successor sql.NullString
	err := s.h().QueryRow(`SELECT archived, superseded_by FROM beliefs WHERE id = ?`, p.ID).Scan(&archived, &successor)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read belief %q before retired-belief admission: %w", p.ID, err)
	}
	if archived == 0 && !successor.Valid {
		return nil
	}
	how := "archived"
	if successor.Valid {
		how = fmt.Sprintf("superseded by %q", successor.String)
	}
	if eventType == ledger.EventBeliefPromote {
		return fmt.Errorf("%w: belief.promote names %q, which is %s; only an active belief is promoted", ErrBeliefRetired, p.ID, how)
	}
	return fmt.Errorf("%w: belief %q is %s; state the claim as a new belief, under a new id", ErrBeliefRetired, p.ID, how)
}

func (s *Store) validateSupersedeSuccessorLocked(eventType ledger.EventType, payload []byte) error {
	if eventType != ledger.EventBeliefSupersede {
		return nil
	}
	var p struct {
		OldID string `json:"old_id"`
		NewID string `json:"new_id"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return nil
	}
	if p.NewID == p.OldID {
		return fmt.Errorf("belief.supersede names %q as its own successor: a belief is superseded by another belief, or archived", p.NewID)
	}
	var one int
	var successor sql.NullString
	err := s.h().QueryRow(`SELECT 1, superseded_by FROM beliefs WHERE id = ? AND archived = 0`, p.NewID).Scan(&one, &successor)
	if errors.Is(err, sql.ErrNoRows) {
		return &MissingEventTargetError{Event: eventType, Kind: "belief", ID: p.NewID}
	}
	if err != nil {
		return fmt.Errorf("read successor belief %q: %w", p.NewID, err)
	}

	if successor.Valid {
		return fmt.Errorf("%w: belief.supersede names %q as the successor, which is superseded by %q; a belief is superseded by an active belief", ErrBeliefRetired, p.NewID, successor.String)
	}
	return nil
}
