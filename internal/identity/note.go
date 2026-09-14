package identity

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/google/uuid"
)

// .

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

	// .
	// .
	// .
	// .
	// .
	if ok, _ := args["duplicate_ok"].(bool); !ok {
		// .
		// .
		dupID, derr := e.store.FindExperienceByContent(content)
		if derr != nil {
			return "", fmt.Errorf("the duplicate check could not run, so nothing was noticed: %w", derr)
		}
		if dupID != "" {
			return "", fmt.Errorf("you have noticed exactly this before (%s). If it recurring is itself the observation, mint anyway with duplicate_ok: true — or reinforce what it supports (note with reinforces: <belief_id>)", dupID)
		}
	}
	expID := "exp_" + uuid.New().String()

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

	// .
	// .
	// .
	// .
	// .
	_, err := e.append(ctx, ledger.EventExperienceCreate, 3, payload)
	if err != nil {
		return "", err
	}

	// .
	// .
	// .
	// .
	if private {
		if err := e.store.MarkExperiencesProcessed([]string{expID}); err != nil {
			log.Printf("CHARTER #9 RISK: private experience %s could not be marked processed: %v — it may be metabolized", expID, err)
			return fmt.Sprintf("Noted (private) — WARNING: could not seal against processing: %v", err), nil
		}
	}

	// .
	// .
	// .
	// .
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

// .
// .
// .
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

// .
// .
// .
// .
// .
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

// .
// .
// .
// .
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

	// .
	// .
	// .
	// .
	// .
	// .
	for _, key := range []string{"evidence_refs", "evidence"} {
		for _, id := range evidenceRefIDs(args[key]) {
			mint(id, "SUPPORTS")
		}
		if _, ok := args[key]; ok {
			break
		}
	}

	// .
	if supports, ok := args["supports"].(string); ok && supports != "" {
		for _, expID := range strings.Split(supports, ",") {
			mint(expID, "SUPPORTS")
		}
	}

	// .
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
