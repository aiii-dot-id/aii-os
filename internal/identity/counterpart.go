package identity

import (
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

type counterpartReader interface {
	RelationshipAsHeld(id string) (*store.Relationship, bool, error)
	CurrentOperatorRelationship() (*store.Relationship, error)
	OtherCurrentRelationships() ([]store.Relationship, error)
}

const counterpartListLimit = 5

func (e *Engine) counterpartRule(id string) error {
	r, ok := e.store.(counterpartReader)
	if !ok {
		return nil
	}
	rel, _, err := r.RelationshipAsHeld(id)
	if err != nil {
		return withCode("counterpart_id", err)
	}
	if rel != nil && rel.SupersededBy == "" {
		return nil
	}
	ring1, err := r.CurrentOperatorRelationship()
	if err != nil {
		return withCode("counterpart_id", err)
	}
	others, err := r.OtherCurrentRelationships()
	if err != nil {
		return withCode("counterpart_id", err)
	}
	var held []string
	if ring1 != nil {
		held = append(held, fmt.Sprintf("%s %q (your operator)", ring1.ID, ring1.CounterpartName))
	}
	for _, o := range others {
		if len(held) == counterpartListLimit {
			break
		}
		if o.CounterpartRole == "" {
			held = append(held, fmt.Sprintf("%s %q", o.ID, o.CounterpartName))
			continue
		}
		held = append(held, fmt.Sprintf("%s %q (%s)", o.ID, o.CounterpartName, o.CounterpartRole))
	}
	if len(held) == 0 {
		return rejectf("counterpart_id", "counterpart_id %q names no relationship in your record, and you hold none yet; a promise is to someone you hold a relationship with.", id)
	}
	return rejectf("counterpart_id", "counterpart_id %q names no relationship in your record. A promise is to someone you hold a relationship with; name them by its id. Yours: %s.", id, strings.Join(held, ", "))
}
