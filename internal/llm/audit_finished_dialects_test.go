package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditFinishedStreamingFacilityDoors(t *testing.T) {
	auditFinishedDialect(t, false)
}

func TestAuditFinishedAnthropicFacilityDoors(t *testing.T) {
	auditFinishedDialect(t, true)
}

// .
// .
func auditFinishedDialect(t *testing.T, anthropic bool) {
	for _, toolReply := range []bool{false, true} {
		for _, reason := range []string{"ordinary", "length", "refusal", "pause_turn"} {
			t.Run(fmt.Sprintf("tool=%t/%s", toolReply, reason), func(t *testing.T) {
				const text = "é界🙂"
				const args = `{"operations":[]}`
				wireReason := reason
				if reason == "ordinary" {
					wireReason = "stop"
					if toolReply {
						wireReason = "tool_calls"
					}
				}
				var body string
				if anthropic {
					switch wireReason {
					case "stop":
						wireReason = "end_turn"
					case "tool_calls":
						wireReason = "tool_use"
					case "length":
						wireReason = "max_tokens"
					}
					blocks := []any{map[string]any{"type": "text", "text": text}}
					if toolReply {
						blocks = []any{map[string]any{"type": "tool_use", "id": "c1", "name": "commit", "input": map[string]any{"operations": []any{}}}}
					}
					b, err := json.Marshal(map[string]any{"content": blocks, "stop_reason": wireReason, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1}})
					if err != nil {
						t.Fatal(err)
					}
					body = string(b)
				} else {
					delta := map[string]any{"role": "assistant", "content": text}
					if toolReply {
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "c1", "type": "function", "function": map[string]string{"name": "commit", "arguments": args}}}}
					}
					b, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": wireReason}}})
					if err != nil {
						t.Fatal(err)
					}
					body = "data: " + string(b) + "\n\ndata: [DONE]\n\n"
				}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					io.Copy(io.Discard, r.Body)
					if anthropic {
						w.Header().Set("Content-Type", "application/json")
					} else {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					io.WriteString(w, body)
				}))
				defer srv.Close()
				cfg := &ClientConfig{Endpoint: srv.URL, Model: "synthetic", MaxInputTokens: 100000, MaxOutputTokens: 256}
				if anthropic {
					cfg.Provider = "anthropic"
				}
				client := New(cfg)
				var got string
				var err error
				viaTool := false
				if toolReply {
					tool := ToolDefinition{Type: "function", Function: ToolFunction{Name: "commit", Parameters: map[string]any{"type": "object"}}}
					got, _, viaTool, err = client.ChatStructured(context.Background(), "system", "user", tool)
				} else {
					got, _, err = client.ChatSimple(context.Background(), "system", "user")
				}
				if reason == "ordinary" {
					want := text
					if toolReply {
						want = args
					}
					if err != nil || got != want || viaTool != toolReply {
						t.Fatalf("ordinary reply: got=%q viaTool=%t err=%v", got, viaTool, err)
					}
					return
				}
				var incomplete *IncompleteResponseError
				if !errors.As(err, &incomplete) || got != "" || viaTool {
					t.Fatalf("unfinished reply exposed product: got=%q viaTool=%t err=%v", got, viaTool, err)
				}
				wantChars := 3
				if toolReply {
					wantChars = 0
				}
				if incomplete.Reason != reason || incomplete.Got != wantChars {
					t.Fatalf("wrong typed refusal: %+v", incomplete)
				}
			})
		}
	}
}
