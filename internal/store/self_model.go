package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

const (
	selfModelMaxTokens      = 2000
	selfModelMinSourceClass = 4
)

// ErrSelfModelUnchanged is the internal admission result for an exact
// material duplicate. The commit ceremony translates it to a successful
// no-op; it is never persisted as identity state.
var ErrSelfModelUnchanged = errors.New("self-model portrait is materially unchanged")

// SelfModelRefusal is a typed pre-append refusal naming the unmet contract.
type SelfModelRefusal struct {
	Requirement string
	Detail      string
}

func (e *SelfModelRefusal) Error() string {
	if e.Detail == "" {
		return "self_model.synthesize refused: " + e.Requirement
	}
	return fmt.Sprintf("self_model.synthesize refused: %s: %s", e.Requirement, e.Detail)
}

func refuseSelfModel(requirement, format string, args ...interface{}) error {
	detail := ""
	if format != "" {
		detail = fmt.Sprintf(format, args...)
	}
	return &SelfModelRefusal{Requirement: requirement, Detail: detail}
}

// SelfModelSynthesis is one accepted projected portrait.
type SelfModelSynthesis struct {
	ID               string
	SynthesisText    string
	ContinuityThread string
	SourceEntityRefs []ledger.SelfModelSourceRef
	ChangesSinceLast string
	CreatedSeq       uint64
	CreatedAt        string
}

type Ring2Evidence struct {
	ID         string
	EdgeType   string
	Content    string
	Provenance string
}

type Ring2Belief struct {
	ID        string
	Statement string
	Evidence  []Ring2Evidence
}

func normalizeSelfModelText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

func canonicalSelfModelRefs(refs []ledger.SelfModelSourceRef) ([]ledger.SelfModelSourceRef, error) {
	out := make([]ledger.SelfModelSourceRef, len(refs))
	copy(out, refs)
	for i := range out {
		out[i].Class = strings.TrimSpace(out[i].Class)
		out[i].ID = strings.TrimSpace(out[i].ID)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Class == out[j].Class {
			return out[i].ID < out[j].ID
		}
		return out[i].Class < out[j].Class
	})
	seenIDs := make(map[string]struct{}, len(out))
	for i, ref := range out {
		if ref.Class == "" || ref.ID == "" {
			return nil, refuseSelfModel("specific durable references", "source_entity_refs[%d] requires class and id", i)
		}
		if !ledger.IsSelfModelSourceClass(ref.Class) {
			return nil, refuseSelfModel("canonical source classes", "source_entity_refs[%d] has unknown class %q", i, ref.Class)
		}
		if i > 0 && ref == out[i-1] {
			return nil, refuseSelfModel("specific durable references", "duplicate source reference %s/%s", ref.Class, ref.ID)
		}
		if _, exists := seenIDs[ref.ID]; exists {
			return nil, refuseSelfModel("source coverage", "entity %s cannot count in more than one source class", ref.ID)
		}
		seenIDs[ref.ID] = struct{}{}
	}
	return out, nil
}

func refsEqual(a, b []ledger.SelfModelSourceRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Store) currentSelfModelLocked() (*SelfModelSynthesis, error) {
	var currentCount int
	if err := s.h().QueryRow(`SELECT COUNT(*) FROM self_model_synthesis WHERE superseded_by IS NULL`).Scan(&currentCount); err != nil {
		return nil, fmt.Errorf("count current self-model synthesis: %w", err)
	}
	if currentCount > 1 {
		return nil, fmt.Errorf("self-model projection has %d current rows; expected at most one", currentCount)
	}
	if currentCount == 0 {
		return nil, nil
	}

	var current SelfModelSynthesis
	var refsJSON string
	err := s.h().QueryRow(`
		SELECT id, synthesis_text, continuity_thread, source_entity_refs,
		       changes_since_last, created_seq, created_at
		FROM self_model_synthesis WHERE superseded_by IS NULL
	`).Scan(&current.ID, &current.SynthesisText, &current.ContinuityThread,
		&refsJSON, &current.ChangesSinceLast, &current.CreatedSeq, &current.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("load current self-model synthesis: %w", err)
	}
	if err := json.Unmarshal([]byte(refsJSON), &current.SourceEntityRefs); err != nil {
		return nil, fmt.Errorf("decode current self-model references: %w", err)
	}
	current.SourceEntityRefs, err = canonicalSelfModelRefs(current.SourceEntityRefs)
	if err != nil {
		return nil, fmt.Errorf("current self-model projection is invalid: %w", err)
	}
	return &current, nil
}

func (s *Store) selfModelRefExistsLocked(ref ledger.SelfModelSourceRef) (bool, error) {
	var query string
	switch ref.Class {
	case "beliefs":
		query = `SELECT 1 FROM beliefs WHERE id = ? AND archived = 0 AND superseded_by IS NULL AND (node_type IS NULL OR (node_type = 'value' AND ring = 3))`
	case "values":
		query = `SELECT 1 FROM beliefs WHERE id = ? AND archived = 0 AND superseded_by IS NULL AND node_type = 'value' AND ring <= 2`
	case "intentions":
		query = `SELECT 1 FROM intentions WHERE id = ? AND archived = 0`
	case "reflections":
		query = `SELECT 1 FROM self_model_synthesis WHERE id = ?`
	case "relationships":
		query = `SELECT 1 FROM relationships WHERE id = ? AND superseded_by IS NULL`
	case "notes":
		query = `SELECT 1 FROM experiences WHERE id = ? AND private = 0 AND category = 'reflection'`
	case "experiences":
		query = `SELECT 1 FROM experiences WHERE id = ? AND private = 0 AND (category IS NULL OR category != 'reflection')`
	case "working_style":
		query = `SELECT 1 FROM beliefs WHERE id = ? AND archived = 0 AND superseded_by IS NULL AND node_type = 'working_style'`
	default:
		return false, nil
	}
	var one int
	err := s.h().QueryRow(query, ref.ID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// selfModelClassesLocked reports every citable class under which id
// resolves right now. Diagnostic only — it grants nothing: the refusal
// that uses it still refuses, it just points at the truth so the
// corrective round can act instead of orbit.
func (s *Store) selfModelClassesLocked(id string) []string {
	var hits []string
	for _, class := range []string{"beliefs", "values", "intentions", "reflections", "relationships", "notes", "experiences", "working_style"} {
		ok, err := s.selfModelRefExistsLocked(ledger.SelfModelSourceRef{Class: class, ID: id})
		if err == nil && ok {
			hits = append(hits, class)
		}
	}
	return hits
}

func (s *Store) validateSelfModelPayloadLocked(payload *ledger.SelfModelSynthesisPayload) ([]ledger.SelfModelSourceRef, *SelfModelSynthesis, error) {
	payload.ID = strings.TrimSpace(payload.ID)
	payload.SynthesisText = normalizeSelfModelText(payload.SynthesisText)
	payload.ContinuityThread = normalizeSelfModelText(payload.ContinuityThread)
	payload.ChangesSinceLast = normalizeSelfModelText(payload.ChangesSinceLast)
	payload.PreviousSynthesisID = strings.TrimSpace(payload.PreviousSynthesisID)

	if payload.ID == "" {
		return nil, nil, refuseSelfModel("event identity", "id is required")
	}
	if payload.SynthesisText == "" {
		return nil, nil, refuseSelfModel("bounded narrative", "synthesis_text is required")
	}
	if tokens := tokenestimate.Estimate(payload.SynthesisText); tokens > selfModelMaxTokens {
		return nil, nil, refuseSelfModel("bounded narrative", "synthesis_text is approximately %d tokens; maximum is %d", tokens, selfModelMaxTokens)
	}
	if payload.ContinuityThread == "" {
		return nil, nil, refuseSelfModel("continuity", "continuity_thread is required")
	}

	refs, err := canonicalSelfModelRefs(payload.SourceEntityRefs)
	if err != nil {
		return nil, nil, err
	}
	classes := make(map[string]struct{})
	for _, ref := range refs {
		exists, err := s.selfModelRefExistsLocked(ref)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve self-model source %s/%s: %w", ref.Class, ref.ID, err)
		}
		if !exists {
			// Name where the id ACTUALLY lives, when it lives anywhere.
			// The generic sentence sent the corrective round in circles
			// live on 2026-08-26: the model re-cited the same two
			// beliefs as working_style for hours, because nothing told
			// it which class was right.
			if hits := s.selfModelClassesLocked(ref.ID); len(hits) > 0 {
				return nil, nil, refuseSelfModel("consistent cited evidence",
					"%s/%s: this id is not available under class %q — it exists under %s; cite it with that class",
					ref.Class, ref.ID, ref.Class, strings.Join(hits, ", "))
			}
			return nil, nil, refuseSelfModel("consistent cited evidence", "%s/%s is unavailable, inactive, private, or belongs to another class", ref.Class, ref.ID)
		}
		classes[ref.Class] = struct{}{}
	}
	if len(classes) < selfModelMinSourceClass {
		return nil, nil, refuseSelfModel("source coverage", "have %d distinct classes; require at least %d", len(classes), selfModelMinSourceClass)
	}

	current, err := s.currentSelfModelLocked()
	if err != nil {
		return nil, nil, err
	}
	if current == nil {
		if payload.PreviousSynthesisID != "" {
			return nil, nil, refuseSelfModel("current predecessor", "previous_synthesis_id %q supplied but no current portrait exists", payload.PreviousSynthesisID)
		}
		return refs, nil, nil
	}
	if payload.PreviousSynthesisID != current.ID {
		return nil, nil, refuseSelfModel("current predecessor", "previous_synthesis_id %q does not match current %q", payload.PreviousSynthesisID, current.ID)
	}
	if payload.SynthesisText == normalizeSelfModelText(current.SynthesisText) &&
		payload.ContinuityThread == normalizeSelfModelText(current.ContinuityThread) &&
		refsEqual(refs, current.SourceEntityRefs) {
		return nil, nil, ErrSelfModelUnchanged
	}
	return refs, current, nil
}

func (s *Store) materializeSelfModelSynthesis(evt *ledger.Event) error {
	payload, err := ledger.DecodeSelfModelSynthesisPayload(evt.Payload)
	if err != nil {
		return err
	}
	refs, current, err := s.validateSelfModelPayloadLocked(&payload)
	if err != nil {
		return err
	}
	refsJSON, err := json.Marshal(refs)
	if err != nil {
		return fmt.Errorf("encode self-model references: %w", err)
	}

	// Insert the accepted successor before writing the forward pointer so
	// the foreign key always names an existing row. Both statements are in
	// the materializer's one transaction and are invisible until commit.
	if _, err := s.h().Exec(`
		INSERT INTO self_model_synthesis
		 (id, synthesis_text, source_entity_refs, changes_since_last,
		  continuity_thread, superseded_by, created_seq, created_at)
		 VALUES (?, ?, ?, ?, ?, NULL, ?, ?)`,
		payload.ID, payload.SynthesisText, string(refsJSON), payload.ChangesSinceLast,
		payload.ContinuityThread, evt.Seq, evt.Timestamp,
	); err != nil {
		return fmt.Errorf("insert self-model synthesis %q: %w", payload.ID, err)
	}
	if current != nil {
		res, err := s.h().Exec(`UPDATE self_model_synthesis SET superseded_by = ? WHERE id = ? AND superseded_by IS NULL`, payload.ID, current.ID)
		if err != nil {
			return fmt.Errorf("supersede prior self-model synthesis %q: %w", current.ID, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("supersede prior self-model synthesis %q affected %d rows; expected 1", current.ID, n)
		}
	}
	return nil
}

// CurrentSelfModel returns the current accepted portrait, or nil if none.
func (s *Store) CurrentSelfModel() (*SelfModelSynthesis, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentSelfModelLocked()
}

// ListSelfModelSyntheses returns accepted portraits newest first.
func (s *Store) ListSelfModelSyntheses(n int, beforeSeq uint64) ([]SelfModelSynthesis, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := `SELECT id, synthesis_text, continuity_thread, source_entity_refs,
	                 changes_since_last, created_seq, created_at
	          FROM self_model_synthesis`
	args := []interface{}{}
	if beforeSeq > 0 {
		query += ` WHERE created_seq < ?`
		args = append(args, beforeSeq)
	}
	query += ` ORDER BY created_seq DESC LIMIT ?`
	args = append(args, n)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SelfModelSynthesis
	for rows.Next() {
		var item SelfModelSynthesis
		var refsJSON string
		if err := rows.Scan(&item.ID, &item.SynthesisText, &item.ContinuityThread,
			&refsJSON, &item.ChangesSinceLast, &item.CreatedSeq, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(refsJSON), &item.SourceEntityRefs); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Ring2Material derives the prompt view from consciously promoted beliefs and
// their active provenance. It stores nothing.
func (s *Store) Ring2Material() ([]Ring2Belief, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ring2MaterialLocked()
}

func (s *Store) ring2MaterialLocked() ([]Ring2Belief, error) {
	rows, err := s.db.Query(`
		SELECT id, statement FROM beliefs
		WHERE ring = 2 AND archived = 0 AND superseded_by IS NULL
		ORDER BY first_seq, id`)
	if err != nil {
		return nil, err
	}
	var out []Ring2Belief
	for rows.Next() {
		var belief Ring2Belief
		if err := rows.Scan(&belief.ID, &belief.Statement); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, belief)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		belief := &out[i]
		edges, err := s.db.Query(`
			WITH linked(id, edge_type, created_seq) AS (
			  SELECT CASE WHEN from_id = ? THEN to_id ELSE from_id END,
			         edge_type, created_seq
			  FROM edges
			  WHERE (from_id = ? OR to_id = ?) AND archived = 0
			    AND edge_type IN ('SHAPED_BY','SUPPORTS','DERIVED_FROM','REINFORCED_BY')
			)
			SELECT id, edge_type,
			       COALESCE(
			         (SELECT statement FROM beliefs WHERE beliefs.id = linked.id),
			         (SELECT content FROM experiences WHERE experiences.id = linked.id),
			         (SELECT statement FROM intentions WHERE intentions.id = linked.id),
			         (SELECT charter_text FROM relationships WHERE relationships.id = linked.id),
			         (SELECT synthesis_text FROM self_model_synthesis WHERE self_model_synthesis.id = linked.id),
			         id),
			       COALESCE((SELECT provenance FROM experiences WHERE experiences.id = linked.id), 'self')
			FROM linked ORDER BY created_seq, id`, belief.ID, belief.ID, belief.ID)
		if err != nil {
			return nil, err
		}
		for edges.Next() {
			var evidence Ring2Evidence
			if err := edges.Scan(&evidence.ID, &evidence.EdgeType, &evidence.Content, &evidence.Provenance); err != nil {
				edges.Close()
				return nil, err
			}
			belief.Evidence = append(belief.Evidence, evidence)
		}
		if err := edges.Err(); err != nil {
			edges.Close()
			return nil, err
		}
		if err := edges.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// PromptIdentity is the ledger-derived identity material rendered in one prompt.
type PromptIdentity struct {
	Charter                 string
	HasOperatorRelationship bool
	Ring2                   []Ring2Belief
	SelfModel               *SelfModelSynthesis
}

// PromptIdentity reads the identity projection under one store lock so a
// prompt cannot splice Ring 1, Ring 2, and the self-model from different
// ledger states.
func (s *Store) PromptIdentity() (PromptIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	charter, hasOperatorRelationship, err := s.operatorCharterLocked()
	if err != nil {
		return PromptIdentity{}, err
	}
	ring2, err := s.ring2MaterialLocked()
	if err != nil {
		return PromptIdentity{}, err
	}
	selfModel, err := s.currentSelfModelLocked()
	if err != nil {
		return PromptIdentity{}, err
	}
	return PromptIdentity{
		Charter: charter, HasOperatorRelationship: hasOperatorRelationship,
		Ring2: ring2, SelfModel: selfModel,
	}, nil
}
