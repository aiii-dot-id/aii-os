package store

// Standing — the read-time derivation of a belief's epistemic standing
// from the live evidence graph (2026-08-17 ruling: delete the lifecycle,
// derive the standing).
//
// The edge graph IS the evidence record. A stored, event-minted status
// was a shadow copy of a derivation — duplicated state, lifecycle
// machinery to keep it consistent, and (post "the gate is evidence"
// ruling) nothing was ever allowed to act on it anyway.
//
// Derivation:
//   suspect   — an ACTIVE CONTRADICTS edge points at the belief
//   confirmed — ≥3 distinct from-entities on active SUPPORTS /
//               REINFORCED_BY / DERIVED_FROM edges, spanning ≥2
//               authorship classes (self/dream/work/system are one
//               equivalence class — the resident's own substrate;
//               confirmation requires an independent voice)
//   trusted   — confirmed AND confirmed_at_ticks > 0 AND
//               lifetime_ticks - confirmed_at_ticks ≥ 50
//   new       — otherwise
//
// confirmed_at_ticks is store-only bookkeeping (the anchor the 'trusted'
// elapsed-time rule needs; not derivable from the edge set once edges
// archive). CONSOLIDATE stamps it when it observes the crossing. Lose the
// DB, lose the anchor: trusted re-derives to confirmed and re-earns its
// time — honest for bookkeeping.

// StandingFor derives the belief's standing from live evidence. Every
// query helper takes its own RLock (Go RWMutex is NOT reentrant — a held
// RLock calling another RLock deadlocks when a writer queues between
// them), so this composes unlocked stateless helpers instead.
func (s *Store) StandingFor(id string) string {
	edges, err := s.ListEdgesForBelief(id)
	if err != nil {
		return "new" // fail-soft: standing is presentation, not truth
	}

	var confirmedAt int64
	s.mu.RLock()
	_ = s.db.QueryRow(`SELECT confirmed_at_ticks FROM beliefs WHERE id = ?`, id).Scan(&confirmedAt)
	s.mu.RUnlock()

	hasContradiction := false
	sources := map[string]bool{}
	var fromIDs []string
	for _, e := range edges {
		if e.EdgeType == "CONTRADICTS" {
			hasContradiction = true
			continue
		}
		if e.EdgeType == "SUPPORTS" || e.EdgeType == "REINFORCED_BY" || e.EdgeType == "DERIVED_FROM" {
			if exists, err := s.EntityExists(e.FromID); err == nil && !exists {
				continue // ghost edge — certifies nothing
			}
			sources[e.FromID] = true
			fromIDs = append(fromIDs, e.FromID)
		}
	}

	if hasContradiction {
		return "suspect"
	}
	if len(sources) >= 3 && s.authorshipClasses(fromIDs) >= 2 {
		var ticks int64
		s.mu.RLock()
		_ = s.db.QueryRow(`SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'`).Scan(&ticks)
		s.mu.RUnlock()
		if confirmedAt > 0 && ticks-confirmedAt >= 50 {
			return "trusted"
		}
		return "confirmed"
	}
	return "new"
}

// StampConfirmed records the confirmed-at anchor (bookkeeping, store-only,
// idempotent — first stamp wins, re-stamping never resets the clock).
func (s *Store) StampConfirmed(id string, ticks int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`UPDATE beliefs SET confirmed_at_ticks = ? WHERE id = ? AND confirmed_at_ticks = 0`,
		ticks, id,
	)
	return err
}

// authorshipClassOf maps a provenance string to its equivalence class:
// operator/external are independent voices; everything else is the
// resident's own substrate.
func authorshipClassOf(provenance string) string {
	switch provenance {
	case "operator", "external":
		return provenance
	default:
		return "resident"
	}
}

func (s *Store) authorshipClasses(fromIDs []string) int {
	prov, err := s.ProvenanceByIDs(fromIDs)
	if err != nil {
		return 1 // fail toward NOT confirming on independent classes
	}
	classes := map[string]bool{}
	for _, id := range fromIDs {
		classes[authorshipClassOf(prov[id])] = true // absent id → resident
	}
	return len(classes)
}

// TensionsView — the DERIVED contradiction surface (UNCONSCIOUS_V2 §2.2):
// entity pairs with a LIVE CONTRADICTS edge. Rendered when present,
// vanished on resolution (edge.archive) — zero lifecycle machinery, zero
// minting (the standing ruling applied to C's 486-line tension system).
type TensionPair struct {
	LeftID, RightID string
	EdgeID          string
}

func (s *Store) TensionsView() ([]TensionPair, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, from_id, to_id FROM edges
		 WHERE edge_type = 'CONTRADICTS' AND archived = 0
		 ORDER BY created_seq ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TensionPair
	for rows.Next() {
		var tp TensionPair
		if err := rows.Scan(&tp.EdgeID, &tp.LeftID, &tp.RightID); err != nil {
			return nil, err
		}
		out = append(out, tp)
	}
	return out, rows.Err()
}

// StatementsFor resolves entity statements for tension rendering (the
// views render what the beliefs SAY, not just their ids).
func (s *Store) StatementsFor(ids []string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		var stmt string
		err := s.db.QueryRow(`SELECT statement FROM beliefs WHERE id = ? AND archived = 0`, id).Scan(&stmt)
		if err == nil {
			out[id] = stmt
		}
		// unresolved ids simply absent — the caller renders ids for ghosts
	}
	return out, nil
}
