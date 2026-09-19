package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
)

// .
// .
// .
// .
// .
// .
func TestAdoptingAWindowlessProviderTellsTheLoop(t *testing.T) {
	a := &App{conv: conversation.New(nil, &appToolExecutor{}, appToolDefiner{}, nil, nil, conversation.Config{})}

	a.activateLLMRuntime(nil, providerEntry{Name: "Local (oMLX)"}, 0)
	if !a.conv.ContextBudgetFallback() {
		t.Fatal("a provider with no context_length was adopted and the loop was not told its budget is a guess")
	}

	a.activateLLMRuntime(nil, providerEntry{Name: "Claude", ContextLength: 200000, MaxOutputTokens: 8192}, 0)
	if a.conv.ContextBudgetFallback() {
		t.Fatal("a provider with a declared window was adopted and the loop still calls its budget a guess")
	}

	a.activateLLMRuntime(nil, providerEntry{Name: "Local (oMLX)"}, 64000)
	if a.conv.ContextBudgetFallback() {
		t.Fatal("the operator's own prompt.max_tokens is not a guess")
	}
}
