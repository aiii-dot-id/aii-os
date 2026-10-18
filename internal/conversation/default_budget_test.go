package conversation

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// A zero context budget is never stored: the loop reads it, at
// construction and on a provider change, as the one declared default.
func TestAZeroContextBudgetIsTheDefault(t *testing.T) {
	if llm.DefaultContextBudgetTokens != 32000 {
		t.Fatalf("the default prompt budget is %d, want 32000", llm.DefaultContextBudgetTokens)
	}
	loop := New(&scriptLLM{}, &fakeTools{}, &fakeDefs{}, nil, nil, Config{})
	if got := loop.cfg.ContextBudgetTokens; got != llm.DefaultContextBudgetTokens {
		t.Fatalf("constructed with a zero budget: %d, want %d", got, llm.DefaultContextBudgetTokens)
	}
	loop.SetModelLimits(2000, 0)
	loop.SetModelLimits(0, 0)
	if got := loop.cfg.ContextBudgetTokens; got != llm.DefaultContextBudgetTokens {
		t.Fatalf("a provider change to a zero budget: %d, want %d", got, llm.DefaultContextBudgetTokens)
	}
}
