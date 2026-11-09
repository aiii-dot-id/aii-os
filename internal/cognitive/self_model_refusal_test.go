package cognitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
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

// refuseOnce refuses the first commit with err and admits the next.
type refuseOnce struct {
	calls int
	err   error
}

func (r *refuseOnce) Definition() wire.ToolDefinition {
	return wire.ToolDefinition{Type: "function", Function: wire.ToolFunction{Name: "commit"}}
}

func (r *refuseOnce) Commit(context.Context, map[string]any) (string, error) {
	if r.calls++; r.calls == 1 {
		return "", r.err
	}
	return "Committed: self_model.synthesize", nil
}

// A portrait refused for its length is told how much to cut, and to what:
// told only its count and the limit, a live identity's first pass under the
// request count (2026-11-08) cut 14 of its 35 tokens over and was refused
// again. The target is the prompt's own, 85 % of the limit at three counts
// to a token; the cut is the draft's counts less it, to the nearest hundred.
func TestAPortraitRefusedForLengthIsToldHowMuchToCut(t *testing.T) {
	m := &mockLLM{overrides: []string{"", ""}}
	committer := &refuseOnce{err: fmt.Errorf("self_model: commit: %w", &store.SelfModelRefusal{Requirement: "bounded narrative",
		Detail: "synthesis_text is approximately 2021 tokens; maximum is 2000 (prompt.self_model_max_tokens)", Tokens: 2021, Limit: 2000})}
	if err := NewSelfModel(selfModelStore(), m, committer).Execute(context.Background()); err != nil {
		t.Fatalf("the corrected pass must succeed: %v", err)
	}
	if len(m.allMessages) != 2 {
		t.Fatalf("%d requests, want the first and one corrective round", len(m.allMessages))
	}
	second := m.allMessages[1]
	last := second[len(second)-1].Content
	want := "Your synthesis_text was about 2021 tokens, over the limit of 2000: shorten it by about 1000 characters, to about 5100, and send the whole call again."
	if !strings.Contains(last, want) || !strings.Contains(last, "maximum is 2000") {
		t.Fatalf("the corrective round does not say how much to cut:\n%s", last)
	}
}

// Any other refusal is passed on as it is, with no sentence about length.
func TestARefusalNotForLengthGetsNoCut(t *testing.T) {
	m := &mockLLM{overrides: []string{"", ""}}
	committer := &refuseOnce{err: fmt.Errorf("self_model: commit: %w", &store.SelfModelRefusal{Requirement: "source coverage", Detail: "three classes cited; four are required"})}
	if err := NewSelfModel(selfModelStore(), m, committer).Execute(context.Background()); err != nil {
		t.Fatalf("the corrected pass must succeed: %v", err)
	}
	second := m.allMessages[1]
	if last := second[len(second)-1].Content; strings.Contains(last, "shorten it") || !strings.Contains(last, "source coverage") {
		t.Fatalf("a refusal not for length was given a cut, or lost: %s", last)
	}
}
