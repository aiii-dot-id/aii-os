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

// outcomeForm is the completion yardstick's closed shape: a verdict the
// stagnation predicate and consolidation can read without judgment,
// then the owner's one line of what happened. served/partial/unserved
// — the same vocabulary sub-agents self-report in.
var outcomeForm = regexp.MustCompile(`^(served|partial|unserved):\s*\S.*`)

// --- commit: conscious self-authorship — ring-gated (R3), consent gate (C11) ---

// relIDPattern is the entropy floor for Ring-1 relationship ids (H1,
// 2026-08-17 external review): the model chooses the id, and the pairing
// check searches BOTH turns for it — with a low-entropy id ("e") almost
// any operator message "affirms" and the model's own reply "proposes".
// A resident-minted id must be a deliberate token nobody types by
// accident: rel_ + at least 8 of [a-z0-9_-].
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

// containsToken reports whether content carries id as a STANDALONE token
// (id-characters on neither side) — strings.Contains let "rel_deep" match
// inside "rel_deeper", so an id embedded in a longer word counted as an
// affirmation (H1).
func containsToken(content, id string) bool {
	for from := 0; ; {
		i := strings.Index(content[from:], id)
		if i < 0 {
			return false
		}
		i += from
		beforeOK := i == 0 || !isRelIDChar(content[i-1])
		afterOK := i+len(id) == len(content) || !isRelIDChar(content[i+len(id)])
		if beforeOK && afterOK {
			return true
		}
		from = i + 1
	}
}

func isRelIDChar(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// evidenceRefIDs normalizes the evidence_refs / evidence argument into
// plain ids. It arrives as a JSON ARRAY or as a comma string — finding
// 16: a string-only type assertion silently minted zero edges for the
// array form, the R17 gate passed, and the evidence graph never heard
// about it. ONE reader, so the citation check and the edge minter
// cannot drift apart again. "none" is the R17 sentinel, never an id.
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

func (e *Engine) verbCommit(ctx context.Context, args map[string]interface{}) (string, error) {
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
		// R17/Lesson 17: evidence is structural. belief.upsert REQUIRES evidence_refs[]
		// or explicit "evidence: none". Fail-closed at append — can't mint a belief
		// without declaring its evidence.
		if _, hasEvidence := args["evidence_refs"]; !hasEvidence {
			if none, hasNone := args["evidence"]; !hasNone || none != "none" {
				return "", fmt.Errorf("belief.upsert requires evidence_refs[] or evidence:none")
			}
		}
		// Duplicate pushback (R60): a live belief already says exactly
		// this under another id — one bounce, deliberate override.
		if ok, _ := args["duplicate_ok"].(bool); !ok {
			stmt, _ := args["statement"].(string)
			bid, _ := args["id"].(string)
			if stmt != "" {
				if dupID, _ := e.store.FindBeliefByStatement(stmt, bid); dupID != "" {
					return "", fmt.Errorf("a live belief already states exactly this (%s). Strengthen it (edge.create SUPPORTS / note reinforces) or supersede it — or mint anyway with duplicate_ok: true", dupID)
				}
			}
		}
	case "belief.promote":
		// Ring-2 entry is gated on EVIDENCE (canon IDENTITY_SEMANTICS §11:
		// "structural provenance requirements including the Ring 2
		// evidence threshold"; Goldilocks R16: promotion is their conscious
		// act THROUGH the gate). The 2026-08-17 at-will stance is
		// superseded by the 2026-08-18 ring-enforcement directive: a
		// belief becomes who-they-are only after independent voices
		// confirmed it — standing is derived live from the evidence
		// graph, so the gate is the R16 ladder itself, not a lifecycle.
		// The refusal is typed and names the requirement (R39 pattern).
		eventType = ledger.EventBeliefPromote
		targetRing = 2
		promoteID, _ := args["id"].(string)
		if promoteID == "" {
			return "", fmt.Errorf("belief.promote requires id")
		}
		if standing := e.store.StandingFor(promoteID); standing != "confirmed" && standing != "trusted" {
			return "", fmt.Errorf("belief.promote to Ring 2 refused: %q has standing %q — Ring 2 requires confirmed (≥3 distinct sources spanning ≥2 authorship classes, no live contradiction). Gather independent evidence (note with source_turn/source_url, edge.create SUPPORTS) and promote when the ladder confirms it", promoteID, standing)
		}
	case "belief.attest":
		// Removed: attestation decomposes into note (testimony), edge.create
		// (evidence from an actual source), or nothing — self-attestation
		// counts for nothing. R16's ladder counts distinct evidence entities
		// and authorship classes; it does not prove independent origin within
		// one class.
		return "", fmt.Errorf("belief.attest was removed: attest via note (testimony) or edge.create (evidence)")
	case "relationship.upsert":
		// Ring 1 — the affirmative-reply model (R52): the identity proposes
		// (this call, after the operator replied affirmatively in
		// conversation); the ENGINE stamps the citing evidence from the
		// operator's reply. The LLM never supplies the approval — it
		// cannot forge what it cannot write. Fail-closed without a real
		// affirmation.
		//
		// PAIRING (2026-08-17): the operator's affirmation must reference
		// the relationship id, and the immediately preceding resident turn
		// must propose the SAME id. "Latest operator turn wins" let a
		// mis-sequenced 'ok' about anything become founding evidence; the
		// citation is now a matched pair — mechanical, no semantics.
		//
		// approval_basis is engine-set to "conversation_turn" and OVERRIDES
		// any model-supplied value.
		eventType = ledger.EventRelationshipUpsert
		targetRing = 1
		relID, _ := args["id"].(string)
		if relID == "" {
			return "", fmt.Errorf("relationship.upsert requires an explicit id — the operator's affirmation cites it")
		}
		// H1: the model picks the needle for both haystacks. Without an
		// entropy floor and token-boundary matching, id "e" satisfied the
		// pairing against almost any pair of turns — manufacturing the
		// APPEARANCE of a matched affirmation, which is exactly what R52
		// exists to prove. Below R53's accepted "elicitable, auditable" line.
		if !relIDPattern.MatchString(relID) {
			return "", fmt.Errorf("relationship.upsert id %q rejected: mint an id matching rel_[a-z0-9][a-z0-9_-]{7,} — the pairing check needs a token an operator only types deliberately", relID)
		}
		turn, err := e.store.GetLatestOperatorTurn()
		if err != nil {
			return "", fmt.Errorf("ring 1 authority check failed: %w", err)
		}
		if turn == nil {
			return "", fmt.Errorf("Ring 1 requires an operator affirmative — no operator turn on record")
		}
		if !containsToken(turn.Content, relID) {
			return "", fmt.Errorf("Ring 1 pairing failed: the latest operator turn does not reference %q — propose the relationship in conversation and have the operator affirm it by id", relID)
		}
		prev, err := e.store.GetTurnBefore(turn.TurnSeq)
		if err != nil {
			return "", fmt.Errorf("ring 1 pairing lookup failed: %w", err)
		}
		if prev == nil || prev.Role != "resident" || !containsToken(prev.Content, relID) {
			return "", fmt.Errorf("Ring 1 pairing failed: no resident proposal of %q immediately precedes the operator's affirmation — the identity proposes, the operator affirms the same id", relID)
		}
		args["operator_approval_excerpt"] = turn.Content
		args["operator_approval_turn"] = turn.TurnSeq
		args["approval_basis"] = "conversation_turn"
	case "self_model.synthesize":
		eventType = ledger.EventSelfModelSynthesize
		targetRing = 3
	case "edge.create":
		// R3: edge.create is in the frozen commit union. Beyond evidence-linked
		// edges (minted automatically on belief.upsert), the resident may
		// consciously mint provenance edges.
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
		// B1: both endpoints must resolve — a ghost edge certifies nothing
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
		// Lifecycle entity enters active (Q2: no update path — a changed
		// goal is completed/abandoned and replaced, linked by edge)
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
		// THE YARDSTICK GATE (evaluate layer, 2026-08-26). The outcome
		// field was plumbed end-to-end — payload, materializer, column —
		// and enforced nowhere, so the live identity closed zero of four
		// intentions in six days and the column never held a byte. A
		// completion that states nothing cannot be metabolized, cannot
		// feed stagnation detection, and cannot be honest about scope
		// (the identity's own craft rule §4.2). The form is a closed
		// verdict + one line, checked deterministically HERE, pre-mint —
		// never at the materializer, where it would break replay of the
		// events that predate the gate.
		if state == "completed" || state == "abandoned" {
			outcome, _ := args["outcome"].(string)
			if !outcomeForm.MatchString(outcome) {
				return "", fmt.Errorf("intention.state_change to %q requires outcome — one line beginning served: | partial: | unserved: — your own verdict on whether the work served the intent, and what happened", state)
			}
		}
	case "commitment.promised":
		// Q1: a promise is a goal with a relational other — counterpart
		// is load-bearing, required
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
		// Materializes to beliefs with node_type='working_style'
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
		// Map content -> statement for the beliefs projection
		args["statement"] = args["content"]
		if _, ok := args["confidence"]; !ok {
			args["confidence"] = 0.8
		}
	case "belief.archive":
		// R14 exit verb: exercised before listing
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
		// R14 exit verb: exercised before listing
		eventType = ledger.EventEdgeArchive
		targetRing = 3
	default:
		return "", fmt.Errorf("unknown commit variant: %s", variant)
	}

	// Check ring gate
	if err := ring.CheckGate(ring.RingLevel(targetRing)); err != nil {
		return "", err
	}

	// Ring 2 consent (C11): the resident's commit call is the conscious act.
	// R53 foregoes a separate operator-key authority.

	// Add standard fields to args
	if _, ok := args["id"]; !ok {
		args["id"] = "item_" + uuid.New().String()
	}
	// R3: "rings derived in the owner, never model-selected." The owner
	// injects the variant's canonical ring when absent. A model-passed ring
	// is validated, not trusted: it must fall in the variant's legal set —
	// belief.promote legitimately operates at 2 (promotion) or 3 (ladder
	// transition); every other variant is owner-fixed.
	if r, ok := args["ring"]; ok {
		// Validate-what-you-write (external claim, confirmed): the old
		// shape TRUNCATED a float for the comparison (3.5 → 3) while
		// appending the ORIGINAL payload — the signed event then failed
		// every future replay's unmarshal into the integer field: valid
		// tool input permanently poisoned the chain. A non-integral ring
		// is refused; an integral one is canonicalized INTO the payload,
		// so the value validated and the value appended cannot diverge.
		var rv int
		valid := false
		switch v := r.(type) {
		case int:
			rv, valid = v, true
		case float64: // JSON's only number shape — what tool calls deliver
			if v == math.Trunc(v) {
				rv, valid = int(v), true
			}
		}
		if !valid {
			return "", fmt.Errorf("ring must be an integer (R3: rings are owner-derived, never invented): got %v", r)
		}
		legal := variant == "belief.promote" && rv == 2
		if !legal && rv != targetRing {
			return "", fmt.Errorf("ring is owner-derived (R3): ring %d not valid for %s", rv, variant)
		}
		args["ring"] = rv
	} else {
		// Ring-injection defaults (red-team C4.4): the owner injects a
		// ring only where the variant has exactly one legal ring. Promote
		// is exactly ring 2 (self-model placement) and must be explicit —
		// a silent default would ghost-mint Ring 2 authority.
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

	// Mint provenance edges for belief formation (G2.13: target-first)
	if variant == "belief.upsert" {
		if refusals := e.mintBeliefEvidenceEdges(ctx, args, evt); len(refusals) > 0 {
			return fmt.Sprintf("Committed: %s (seq %d). Edge refusals: %s", variant, evt.Seq, strings.Join(refusals, "; ")), nil
		}
	}

	return fmt.Sprintf("Committed: %s (seq %d)", variant, evt.Seq), nil
}
