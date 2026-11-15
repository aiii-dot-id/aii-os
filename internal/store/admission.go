package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
)

func (s *Store) validateLiveOnlyLocked(evt *ledger.Event) error {
	switch evt.Type {
	case ledger.EventExperienceCreate:
		var p experiencePayload
		if json.Unmarshal(evt.Payload, &p) != nil || p.Content == "" || p.Provenance != "operator" ||
			s.validateExperienceLocked(p.Provenance, p.SourceTurn, p.SourceURL) != nil {
			return nil
		}
		return s.citedOperatorTurnLocked(p.SourceTurn, "operator provenance", "verify source turn", false)
	case ledger.EventRelationshipUpsert:
		p, err := readRelationshipPayload(evt.Payload)
		if err != nil {
			return nil
		}
		inv := invariantsOf(p)
		if p.CounterpartRole != "operator" {

			var held string
			err := s.h().QueryRow(`SELECT counterpart_role FROM relationships WHERE id = ?`, p.ID).Scan(&held)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("verify the role relationship %q holds: %w", p.ID, err)
			}
			if held != "operator" {
				return nil
			}
			inv.CounterpartRole = held
			if err := inv.approvalShape(); err != nil {
				return fmt.Errorf("%w: relationship %q is held as the operator's, and naming role %q in the entry does not change that: %w", ErrOperatorRelationshipUnapproved, p.ID, p.CounterpartRole, err)
			}
		} else if inv.approvalShape() != nil {
			return nil
		}

		if err := s.citedOperatorTurnLocked(p.ApprovalTurn, "operator approval", "verify approval turn", true); err != nil {
			return err
		}

		if p.ApprovalDigest != "" {
			if err := s.pressTurnLocked(p.ApprovalPressTurn, p.ApprovalDigest); err != nil {
				return fmt.Errorf("%w: %w", ErrOperatorRelationshipUnapproved, err)
			}
		}
		return nil
	case ledger.EventConsolidationRun, ledger.EventDreamRun:
		var p FacilityRunPayload
		if json.Unmarshal(evt.Payload, &p) != nil || len(p.Inputs) == 0 || s.runOutputsPrecedeLocked(evt, p.Outputs) != nil {
			return nil
		}
		seen := map[string]bool{}
		for _, id := range p.Inputs {
			var raw int
			err := s.h().QueryRow(`SELECT raw FROM experiences WHERE id = ?`, id).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("verify run input %q: %w", id, err)
			}
			if raw == 0 || seen[id] {
				return fmt.Errorf("%s cites experience %q which is not raw — double consumption, or a note kept out of DREAM and CONSOLIDATE, refused", evt.Type, id)
			}
			seen[id] = true
		}
		return s.crossingsNotAheadLocked(evt.Type, p.Confirmed)
	}
	return nil
}

func (s *Store) crossingsNotAheadLocked(et ledger.EventType, crossings []ConfirmedCrossing) error {
	if len(crossings) == 0 {
		return nil
	}
	var lived int64
	err := s.h().QueryRow(standingLifetimeSQL).Scan(&lived)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the lived clock: %w", err)
	}
	for _, c := range crossings {
		if c.ID == "" || c.Ticks <= 0 || c.Ticks <= lived {
			continue
		}
		var one int
		if err := s.h().QueryRow(`SELECT 1 FROM beliefs WHERE id = ?`, c.ID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("verify confirmed belief %q: %w", c.ID, err)
		}
		return fmt.Errorf("%s confirms %q at tick %d, ahead of the lived clock (%d) — a crossing is recorded at the tick it was observed, never ahead of it", et, c.ID, c.Ticks, lived)
	}
	return nil
}

func (s *Store) citedOperatorTurnLocked(turn uint64, claim, verify string, affirmation bool) error {
	var role string
	err := s.h().QueryRow(`SELECT role FROM conversations WHERE turn_seq = ?`, turn).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s cites turn %d — no such turn: fabricated evidence fails closed", claim, turn)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", verify, err)
	}
	if role != "operator" {
		return fmt.Errorf("%s cites turn %d with role %q — not an operator turn", claim, turn, role)
	}
	selection, what := "role = 'operator' AND "+notRoomSpeech, "which the host recorded as the room's speech, not the operator's"
	if affirmation {
		selection, what = s.operatorWordSelection(), "which is not the operator's own word to the identity (a turn they typed or spoke to it; not the room's speech, a grade, a press on an ask card, or a line the host wrote)"
	}
	var theirs int
	if err := s.h().QueryRow(`SELECT COUNT(*) FROM conversations WHERE turn_seq = ? AND `+selection, turn).Scan(&theirs); err != nil {
		return fmt.Errorf("%s: %w", verify, err)
	}
	if theirs == 0 {
		return fmt.Errorf("%s cites turn %d, %s: fails closed", claim, turn, what)
	}

	if affirmation {
		if err := s.spokenByTheOperator(s.h(), turn); err != nil {
			var unidentified *UnidentifiedSpeechError
			var unconfirmed *UnconfirmedSpeechError
			if errors.As(err, &unidentified) || errors.As(err, &unconfirmed) {
				return fmt.Errorf("%s cites a spoken turn that cannot stand as the operator's own word (%v): fails closed", claim, err)
			}
			return fmt.Errorf("%s: %w", verify, err)
		}
	}
	return nil
}

func (s *Store) ValidateEvent(eventType ledger.EventType, ringLevel int, payload []byte) (retErr error) {
	legal := ledger.CanonicalRings(eventType)
	ringOK := false
	for _, r := range legal {
		if r == ringLevel {
			ringOK = true
			break
		}
	}
	if !ringOK {
		return fmt.Errorf("ring %d is not a legal authority for %s (canonical: %v) — rings are owner-derived and gate-validated", ringLevel, eventType, legal)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return fmt.Errorf("begin preflight transaction: %w", err)
	}
	s.txh = tx
	defer func() {
		s.txh = nil
		if err := tx.Rollback(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("rollback preflight transaction: %w", err))
		}
	}()

	var seq uint64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM ledger`).Scan(&seq); err != nil {
		return fmt.Errorf("preflight seq probe: %w", err)
	}
	cand := &ledger.Event{
		Seq:       seq,
		Prev:      "preflight",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Type:      eventType,
		Ring:      ringLevel,
		Payload:   payload,

		Content: "preflight",
		Sig:     "preflight",
	}

	if err := s.validateCitationsLocked(eventType, payload); err != nil {
		return err
	}

	if err := s.validateDreamedThroughLocked(eventType, payload); err != nil {
		return err
	}
	if err := s.validateRing2TextLocked(eventType, payload); err != nil {
		return err
	}
	if err := s.validateBeliefAdmissionLocked(eventType, payload); err != nil {
		return err
	}
	if err := s.validateRetiredBeliefLocked(eventType, payload); err != nil {
		return err
	}
	if err := s.validateSupersedeSuccessorLocked(eventType, payload); err != nil {
		return err
	}

	if eventType == ledger.EventSelfModelSynthesize {
		portrait, err := ledger.DecodeSelfModelSynthesisPayload(payload)
		if err != nil {
			return err
		}

		if err := s.admitSelfModelBudget(portrait); err != nil {
			return err
		}
		if err := s.admitSelfModelSourcesLocked(portrait); err != nil {
			return err
		}
	}

	if err := s.validateLiveOnlyLocked(cand); err != nil {
		return err
	}

	if eventType == ledger.EventRelationshipUpsert {
		if err := s.admitCharterBudgetLocked(payload); err != nil {
			return err
		}
	}

	if err := s.validateSourceIDLocked(eventType, payload); err != nil {
		return err
	}

	if err := ledger.AdmitPayloadMembers(eventType, payload); err != nil {
		return err
	}
	if err := s.materializeLocked(cand); err != nil {
		return err
	}
	if eventType == ledger.EventEdgeArchive {
		return s.validateActiveBeliefGroundingLocked(payload)
	}
	return nil
}

func (s *Store) validateSourceIDLocked(eventType ledger.EventType, payload []byte) error {
	var own, other, kind string
	switch eventType {
	case ledger.EventBeliefUpsert, ledger.EventWorkingStyleUpsert:
		own, other, kind = "beliefs", "experiences", "an experience"
	case ledger.EventExperienceCreate:
		own, other, kind = "experiences", "beliefs", "a belief"
	default:
		return nil
	}
	var p struct{ ID string }
	if json.Unmarshal(payload, &p) != nil || p.ID == "" {
		return nil
	}
	holds := func(table string) (bool, error) {
		var one int
		err := s.h().QueryRow("SELECT 1 FROM "+table+" WHERE id = ?", p.ID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("read %s for id %q before admission: %w", table, p.ID, err)
		}
		return true, nil
	}
	if held, err := holds(other); err != nil || !held {
		return err
	}
	if mine, err := holds(own); err != nil || mine {
		return err
	}
	return fmt.Errorf("%w: %s names id %q, which %s already holds; mint it under an id of its own", ErrSourceIDHeld, eventType, p.ID, kind)
}

func (s *Store) validateCitationsLocked(eventType ledger.EventType, payload []byte) error {
	cites, err := ledger.ParseCitations(payload)
	if err != nil {
		return fmt.Errorf("%s: %w", eventType, err)
	}
	if len(cites) == 0 {
		return nil
	}
	if !ledger.CitesAllowed(eventType) {
		return fmt.Errorf("%s: %w: citations are carried by experience.create, belief.upsert and edge.create only", eventType, ledger.ErrCitation)
	}
	self, err := s.ownFingerprintLocked()
	if err != nil {
		return fmt.Errorf("%s: cites: %w", eventType, err)
	}
	for i, c := range cites {
		if self == "" || c.Identity != self {
			continue
		}
		var evt ledger.Event
		var typ string
		var ring sql.NullInt64
		err := s.h().QueryRow(`SELECT seq, prev, ts, type, ring, content FROM ledger WHERE seq = ?`, c.Seq).
			Scan(&evt.Seq, &evt.Prev, &evt.Timestamp, &typ, &ring, &evt.Content)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%s: %w: citation %d names record %d of your own record, which does not hold one — a record cites what is already written", eventType, ledger.ErrCitation, i, c.Seq)
		}
		if err != nil {
			return fmt.Errorf("%s: cites: read record %d: %w", eventType, c.Seq, err)
		}
		if !ring.Valid {
			return fmt.Errorf("%s: cites: record %d carries no ring — its entry hash cannot be recomputed", eventType, c.Seq)
		}
		evt.Type, evt.Ring = ledger.EventType(typ), int(ring.Int64)
		if got := evt.EntryHash(); got != c.EntryHash {
			return fmt.Errorf("%s: %w: citation %d says your record %d has entry hash %s; it has %s", eventType, ledger.ErrCitation, i, c.Seq, c.EntryHash, got)
		}
	}
	return nil
}

func (s *Store) validateDreamedThroughLocked(eventType ledger.EventType, payload []byte) error {
	claim, err := ledger.ParseDreamedThrough(payload)
	if err != nil {
		return fmt.Errorf("%s: %w", eventType, err)
	}
	if claim == nil {
		return nil
	}
	var p struct {
		Provenance string `json:"provenance"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("%s: dreamed_through: %w", eventType, err)
	}
	if !ledger.DreamedThroughAllowed(eventType) || p.Provenance != "dream" {
		return fmt.Errorf("%s: %w: it is carried by a dream note — an experience.create with provenance dream — and by nothing else", eventType, ledger.ErrDreamedThrough)
	}

	var content string
	err = s.h().QueryRow(`SELECT content FROM conversations WHERE id = ?`, claim.Turn).Scan(&content)
	length := uint64(cursor.TurnLength(content))
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w: it names turn %q, which the transcript does not hold — a note claims what it read", eventType, ledger.ErrDreamedThrough, claim.Turn)
	}
	if err != nil {
		return fmt.Errorf("%s: dreamed_through: read turn %q: %w", eventType, claim.Turn, err)
	}
	if claim.Position > length {
		return fmt.Errorf("%s: %w: position %d lies past the end of turn %q (%d code points)", eventType, ledger.ErrDreamedThrough, claim.Position, claim.Turn, length)
	}
	return nil
}

func (s *Store) ownFingerprintLocked() (string, error) {
	var payload []byte
	err := s.h().QueryRow(`SELECT payload FROM ledger WHERE type = 'ring0.genesis' ORDER BY seq ASC LIMIT 1`).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the genesis: %w", err)
	}
	var p struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return "", fmt.Errorf("read the genesis: %w", err)
	}
	return p.Fingerprint, nil
}
