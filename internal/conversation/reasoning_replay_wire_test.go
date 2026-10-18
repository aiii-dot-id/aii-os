package conversation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// End to end through a real client: the second request of a turn carries
// the first call's reasoning as reasoning_content only when the client's
// provider entry replays it, and the turn's record keeps it either way.
func TestTheSecondRequestCarriesReasoningOnlyWhenReplayed(t *testing.T) {
	replies := []string{
		`{"id":"a","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}],"reasoning_content":"read it first"},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`{"id":"b","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`,
	}
	for _, replay := range []bool{false, true} {
		var mu sync.Mutex
		var bodies []map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			mu.Lock()
			n := len(bodies)
			bodies = append(bodies, body)
			mu.Unlock()
			if n >= len(replies) {
				http.Error(w, "script exhausted", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(replies[n]))
		}))
		client := llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "m", NoStream: true, ReplayReasoning: replay})
		loop := New(client, &fakeTools{results: map[string]string{"read": "ok"}}, &fakeDefs{}, nil, nil, Config{ContextBudgetTokens: 100000})
		res, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}})
		srv.Close()
		if err != nil {
			t.Fatalf("replay=%v: %v", replay, err)
		}
		if len(bodies) != 2 {
			t.Fatalf("replay=%v: %d requests, want 2", replay, len(bodies))
		}
		msgs, _ := bodies[1]["messages"].([]any)
		var sent any
		found := false
		for _, m := range msgs {
			mm, _ := m.(map[string]any)
			if _, calls := mm["tool_calls"]; calls {
				sent, found = mm["reasoning_content"], true
			}
		}
		if !found {
			t.Fatalf("replay=%v: the second request lacks the first call's message", replay)
		}
		if replay && sent != "read it first" {
			t.Fatalf("replay on: the second request carried reasoning_content %v", sent)
		}
		if !replay && sent != nil {
			t.Fatalf("replay off: the second request carried reasoning_content %v", sent)
		}
		if len(res.Reasoning) != 1 || res.Reasoning[0].Text != "read it first" {
			t.Fatalf("replay=%v: the turn's record: %+v", replay, res.Reasoning)
		}
	}
}
