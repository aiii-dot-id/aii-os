package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// .
// .
// .
// .
// .
// .
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
		ThinkingBudget:  8192,
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

// .
// .
// .
// .
func TestConfiguredThinkingAppliesWithoutTheLoop(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer srv.Close()

	// .
	// .
	// .
	// .
	// .
	// .
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
	// .
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{ThinkingBudget: 3000}); err != nil {
		t.Fatal(err)
	}
	th, _ = body["thinking"].(map[string]any)
	if th["budget_tokens"] != 3000.0 {
		t.Fatalf("an explicit per-turn budget must win, got %v", th["budget_tokens"])
	}
}
