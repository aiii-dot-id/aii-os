package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .

func yieldFixture(t *testing.T) *App {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rm := ring.NewManager()
	_ = rm.SealSafePosture("test constitution")
	rm.Set(ring.Ring5, &ring.RingContent{Level: ring.Ring5, Content: "test floor"})
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a := New(&Config{
		SourcePath: filepath.Join(t.TempDir(), "config.json"),
		Prompt:     PromptConfig{MaxTokens: 32000, MaxToolResultChars: 32000},
		Agency:     AgencyConfig{MaxToolRounds: 2},
	})
	a.store = st
	a.toolReg = reg
	a.engine = identity.NewEngine(st, nil, rm, toolDiscovererAdapter{reg})
	return a
}

func yieldCall(args string) llm.ToolCall {
	var call llm.ToolCall
	call.Type = "function"
	call.Function.Name = "work"
	call.Function.Arguments = args
	return call
}

// .
// .
func TestYieldGateCountsYieldsAndASpentFleet(t *testing.T) {
	a := yieldFixture(t)
	if need, _ := a.yieldGate(); need {
		t.Fatal("a fresh ask must admit a yield")
	}
	a.noteYield(false)
	a.noteYield(true)
	a.noteYield(true)
	if need, why := a.yieldGate(); !need || !strings.Contains(why, "yield 3") {
		t.Fatalf("third yield must be gated: need=%v why=%q", need, why)
	}
	a.resetAsk()
	if need, _ := a.yieldGate(); need {
		t.Fatal("the operator speaking must reset the count")
	}
	a.markFleetSpent()
	if need, why := a.yieldGate(); !need || !strings.Contains(why, "unfinished or failed") {
		t.Fatalf("a spent fleet must gate the first yield: need=%v why=%q", need, why)
	}
	a.resetAsk()
	if need, _ := a.yieldGate(); need {
		t.Fatal("the operator speaking must clear the spent flag")
	}
}

// .
// .
func TestYieldRefusedWithoutAnswerAndCarriedWithOne(t *testing.T) {
	a := yieldFixture(t)
	a.engine.SetYieldGate(func() (bool, string) { return true, "the fleet is spent" })

	obs := a.executeToolCall(t.Context(), yieldCall(`{"action":"yield"}`))
	if !obs.Failed || obs.EndTurn {
		t.Fatalf("a silent yield under the gate must be refused, not end the turn: %+v", obs)
	}
	for _, want := range []string{"yield refused", "the fleet is spent", "answer="} {
		if !strings.Contains(obs.Text, want) {
			t.Errorf("refusal is missing %q: %s", want, obs.Text)
		}
	}

	obs = a.executeToolCall(t.Context(), yieldCall(`{"action":"yield","answer":"  Established: A and B. Missing: C. A green run of D would settle it. "}`))
	if obs.Failed || !obs.EndTurn {
		t.Fatalf("a yield with an answer must end the turn: %+v", obs)
	}
	if obs.EndTurnAnswer != "Established: A and B. Missing: C. A green run of D would settle it." {
		t.Fatalf("the answer did not travel to the loop trimmed: %q", obs.EndTurnAnswer)
	}

	a.engine.SetYieldGate(func() (bool, string) { return false, "" })
	if obs := a.executeToolCall(t.Context(), yieldCall(`{"action":"yield"}`)); obs.Failed || !obs.EndTurn {
		t.Fatalf("with the gate open a silent yield ends the turn as before: %+v", obs)
	}
}

// .
func TestYieldAnswerGate(t *testing.T) {
	f, tr := false, true
	for _, c := range []struct {
		name string
		v    *bool
		want bool
	}{{"absent is on", nil, true}, {"true is on", &tr, true}, {"false disarms", &f, false}} {
		if got := agencyOn(c.v); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
