package prompt

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

// A zero budget is never stored — the fold ladder at zero would omit every
// elastic section — so the composer and the gate read it, at construction
// and on a provider change, as the one declared default.
func TestAZeroBudgetIsTheDefaultNotAnEmptyPrompt(t *testing.T) {
	const want = wire.DefaultContextBudgetTokens
	if want != 32000 {
		t.Fatalf("the default prompt budget is %d, want 32000", want)
	}
	c := New(nil, 0)
	if got := c.MaxTokens(); got != want {
		t.Fatalf("composer built with 0: %d, want %d", got, want)
	}
	c.SetMaxTokens(100)
	c.SetMaxTokens(0)
	if got := c.MaxTokens(); got != want {
		t.Fatalf("composer set to 0: %d, want %d", got, want)
	}
	g := NewGate(fakeRings{}, 0)
	if got := int(g.maxTokens.Load()); got != want {
		t.Fatalf("gate built with 0: %d, want %d", got, want)
	}
	g.SetMaxTokens(100)
	g.SetMaxTokens(0)
	if got := int(g.maxTokens.Load()); got != want {
		t.Fatalf("gate set to 0: %d, want %d", got, want)
	}
}
