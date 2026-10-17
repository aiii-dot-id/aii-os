package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestModelIsToldAtBootAndOnChange(t *testing.T) {
	a := &App{cfg: &Config{LLM: LLMConfig{Provider: "Local (oMLX)"}}}
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "GLM-5.3-MLX-mixed-4_8bit"}))

	first := a.modelNotice()
	if !strings.Contains(first, "GLM-5.3-MLX-mixed-4_8bit") || !strings.Contains(first, "Local (oMLX)") {
		t.Fatalf("the first turn after boot does not name the model and its provider: %q", first)
	}
	if again := a.modelNotice(); again != first {
		t.Fatalf("an undelivered notice changed before any turn succeeded: %q then %q", first, again)
	}
	a.markComposedHarvests("") // a turn succeeded and its facts were folded without the notice
	if n := a.modelNotice(); n != first {
		t.Fatalf("a notice no turn carried was marked told: %q", n)
	}
	a.markComposedHarvests(first) // the turn succeeded and carried it: the notice is delivered
	if n := a.modelNotice(); n != "" {
		t.Fatalf("a delivered notice was repeated: %q", n)
	}

	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Model: "stealth/space-bunny-alpha"}))
	changed := a.modelNotice()
	if !strings.Contains(changed, "GLM-5.3-MLX-mixed-4_8bit") || !strings.Contains(changed, "stealth/space-bunny-alpha") {
		t.Fatalf("a model change does not name both models: %q", changed)
	}
	a.markComposedHarvests(changed)
	if n := a.modelNotice(); n != "" {
		t.Fatalf("a delivered change was repeated: %q", n)
	}

	// The same model under another provider entry is a different source.
	a.cfg = &Config{LLM: LLMConfig{Provider: "OpenRouter"}}
	if n := a.modelNotice(); !strings.Contains(n, "OpenRouter") {
		t.Fatalf("a provider change was not told: %q", n)
	}
}

// With no live client there is nothing true to say, and nothing is said.
func TestNoModelNoticeWithoutAClient(t *testing.T) {
	a := &App{cfg: &Config{}}
	if n := a.modelNotice(); n != "" {
		t.Fatalf("a notice without a client: %q", n)
	}
}

// A notice composed for a turn that FAILED is not delivered by a later
// turn that succeeded without it: if the client in between names no
// model, the pending notice is dropped, and the identity is told when
// that model comes back.
func TestAnUndeliveredNoticeIsNotMarkedTold(t *testing.T) {
	a := &App{cfg: &Config{LLM: LLMConfig{Provider: "P"}}}
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "A"}))
	a.markComposedHarvests(a.modelNotice()) // told A
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Model: "B"}))
	if n := a.modelNotice(); !strings.Contains(n, "B") {
		t.Fatalf("the change to B was not composed: %q", n)
	} // ...and that turn fails: no markComposedHarvests
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Model: ""}))
	if n := a.modelNotice(); n != "" {
		t.Fatalf("a client with no model name produced a notice: %q", n)
	}
	a.markComposedHarvests("") // a turn succeeded WITHOUT the B notice
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Model: "B"}))
	if n := a.modelNotice(); !strings.Contains(n, "B") {
		t.Fatalf("B was never told, yet counts as told: %q", n)
	}
}
