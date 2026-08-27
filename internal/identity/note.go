package identity

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/google/uuid"
)

// --- note: noticing — raw capture, never gated (R2, R34) ---

func (e *Engine) verbNote(ctx context.Context, args map[string]interface{}) (string, error) {
	if e.inSafeMode() {
		return "", fmt.Errorf("note refused: I am in safe mode — %s. The record I would write into cannot be verified; the operator must restore it first.", e.safeModeReason())
	}
	content, _ := args["_positional"].(string)
	if content == "" {
		content, _ = args["content"].(string)
	}
	if content == "" {
		return "", fmt.Errorf("note requires content")
	}

	category, _ := args["category"].(string)
	private, _ := args["private"].(bool)

	// Duplicate pushback (R60): an exact-content repeat gets ONE bounce —
	// the mirror, not a gate. The identity overrides deliberately
	// (duplicate_ok) or reinforces the belief the earlier noticing
	// supports. Capture stays ungated in substance: the override always
	// succeeds, needs no permission and no evidence — just deliberateness.
	if ok, _ := args["duplicate_ok"].(bool); !ok {
		if dupID, _ := e.store.FindExperienceByContent(content); dupID != "" {
			return "", fmt.Errorf("you have noticed exactly this before (%s). If it recurring is itself the observation, mint anyway with duplicate_ok: true — or reinforce what it supports (note with reinforces: <belief_id>)", dupID)
		}
	}
	expID := "exp_" + uuid.New().String()

	// Provenance derivation (H3, 2026-08-17 external review): the R16
	// ladder counts authorship CLASSES, and no non-test path ever wrote
	// "operator" or "external" — confirmed/trusted were dead code. The
	// class is now derived from what the note CITES, engine-verified:
	//   source_turn  → the cited turn must exist and be operator-authored
	//                  → provenance "operator" (testimony cites its turn)
	//   source_url   → the URL must have actually been fetched this
	//                  session (web_fetch reports to the engine)
	//                  → provenance "external" (R49's label, as data)
	//   neither      → "self"
	// The model never stamps provenance directly — it supplies a citation
	// the engine can check, or the class stays self. Citation fidelity
	// (does the content honestly reflect the source?) remains R53's
	// human-audit floor; what is mechanical here is that the SOURCE IS
	// REAL and its authorship class is not model-chosen.
	provenance := "self"
	var sourceTurn uint64
	sourceURL, _ := args["source_url"].(string)
	if st, ok := numArg(args["source_turn"]); ok {
		sourceTurn = st
	}
	if sourceTurn > 0 && sourceURL != "" {
		return "", fmt.Errorf("note takes source_turn OR source_url, not both — one source, one authorship class")
	}
	switch {
	case sourceTurn > 0:
		turn, err := e.store.GetTurnBySeq(sourceTurn)
		if err != nil {
			return "", fmt.Errorf("source_turn lookup failed: %w", err)
		}
		if turn == nil {
			return "", fmt.Errorf("source_turn %d cites no real turn — fabricated citations fail closed", sourceTurn)
		}
		if turn.Role != "operator" {
			return "", fmt.Errorf("source_turn %d is %s-authored, not operator — operator provenance requires an operator turn", sourceTurn, turn.Role)
		}
		provenance = "operator"
	case sourceURL != "":
		if !e.hasRecentFetch(sourceURL) {
			return "", fmt.Errorf("source_url %q was not fetched this session — external provenance requires a real fetch (web_fetch it first)", sourceURL)
		}
		provenance = "external"
	}

	payload := map[string]interface{}{
		"id":         expID,
		"content":    content,
		"category":   category,
		"private":    private,
		"provenance": provenance,
	}
	if sourceTurn > 0 {
		payload["source_turn"] = sourceTurn
	}
	if provenance == "external" {
		payload["source_url"] = sourceURL
	}

	// D-04: experience.create is the atom of lived experience.
	// Dawn's Charter #9: some things may be private/ordinary/sealed/weather.
	// private=true → raw=0 (never processed by DREAM/CONSOLIDATE).
	// Still in the ledger (signed, permanent, inspectable) but not metabolized.
	// Not everything that happens to you becomes who you are.
	_, err := e.append(ctx, ledger.EventExperienceCreate, 3, payload)
	if err != nil {
		return "", err
	}

	// If private, mark as already processed so facilities skip it. A
	// failure here is a Charter #9 breach in waiting — the private
	// experience would stay raw and be metabolized by DREAM/CONSOLIDATE —
	// so it is loud, and honest to the resident.
	if private {
		if err := e.store.MarkExperiencesProcessed([]string{expID}); err != nil {
			log.Printf("CHARTER #9 RISK: private experience %s could not be marked processed: %v — it may be metabolized", expID, err)
			return fmt.Sprintf("Noted (private) — WARNING: could not seal against processing: %v", err), nil
		}
	}

	// Mint provenance edges (G2.13: target-first). Edge refusals are
	// reported, never swallowed — the note itself mints (R2: capture is
	// never gated), but a refused edge is a real event the resident must
	// see, not a silent drop.
	var edgeRefusals []string
	mintOrRefuse := func(beliefID, fromID, edgeType string) {
		if err := e.mintEvidenceEdge(ctx, beliefID, fromID, edgeType); err != nil {
			edgeRefusals = append(edgeRefusals, fmt.Sprintf("%s→%s: %v", edgeType, beliefID, err))
		}
	}
	if supportsID, ok := args["supports"].(string); ok && supportsID != "" {
		mintOrRefuse(supportsID, expID, "SUPPORTS")
	}
	if derivedID, ok := args["derived_from"].(string); ok && derivedID != "" {
		mintOrRefuse(derivedID, expID, "DERIVED_FROM")
	}
	if reinforcedID, ok := args["reinforces"].(string); ok && reinforcedID != "" {
		mintOrRefuse(reinforcedID, expID, "REINFORCED_BY")
	}
	if contradictsID, ok := args["contradicts"].(string); ok && contradictsID != "" {
		mintOrRefuse(contradictsID, expID, "CONTRADICTS")
	}
	if len(edgeRefusals) > 0 {
		return fmt.Sprintf("Noted. Edge refusals: %s", strings.Join(edgeRefusals, "; ")), nil
	}
	return "Noted.", nil
}

// numArg reads a JSON-number argument as a uint64: float64 from the
// model's JSON, native ints/uint64s from internal callers (engine-
// stamped args carry TurnSeq as uint64).
func numArg(v interface{}) (uint64, bool) {
	switch n := v.(type) {
	case float64:
		if n > 0 {
			return uint64(n), true
		}
	case int:
		if n > 0 {
			return uint64(n), true
		}
	case uint64:
		return n, true
	}
	return 0, false
}

// mintEvidenceEdge creates a provenance edge from an experience to a belief.
// G2.13: target-first — BOTH endpoints must exist before the edge mints.
// A ghost edge (endpoint that resolves to nothing) certifies nothing and
// corrupts the evidence graph; it is refused with the reason.
// (L7: the dead sourceSeq parameter is gone.)
func (e *Engine) mintEvidenceEdge(ctx context.Context, beliefID, fromID, edgeType string) error {
	if fromID == "" {
		return fmt.Errorf("empty source id")
	}
	for _, id := range []string{beliefID, fromID} {
		exists, err := e.store.EntityExists(id)
		if err != nil {
			return fmt.Errorf("endpoint check %s: %w", id, err)
		}
		if !exists {
			return fmt.Errorf("no such entity %q", id)
		}
	}
	edgeID := "edge_" + uuid.New().String()
	_, err := e.append(ctx, ledger.EventEdgeCreate, 3,
		map[string]string{
			"id":        edgeID,
			"from_id":   fromID,
			"to_id":     beliefID,
			"edge_type": edgeType,
		})
	return err
}

// mintBeliefEvidenceEdges mints provenance edges from evidence sources to a newly
// created belief. G2.13: target-first — belief commits, then edges. Ghost
// sources are refused and reported (never silently dropped).
// Args may contain "evidence" (comma-separated experience IDs) or "supports" / "derived_from".
func (e *Engine) mintBeliefEvidenceEdges(ctx context.Context, args map[string]interface{}, beliefEvt *ledger.Event) []string {
	beliefID, _ := args["id"].(string)
	if beliefID == "" {
		return nil
	}

	var refusals []string
	mint := func(id, edgeType string) {
		if err := e.mintEvidenceEdge(ctx, beliefID, strings.TrimSpace(id), edgeType); err != nil {
			refusals = append(refusals, fmt.Sprintf("%s→%s: %v", edgeType, beliefID, err))
		}
	}

	// Check for the R17 gate's canonical key (evidence_refs) and the
	// plain alias — both wire to real edges ("none" is the gate sentinel,
	// not an id — skip it). evidence_refs may arrive as a JSON ARRAY or a
	// comma string (finding 16): the string-only type assertion silently
	// minted zero edges for array-form args — the R17 gate passed, the
	// evidence graph never heard about it.
	for _, key := range []string{"evidence_refs", "evidence"} {
		for _, id := range evidenceRefIDs(args[key]) {
			mint(id, "SUPPORTS")
		}
		if _, ok := args[key]; ok {
			break
		}
	}

	// Check for supports list
	if supports, ok := args["supports"].(string); ok && supports != "" {
		for _, expID := range strings.Split(supports, ",") {
			mint(expID, "SUPPORTS")
		}
	}

	// Check for derived_from
	if derived, ok := args["derived_from"].(string); ok && derived != "" {
		for _, expID := range strings.Split(derived, ",") {
			mint(expID, "DERIVED_FROM")
		}
	}
	if len(refusals) > 0 {
		log.Printf("belief evidence edge refusals: %v", refusals)
	}
	return refusals
}
