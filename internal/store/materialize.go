package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) Materialize(evt *ledger.Event) error {
	return s.materializeAtomic(evt, false)
}

// .
func (s *Store) MaterializeReplay(evt *ledger.Event) error {
	return s.materializeAtomic(evt, true)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) materializeAtomic(evt *ledger.Event, replayMode bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txh != nil {
		return s.materializeLocked(evt, replayMode)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin materialize transaction: %w", err)
	}
	s.txh = tx
	err = s.materializeLocked(evt, replayMode)
	s.txh = nil
	if err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback materialize transaction: %w", rollbackErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit materialize transaction: %w", err)
	}
	return nil
}

// .
// .
func (s *Store) materializeLocked(evt *ledger.Event, replayMode bool) error {
	// .
	if _, err := s.h().Exec(
		`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content, sig)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		evt.Seq, evt.Prev, evt.Timestamp, evt.Type, evt.Ring,
		string(evt.Payload), evt.Content, evt.Sig,
	); err != nil {
		return fmt.Errorf("ledger mirror insert failed: %w", err)
	}

	switch evt.Type {
	case ledger.EventRing0Genesis:
		return s.materializeBirth(evt)
	case ledger.EventBeliefUpsert:
		return s.materializeBeliefUpsert(evt)
	case ledger.EventWorkingStyleUpsert:
		return s.materializeWorkingStyle(evt)
	case ledger.EventBeliefPromote:
		return s.materializeBeliefPromote(evt)
	case ledger.EventBeliefArchive:
		return s.materializeBeliefArchive(evt)
	case ledger.EventBeliefSupersede:
		return s.materializeBeliefSupersede(evt)
	case ledger.EventSelfModelSynthesize:
		return s.materializeSelfModelSynthesis(evt)
	case ledger.EventRelationshipUpsert:
		return s.materializeRelationshipLocked(evt, replayMode)
	case ledger.EventEdgeCreate:
		return s.materializeEdgeLocked(evt)
	case ledger.EventEdgeArchive:
		return s.materializeEdgeArchive(evt)
	case ledger.EventSystemWitnessed:
		return s.materializeSystemWitnessed(evt)
	case ledger.EventTrustEpochAccepted:
		return s.materializeTrustEpochAccepted(evt)
	case ledger.EventNetworkNameClaimed:
		return s.materializePublicName(evt)
	case ledger.EventExperienceCreate:
		return s.materializeExperience(evt, replayMode)
	case ledger.EventConsolidationRun, ledger.EventDreamRun:
		return s.materializeFacilityRun(evt, replayMode)
	case ledger.EventIntentionCreate:
		return s.materializeIntentionCreate(evt)
	case ledger.EventIntentionStateChange:
		return s.materializeIntentionStateChange(evt)
	case ledger.EventCommitmentPromised:
		return s.materializeCommitmentPromised(evt)
	case ledger.EventCommitmentStateChange:
		return s.materializeCommitmentStateChange(evt)
	default:
		return fmt.Errorf("unknown event type: %s", evt.Type)
	}
}

// .
// .
func (s *Store) MaterializeAll(events []ledger.Event) error {
	for i := range events {
		if err := s.MaterializeReplay(&events[i]); err != nil {
			return fmt.Errorf("materialize failed at seq %d: %w", events[i].Seq, err)
		}
	}
	return nil
}

func (s *Store) materializeBirth(evt *ledger.Event) error {
	// .
	var payload struct {
		Name string `json:"name"`
	}
	json.Unmarshal(evt.Payload, &payload)

	_, err := s.h().Exec(
		`INSERT OR IGNORE INTO identity_lifetime (singleton_id, birth_at, lifetime_ticks, last_tick_at)
		 VALUES ('current', ?, 0, ?)`,
		evt.Timestamp, evt.Timestamp,
	)
	return err
}

func (s *Store) materializeBeliefUpsert(evt *ledger.Event) error {
	var p struct {
		ID         string  `json:"id"`
		Statement  string  `json:"statement"`
		Content    string  `json:"content"`
		Ring       int     `json:"ring"`
		Confidence float64 `json:"confidence"`
		NodeType   *string `json:"node_type"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse belief.upsert: %w", err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	statement := p.Statement
	if statement == "" {
		statement = p.Content
	}
	if statement == "" {
		return fmt.Errorf("belief.upsert requires statement or content — a belief with no text is a row, not a belief")
	}

	// .
	// .
	// .
	if p.Ring < 1 || p.Ring > 3 {
		p.Ring = 3
	}

	_, err := s.h().Exec(
		`INSERT INTO beliefs (id, statement, ring, node_type, confidence, evidence_count, first_seq, last_seq)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   statement = excluded.statement,
		   confidence = excluded.confidence,
		   last_seq = excluded.last_seq`,
		p.ID, statement, p.Ring, p.NodeType, p.Confidence, evt.Seq, evt.Seq,
	)
	return err
}

func (s *Store) materializeBeliefPromote(evt *ledger.Event) error {
	var p struct {
		ID   string `json:"id"`
		Ring int    `json:"ring"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse belief.promote: %w", err)
	}

	// .
	// .
	// .
	if p.Ring < 2 || p.Ring > 3 {
		return fmt.Errorf("belief.promote requires an explicit ring (2 = self-model placement, 3 = working truth)")
	}

	res, err := s.h().Exec(
		`UPDATE beliefs SET ring = ?, last_seq = ? WHERE id = ?`,
		p.Ring, evt.Seq, p.ID,
	)
	if err != nil {
		return err
	}
	// .
	// .
	// .
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("belief.promote cites unknown belief %q — signed event applies to nothing", p.ID)
	}
	return nil
}

// .
// .
// .
func (s *Store) materializeWorkingStyle(evt *ledger.Event) error {
	var p struct {
		ID         string  `json:"id"`
		Content    string  `json:"content"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse working_style.upsert: %w", err)
	}
	if p.Content == "" {
		return fmt.Errorf("working_style.upsert requires content — a working style with no text describes nothing")
	}
	nodeType := "working_style"
	_, err := s.h().Exec(
		`INSERT INTO beliefs (id, statement, ring, node_type, confidence, evidence_count, first_seq, last_seq)
		 VALUES (?, ?, 3, ?, ?, 0, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   statement = excluded.statement,
		   confidence = excluded.confidence,
		   last_seq = excluded.last_seq`,
		p.ID, p.Content, nodeType, p.Confidence, evt.Seq, evt.Seq,
	)
	return err
}

// .
// .
func (s *Store) materializeBeliefArchive(evt *ledger.Event) error {
	var p struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse belief.archive: %w", err)
	}
	res, err := s.h().Exec(
		`UPDATE beliefs SET archived = 1, last_seq = ? WHERE id = ?`,
		evt.Seq, p.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("belief.archive cites unknown belief %q — signed event applies to nothing", p.ID)
	}
	return nil
}

// .
// .
func (s *Store) materializeBeliefSupersede(evt *ledger.Event) error {
	var p struct {
		OldID  string `json:"old_id"`
		NewID  string `json:"new_id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse belief.supersede: %w", err)
	}
	supRes, err := s.h().Exec(
		`UPDATE beliefs SET superseded_by = ?, last_seq = ? WHERE id = ?`,
		p.NewID, evt.Seq, p.OldID,
	)
	if err != nil {
		return err
	}
	if n, _ := supRes.RowsAffected(); n == 0 {
		return fmt.Errorf("belief.supersede cites unknown belief %q — signed event applies to nothing", p.OldID)
	}
	edgeID := "edge_" + fmt.Sprintf("%d", evt.Seq)
	_, err = s.h().Exec(
		`INSERT OR IGNORE INTO edges (id, from_id, to_id, edge_type, context, created_seq)
		 VALUES (?, ?, ?, 'SUPERSEDES', ?, ?)`,
		edgeID, p.NewID, p.OldID, p.Reason, evt.Seq,
	)
	return err
}

// .
func (s *Store) materializeEdgeArchive(evt *ledger.Event) error {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse edge.archive: %w", err)
	}
	res, err := s.h().Exec(
		`UPDATE edges SET archived = 1 WHERE id = ?`,
		p.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("edge.archive cites unknown edge %q — signed event applies to nothing", p.ID)
	}
	return nil
}

// .
func (s *Store) materializeIntentionCreate(evt *ledger.Event) error {
	var p struct {
		ID        string `json:"id"`
		Statement string `json:"statement"`
		Why       string `json:"why"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse intention.create: %w", err)
	}
	if p.Statement == "" {
		return fmt.Errorf("intention.create requires statement — an intention with no text commits to nothing")
	}
	res, err := s.h().Exec(
		`INSERT INTO intentions (id, statement, state, why, created_seq, updated_seq)
		 VALUES (?, ?, 'active', ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		p.ID, p.Statement, p.Why, evt.Seq, evt.Seq,
	)
	if err != nil {
		return err
	}
	// .
	// .
	// .
	// .
	// .
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("intention.create %q already exists — a signed create that creates nothing is a ledgered no-op", p.ID)
	}
	return nil
}

// .
// .
// .
func (s *Store) materializeIntentionStateChange(evt *ledger.Event) error {
	var p struct {
		ID      string `json:"id"`
		State   string `json:"state"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse intention.state_change: %w", err)
	}
	valid := map[string]bool{"active": true, "completed": true, "abandoned": true}
	if !valid[p.State] {
		return fmt.Errorf("intention.state_change: invalid state %q", p.State)
	}
	res, err := s.h().Exec(
		`UPDATE intentions SET state = ?, outcome = ?, updated_seq = ? WHERE id = ?`,
		p.State, p.Outcome, evt.Seq, p.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("intention.state_change cites unknown intention %q — signed event applies to nothing", p.ID)
	}
	return nil
}

// .
// .
// .
func (s *Store) materializeCommitmentPromised(evt *ledger.Event) error {
	var p struct {
		ID            string `json:"id"`
		Description   string `json:"description"`
		CounterpartID string `json:"counterpart_id"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse commitment.promised: %w", err)
	}
	if p.CounterpartID == "" {
		return fmt.Errorf("commitment.promised requires counterpart_id — a promise is TO someone")
	}
	res, err := s.h().Exec(
		`INSERT INTO commitments (id, description, counterpart_id, state, created_seq, updated_seq)
		 VALUES (?, ?, ?, 'promised', ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		p.ID, p.Description, p.CounterpartID, evt.Seq, evt.Seq,
	)
	if err != nil {
		return err
	}
	// .
	// .
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("commitment.promised %q already exists — a signed create that creates nothing is a ledgered no-op", p.ID)
	}
	return nil
}

// .
// .
// .
func (s *Store) materializeCommitmentStateChange(evt *ledger.Event) error {
	var p struct {
		ID          string `json:"id"`
		State       string `json:"state"`
		Result      string `json:"result"`
		RepairState string `json:"repair_state"`
		Note        string `json:"note"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse commitment.state_change: %w", err)
	}
	valid := map[string]bool{"promised": true, "in_progress": true, "completed": true, "abandoned": true, "repaired": true}
	if !valid[p.State] {
		return fmt.Errorf("commitment.state_change: invalid state %q", p.State)
	}
	res, err := s.h().Exec(
		`UPDATE commitments SET state = ?, result = ?, repair_state = ?, updated_seq = ? WHERE id = ?`,
		p.State, p.Result, p.RepairState, evt.Seq, p.ID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("commitment.state_change cites unknown commitment %q — signed event applies to nothing", p.ID)
	}
	return nil
}

// .
// .
// .
// .
// .
// .
type WitnessReceiptPayload struct {
	IdentityID                     string `json:"identity_id"`
	PreviousWitnessedLedgerOrdinal int64  `json:"previous_witnessed_ledger_ordinal"`
	PreviousWitnessedLedgerHash    string `json:"previous_witnessed_ledger_hash"`
	LedgerOrdinal                  int64  `json:"ledger_ordinal"`
	LedgerHash                     string `json:"ledger_hash"`
	WitnessedAt                    string `json:"witnessed_at"`
	WitnessKeyID                   string `json:"witness_key_id"`
	WitnessSigB64                  string `json:"witness_sig_b64"`
}

// .
// .
// .
func (s *Store) materializeSystemWitnessed(evt *ledger.Event) error {
	var p struct {
		Receipt      *WitnessReceiptPayload `json:"receipt"`
		BeforeRewrap json.RawMessage        `json:"receipt_before_rewrap"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse system.witnessed: %w", err)
	}
	if p.Receipt == nil {
		if len(p.BeforeRewrap) > 0 {
			// .
			// .
			return nil
		}
		return fmt.Errorf("system.witnessed carries no receipt")
	}
	if p.Receipt.LedgerOrdinal == 0 || p.Receipt.LedgerHash == "" {
		return fmt.Errorf("system.witnessed receipt missing ledger fields")
	}
	receiptJSON, err := json.Marshal(p.Receipt)
	if err != nil {
		return err
	}
	_, err = s.h().Exec(
		`INSERT INTO witness_receipts (anchored_seq, receipt_json, received_at) VALUES (?, ?, ?)`,
		p.Receipt.LedgerOrdinal, string(receiptJSON), p.Receipt.WitnessedAt,
	)
	return err
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) validateExperienceLocked(provenance string, sourceTurn uint64, sourceURL string, replayMode bool) error {
	switch provenance {
	case "", "self", "dream", "system":
		// .
	case "operator":
		if sourceTurn == 0 {
			return fmt.Errorf("operator provenance requires source_turn — testimony cites its turn")
		}
		if !replayMode {
			var role string
			err := s.h().QueryRow(
				`SELECT role FROM conversations WHERE turn_seq = ?`, sourceTurn,
			).Scan(&role)
			if err == sql.ErrNoRows {
				return fmt.Errorf("operator provenance cites turn %d — no such turn: fabricated evidence fails closed", sourceTurn)
			}
			if err != nil {
				return fmt.Errorf("verify source turn: %w", err)
			}
			if role != "operator" {
				return fmt.Errorf("operator provenance cites turn %d with role %q — not an operator turn", sourceTurn, role)
			}
		}
	case "external":
		if sourceURL == "" {
			return fmt.Errorf("external provenance requires source_url — foreign text cites where it came from")
		}
	default:
		return fmt.Errorf("unknown provenance %q — sanctioned: self, dream, system, operator, external", provenance)
	}
	return nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

	// .
	// .
	// .
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
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

	// .
	// .
	// .
	// .
	// .
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
		// .
		Content: "preflight",
		Sig:     "preflight",
	}
	// .
	// .
	// .
	return s.materializeLocked(cand, false)
}

func (s *Store) materializeExperience(evt *ledger.Event, replayMode bool) error {
	var p struct {
		ID         string `json:"id"`
		Content    string `json:"content"`
		Category   string `json:"category"`
		Private    bool   `json:"private"`
		Provenance string `json:"provenance"`
		SourceTurn uint64 `json:"source_turn"`
		SourceURL  string `json:"source_url"`
		// .
		// .
		// .
		// .
		Raw *bool `json:"raw"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse experience.create: %w", err)
	}
	if p.Content == "" {
		return fmt.Errorf("experience.create requires content — an experience with no text records nothing")
	}

	if err := s.validateExperienceLocked(p.Provenance, p.SourceTurn, p.SourceURL, replayMode); err != nil {
		return err
	}

	// .
	rawVal := 1
	privateVal := 0
	if p.Private {
		rawVal = 0
		privateVal = 1
	}
	if p.Raw != nil && !*p.Raw {
		rawVal = 0
	}

	// .
	if p.Provenance == "" {
		p.Provenance = "self"
	}

	// .
	// .
	// .
	validCategories := map[string]bool{
		"observation": true, "reflection": true, "work": true,
		"learning": true, "communication": true,
	}
	var categoryVal interface{}
	if validCategories[p.Category] {
		categoryVal = p.Category
	}

	_, err := s.h().Exec(
		`INSERT OR REPLACE INTO experiences (id, content, category, raw, private, provenance, created_seq, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Content, categoryVal, rawVal, privateVal, p.Provenance, evt.Seq, evt.Timestamp,
	)
	return err
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
type FacilityRunPayload struct {
	Inputs    []string            `json:"inputs"`
	Outputs   []uint64            `json:"outputs"`
	Confirmed []ConfirmedCrossing `json:"confirmed,omitempty"`
}

// .
// .
type ConfirmedCrossing struct {
	ID    string `json:"id"`
	Ticks int64  `json:"ticks"`
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) materializeFacilityRun(evt *ledger.Event, replayMode bool) error {
	var p FacilityRunPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse %s: %w", evt.Type, err)
	}
	if len(p.Inputs) == 0 {
		return fmt.Errorf("%s with no inputs — a run that consumed nothing is a ledgered no-op", evt.Type)
	}
	for _, seq := range p.Outputs {
		if seq >= evt.Seq {
			return fmt.Errorf("%s cites output seq %d at or after itself (seq %d) — outputs precede their marker", evt.Type, seq, evt.Seq)
		}
		var one int
		if err := s.h().QueryRow(`SELECT 1 FROM ledger WHERE seq = ?`, seq).Scan(&one); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%s cites output seq %d — no such event: fabricated provenance fails closed", evt.Type, seq)
			}
			return fmt.Errorf("verify output seq %d: %w", seq, err)
		}
	}
	for _, id := range p.Inputs {
		var raw int
		err := s.h().QueryRow(`SELECT raw FROM experiences WHERE id = ?`, id).Scan(&raw)
		if err == sql.ErrNoRows {
			return fmt.Errorf("%s cites unknown experience %q — signed event applies to nothing", evt.Type, id)
		}
		if err != nil {
			return fmt.Errorf("verify run input %q: %w", id, err)
		}
		if !replayMode && raw == 0 {
			return fmt.Errorf("%s cites experience %q which is not raw — double consumption (or a Charter #9 private seal) refused", evt.Type, id)
		}
		if _, err := s.h().Exec(`UPDATE experiences SET raw = 0 WHERE id = ?`, id); err != nil {
			return fmt.Errorf("consume experience %q: %w", id, err)
		}
	}
	for _, c := range p.Confirmed {
		if c.ID == "" || c.Ticks <= 0 {
			return fmt.Errorf("%s confirms %q at tick %d — a crossing names a belief and a positive tick", evt.Type, c.ID, c.Ticks)
		}
		var one int
		if err := s.h().QueryRow(`SELECT 1 FROM beliefs WHERE id = ?`, c.ID).Scan(&one); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("%s confirms belief %q — no such belief: a signed crossing applies to nothing", evt.Type, c.ID)
			}
			return fmt.Errorf("verify confirmed belief %q: %w", c.ID, err)
		}
		// .
		// .
		// .
		// .
		// .
		if _, err := s.h().Exec(
			`UPDATE beliefs SET confirmed_at_ticks = MIN(?, COALESCE((SELECT lifetime_ticks FROM identity_lifetime WHERE singleton_id = 'current'), ?))
			 WHERE id = ? AND confirmed_at_ticks = 0`, c.Ticks, c.Ticks, c.ID); err != nil {
			return fmt.Errorf("stamp confirmed-at for %q: %w", c.ID, err)
		}
	}
	return nil
}

// .
// .
type relationshipInvariants struct {
	ID               string
	CounterpartRole  string
	RelationshipType string
	Supersedes       string
	OperatorApproval string
	ApprovalTurn     uint64
	ApprovalBasis    string
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (s *Store) validateRelationshipLocked(p relationshipInvariants, replayMode bool) error {
	if p.CounterpartRole == "operator" {
		if p.OperatorApproval == "" {
			return fmt.Errorf("operator relationship requires operator_approval_excerpt — Ring 1 is not identity-unilateral")
		}
		if p.ApprovalBasis != "conversation_turn" {
			return fmt.Errorf("operator relationship requires approval_basis %q (got %q)", "conversation_turn", p.ApprovalBasis)
		}
		// .
		// .
		if p.ApprovalTurn <= 0 {
			return fmt.Errorf("conversation_turn basis requires operator_approval_turn")
		}
		if !replayMode && p.ApprovalTurn > 0 {
			// .
			// .
			// .
			// .
			var role string
			err := s.h().QueryRow(
				`SELECT role FROM conversations WHERE turn_seq = ?`, p.ApprovalTurn,
			).Scan(&role)
			if err == sql.ErrNoRows {
				return fmt.Errorf("operator approval cites turn %d — no such turn: fabricated evidence fails closed", p.ApprovalTurn)
			}
			if err != nil {
				return fmt.Errorf("verify approval turn: %w", err)
			}
			if role != "operator" {
				return fmt.Errorf("operator approval cites turn %d with role %q — not an operator turn", p.ApprovalTurn, role)
			}
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if p.CounterpartRole == "operator" {
		var currentID string
		err := s.h().QueryRow(
			`SELECT id FROM relationships
			  WHERE counterpart_role = 'operator' AND superseded_by IS NULL AND id != ?
			  ORDER BY created_seq DESC LIMIT 1`, p.ID,
		).Scan(&currentID)
		switch {
		case err == sql.ErrNoRows:
			// .
		case err != nil:
			return fmt.Errorf("current operator relationship check: %w", err)
		case p.Supersedes == "":
			return fmt.Errorf("operator relationship %q must supersede the current one (%q) — "+
				"two unsuperseded operator relationships fork Ring 1, and the identity would answer to whichever sorted last",
				p.ID, currentID)
		case p.Supersedes != currentID:
			return fmt.Errorf("operator relationship %q supersedes %q, but the CURRENT operator relationship is %q — "+
				"superseding an already-superseded row leaves Ring 1 forked",
				p.ID, p.Supersedes, currentID)
		}
	}

	// .
	// .
	// .
	// .
	// .
	if p.Supersedes != "" {
		var supersededRole string
		err := s.h().QueryRow(
			`SELECT counterpart_role FROM relationships WHERE id = ?`, p.Supersedes,
		).Scan(&supersededRole)
		if err == sql.ErrNoRows {
			return fmt.Errorf("supersedes %q — no such relationship: succession must name a real row", p.Supersedes)
		}
		if err != nil {
			return fmt.Errorf("supersede role check: %w", err)
		}
		if supersededRole == "operator" && p.CounterpartRole != "operator" {
			return fmt.Errorf("relationship %q (role %q) cannot supersede operator relationship %q — operator succession requires an operator-role successor with its own evidence", p.ID, p.CounterpartRole, p.Supersedes)
		}
	}
	return nil
}

func (s *Store) materializeRelationshipLocked(evt *ledger.Event, replayMode bool) error {
	var p struct {
		ID               string `json:"id"`
		CounterpartName  string `json:"counterpart_name"`
		CounterpartRole  string `json:"counterpart_role"`
		TrustLevel       string `json:"trust_level"`
		AutonomyLevel    string `json:"autonomy_level"`
		RelationshipType string `json:"relationship_type"`
		Supersedes       string `json:"supersedes"`
		CharterText      string `json:"charter_text"`
		OperatorApproval string `json:"operator_approval_excerpt"`
		ApprovalTurn     uint64 `json:"operator_approval_turn"`
		ApprovalBasis    string `json:"approval_basis"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse relationship payload: %w", err)
	}

	if p.CounterpartRole == "" {
		p.CounterpartRole = "operator"
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if p.TrustLevel == "" {
		p.TrustLevel = "building"
	}
	if p.AutonomyLevel == "" {
		p.AutonomyLevel = "supervised"
	}
	if p.RelationshipType == "" {
		p.RelationshipType = "founding_operator"
	}

	if err := s.validateRelationshipLocked(relationshipInvariants{
		ID: p.ID, CounterpartRole: p.CounterpartRole, RelationshipType: p.RelationshipType,
		Supersedes: p.Supersedes, OperatorApproval: p.OperatorApproval,
		ApprovalTurn: p.ApprovalTurn, ApprovalBasis: p.ApprovalBasis,
	}, replayMode); err != nil {
		return err
	}

	// .
	// .
	// .
	// .
	// .
	_, err := s.h().Exec(`
		INSERT INTO relationships (id, counterpart_name, counterpart_role, trust_level,
		                           autonomy_level, relationship_type, charter_text, operator_approval, created_seq, updated_seq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			counterpart_name = excluded.counterpart_name,
			trust_level = excluded.trust_level,
			autonomy_level = excluded.autonomy_level,
			charter_text = CASE WHEN excluded.charter_text != ''
			                   THEN excluded.charter_text
			                   ELSE relationships.charter_text END,
			operator_approval = CASE WHEN excluded.operator_approval != ''
			                        THEN excluded.operator_approval
			                        ELSE relationships.operator_approval END,
			updated_seq = ?
	`, p.ID, p.CounterpartName, p.CounterpartRole, p.TrustLevel,
		p.AutonomyLevel, p.RelationshipType, p.CharterText, p.OperatorApproval, evt.Seq, evt.Seq)
	if err != nil {
		return err
	}

	if p.Supersedes != "" {
		res, err := s.h().Exec(
			`UPDATE relationships SET superseded_by = ? WHERE id = ? AND superseded_by IS NULL`,
			p.ID, p.Supersedes,
		)
		if err != nil {
			return fmt.Errorf("supersede relationship: %w", err)
		}
		// .
		// .
		// .
		// .
		// .
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("supersede relationship: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("relationship %q supersedes %q, which is already superseded — the succession changed nothing",
				p.ID, p.Supersedes)
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if p.CounterpartRole == "operator" {
		var n int
		if err := s.h().QueryRow(
			`SELECT COUNT(*) FROM relationships WHERE counterpart_role = 'operator' AND superseded_by IS NULL`,
		).Scan(&n); err != nil {
			return fmt.Errorf("count current operator relationships: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("relationship %q leaves %d current operator relationships — Ring 1 requires exactly one: "+
				"the charter the identity answers to is the single unsuperseded operator row", p.ID, n)
		}
	}
	return nil
}

func (s *Store) materializeEdgeLocked(evt *ledger.Event) error {
	var p struct {
		ID       string   `json:"id"`
		FromID   string   `json:"from_id"`
		ToID     string   `json:"to_id"`
		EdgeType string   `json:"edge_type"`
		Strength *float64 `json:"strength"`
		Context  *string  `json:"context"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse edge payload: %w", err)
	}

	res, err := s.h().Exec(
		`INSERT OR IGNORE INTO edges (id, from_id, to_id, edge_type, strength, context, created_seq)
		 VALUES (?, ?, ?, ?, COALESCE(?, 1.0), ?, ?)`,
		p.ID, p.FromID, p.ToID, p.EdgeType, p.Strength, p.Context, evt.Seq,
	)
	if err != nil {
		return err
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	inserted, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("edge.create %q: cannot tell whether the edge was created: %w", p.ID, err)
	}
	if inserted == 0 {
		return fmt.Errorf("edge.create %q: %s -> %s (%s) already exists — a signed create that creates nothing is a ledgered no-op",
			p.ID, p.FromID, p.ToID, p.EdgeType)
	}

	switch p.EdgeType {
	case "SUPPORTS", "REINFORCED_BY", "DERIVED_FROM":
		if _, err := s.h().Exec(
			`UPDATE beliefs SET evidence_count = evidence_count + 1, last_seq = ? WHERE id = ?`,
			evt.Seq, p.ToID,
		); err != nil {
			return err
		}
	}
	return nil
}
