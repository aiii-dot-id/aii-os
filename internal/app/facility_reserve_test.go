package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// reserveRings is the gate's view of the rings with its own sizes: a
// constitution that is most of the window, and a working truth with no
// structure to fold, which can only be left out, declared.
type reserveRings struct{ r0, r3 string }

func (r reserveRings) Ring0() string { return r.r0 }
func (r reserveRings) Ring5() string { return "" }
func (r reserveRings) Ring3() string { return r.r3 }
func (r reserveRings) Ring4() string { return "" }

// windowModel admits a request by the client's own rule against a stated
// ceiling, asked before anything is sent, and answers with a note.
type windowModel struct {
	ceiling int
	calls   int
	system  string
	user    string
}

func (m *windowModel) admits(system, user string) error {
	_, err := llm.AdmitInput([]llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}}, nil, m.ceiling)
	return err
}
func (m *windowModel) CheckSimple(_ context.Context, system, user string) error {
	return m.admits(system, user)
}
func (m *windowModel) ChatSimple(_ context.Context, system, user string) (string, string, error) {
	if err := m.admits(system, user); err != nil {
		return "", "", err
	}
	m.calls++
	m.system, m.user = system, user
	return "You notice the pattern beneath what happened.", "window-model", nil
}
func (m *windowModel) ChatStructured(ctx context.Context, system, user string, _ llm.ToolDefinition) (string, string, bool, error) {
	text, model, err := m.ChatSimple(ctx, system, user)
	return text, model, false, err
}

// reserveDreamStore is DREAM's raw queue: three experiences waiting.
type reserveDreamStore struct{ raw []store.Experience }

func (s *reserveDreamStore) UnprocessedExperienceCount() (int, error) { return len(s.raw), nil }
func (s *reserveDreamStore) ListRawExperiences(n int) ([]store.Experience, error) {
	return s.raw[:min(n, len(s.raw))], nil
}
func (s *reserveDreamStore) ListRawExperiencesExcept(n int, _ string) ([]store.Experience, error) {
	return s.ListRawExperiences(n)
}

type reserveDoor struct{ seq uint64 }

func (d *reserveDoor) Append(t ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error) {
	d.seq++
	return &ledger.Event{Seq: d.seq, Type: t, Ring: ring}, nil
}

func TestTheRingsFillingTheWindowLeaveTheFacilityItsReserve(t *testing.T) {
	const window = 10_000
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rings := reserveRings{
		r0: "R0-CONSTITUTION " + strings.Repeat("word ", 4500), // ~7,500 tokens, verbatim
		r3: "R3-WORKING-TRUTH " + strings.Repeat("x", 4500),    // ~1,500 tokens, no unit to fold
	}
	gate := prompt.NewGate(rings, window)
	model := &windowModel{ceiling: window}
	queue := &reserveDreamStore{raw: []store.Experience{
		{ID: "e1", Content: "NOTED-e1 the build passed", Raw: 1},
		{ID: "e2", Content: "NOTED-e2 the operator asked why", Raw: 1},
		{ID: "e3", Content: "NOTED-e3 I answered with the log", Raw: 1},
	}}
	dream := cognitive.NewDream(queue, model, &reserveDoor{}, nil, cognitive.DreamConfig{Threshold: 1})
	dream.SetAuthority(ringAuthority{gate, st})

	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if model.calls != 1 {
		t.Fatalf("THE RINGS TOOK THE WHOLE WINDOW: %d call(s); the gate must leave DREAM its own prompt and its smallest request", model.calls)
	}
	if strings.Count(model.user, "NOTED-") != 3 {
		t.Fatalf("the pass carries the experiences that waited:\n%s", model.user)
	}
	if !strings.Contains(model.system, "R0-CONSTITUTION") || strings.Contains(model.system, "R3-WORKING-TRUTH") || !strings.Contains(model.system, "Not Shown") {
		t.Fatalf("Ring 0 stays verbatim and the working truth yields, declared:\n%.400s", model.system)
	}
}

func TestTheRouteToMoreRoomIsTrueOfTheBudgetInForce(t *testing.T) {
	const budget = 10_000
	for _, c := range []struct {
		name         string
		entry        providerEntry
		conversation int
		source       budgetSource
		route, never string
		card         string
	}{
		{"the model's window, under a narrower conversation ceiling", providerEntry{Name: "wide", ContextLength: budget + 1000 + promptSafetyTokens, MaxOutputTokens: 1000}, 5_000, budgetDerived,
			"ask your operator for a model with a wider window", "prompt budget", cognitive.WiderWindow},
		{"no window known: the operator's ceiling", providerEntry{Name: "dark"}, budget, budgetDeclared,
			"ask your operator to declare this model's context length, or to raise the prompt budget", "wider window", "prompt.max_tokens"},
	} {
		st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
		if err != nil {
			t.Fatal(err)
		}
		a := &App{}
		a.promptGate = prompt.NewGate(reserveRings{
			r0: "R0-CONSTITUTION " + strings.Repeat("word ", 4500),
			r3: "R3-WORKING-TRUTH " + strings.Repeat("x", 4500),
		}, 0)
		a.activateLLMRuntime(nil, c.entry, Config{Prompt: PromptConfig{MaxTokens: c.conversation}})
		if got, source := a.currentFacilityBudget(); got != budget || source != c.source {
			t.Fatalf("%s: rig: the rhythm's budget is %d (%s); want %d (%s)", c.name, got, source, budget, c.source)
		}
		model := &windowModel{ceiling: budget}
		dream := cognitive.NewDream(&reserveDreamStore{raw: []store.Experience{{ID: "e1", Content: "NOTED-e1 the build passed", Raw: 1}}}, model, &reserveDoor{}, nil, cognitive.DreamConfig{Threshold: 1})
		dream.SetAuthority(ringAuthority{a.promptGate, st})
		if err := dream.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		st.Close()
		if model.calls != 1 || !strings.Contains(model.system, "Not Shown") {
			t.Fatalf("%s: rig: the working truth yields, declared, and the pass is sent: %d call(s)\n%.400s", c.name, model.calls, model.system)
		}
		if !strings.Contains(model.system, c.route) || strings.Contains(model.system, c.never) {
			t.Errorf("%s: THE IDENTITY IS SENT TO ASK FOR WHAT CHANGES NOTHING: the omission must name %q and never %q:\n%s", c.name, c.route, c.never, model.system[strings.Index(model.system, "Not Shown"):])
		}
		if room := a.facilityRoom(); !strings.Contains(room, c.card) || (c.card != cognitive.WiderWindow && strings.Contains(room, cognitive.WiderWindow)) {
			t.Errorf("%s: the card's conditions name %q as what makes room; want %q", c.name, room, c.card)
		}
	}
}
