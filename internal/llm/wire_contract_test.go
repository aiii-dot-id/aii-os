package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// EVERY configured field must reach the wire — and nothing must be sent
// that the dialect does not define. The older probe covered four fields
// and passed while a fifth, thinking_budget, was emitted onto the
// OpenAI path where no provider defines it: it rendered, saved,
// persisted, displayed, and did nothing. Offering a control that cannot
// work is worse than not offering it.
func TestEveryConfiguredFieldReachesTheWire(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	temp, topP := 0.25, 0.75
	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "cfg-model",
		MaxOutputTokens: 4096,
		Temperature:     &temp,
		TopP:            &topP,
		ThinkingBudget:  8192, // anthropic-only; must NOT appear here
		ReasoningEffort: "high",
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		field string
		want  any
		set   string
	}{
		{"model", "cfg-model", "substrate: MODEL"},
		{"max_tokens", 4096.0, "provider: MAX OUTPUT TOKENS"},
		{"temperature", 0.25, "provider: TEMPERATURE"},
		{"top_p", 0.75, "provider: TOP_P"},
		{"reasoning_effort", "high", "provider: REASONING EFFORT"},
	} {
		got, ok := body[tc.field]
		if !ok {
			t.Errorf("%q never reached the wire — the operator sets it at %q and nothing happens", tc.field, tc.set)
			continue
		}
		if got != tc.want {
			t.Errorf("%q = %v, want %v (set at %q)", tc.field, got, tc.want, tc.set)
		}
	}
	if _, present := body["thinking_budget"]; present {
		t.Error("thinking_budget was sent on the OpenAI path — no OpenAI-shaped provider defines it, so offering the control there is a promise we cannot keep")
	}
}

// A client built from CONFIG ALONE must think as configured. The
// conversation loop threads a live per-turn value, but birth, sub-agents
// and ChatSimple pass zero — and zero used to mean "do not think" rather
// than "use what the operator set".
func TestConfiguredThinkingAppliesWithoutTheLoop(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	// ThinkingMode "budget" pins this to the pre-4.6 shape deliberately:
	// the invariant under test is that a CONFIGURED budget reaches the
	// wire and that a per-turn value overrides it, and budget_tokens is
	// the only shape where a number is on the wire at all. The default
	// (adaptive) carries no budget by design and is covered in
	// TestAnthropicThinkingModeShapes.
	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "claude-x",
		Provider: "anthropic", MaxOutputTokens: 20000, ThinkingBudget: 8192,
		ThinkingMode: "budget",
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	th, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("the anthropic dialect must carry the configured thinking budget, got %v", body)
	}
	if th["budget_tokens"] != 8192.0 {
		t.Fatalf("budget_tokens = %v, want 8192", th["budget_tokens"])
	}
	// And an explicit per-turn value still wins over the configured one.
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{ThinkingBudget: 3000}); err != nil {
		t.Fatal(err)
	}
	th, _ = body["thinking"].(map[string]any)
	if th["budget_tokens"] != 3000.0 {
		t.Fatalf("an explicit per-turn budget must win, got %v", th["budget_tokens"])
	}
}
