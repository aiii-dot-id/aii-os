package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// WHAT A SERVER RETURNS BESIDE A TOOL CALL GOES BACK WITH THE CALL.
// Google's OpenAI-compatible endpoint carries a Gemini 3 model's thought
// signature in the call's extra_content, on the first call of a step,
// and refuses the turn's next request with a 400 when the call returns
// without it ("Function call … is missing a thought_signature"). The
// chat dialect read a call into three members and dropped the fourth, so
// every tool-using turn on that endpoint failed at its second request.
// Streamed and whole, the member is kept as it came and sent back as it
// was; a call that had none gains none, and a null is none.
func TestAToolCallsExtraContentGoesBackWithIt(t *testing.T) {
	const signature = `{"google":{"thought_signature":"U0lHLUE="}}`
	streamed := `data: {"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"function-call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":"},"extra_content":` + signature + `}]}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}},{"index":1,"id":"function-call-2","type":"function","function":{"name":"weather","arguments":"{\"city\":\"London\"}"},"extra_content":null}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	whole := `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"function-call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"},"extra_content":` + signature + `},{"id":"function-call-2","type":"function","function":{"name":"weather","arguments":"{\"city\":\"London\"}"},"extra_content":null}]},"finish_reason":"tool_calls"}]}`
	for name, tc := range map[string]struct {
		reply    string
		noStream bool
	}{"streamed": {streamed, false}, "whole": {whole, true}} {
		t.Run(name, func(t *testing.T) {
			var bodies []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				bodies = append(bodies, string(body))
				if !tc.noStream {
					w.Header().Set("Content-Type", "text/event-stream")
				}
				io.WriteString(w, tc.reply)
			}))
			defer srv.Close()
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "gemini-fixture", NoStream: tc.noStream})
			convo := []Message{{Role: "user", Content: "Weather in Paris and London?"}}
			resp, err := c.Chat(context.Background(), convo, ChatOptions{})
			if err != nil {
				t.Fatal(err)
			}
			calls := resp.Choices[0].Message.ToolCalls
			if len(calls) != 2 || string(calls[0].ExtraContent) != signature || calls[1].ExtraContent != nil || calls[0].Function.Arguments != `{"city":"Paris"}` {
				t.Fatalf("the calls read as %+v", calls)
			}
			convo = append(convo, resp.Choices[0].Message, FormatToolResult(calls[0].ID, "15C"), FormatToolResult(calls[1].ID, "12C"))
			if _, err := c.Chat(context.Background(), convo, ChatOptions{}); err != nil {
				t.Fatal(err)
			}
			var sent struct {
				Messages []struct {
					ToolCalls []json.RawMessage `json:"tool_calls"`
				} `json:"messages"`
			}
			if err := json.Unmarshal([]byte(bodies[1]), &sent); err != nil || len(sent.Messages) != 4 || len(sent.Messages[1].ToolCalls) != 2 {
				t.Fatalf("the second request: %v\n%s", err, bodies[1])
			}
			const first = `{"id":"function-call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"},"extra_content":` + signature + `}`
			const second = `{"id":"function-call-2","type":"function","function":{"name":"weather","arguments":"{\"city\":\"London\"}"}}`
			if got := sent.Messages[1].ToolCalls; string(got[0]) != first || string(got[1]) != second {
				t.Errorf("the calls went back as\n%s\n%s\nwant\n%s\n%s", got[0], got[1], first, second)
			}
			if strings.Count(bodies[1], "extra_content") != 1 {
				t.Errorf("extra_content rides once, on the call it came with:\n%s", bodies[1])
			}
		})
	}
}
