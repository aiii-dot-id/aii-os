package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// Materialize processes a ledger event and updates the appropriate projection
// tables. This is called after each ledger append to keep projections in sync.
//
// The principle: every projection is f(ledger). If the DB is destroyed,
// it can be rebuilt by replaying all events through Materialize.
//
// Live materialization (replayMode=false) additionally consults live
// witnesses where they exist — the consistency layer. Replay
// (replayMode=true) is a PURE function of the event: same ledger, same
// projections, regardless of incidental store state. The store's
// conversations table is a witness of process, not identity truth — a
// replay that depended on it would violate f(ledger) (2026-08-17
// ruling: conversation turns are not ledger events; evidence cited by
// truth events is carried IN the signed payload).
func (s *Store) Materialize(evt *ledger.Event) error {
	return s.materializeAtomic(evt, false)
}

// MaterializeReplay is the replay-path materializer — pure f(ledger).
func (s *Store) MaterializeReplay(evt *ledger.Event) error {
	return s.materializeAtomic(evt, true)
}

// materializeAtomic wraps ONE event's mirror insert + projection effect
// in a single transaction — canon PROJECTION.md "Incremental
// materialization atomicity": the effect and the cursor advance MUST be
// in the same database transaction, and the mirror row IS this store's
// cursor. A failed effect therefore rolls its mirror row back with it;
// the pre-fix shape committed the mirror alone and left a half-written
// pair (external claim H5, confirmed). When an enclosing transaction is
// already open (replay's all-or-nothing rebuild), the event JOINS it —
// commit and rollback belong to the enclosure.
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

// materializeLocked is the one real materializer body. Caller holds
// s.mu and owns the transaction lifecycle (see h()).
func (s *Store) materializeLocked(evt *ledger.Event, replayMode bool) error {
	// First, insert the event into the store's ledger table (for FK integrity)
	if _, err := s.h().Exec(
		`INSERT INTO ledger (seq, prev_hash, timestamp, type, author, ring, payload, content_hash, signature, sig_alg, sig_key_id, model_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		evt.Seq, evt.PrevHash, evt.Timestamp, evt.Type, evt.Author, evt.Ring,
		string(evt.Payload), evt.ContentHash, evt.Signature, evt.SigAlg, evt.SigKeyID,
		evt.ModelID,
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

// MaterializeAll replays all events through Materialize (replay mode —
// pure f(ledger)). Used for full projection rebuild from ledger.
func (s *Store) MaterializeAll(events []ledger.Event) error {
	for i := range events {
		if err := s.MaterializeReplay(&events[i]); err != nil {
			return fmt.Errorf("materialize failed at seq %d: %w", events[i].Seq, err)
		}
	}
	return nil
}

func (s *Store) materializeBirth(evt *ledger.Event) error {
	// Initialize identity_lifetime singleton
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
	// The belief's text sits under EITHER key, because the organ writes
	// both: identity/commit.go copies content into statement before it
	// mints. Reading only one of the two was arbitrary, and it cost
	// three real beliefs — 750, 514 and 1225 characters of a resident's
	// own convictions materialized as empty rows, silently, because
	// json.Unmarshal zero-values a key it does not find.
	//
	// This is NOT a legacy accommodation. It grants no authority and
	// admits no shape that was not already canonical: a belief still
	// earns its standing through the evidence graph like any other. It
	// simply reads the payload the producer actually writes.
	statement := p.Statement
	if statement == "" {
		statement = p.Content
	}
	if statement == "" {
		return fmt.Errorf("belief.upsert requires statement or content — a belief with no text is a row, not a belief")
	}

	// Ring 0 means omitted — owner didn't inject and payload lacked it.
	// Beliefs are born at Ring 3 (working truth); promotion is the only
	// path upward. R3: rings derived in the owner, never model-selected.
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

	// 2026-08-17 ruling: the ladder lifecycle is DELETED — standing is a
	// read-time derivation from the evidence graph (standing.go). promote
	// is the resident's ring-placement act, ring-only.
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
	// Finding 11 (2026-08-17 review): a signed event that applies to
	// NOTHING is tamper evidence — the old code returned nil and the
	// projection silently diverged from a chain that "verified". Loud.
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("belief.promote cites unknown belief %q — signed event applies to nothing", p.ID)
	}
	return nil
}

// materializeWorkingStyle routes working_style.upsert to beliefs with
// node_type='working_style' — working style is what the identity believes
// about how it works; beliefs are already the structural spine.
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

// materializeBeliefArchive: soft-delete — archived=1, excluded from active
// queries. True History: the row and its ledger events remain.
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

// materializeBeliefSupersede: marks old belief superseded_by -> new, and
// mints the SUPERSEDES edge in the same event (per the supersession model).
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

// materializeEdgeArchive: soft-delete — archived=1.
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

// materializeIntentionCreate: goal enters the world active.
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
	// A create that creates NOTHING is finding-11's shape in reverse: the
	// ledger must not record no-ops that claim to create (external claim
	// H4, confirmed — ON CONFLICT silence made duplicates ledgered
	// no-ops). Loud in both modes: live, the R56 preflight turns this
	// into a refusal BEFORE the append; on replay it is tamper evidence.
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("intention.create %q already exists — a signed create that creates nothing is a ledgered no-op", p.ID)
	}
	return nil
}

// materializeIntentionStateChange: lifecycle transition. Q2: intentions
// transition, never upsert content — a changed goal is completed/abandoned
// and replaced, linked by edge if lineage matters.
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

// materializeCommitmentPromised: a promise enters the world owed. The
// counterpart (Q1) is load-bearing — who this is TO is what makes it a
// promise rather than a plan.
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
	// Same law as intention.create: no ledgered no-ops that claim to
	// create (H4). The preflight refuses this before it can ever append.
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("commitment.promised %q already exists — a signed create that creates nothing is a ledgered no-op", p.ID)
	}
	return nil
}

// materializeCommitmentStateChange: promised -> in_progress ->
// completed/abandoned/repaired. Blocked is in_progress with a note; failure
// is abandoned + repair_state; making good is repaired.
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

// WitnessReceiptPayload is the receipt subset carried in system.witnessed
// events (defined here so store needs no witness-package import; the
// anchorer marshals the same shape).
type WitnessReceiptPayload struct {
	WitnessVersion                 string `json:"witness_version"`
	IdentityID                     string `json:"identity_id"`
	PreviousWitnessedLedgerOrdinal int64  `json:"previous_witnessed_ledger_ordinal"`
	PreviousWitnessedLedgerHash    string `json:"previous_witnessed_ledger_hash"`
	LedgerOrdinal                  int64  `json:"ledger_ordinal"`
	LedgerHash                     string `json:"ledger_hash"`
	RangeStartOrdinal              int64  `json:"range_start_ordinal"`
	RangeHash                      string `json:"range_hash"`
	WitnessedAt                    string `json:"witnessed_at"`
	WitnessKeyID                   string `json:"witness_key_id"`
	WitnessSigB64                  string `json:"witness_sig_b64"`
}

// materializeSystemWitnessed lands a verified witness receipt into the
// witness_receipts projection. Receipts are f(ledger): replay rebuilds
// them; the runtime never writes this table directly.
func (s *Store) materializeSystemWitnessed(evt *ledger.Event) error {
	var p struct {
		Receipt WitnessReceiptPayload `json:"receipt"`
	}
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("parse system.witnessed: %w", err)
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

// validateExperienceLocked runs the experience provenance invariants
// (H3) WITHOUT writing. The preflight reaches it THROUGH the real
// materializer now (R56 single boundary), so live mint and rollback-only
// validation are one code path by construction. The independent classes —
// the ones the R16 ladder counts — must carry their citation.
// Payload-only checks run in both modes; the turn cross-check is
// live-only, mirroring the R52 approval pattern. Caller holds s.mu.
// The sanctioned vocabulary is ONE set with two encodings — this switch
// and the experiences.provenance CHECK in schema.sql; the preflight
// drift probe (TestExperienceProvenanceVocabularyMatchesSchema) pins
// them equal, so a token can never pass semantics here only to die as
// a raw CHECK error in the same transaction. "work" is an experience
// CATEGORY and a verb, never a provenance.
func (s *Store) validateExperienceLocked(provenance string, sourceTurn uint64, sourceURL string, replayMode bool) error {
	switch provenance {
	case "", "self", "dream", "system":
		// the resident's own substrate — one equivalence class
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
			return fmt.Errorf("external provenance requires source_url — foreign text cites where it came from (R49)")
		}
	default:
		return fmt.Errorf("unknown provenance %q — sanctioned: self, dream, system, operator, external", provenance)
	}
	return nil
}

// ValidateEvent is THE preflight boundary (R56; M1, 2026-08-17 external
// review): mint paths call it BEFORE ledger.Append, so an event that
// would be refused live never becomes durable truth that replay must
// later accept.
//
// It is the REAL materializer run in a rollback-only transaction — one
// code path, no per-family reimplementation (canon EVENT_VALIDATION.md
// step 5: the preflight "executes the existing materializer ... inside
// a rollback-only transaction ... The same target constraints, foreign
// keys, value checks, and UPDATE row-existence rule that govern
// materialization therefore reject the proposed event before it is
// signed or appended. This is a validation use of the existing
// materializer, not a second materialization path"). The pre-fix shape
// re-implemented invariants for two families and validated everything
// else trivially — archive/supersede/promote/state-change events with
// unknown targets, duplicate creates, and unparseable payloads all
// appended durably and then failed materialization forever (external
// claims #3/H4, confirmed).
//
// RING AUTHORITY (canon IDENTITY_SEMANTICS §11, 2026-08-18 sprint): the
// gate first validates the claimed ring against the type's canonical
// table — rings derive in the owner and are VALIDATED here, never
// trusted. A verb bug, a facility passing the wrong ring, or a future
// caller inventing authority all DENY with the reason.
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

	// The write lock, not the read lock: the rollback-only run WRITES
	// (and vanishes), so it takes its turn in the single-writer queue
	// like every materialization.
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

	// The candidate event: the next mirror seq (unique within this
	// transaction's view; rolled back with everything else), a wall
	// timestamp, and placeholder crypto fields — the materializer never
	// reads signatures (VerifyChain owns those), and nothing here is
	// signed yet by design: refusal must precede signing.
	var seq uint64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM ledger`).Scan(&seq); err != nil {
		return fmt.Errorf("preflight seq probe: %w", err)
	}
	cand := &ledger.Event{
		Seq:       seq,
		PrevHash:  "preflight",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Type:      eventType,
		Author:    "preflight",
		Ring:      ringLevel,
		Payload:   payload,
		// Non-empty placeholders keep NOT NULL mirror columns honest.
		ContentHash: "preflight",
		Signature:   "preflight",
		SigAlg:      "preflight",
		SigKeyID:    "preflight",
	}
	// Live mode (replayMode=false): the preflight keeps every live
	// cross-check — turn citations, approval evidence — exactly as the
	// real admission would run them.
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
		// Raw is an internal override for facility-minted notes (dream):
		// the product is already metabolized and must not re-enter its own
		// processing loop. The note verb's engine path never includes it —
		// resident notes always derive raw from private (Charter #9).
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

	// Dawn's Charter #9: private experiences are raw=0 (never processed by facilities)
	rawVal := 1
	privateVal := 0
	if p.Private {
		rawVal = 0
		privateVal = 1
	}
	if p.Raw != nil && !*p.Raw {
		rawVal = 0
	}

	// Provenance defaults to self (identity-authored)
	if p.Provenance == "" {
		p.Provenance = "self"
	}

	// R2: capture before judgment — an unknown category normalizes to null,
	// never blocks capture. The closed vocabulary is enforced by normalization,
	// not rejection.
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

// FacilityRunPayload is the closed payload of a facility run marker
// (consolidation.run / dream.run — external review 2026-08-20, H6/#4).
// Inputs are the experience ids the run READ; outputs are the ledger
// seqs of the events the run MINTED (empty for an honest no-change
// pass). Results, never instructions — replay never re-runs an LLM.
// Defined here so cognition and store share one shape (the
// TrustEpochPayload pattern).
type FacilityRunPayload struct {
	Inputs  []string `json:"inputs"`
	Outputs []uint64 `json:"outputs"`
}

// materializeFacilityRun lands a run marker: the cited input experiences
// are marked consumed (raw=0) HERE — the materializer is the only writer
// of consumed state, so replay restores it (the pre-fix shape was a bare
// UPDATE from cognition that every replay wiped, resurrecting the whole
// backlog for re-metabolization each boot).
//
// Fail-closed shape, both modes:
//   - no inputs = a run that consumed nothing — a ledgered no-op (H4 law)
//   - an output seq that names no prior chain event is fabricated
//     provenance (outputs precede the marker by the LAST-mint ordering,
//     so the lookup is pure f(ledger))
//   - an input id that names no experience: the signed marker applies to
//     nothing (finding 11)
//
// Live-only (the R52/H3 cross-check pattern — replay stays pure):
//   - an input already consumed is double consumption; the preflight
//     refuses it BEFORE the append. This is also Charter #9's tooth:
//     private experiences live at raw=0, so a run marker citing one
//     refuses instead of metabolizing what was sealed.
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
	return nil
}

// relationshipInvariants carries the fields the fail-closed relationship
// checks consume — shared by live materialization and the M1 pre-flight.
type relationshipInvariants struct {
	ID               string
	CounterpartRole  string
	RelationshipType string
	Supersedes       string
	OperatorApproval string
	ApprovalTurn     uint64
	ApprovalBasis    string
}

// validateRelationshipLocked runs every fail-closed relationship
// invariant WITHOUT writing. Caller holds s.mu (read or write).
//
// Ring 1 authority — the affirmative-reply model (R52): the identity
// proposes, the operator replies, the engine stamps the citing evidence
// from that exact turn. An operator relationship without recorded
// approval fails closed; an approval citing a turn that is not real or
// not operator-authored fails closed. The LLM never supplies the
// approval — it cannot forge what it cannot write. The engine stamps
// "conversation_turn" on every verb-path mint. Payload invariants hold
// in BOTH modes; the turn cross-check is
// live-only (conversations is a process witness, not identity truth —
// fresh-DB replay used to fail here as 'fabricated evidence' for
// honestly-signed events).
func (s *Store) validateRelationshipLocked(p relationshipInvariants, replayMode bool) error {
	if p.CounterpartRole == "operator" {
		if p.OperatorApproval == "" {
			return fmt.Errorf("operator relationship requires operator_approval_excerpt — Ring 1 is not identity-unilateral")
		}
		if p.ApprovalBasis != "conversation_turn" {
			return fmt.Errorf("operator relationship requires approval_basis %q (got %q)", "conversation_turn", p.ApprovalBasis)
		}
		// The citation IS the evidence class — a conversation_turn
		// basis without a turn number is a malformed assertion.
		if p.ApprovalTurn <= 0 {
			return fmt.Errorf("conversation_turn basis requires operator_approval_turn")
		}
		if !replayMode && p.ApprovalTurn > 0 {
			// NOTE: query the handle directly (s.h() — the open
			// transaction when one is in flight) — the caller holds s.mu;
			// calling GetTurnBySeq would re-lock and deadlock (the C
			// codebase's PG-mutex lesson, Go edition).
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

	// AT MOST ONE CURRENT OPERATOR RELATIONSHIP. Ring 1 is the charter,
	// and supersedes was OPTIONAL: a second operator relationship that
	// named no predecessor simply appeared beside the first, both with
	// superseded_by NULL. Nothing refused it, and
	// CurrentOperatorRelationship resolves the tie with ORDER BY
	// created_seq DESC — so which operator the identity answers to was
	// decided by a sort, silently. Naming a predecessor that was ALREADY
	// superseded reached the same place: the existence check below
	// proved the row was real, never that it was current.
	//
	// Runs in BOTH modes: relationships is derived from the ledger in
	// ledger order, so "which row is current" is a pure function of the
	// chain — the same reason the succession role gate below runs in
	// both.
	if p.CounterpartRole == "operator" {
		var currentID string
		err := s.h().QueryRow(
			`SELECT id FROM relationships
			  WHERE counterpart_role = 'operator' AND superseded_by IS NULL AND id != ?
			  ORDER BY created_seq DESC LIMIT 1`, p.ID,
		).Scan(&currentID)
		switch {
		case err == sql.ErrNoRows:
			// The founding operator. Nothing to supersede.
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

	// Succession role gate (H1-adjacent): an operator-role row may only
	// be superseded by another operator-role relationship, which
	// necessarily carried its own R52 evidence through the gate above.
	// Runs in BOTH modes — the target row precedes its successor in
	// ledger order, so the lookup is a pure function of the ledger.
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
			return fmt.Errorf("relationship %q (role %q) cannot supersede operator relationship %q — operator succession requires an operator-role successor with its own R52 evidence", p.ID, p.CounterpartRole, p.Supersedes)
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
	// SIGNED BUT INERT, as of 2026-08-24. trust_level and autonomy_level
	// are minted into the chain, materialized here, and rendered to the
	// operator — and nothing branches on either, nor do they reach the
	// identity's prompt (Ring 1 carries charter_text alone). They
	// describe; they do not govern.
	//
	// Left that way deliberately pending a ruling. autonomy_level in
	// particular is authority-shaped language sitting OUTSIDE the ring
	// model, and wiring it to "act without confirmation" would be
	// inventing a second authority model beside the one canon defines
	// (AGENTS.md 1.1). Messaging uses the standing ladder instead —
	// unknown / known / chartered — which governs reach and attention
	// only. Recorded here so a later reader does not mistake a stored
	// value for a live control.
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

	// Succession: a new relationship carrying `supersedes` marks the old
	// row's superseded_by pointing to the new. INSERT the successor FIRST —
	// superseded_by references relationships(id), so the FK fails if the
	// successor row doesn't exist yet. Operator relationships are replaced,
	// not archived.
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
		// A conditional UPDATE that matched nothing is a SUCCESSION THAT
		// DID NOT HAPPEN. Ignoring the row count let a signed event say
		// "I replaced X" while X kept standing. The gate above should
		// make this unreachable for operator rows; unreachable and
		// unchecked are different things.
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("supersede relationship: %w", err)
		}
		if n == 0 {
			return fmt.Errorf("relationship %q supersedes %q, which is already superseded — the succession changed nothing",
				p.ID, p.Supersedes)
		}
	}

	// RING 1 IS WHAT THE PROJECTION HOLDS, not what the payload claimed.
	// Every gate above reads the incoming payload, and three upserts
	// satisfied all of them and still left ZERO unsuperseded operator
	// rows — so the charter the prompt reads came from nowhere, with
	// nothing refused (B5, 2026-08-27): a row naming ITSELF (the
	// at-most-one-current probe excludes the incoming id, so it read as
	// "founding operator, nothing to supersede"); a CYCLE — B supersedes
	// A, then A is re-minted superseding B; and a stored PEER row
	// carrying an operator payload, which validates as an operator and
	// then supersedes one while the upsert's ON CONFLICT leaves
	// counterpart_role alone. Asserting the OUTCOME closes all three, and
	// the next route nobody has found yet. Materialization owns a
	// transaction (materializeAtomic, or replay's enclosure), so this
	// refusal takes the write back out with it.
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

	// The same law intention.create and commitment.promised already
	// enforce (H4): NO LEDGERED NO-OPS THAT CLAIM TO CREATE. Edges were
	// the one create left out of it.
	//
	// The duplicate arrives under a FRESH id — uniqueness here is
	// (from_id, to_id, edge_type), so ON CONFLICT never fires on the id
	// and the row count is the only witness. Returning nil made the
	// preflight pass, so the event was signed and durably appended
	// having created nothing: a chain entry asserting a connection that
	// the projection does not have.
	//
	// Loud in both modes, like its siblings: live, the R56 preflight
	// turns it into a refusal BEFORE the append; on replay it is tamper
	// evidence.
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
