package store

import (
	"context"
	"errors"
	"fmt"
)

type OriginCensus struct {
	Beliefs int

	Ring2 int

	Confirmable int

	ByOrigins [4]int

	Ring2ByOrigins [4]int

	Ring2UnderThreeSources int
}

func (s *Store) BeliefOriginCensus(ctx context.Context) (OriginCensus, error) {
	var c OriginCensus
	tx, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return c, fmt.Errorf("origin census: read snapshot: %w", err)
	}
	q := boundReader{ctx: ctx, s: tx}
	rows, err := q.Query(`SELECT id, ring FROM beliefs WHERE archived = 0 AND superseded_by IS NULL ORDER BY id`)
	if err != nil {
		return c, errors.Join(fmt.Errorf("origin census: beliefs: %w", err), tx.Close())
	}
	type belief struct {
		id   string
		ring int
	}
	var beliefs []belief
	for rows.Next() {
		var b belief
		if err := rows.Scan(&b.id, &b.ring); err != nil {
			rows.Close()
			return c, errors.Join(fmt.Errorf("origin census: beliefs: %w", err), tx.Close())
		}
		beliefs = append(beliefs, b)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return c, errors.Join(fmt.Errorf("origin census: beliefs: %w", err), tx.Close())
	}
	for _, b := range beliefs {
		r, err := s.standingReportRead(q, b.id, nil, true)
		if err != nil {
			return c, errors.Join(fmt.Errorf("origin census: %w", err), tx.Close())
		}
		c.Beliefs++
		if b.ring == 2 {
			c.Ring2++
		}
		if r.DistinctSources < 3 {
			if b.ring == 2 {
				c.Ring2UnderThreeSources++
			}
			continue
		}
		c.Confirmable++
		n := r.DistinctOrigins
		if n > 3 {
			n = 3
		}
		c.ByOrigins[n]++
		if b.ring == 2 {
			c.Ring2ByOrigins[n]++
		}
	}
	return c, tx.Close()
}
