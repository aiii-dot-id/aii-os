package cognitive

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// sizedChat is SELF_MODEL's model with an input ceiling in characters of
// the user message: refused before sending over it, as the client refuses,
// and answering with the valid commit call under it.
type sizedChat struct {
	limit int
	sent  []string
}

func (m *sizedChat) over(user string) error {
	if len(user) > m.limit {
		return &wire.ContextLimitError{Required: len(user) / 3, Limit: m.limit / 3}
	}
	return nil
}

func (m *sizedChat) CheckWithTools(_ context.Context, _, user string, _ []wire.ToolDefinition) error {
	return m.over(user)
}

func (m *sizedChat) Chat(_ context.Context, messages []wire.Message, _ wire.ChatOptions) (*wire.Response, error) {
	user := messages[len(messages)-1].Content
	if err := m.over(user); err != nil {
		return nil, err
	}
	m.sent = append(m.sent, user)
	var call wire.ToolCall
	call.Type, call.Function.Name = "function", "commit"
	call.Function.Arguments = `{"variant":"self_model.synthesize","id":"syn_fit","synthesis_text":"I hold what I can see.","continuity_thread":"I remain.","source_entity_refs":[{"class":"beliefs","id":"b1"},{"class":"experiences","id":"x1"},{"class":"intentions","id":"i1"},{"class":"relationships","id":"rel1"}]}`
	return &wire.Response{Choices: []wire.Choice{{Message: wire.Message{ToolCalls: []wire.ToolCall{call}}}}, ModelID: "sized-chat"}, nil
}

func TestASelfModelPassWithABeliefListPastTheBudgetStillProducesAPortraitAndSaysWhatItLeftOut(t *testing.T) {
	st := selfModelStore()
	for i := 0; i < 300; i++ {
		st.beliefs = append(st.beliefs, store.Belief{ID: fmt.Sprintf("b%03d", i), Ring: 3,
			Statement: fmt.Sprintf("Belief %03d, at length: ", i) + strings.Repeat("a careful clause of what I hold. ", 10)})
	}
	st.beliefs = append(st.beliefs, store.Belief{ID: "b_style", Ring: 3, NodeType: "working_style", Statement: "I verify before I claim."})
	model := &sizedChat{limit: 12_000}
	committer := &mockSelfModelCommitter{}
	lg := &mockLedger{st: st}
	sm := NewSelfModel(st, model, committer)
	sm.SetDoor(rememberingLedger{lg})

	if err := sm.Execute(context.Background()); err != nil {
		t.Fatalf("THE PORTRAIT FROZE: %v", err)
	}
	if committer.calls != 1 || len(model.sent) != 1 {
		t.Fatalf("the pass commits a portrait from what fits: %d commit(s), %d request(s) sent", committer.calls, len(model.sent))
	}
	sent := model.sent[0]
	if !strings.Contains(sent, "of 302 beliefs shown") || !strings.Contains(sent, "recall (source=ledger)") {
		t.Fatalf("the request does not declare what it left out, with the route to it:\n%.500s", sent)
	}
	if !strings.Contains(sent, "[working_style id=b_style") {
		t.Fatalf("the beliefs shown lead with one of each class; the working style is missing:\n%.500s", sent)
	}
	told := toldIdentity(lg)
	if len(told) != 1 {
		t.Fatalf("the identity is told once what their portrait was not drawn from: %v", told)
	}
	for _, content := range told {
		if !strings.Contains(content, "of your 302 beliefs") {
			t.Fatalf("what the identity is told does not say how many of how many: %q", content)
		}
	}
}

// steppingOutcomes is the record's outcomes behind a cursor that moves.
type steppingOutcomes struct {
	all    []store.Outcome
	cursor uint64
}

func (s *steppingOutcomes) NextOutcomes(int) (store.OutcomeBatch, error) {
	b := store.OutcomeBatch{From: s.cursor, Through: s.cursor}
	for _, o := range s.all {
		if o.Seq > s.cursor {
			b.Outcomes = append(b.Outcomes, o)
			b.Through = o.Seq
		}
	}
	return b, nil
}
func (s *steppingOutcomes) PublishOutcomeCursor(_, through uint64) error {
	s.cursor = through
	return nil
}
func (s *steppingOutcomes) OwnFingerprint() (string, error) { return strings.Repeat("cd", 32), nil }

func TestAnOutcomeIntakeRefusedForWhatItAskedHalvesAndPassesOverOneSaid(t *testing.T) {
	src := &steppingOutcomes{}
	for seq := uint64(9); seq <= 12; seq++ {
		src.all = append(src.all, store.Outcome{Seq: seq, EntryHash: strings.Repeat("ab", 32), Kind: "intention", ID: fmt.Sprintf("i%d", seq), State: "completed", Was: "a thing", Said: "done"})
	}
	model := &mockLLM{override: strings.Repeat("too long. ", 20)}
	door := &captureDoor{}
	c := NewConsolidate(nil, model, door, nil, ConsolidateConfig{ObservationMaxChars: 50})
	c.SetOutcomes(src)
	var shown []int
	for i := 0; i < 12 && src.cursor < 12; i++ {
		before := model.calls
		_ = c.ObserveOutcomes(context.Background())
		if model.calls > before {
			shown = append(shown, strings.Count(model.lastUser, "- [record "))
		}
	}
	if len(shown) < 3 || fmt.Sprint(shown[:3]) != "[4 2 1]" {
		t.Fatalf("THE SAME INTAKE WAS ASKED AGAIN AS IT WAS: the outcomes shown per intake were %v; want 4, 2, then 1", shown)
	}
	if src.cursor != 12 {
		t.Fatalf("the intakes after a passed-over outcome reach the outcomes behind it: the cursor stands at %d of 12", src.cursor)
	}
	passed := 0
	for _, p := range door.payloads {
		if p["provenance"] == "system" && strings.Contains(fmt.Sprint(p["content"]), "no observation could be made of the outcome in record") {
			passed++
		}
	}
	if passed != 4 {
		t.Fatalf("each outcome passed over is told once, and it is the product that moved the cursor: %d tellings for 4 outcomes (%v)", passed, shown)
	}
	alone := 0
	for _, n := range shown {
		if n == 1 {
			alone++
		}
	}
	if alone != 4 {
		t.Fatalf("each outcome was shown alone once before it was passed over: %v", shown)
	}
}

// checkedOutcomeModel admits an intake of at most two outcomes.
type checkedOutcomeModel struct{ *mockLLM }

func (m checkedOutcomeModel) CheckSimple(_ context.Context, _, user string) error {
	if strings.Count(user, "- [record ") > 2 {
		return &wire.ContextLimitError{Required: 2, Limit: 1}
	}
	return nil
}

func TestAnOutcomeIntakeIsFittedAndItsCursorMovesOnlyThroughWhatWasShown(t *testing.T) {
	src := &steppingOutcomes{}
	for seq := uint64(9); seq <= 12; seq++ {
		src.all = append(src.all, store.Outcome{Seq: seq, EntryHash: strings.Repeat("ab", 32), Kind: "intention", ID: fmt.Sprintf("i%d", seq), State: "completed", Was: "a thing", Said: "done"})
	}
	model := checkedOutcomeModel{&mockLLM{override: nothingSurfaced}}
	c := NewConsolidate(nil, model, &captureDoor{}, nil, ConsolidateConfig{})
	c.SetOutcomes(src)
	if err := c.ObserveOutcomes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 || strings.Count(model.lastUser, "- [record ") != 2 || !strings.Contains(model.lastUser, "[record 9]") || !strings.Contains(model.lastUser, "[record 10]") {
		t.Fatalf("AN INTAKE OVER THE MODEL'S BUDGET WAS NOT FITTED: %d call(s)\n%s", model.calls, model.lastUser)
	}
	if src.cursor != 10 {
		t.Fatalf("the cursor moves only through what was shown: it stands at %d, want 10", src.cursor)
	}
}
