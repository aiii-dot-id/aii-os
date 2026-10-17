package cognitive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

// captureDoor records every payload minted through it.
type captureDoor struct {
	payloads []map[string]any
}

func (d *captureDoor) Append(eventType ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error) {
	if m, ok := payload.(map[string]any); ok {
		d.payloads = append(d.payloads, m)
	}
	return &ledger.Event{}, nil
}

func TestVariantRefusalNamesWhatArrived(t *testing.T) {
	s := &SelfModelFacility{}
	tc := wire.ToolCall{ID: "c1", Type: "function"}
	tc.Function.Name = "commit"
	tc.Function.Arguments = `{"variant":"belief.update"}`
	resp := &wire.Response{Choices: []wire.Choice{{Message: wire.Message{ToolCalls: []wire.ToolCall{tc}}}}}
	err := s.applyResponse(context.Background(), resp, nil)
	if err == nil {
		t.Fatal("a wrong-variant commit was accepted")
	}
	if !strings.Contains(err.Error(), `received "belief.update"`) {
		t.Fatalf("the refusal does not name the arriving variant: %v", err)
	}
}

// A PORTRAIT IS NOT TAKEN FROM A REPLY THAT DID NOT FINISH — cut off at the
// output limit, declined, paused — even when what had arrived happens to
// parse, and even when it is the exact NO_CHANGE. And the refusal says
// why, so the corrective round is not told "contract violation" about a
// reply the provider cut.
func TestAPortraitIsNotTakenFromAnUnfinishedReply(t *testing.T) {
	s := &SelfModelFacility{}
	tc := wire.ToolCall{ID: "c1", Type: "function"}
	tc.Function.Name = "commit"
	tc.Function.Arguments = `{"variant":"self_model.synthesize"}`
	for name, choice := range map[string]wire.Choice{
		"a commit call in a reply cut off at the limit": {Message: wire.Message{ToolCalls: []wire.ToolCall{tc}}, FinishReason: "length"},
		"NO_CHANGE in a reply the provider declined":    {Message: wire.Message{Content: "NO_CHANGE"}, FinishReason: "refusal"},
	} {
		err := s.applyResponse(context.Background(), &wire.Response{Choices: []wire.Choice{choice}}, nil)
		var incomplete *wire.IncompleteResponseError
		if !errors.As(err, &incomplete) {
			t.Errorf("%s: got %v, want the refusal to say the reply was not finished", name, err)
		}
	}
}

func TestFailureExperienceCarriesBothRefusals(t *testing.T) {
	door := &captureDoor{}
	s := NewSelfModel(selfModelStore(), nil, nil) // the record is asked first whether it holds the experience
	s.SetDoor(door)

	first := errors.New("citation refused: item_x exists under beliefs")
	second := errors.New("commit variant must be self_model.synthesize")
	s.mintFailureExperience(first, second, "test-model")

	if len(door.payloads) != 1 {
		t.Fatalf("minted %d experiences, want 1", len(door.payloads))
	}
	content, _ := door.payloads[0]["content"].(string)
	if !strings.Contains(content, first.Error()) {
		t.Fatalf("first refusal missing from the record: %q", content)
	}
	if !strings.Contains(content, second.Error()) {
		t.Fatalf("second refusal missing from the record: %q", content)
	}
	id, _ := door.payloads[0]["id"].(string)
	if !strings.HasPrefix(id, "exp_facility_") {
		t.Fatalf("failure experience id lost its facility prefix: %q", id)
	}
}
