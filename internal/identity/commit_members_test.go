package identity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestACommitCarryingAMemberItsTypeDoesNotDeclareIsRefused(t *testing.T) {
	e, _, lg, _ := citingEngine(t)
	ctx := context.Background()
	before := lg.LastSeq()
	_, err := e.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "intention.create", "id": "int_m1", "statement": "an invented aim", "mood": "bright",
	})
	var undeclared *ledger.UndeclaredMemberError
	if !errors.As(err, &undeclared) || !strings.Contains(err.Error(), `intention.create does not declare the member "mood", so nothing was recorded; its members are cites, id, model_id, ring, statement, variant, why`) {
		t.Fatalf("A COMMIT WITH AN UNDECLARED MEMBER WAS NOT REFUSED in the drafted words: %v", err)
	}
	if lg.LastSeq() != before {
		t.Fatalf("a refused commit was appended: seq %d, was %d", lg.LastSeq(), before)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "intention.create", "id": "int_m1", "statement": "an invented aim", "why": "an invented reason",
	}); err != nil {
		t.Fatalf("the same act with its declared members was refused: %v", err)
	}
	if lg.LastSeq() != before+1 {
		t.Fatalf("the admitted commit was not appended: seq %d", lg.LastSeq())
	}
}

// The positional word given to working_style.upsert is its content, and is
// no member of the record.
func TestAWorkingStylesPositionalWordIsItsContentAndIsNotSigned(t *testing.T) {
	e, _, lg, _ := citingEngine(t)
	ctx := context.Background()
	if _, err := e.ExecuteAction(ctx, "verb", "note", map[string]any{"content": "an invented observation of how work goes"}); err != nil {
		t.Fatal(err)
	}
	var noteID struct {
		ID string `json:"id"`
	}
	mustUnmarshal(t, lastOfType(t, lg, ledger.EventExperienceCreate).Payload, &noteID)
	if _, err := e.ExecuteAction(ctx, "verb", "commit", map[string]any{
		"variant": "working_style.upsert", "_positional": "an invented way of working", "evidence_refs": []any{noteID.ID},
	}); err != nil {
		t.Fatalf("a working style given by its positional word was refused: %v", err)
	}
	payload := string(lastOfType(t, lg, ledger.EventWorkingStyleUpsert).Payload)
	if strings.Contains(payload, "_positional") || !strings.Contains(payload, `"content":"an invented way of working"`) {
		t.Fatalf("the positional word was signed, or not read as the content: %s", payload)
	}
}
