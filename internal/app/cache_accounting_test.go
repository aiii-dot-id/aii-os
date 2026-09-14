package app

import (
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"strings"
	"testing"
)

func TestCacheInterruptedMeterDoesNotRequireSpeech(t *testing.T) {
	a := &App{lastTurn: "previous-turn gauge"}
	a.recordInterruptedTurn(conversation.Result{Usage: conversation.TurnUsage{PromptTokens: 1000, CompletionTokens: 10, TotalTokens: 1010, CachedPromptTokens: 900, Calls: 2, Silent: 1}, Interrupted: "substrate failed"})
	if got := a.lastTurnCost(); !strings.Contains(got, "1010") {
		t.Fatalf("known partial usage was supplied, but last-turn gauge stayed %q because Spoken was empty", got)
	}
}
func TestCacheChildTargetKeepsOriginalClient(t *testing.T) {
	old := llm.New(&llm.ClientConfig{Endpoint: "https://old.invalid", Model: "old"})
	a := &App{composer: prompt.New(ring.NewManager(), 100), llmSwap: newSwappableLLM(old)}
	target := a.activeRunTarget(false, "")
	a.cfgMu.Lock()
	a.composer.SetMaxTokens(200)
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: "https://new.invalid", Model: "new"}))
	a.cfgMu.Unlock()
	if target.client != old || target.modelID != "old" || target.budget != 100 {
		t.Fatalf("existing child target changed after a resident switch: %+v", target)
	}
	next := a.activeRunTarget(false, "")
	if next.modelID != "new" || next.budget != 200 {
		t.Fatalf("next child did not pick up the new target: %+v", next)
	}
	t.Log("existing target retains old client and budget; next target uses new client and budget")
}
