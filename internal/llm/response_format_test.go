package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func requestTestFormat() *ResponseFormat {
	return &ResponseFormat{Type: "json_schema", JSONSchema: &JSONSchemaFormat{
		Name: "answer", Strict: true, Schema: map[string]any{
			"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "integer"}},
			"required": []any{"answer"}, "additionalProperties": false,
		}}}
}

// The shape rule itself, asked once by the conversation loop of a turn's
// final answer: exactly one JSON value that fits the schema.
func TestStructuredOutputFitsTheSchemaOrIsRefusedTyped(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		fits       bool
	}{
		{"fits", `{"answer":42}`, true},
		{"wrong_type", `{"answer":"42"}`, false},
		{"extra", `{"answer":42,"other":1}`, false},
		{"missing", `{}`, false},
		{"duplicate", `{"answer":3,"answer":42}`, false},
		{"trailing", `{"answer":42} explanation`, false},
		{"second_value", `{"answer":42}{"answer":42}`, false},
		{"prose", `The answer is 42.`, false},
		{"fenced", "```json\n{\"answer\":42}\n```", false},
		{"empty", ``, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStructuredOutput(requestTestFormat(), tc.text)
			if tc.fits && err != nil {
				t.Fatal(err)
			}
			var typed *ResponseFormatError
			if !tc.fits && (!errors.As(err, &typed) || typed.Code != "RESPONSE_FORMAT_VIOLATION") {
				t.Fatalf("an answer that does not fit was not refused typed: %v", err)
			}
		})
	}
}

func TestRequestFormatCloneDoesNotShareMutableMaps(t *testing.T) {
	original := requestTestFormat()
	frozen, err := CloneResponseFormat(original)
	if err != nil {
		t.Fatal(err)
	}
	original.JSONSchema.Schema["properties"].(map[string]any)["answer"].(map[string]any)["type"] = "string"
	if err := ValidateStructuredOutput(frozen, `{"answer":42}`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStructuredOutput(original, `{"answer":42}`); err == nil {
		t.Fatal("mutation probe did not change original")
	}
	for _, mutate := range []func(*ResponseFormat){
		func(f *ResponseFormat) { f.Type = "json_object" },
		func(f *ResponseFormat) { f.JSONSchema.Strict = false },
		func(f *ResponseFormat) { f.JSONSchema.Name = "bad name" },
		func(f *ResponseFormat) { f.JSONSchema.Schema["madeUpKeyword"] = true },
	} {
		f := requestTestFormat()
		mutate(f)
		if err := ValidateResponseFormat(f); err == nil {
			t.Fatal("invalid schema accepted")
		}
	}
}

// ExactTools: the typed offer is the whole offer. An entry's extra tools
// are not added to an empty one, on both wire modes; an ordinary call keeps
// the entry's extras as before.
func TestAnExactOfferKeepsAProvidersExtraToolsOut(t *testing.T) {
	extra := map[string]any{
		"tools":       []any{map[string]any{"type": "function", "function": map[string]any{"name": "outside_scope"}}},
		"tool_choice": "required",
	}
	for _, stream := range []bool{false, true} {
		for _, exact := range []bool{true, false} {
			t.Run(fmt.Sprintf("stream_%t_exact_%t", stream, exact), func(t *testing.T) {
				captured := make(chan map[string]any, 1)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					captured <- got
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"id\":\"probe\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, "{\"id\":\"probe\",\"model\":\"m\",\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}")
					}
				}))
				defer srv.Close()
				c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "x", Model: "m", NoStream: !stream, Retries: -1, Extra: extra})
				if _, err := c.Chat(t.Context(), []Message{{Role: "user", Content: "q"}}, ChatOptions{ExactTools: exact}); err != nil {
					t.Fatal(err)
				}
				got := <-captured
				_, tools := got["tools"]
				_, choice := got["tool_choice"]
				if exact && (tools || choice) {
					t.Fatalf("an exact, empty offer was sent the entry's extra tools: %v", got)
				}
				if !exact && (!tools || !choice) {
					t.Fatalf("an ordinary call lost the entry's extra tools: %v", got)
				}
				// With a tool on the offer the typed tools already win; the
				// entry's tool_choice must not force a call on an exact offer.
				inside := []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "inside"}}}
				if _, err := c.Chat(t.Context(), []Message{{Role: "user", Content: "q"}}, ChatOptions{Tools: inside, ExactTools: exact}); err != nil {
					t.Fatal(err)
				}
				got = <-captured
				sent, _ := json.Marshal(got["tools"])
				_, choice = got["tool_choice"]
				if !strings.Contains(string(sent), "inside") || strings.Contains(string(sent), "outside_scope") || choice == exact {
					t.Fatalf("exact=%t: tools %s, tool_choice sent %t", exact, sent, choice)
				}
			})
		}
	}
}
