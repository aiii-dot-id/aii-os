package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestTheFacilitiesBudgetIsTheWindowsAndTheConversationsCeilingDoesNotNarrowIt(t *testing.T) {
	entry := providerEntry{Name: "wide", ContextLength: 200_000, MaxOutputTokens: 8192}
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	a := New(cfg)
	conversation := a.rememberPromptBudget(entry, 20_000)
	facility, source := a.currentFacilityBudget()
	if conversation != 20_000 {
		t.Fatalf("rig: the conversation is held to the operator's ceiling, got %d", conversation)
	}
	if want := 200_000 - 8192 - promptSafetyTokens; facility != want || source != budgetDerived {
		t.Fatalf("THE FACILITIES BORROWED THE CONVERSATION'S CEILING: %d (%s); want the window's %d (derived)", facility, source, want)
	}

	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Provider: "openai", Model: "m", MaxInputTokens: conversation}))
	door := facilityLLM{a.llmSwap, a}
	big := strings.Repeat("an experience worth consolidating. ", 3500) // ~40,000 tokens
	var over *llm.ContextLimitError
	if err := a.llmSwap.CheckSimple(context.Background(), "system", big); !errors.As(err, &over) {
		t.Fatalf("rig: the conversation's client refuses a request over its ceiling: %v", err)
	}
	if err := door.CheckSimple(context.Background(), "system", big); err != nil {
		t.Fatalf("a facility's request inside the window was refused by the conversation's ceiling: %v", err)
	}
	if err := door.CheckWithTools(context.Background(), "system", big, nil); err != nil {
		t.Fatalf("the facility door's tool check holds the conversation's ceiling: %v", err)
	}

	// No window known: the conversation's number is the only one there is.
	unknown, src := facilityBudgetFor(providerEntry{Name: "dark"}, 32_000, budgetFallback)
	if unknown != 32_000 || src != budgetFallback {
		t.Fatalf("with no window known the facilities take the conversation's number and its source: %d (%s)", unknown, src)
	}
}

func TestWhatAPassCameToReachesTheCognitionCard(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	a := New(cfg)
	if got := a.configState().Cognition.Conditions; len(got) != 0 {
		t.Fatalf("rig: nothing said yet, got %v", got)
	}
	a.noteCondition(cognitive.Condition{Facility: "consolidate", Kind: cognitive.ConditionFitted, Said: "12 of 600 current beliefs were shown"})
	a.noteCondition(cognitive.Condition{Facility: "outcomes", Kind: cognitive.ConditionSetAside, Said: "the outcome in record 9 was passed over"})
	got := a.configState().Cognition.Conditions
	if len(got) != 2 || got[0].Facility != "consolidate" || got[0].Kind != "fitted" || !strings.Contains(got[0].Said, "12 of 600") || got[0].At == "" || got[1].Kind != "set_aside" {
		t.Fatalf("the card does not carry what the passes came to: %+v", got)
	}
	a.noteCondition(cognitive.Condition{Facility: "consolidate", Kind: cognitive.ConditionWhole, Said: "the last pass carried all it was asked"})
	if got := a.configState().Cognition.Conditions; len(got) != 1 || got[0].Facility != "outcomes" {
		t.Fatalf("a whole pass clears its facility's line: %+v", got)
	}
}
