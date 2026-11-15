package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func (s *Store) materializeBeliefOriginsLocked(evt *ledger.Event, target string, refs *[]beliefOrigin) error {
	if refs == nil {
		return nil
	}
	if len(*refs) == 0 {
		return fmt.Errorf("belief %q requires at least one origin", target)
	}
	seen := map[string]bool{}
	for i, ref := range *refs {
		if ref.ID == "" || ref.ID == target {
			return fmt.Errorf("belief %q has an empty or self-referential origin", target)
		}
		var table, other string
		switch ref.Kind {
		case "experience":
			table, other = "experiences", "beliefs"
		case "belief":
			table, other = "beliefs", "experiences"
		default:
			return fmt.Errorf("belief %q has unsupported origin kind %q", target, ref.Kind)
		}
		key := ref.Kind + ":" + ref.ID
		if seen[key] {
			return fmt.Errorf("belief %q repeats origin %s", target, key)
		}
		seen[key] = true
		var one int
		if err := s.h().QueryRow("SELECT 1 FROM "+table+" WHERE id = ?", ref.ID).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("belief %q cites missing %s %q", target, ref.Kind, ref.ID)
			}
			return fmt.Errorf("check belief origin %s: %w", key, err)
		}
		err := s.h().QueryRow("SELECT 1 FROM "+other+" WHERE id = ?", ref.ID).Scan(&one)
		if err == nil {
			return fmt.Errorf("belief %q cites ambiguous origin id %q", target, ref.ID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check belief origin ambiguity %s: %w", key, err)
		}
		res, err := s.h().Exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq)
			VALUES (?, ?, ?, 'DERIVED_FROM', ?)
			ON CONFLICT(from_id, to_id, edge_type) DO NOTHING`, fmt.Sprintf("edge_origin_%d_%d", evt.Seq, i), ref.ID, target, evt.Seq)
		if err != nil {
			return fmt.Errorf("materialize belief origin %s: %w", key, err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("read belief origin effect %s: %w", key, err)
		} else if n > 0 {
			if _, err := s.h().Exec(`UPDATE beliefs SET evidence_count = evidence_count + 1 WHERE id = ?`, target); err != nil {
				return err
			}
		}

		if err := s.h().QueryRow(`SELECT 1 FROM edges WHERE from_id = ? AND to_id = ?
			AND edge_type = 'DERIVED_FROM' AND archived = 0`, ref.ID, target).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("belief %q would omit active origin %s", target, key)
			}
			return err
		}
	}
	var active int
	if err := s.h().QueryRow(`SELECT COUNT(*) FROM edges WHERE to_id = ? AND archived = 0
		AND edge_type IN ('SUPPORTS','DERIVED_FROM','REINFORCED_BY')`, target).Scan(&active); err != nil {
		return err
	}
	if active == 0 {
		return fmt.Errorf("belief %q would have no active evidence edge", target)
	}
	return nil
}

func (s *Store) Materialize(evt *ledger.Event) error {
	return s.materializeAtomic(evt)
}

func (s *Store) materializeAtomic(evt *ledger.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.txh != nil {
		return s.materializeLocked(evt)
	}
	tx, err := s.w().Begin()
	if err != nil {
		return fmt.Errorf("begin materialize transaction: %w", err)
	}
	s.txh = tx
	err = s.materializeLocked(evt)
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

func (s *Store) materializeLocked(evt *ledger.Event) error {

	if _, err := s.h().Exec(
		`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		evt.Seq, evt.Prev, evt.Timestamp, evt.Type, evt.Ring,
		string(evt.Payload), evt.Content,
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
		return s.materializeRelationshipLocked(evt)
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
		return s.materializeExperience(evt)
	case ledger.EventConsolidationRun, ledger.EventDreamRun:
		return s.materializeFacilityRun(evt)
	case ledger.EventIntentionCreate:
		return s.materializeIntentionCreate(evt)
	case ledger.EventIntentionStateChange:
		return s.materializeIntentionStateChange(evt)
	case ledger.EventCommitmentPromised:
		return s.materializeCommitmentPromised(evt)
	case ledger.EventCommitmentStateChange:
		return s.materializeCommitmentStateChange(evt)
	default:
		return &UnknownEventTypeError{Type: evt.Type}
	}
}

func (s *Store) materializeBirth(evt *ledger.Event) error {

	result, err := s.h().Exec(
		`INSERT OR IGNORE INTO identity_lifetime (singleton_id, birth_at, lifetime_ticks, last_tick_at)
		 VALUES ('current', ?, 0, ?)`,
		evt.Timestamp, evt.Timestamp,
	)
	if err == nil && s.schemaContext != nil {
		if n, e := result.RowsAffected(); e != nil {
			return e
		} else if n == 1 {
			if s.schemaConversions == nil {
				s.schemaConversions = map[string]RuntimeConversion{}
			}
			s.schemaConversions["identity_lifetime"] = RuntimeConversion{Before: 0, After: 1, Reason: "recorded birth initializes an absent lived clock"}
		}
	}
	return err
}

func (s *Store) materializeBeliefUpsert(evt *ledger.Event) error {
	var p struct {
		ID         string          `json:"id"`
		Statement  string          `json:"statement"`
		Content    string          `json:"content"`
		Ring       int             `json:"ring"`
		Confidence float64         `json:"confidence"`
		NodeType   *string         `json:"node_type"`
		OriginRefs *[]beliefOrigin `json:"origin_refs"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse belief.upsert: %w", err)
	}

	statement := p.Statement
	if statement == "" {
		statement = p.Content
	}
	if statement == "" {
		return fmt.Errorf("belief.upsert requires statement or content — a belief with no text is a row, not a belief")
	}

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
	if err != nil {
		return err
	}
	return s.materializeBeliefOriginsLocked(evt, p.ID, p.OriginRefs)
}

func (s *Store) materializeBeliefPromote(evt *ledger.Event) error {
	var p struct {
		ID   string `json:"id"`
		Ring int    `json:"ring"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse belief.promote: %w", err)
	}

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

	if n, _ := res.RowsAffected(); n == 0 {
		return &MissingEventTargetError{Event: evt.Type, Kind: "belief", ID: p.ID}
	}
	return nil
}

func (s *Store) materializeWorkingStyle(evt *ledger.Event) error {
	var p struct {
		ID         string          `json:"id"`
		Content    string          `json:"content"`
		Confidence float64         `json:"confidence"`
		OriginRefs *[]beliefOrigin `json:"origin_refs"`
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
	if err != nil {
		return err
	}
	return s.materializeBeliefOriginsLocked(evt, p.ID, p.OriginRefs)
}

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
		return &MissingEventTargetError{Event: evt.Type, Kind: "belief", ID: p.ID}
	}
	return nil
}

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
		return &MissingEventTargetError{Event: evt.Type, Kind: "belief", ID: p.OldID}
	}
	edgeID := "edge_" + fmt.Sprintf("%d", evt.Seq)
	_, err = s.h().Exec(
		`INSERT OR IGNORE INTO edges (id, from_id, to_id, edge_type, context, created_seq)
		 VALUES (?, ?, ?, 'SUPERSEDES', ?, ?)`,
		edgeID, p.NewID, p.OldID, p.Reason, evt.Seq,
	)
	return err
}

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
		return &MissingEventTargetError{Event: evt.Type, Kind: "edge", ID: p.ID}
	}
	var target, edgeType string
	if err := s.h().QueryRow(`SELECT to_id, edge_type FROM edges WHERE id = ?`, p.ID).Scan(&target, &edgeType); err != nil {
		return err
	}
	if edgeType == "SUPPORTS" || edgeType == "REINFORCED_BY" || edgeType == "DERIVED_FROM" {
		if _, err := s.h().Exec(`UPDATE beliefs SET evidence_count = (
			SELECT COUNT(*) FROM edges WHERE to_id = ? AND archived = 0
			AND edge_type IN ('SUPPORTS','REINFORCED_BY','DERIVED_FROM')) WHERE id = ?`, target, target); err != nil {
			return err
		}
	}
	return nil
}

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

	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("intention.create %q already exists — a signed create that creates nothing is a ledgered no-op", p.ID)
	}
	return nil
}

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
		return &MissingEventTargetError{Event: evt.Type, Kind: "intention", ID: p.ID}
	}
	return nil
}

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

	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("commitment.promised %q already exists — a signed create that creates nothing is a ledgered no-op", p.ID)
	}
	return nil
}

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
		return &MissingEventTargetError{Event: evt.Type, Kind: "commitment", ID: p.ID}
	}
	return nil
}

type WitnessReceiptPayload struct {
	IdentityID                     string `json:"identity_id"`
	PreviousWitnessedLedgerOrdinal int64  `json:"previous_witnessed_ledger_ordinal"`
	PreviousWitnessedLedgerHash    string `json:"previous_witnessed_ledger_hash"`
	LedgerOrdinal                  int64  `json:"ledger_ordinal"`
	LedgerHash                     string `json:"ledger_hash"`
	WitnessedAt                    string `json:"witnessed_at"`
	WitnessKeyID                   string `json:"witness_key_id"`
	WitnessSigB64                  string `json:"witness_sig_b64"`
	ContextSHA256                  string `json:"context_sha256,omitempty"`
}

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

var ErrSourceIDHeld = errors.New("an id names one evidence source")

var ErrOperatorRelationshipUnapproved = errors.New("an upsert of the operator's relationship requires the operator's approval")

var ErrRing2TextChange = errors.New("Ring 2 text cannot be replaced by a Ring 3 upsert")

var ErrBeliefRetired = errors.New("a retired belief stays as it was retired")

type experiencePayload = struct {
	ID       string `json:"id"`
	Content  string `json:"content"`
	Category string `json:"category"`

	Unmetabolized  bool   `json:"unmetabolized"`
	EarlierPrivate bool   `json:"private"`
	Provenance     string `json:"provenance"`
	SourceTurn     uint64 `json:"source_turn"`
	SourceURL      string `json:"source_url"`

	Raw *bool `json:"raw"`
}

func (s *Store) materializeExperience(evt *ledger.Event) error {
	var p experiencePayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse experience.create: %w", err)
	}
	if p.Content == "" {
		return fmt.Errorf("experience.create requires content — an experience with no text records nothing")
	}

	if err := s.validateExperienceLocked(p.Provenance, p.SourceTurn, p.SourceURL); err != nil {
		return err
	}

	rawVal := 1
	unmetabolizedVal := 0
	if p.Unmetabolized || p.EarlierPrivate {
		rawVal = 0
		unmetabolizedVal = 1
	}
	if p.Raw != nil && !*p.Raw {
		rawVal = 0
	}

	if p.Provenance == "" {
		p.Provenance = "self"
	}

	validCategories := map[string]bool{
		"observation": true, "reflection": true, "work": true,
		"learning": true, "communication": true,
	}
	var categoryVal any
	if validCategories[p.Category] {
		categoryVal = p.Category
	}

	_, err := s.h().Exec(
		`INSERT OR REPLACE INTO experiences (id, content, category, raw, unmetabolized, provenance, created_seq, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Content, categoryVal, rawVal, unmetabolizedVal, p.Provenance, evt.Seq, evt.Timestamp,
	)
	return err
}

func (s *Store) materializeFacilityRun(evt *ledger.Event) error {
	var p FacilityRunPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse %s: %w", evt.Type, err)
	}
	if len(p.Inputs) == 0 {
		return fmt.Errorf("%s with no inputs — a run that consumed nothing is a ledgered no-op", evt.Type)
	}
	if err := s.runOutputsPrecedeLocked(evt, p.Outputs); err != nil {
		return err
	}
	for _, id := range p.Inputs {
		var raw int
		err := s.h().QueryRow(`SELECT raw FROM experiences WHERE id = ?`, id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return &MissingEventTargetError{Event: evt.Type, Kind: "experience", ID: id}
		}
		if err != nil {
			return fmt.Errorf("verify run input %q: %w", id, err)
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
			if errors.Is(err, sql.ErrNoRows) {
				return &MissingEventTargetError{Event: evt.Type, Kind: "belief", ID: c.ID}
			}
			return fmt.Errorf("verify confirmed belief %q: %w", c.ID, err)
		}

		if _, err := s.h().Exec(
			`UPDATE beliefs SET confirmed_at_ticks = ? WHERE id = ? AND confirmed_at_ticks = 0`, c.Ticks, c.ID); err != nil {
			return fmt.Errorf("stamp confirmed-at for %q: %w", c.ID, err)
		}
	}
	return nil
}

func (s *Store) materializeRelationshipLocked(evt *ledger.Event) error {
	p, err := readRelationshipPayload(evt.Payload)
	if err != nil {
		return err
	}
	if err := s.validateRelationshipLocked(invariantsOf(p)); err != nil {
		return err
	}

	_, err = s.h().Exec(`
		INSERT INTO relationships (id, counterpart_name, counterpart_role, trust_level,
		                           autonomy_level, relationship_type, charter_text, operator_approval, approval_digest, approved_level, created_seq, updated_seq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CASE WHEN ? != '' THEN ? ELSE '' END, ?, NULL)
		ON CONFLICT(id) DO UPDATE SET
			counterpart_name = excluded.counterpart_name,
			trust_level = excluded.trust_level,
			autonomy_level = excluded.autonomy_level,
			approval_digest = CASE WHEN excluded.approval_digest != '' THEN excluded.approval_digest ELSE relationships.approval_digest END,
			approved_level = CASE WHEN excluded.approval_digest != '' THEN excluded.autonomy_level ELSE relationships.approved_level END,
			charter_text = CASE WHEN excluded.charter_text != ''
			                   THEN excluded.charter_text
			                   ELSE relationships.charter_text END,
			operator_approval = CASE WHEN excluded.operator_approval != ''
			                        THEN excluded.operator_approval
			                        ELSE relationships.operator_approval END,
			updated_seq = ?
	`, p.ID, p.CounterpartName, p.CounterpartRole, p.TrustLevel,
		p.AutonomyLevel, p.RelationshipType, p.CharterText, p.OperatorApproval, p.ApprovalDigest, p.ApprovalDigest, p.AutonomyLevel, evt.Seq, evt.Seq)
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

		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("supersede relationship: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("relationship %q supersedes %q, which is already superseded — the succession changed nothing",
				p.ID, p.Supersedes)
		}
	}

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
