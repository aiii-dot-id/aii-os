package store

import (
	"context"
	"fmt"
)

type BeliefFooting struct {
	Stands bool

	Suspect bool
	Core    bool
}

const beliefFootingRestsSQL = `SELECT b.id, b.ring FROM edges e JOIN beliefs b ON b.id = e.to_id
	WHERE e.from_id = ? AND e.archived = 0 AND e.edge_type IN ('SUPPORTS', 'REINFORCED_BY', 'DERIVED_FROM')
	AND b.archived = 0 AND b.superseded_by IS NULL`

func (s *Store) BeliefFootings(ctx context.Context, ids []string) (map[string]BeliefFooting, error) {
	var out map[string]BeliefFooting
	err := s.ReadWith(ctx, func(db Reader) error {
		var err error
		out, err = s.BeliefFootingsIn(ctx, db, ids)
		return err
	})
	return out, err
}

func (s *Store) BeliefFootingsIn(ctx context.Context, db Reader, ids []string) (map[string]BeliefFooting, error) {
	q := boundReader{ctx: ctx, s: db}
	stands, suspect := map[string]bool{}, map[string]bool{}
	standsOf := func(id string) (bool, error) {
		if v, ok := stands[id]; ok {
			return v, nil
		}
		r, err := s.standingReportRead(q, id, nil, false)
		if err != nil {
			return false, err
		}
		v := r.Standing == "confirmed" || r.Standing == "trusted"
		stands[id] = v
		suspect[id] = r.Standing == "suspect"
		return v, nil
	}
	out := make(map[string]BeliefFooting, len(ids))
	for _, id := range ids {
		var f BeliefFooting
		var err error
		if f.Stands, err = standsOf(id); err != nil {
			return nil, err
		}
		f.Suspect = suspect[id]
		rows, err := q.Query(beliefFootingRestsSQL, id)
		if err != nil {
			return nil, fmt.Errorf("footing of %s: what rests on it: %w", id, err)
		}
		type rest struct {
			id   string
			ring int
		}
		var rests []rest
		for rows.Next() {
			var r rest
			if err := rows.Scan(&r.id, &r.ring); err != nil {
				rows.Close()
				return nil, fmt.Errorf("footing of %s: what rests on it: %w", id, err)
			}
			rests = append(rests, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("footing of %s: what rests on it: %w", id, err)
		}
		for _, r := range rests {
			if r.ring == 2 {
				f.Core = true
				break
			}
			if r.ring == 3 && r.id != id {
				ok, err := standsOf(r.id)
				if err != nil {
					return nil, err
				}
				if ok {
					f.Core = true
					break
				}
			}
		}
		out[id] = f
	}
	return out, nil
}
