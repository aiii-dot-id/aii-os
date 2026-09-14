package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/google/uuid"
)

// .
// .
// .
// .
var outcomeForm = regexp.MustCompile(`^(served|partial|unserved):\s*\S.*`)

// .
// .
// .
// .
func HasOutcomeVerdict(s string) bool { return outcomeForm.MatchString(s) }

// .

// .
// .
// .
// .
// .
var relIDPattern = regexp.MustCompile(`^rel_[a-z0-9][a-z0-9_-]{7,63}$`)

func selfModelCommitPayload(args map[string]interface{}) (ledger.SelfModelSynthesisPayload, error) {
	wire := map[string]interface{}{}
	for key, value := range args {
		if key != "variant" && key != "ring" {
			wire[key] = value
		}
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return ledger.SelfModelSynthesisPayload{}, fmt.Errorf("encode self_model.synthesize arguments: %w", err)
	}
	return ledger.DecodeSelfModelSynthesisPayload(raw)
}

// .
// .
// .
// .
// .
// .
func evidenceRefIDs(v interface{}) []string {
	var out []string
	add := func(raw string) {
		if id := strings.TrimSpace(raw); id != "" && id != "none" {
			out = append(out, id)
		}
	}
	switch ev := v.(type) {
	case string:
		if ev != "none" {
			for _, part := range strings.Split(ev, ",") {
				add(part)
			}
		}
	case []interface{}:
		for _, item := range ev {
			if raw, ok := item.(string); ok {
				add(raw)
			}
		}
	}
	return out
}

// .
// .
// .
// .
// .
func requireEvidenceDeclared(variant string, args map[string]interface{}) error {
	if _, hasEvidence := args["evidence_refs"]; hasEvidence {
		return nil
	}
	if none, hasNone := args["evidence"]; hasNone && none == "none" {
		return nil
	}
	return fmt.Errorf("%s requires evidence_refs[] or evidence:none — a belief comes from experiences or other beliefs, never from nowhere", variant)
}

func (e *Engine) verbCommit(ctx context.Context, args map[string]interface{}) (string, error) {
	// .
	// .
	// .
	// .
	if v, _ := args["variant"].(string); v == "skill.propose" {
		return e.verbSkill(ctx, withAction(args, "propose"))
	}
	if e.inSafeMode() {
		return "", fmt.Errorf("commit refused: I am in safe mode — %s. The record I would write into cannot be verified; the operator must restore it first.", e.safeModeReason())
	}
	variant, _ := args["variant"].(string)
	if variant == "" {
		return "", fmt.Errorf("commit requires variant")
	}

	var eventType ledger.EventType
	var targetRing int

	switch variant {
	case "belief.upsert":
		eventType = ledger.EventBeliefUpsert
		targetRing = 3
		// .
		// .
		// .
		if err := requireEvidenceDeclared("belief.upsert", args); err != nil {
			return "", err
		}
		// .
		// .
		if ok, _ := args["duplicate_ok"].(bool); !ok {
			stmt, _ := args["statement"].(string)
			bid, _ := args["id"].(string)
			if stmt != "" {
				// .
				// .
				// .
				// .
				dupID, err := e.store.FindBeliefByStatement(stmt, bid)
				if err != nil {
					return "", fmt.Errorf("the duplicate check could not run, so nothing was minted: %w", err)
				}
				if dupID != "" {
					return "", fmt.Errorf("a live belief already states exactly this (%s). Strengthen it (edge.create SUPPORTS / note reinforces) or supersede it — or mint anyway with duplicate_ok: true", dupID)
				}
			}
		}
	case "belief.promote":
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		eventType = ledger.EventBeliefPromote
		targetRing = 2
		promoteID, _ := args["id"].(string)
		if promoteID == "" {
			return "", fmt.Errorf("belief.promote requires id")
		}
		standing, err := e.store.StandingFor(promoteID)
		if err != nil {
			// .
			// .
			return "", fmt.Errorf("belief.promote to Ring 2 refused: %q's standing cannot be proven (%v) — the evidence graph must be readable to promote", promoteID, err)
		}
		if standing != "confirmed" && standing != "trusted" {
			return "", fmt.Errorf("belief.promote to Ring 2 refused: %q has standing %q — Ring 2 requires confirmed (≥3 distinct sources spanning ≥2 authorship classes, no live contradiction). Gather independent evidence (note with source_turn/source_url, edge.create SUPPORTS) and promote when the ladder confirms it", promoteID, standing)
		}
	case "belief.attest":
		// .
		// .
		// .
		// .
		// .
		return "", fmt.Errorf("belief.attest was removed: attest via note (testimony) or edge.create (evidence)")
	case "relationship.upsert":
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
		eventType = ledger.EventRelationshipUpsert
		targetRing = 1
		relID, _ := args["id"].(string)
		if relID == "" {
			return "", fmt.Errorf("relationship.upsert requires an explicit id — the operator's affirmation cites it")
		}
		// .
		// .
		// .
		if !relIDPattern.MatchString(relID) {
			return "", fmt.Errorf("relationship.upsert id %q rejected: mint an id matching rel_[a-z0-9][a-z0-9_-]{7,} — ids must be unambiguous and greppable", relID)
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
		role, _ := args["counterpart_role"].(string)
		if role == "" {
			role = "operator"
		}
		if role == "operator" {
			if charter, _ := args["charter_text"].(string); strings.TrimSpace(charter) == "" {
				return "", fmt.Errorf("an operator relationship requires charter_text — the Ring 1 document you will read every turn. The operator's approval is evidence that you may hold this relationship; it is not the document. Write what the relationship IS, then propose it for their affirmation")
			}
		}
		turn, err := e.store.GetLatestOperatorTurn()
		if err != nil {
			return "", fmt.Errorf("ring 1 authority check failed: %w", err)
		}
		if turn == nil {
			return "", fmt.Errorf("Ring 1 requires an operator affirmative — no operator turn on record")
		}
		args["operator_approval_excerpt"] = turn.Content
		args["operator_approval_turn"] = turn.TurnSeq
		args["approval_basis"] = "conversation_turn"
	case "self_model.synthesize":
		eventType = ledger.EventSelfModelSynthesize
		targetRing = 3
	case "edge.create":
		// .
		// .
		// .
		eventType = ledger.EventEdgeCreate
		targetRing = 3
		validEdges := map[string]bool{
			"DERIVED_FROM": true, "SUPPORTS": true, "CONTRADICTS": true,
			"SUPERSEDES": true, "REINFORCED_BY": true, "SHAPED_BY": true,
			"INTERPRETS": true,
		}
		et, _ := args["edge_type"].(string)
		if !validEdges[et] {
			return "", fmt.Errorf("edge.create requires edge_type from the canonical 7")
		}
		fromID, _ := args["from_id"].(string)
		toID, _ := args["to_id"].(string)
		if fromID == "" || toID == "" {
			return "", fmt.Errorf("edge.create requires from_id and to_id")
		}
		// .
		for _, id := range []string{fromID, toID} {
			exists, err := e.store.EntityExists(id)
			if err != nil {
				return "", fmt.Errorf("edge.create endpoint check: %w", err)
			}
			if !exists {
				return "", fmt.Errorf("edge.create refused: no such entity %q", id)
			}
		}
	case "intention.create":
		// .
		// .
		eventType = ledger.EventIntentionCreate
		targetRing = 3
		if stmt, _ := args["statement"].(string); stmt == "" {
			return "", fmt.Errorf("intention.create requires statement")
		}
	case "intention.state_change":
		eventType = ledger.EventIntentionStateChange
		targetRing = 3
		state, _ := args["state"].(string)
		valid := map[string]bool{"active": true, "completed": true, "abandoned": true}
		if !valid[state] {
			return "", fmt.Errorf("intention.state_change requires state: active|completed|abandoned")
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
		if state == "completed" || state == "abandoned" {
			outcome, _ := args["outcome"].(string)
			if !outcomeForm.MatchString(outcome) {
				return "", fmt.Errorf("intention.state_change to %q requires outcome — one line beginning served: | partial: | unserved: — your own verdict on whether the work served the intent, and what happened", state)
			}
		}
	case "commitment.promised":
		// .
		// .
		eventType = ledger.EventCommitmentPromised
		targetRing = 3
		if _, ok := args["counterpart_id"]; !ok {
			return "", fmt.Errorf("commitment.promised requires counterpart_id — a promise is TO someone")
		}
		if desc, _ := args["description"].(string); desc == "" {
			return "", fmt.Errorf("commitment.promised requires description")
		}
	case "commitment.state_change":
		eventType = ledger.EventCommitmentStateChange
		targetRing = 3
		state, _ := args["state"].(string)
		valid := map[string]bool{"promised": true, "in_progress": true, "completed": true, "abandoned": true, "repaired": true}
		if !valid[state] {
			return "", fmt.Errorf("commitment.state_change requires state: promised|in_progress|completed|abandoned|repaired")
		}
	case "working_style.upsert":
		// .
		eventType = ledger.EventWorkingStyleUpsert
		targetRing = 3
		if _, ok := args["content"]; !ok {
			if pos, ok2 := args["_positional"].(string); ok2 {
				args["content"] = pos
			}
		}
		if content, _ := args["content"].(string); content == "" {
			return "", fmt.Errorf("working_style.upsert requires content")
		}
		// .
		// .
		// .
		// .
		if err := requireEvidenceDeclared("working_style.upsert", args); err != nil {
			return "", err
		}
		// .
		args["statement"] = args["content"]
		if _, ok := args["confidence"]; !ok {
			args["confidence"] = 0.8
		}
	case "belief.archive":
		// .
		eventType = ledger.EventBeliefArchive
		targetRing = 3
	case "belief.supersede":
		eventType = ledger.EventBeliefSupersede
		targetRing = 3
		if _, ok := args["old_id"]; !ok {
			return "", fmt.Errorf("belief.supersede requires old_id and new_id")
		}
		if _, ok := args["new_id"]; !ok {
			return "", fmt.Errorf("belief.supersede requires old_id and new_id")
		}
	case "edge.archive":
		// .
		eventType = ledger.EventEdgeArchive
		targetRing = 3
	default:
		return "", fmt.Errorf("unknown commit variant: %s", variant)
	}

	// .
	if err := ring.CheckGate(ring.RingLevel(targetRing)); err != nil {
		return "", err
	}

	// .
	// .

	// .
	if _, ok := args["id"]; !ok {
		args["id"] = "item_" + uuid.New().String()
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if r, ok := args["ring"]; ok {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		var rv int
		valid := false
		switch v := r.(type) {
		case int:
			rv, valid = v, true
		case float64:
			if v == math.Trunc(v) {
				rv, valid = int(v), true
			}
		}
		if !valid {
			return "", fmt.Errorf("ring must be an integer (rings are owner-derived, never invented): got %v", r)
		}
		if rv != targetRing {
			return "", fmt.Errorf("ring is owner-derived: ring %d not valid for %s", rv, variant)
		}
		args["ring"] = rv
	} else {
		// .
		// .
		// .
		// .
		if variant == "belief.promote" {
			return "", fmt.Errorf("belief.promote requires explicit ring (2 = self-model placement)")
		}
		args["ring"] = targetRing
	}

	payload := interface{}(args)
	if variant == "self_model.synthesize" {
		selfModelPayload, err := selfModelCommitPayload(args)
		if err != nil {
			return "", err
		}
		payload = selfModelPayload
	}

	evt, err := e.append(ctx, eventType, targetRing, payload)
	if errors.Is(err, store.ErrSelfModelUnchanged) {
		return "No change: the current self-model already has the same portrait, continuity, and grounding", nil
	}
	if err != nil {
		return "", err
	}

	// .
	// .
	if variant == "belief.upsert" || variant == "working_style.upsert" {
		if refusals := e.mintBeliefEvidenceEdges(ctx, args, evt); len(refusals) > 0 {
			return fmt.Sprintf("Committed: %s (seq %d). Edge refusals: %s", variant, evt.Seq, strings.Join(refusals, "; ")), nil
		}
	}

	return fmt.Sprintf("Committed: %s (seq %d)", variant, evt.Seq), nil
}
