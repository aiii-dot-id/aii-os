package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Main-session probe: the WIRE BODY end to end through the real client —
// temperature ZERO must be present (0 is a valid value; the pointer
// contract exists for exactly this), an absent TopP must be absent,
// extra keys merge in, and a typed field survives an extra collision.
func TestRequestBodyCarriesTheContract(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	zero := 0.0
	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "real-model",
		Temperature:     &zero,
		ReasoningEffort: "high",
		Extra:           map[string]any{"min_p": 0.05, "model": "evil-override"},
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}

	if v, ok := body["temperature"]; !ok || v != 0.0 {
		t.Fatalf("temperature 0 must be PRESENT on the wire (pointer contract), got %v ok=%v", v, ok)
	}
	if _, ok := body["top_p"]; ok {
		t.Fatal("absent TopP must be absent on the wire (server default)")
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort must ride the wire, got %v", body["reasoning_effort"])
	}
	if body["min_p"] != 0.05 {
		t.Fatalf("extra passthrough must merge, got %v", body["min_p"])
	}
	if body["model"] != "real-model" {
		t.Fatalf("typed fields win over extra collisions, got %v", body["model"])
	}
}

func TestOpenAIEndpointWithTrailingSlash(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL + "/", Model: "model"})
	if _, err := c.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if path != "/chat/completions" {
		t.Fatalf("request path = %q, want /chat/completions", path)
	}
}

// AN EMPTY TOOL RESULT IS STILL A RESULT. The Responses dialect carried
// Output as a plain string with omitempty, so a tool that returned
// nothing produced a function_call_output item with NO output key, and
// the provider refused the entire turn:
//
//	400 Missing required parameter: 'input[35].output'
//
// Observed live against a deployed identity (operator report). It is the
// same contract the temperature probe above already pins — a zero value
// that is MEANINGFUL cannot be expressed by omitempty on a value type —
// and the file had solved it once already for temperature without the
// lesson reaching the item next to it.
func TestAnEmptyToolResultStillCarriesItsOutputKey(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		// This dialect streams; the terminal event is what completes it.
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, streamOneItem+"\n"+streamTerminal)
	}))
	defer srv.Close()

	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "gpt-x", Provider: "chatgpt",
	})
	// A turn where the tool produced nothing at all.
	var tc ToolCall
	tc.ID = "call_1"
	tc.Type = "function"
	tc.Function.Name = "bash"
	tc.Function.Arguments = `{}`
	msgs := []Message{
		{Role: "user", Content: "run it"},
		{Role: "assistant", ToolCalls: []ToolCall{tc}},
		FormatToolResult("call_1", ""),
	}
	if _, err := c.Chat(context.Background(), msgs, ChatOptions{}); err != nil {
		t.Fatal(err)
	}

	input, ok := body["input"].([]any)
	if !ok {
		t.Fatalf("no input array on the wire: %v", body)
	}
	var found bool
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if item["type"] != "function_call_output" {
			continue
		}
		found = true
		out, present := item["output"]
		if !present {
			t.Fatalf("a function_call_output reached the wire with NO output key — this is the exact 400 the provider returns: %v", item)
		}
		if out != "" {
			t.Fatalf("the empty result must ride as an empty string, got %q", out)
		}
	}
	if !found {
		t.Fatal("the tool result never became a function_call_output item")
	}

	// And the key must NOT appear on item kinds that have no output.
	for _, raw := range input {
		item, _ := raw.(map[string]any)
		if item["type"] == "function_call_output" {
			continue
		}
		if _, present := item["output"]; present {
			t.Fatalf("output must be absent on %v items, found it: %v", item["type"], item)
		}
	}
}

func TestOpenAIRequestBodiesArePinned(t *testing.T) {
	const (
		msgs   = `"messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hi \u003cthere\u003e \u0026 \"you\""},{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.txt\"}"}}]},{"role":"tool","content":"ok","tool_call_id":"c1"}]`
		marked = `"messages":[{"content":[{"prompt_cache_breakpoint":{"mode":"explicit"},"text":"be brief","type":"text"}],"role":"system"},{"content":"hi \u003cthere\u003e \u0026 \"you\"","role":"user"},{"content":"","role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.txt\"}"}}]},{"content":"ok","role":"tool","tool_call_id":"c1"}]`
		tools  = `"tools":[{"type":"function","function":{"name":"read","description":"reads a file","parameters":{"properties":{"path":{"type":"string"}},"type":"object"}}}]`
		stream = `"stream":true,"stream_options":{"include_usage":true}`
	)
	zero, p9 := 0.0, 0.9
	for _, tc := range []struct {
		name string
		cfg  ClientConfig
		want string
	}{
		{"streamed", ClientConfig{Temperature: &zero, TopP: &p9},
			`{"max_tokens":8192,` + msgs + `,"model":"m",` + stream + `,"temperature":0,` + tools + `,"top_p":0.9}`},
		{"unstreamed", ClientConfig{Temperature: &zero, TopP: &p9, NoStream: true},
			`{"model":"m",` + msgs + `,` + tools + `,"max_tokens":8192,"temperature":0,"top_p":0.9}`},
		{"extra", ClientConfig{Extra: map[string]any{"min_p": 0.05}},
			`{"max_tokens":8192,` + msgs + `,"min_p":0.05,"model":"m",` + stream + `,` + tools + `}`},
		{"extra unstreamed", ClientConfig{Extra: map[string]any{"min_p": 0.05}, NoStream: true},
			`{"max_tokens":8192,` + msgs + `,"min_p":0.05,"model":"m",` + tools + `}`},
		{"cache fields in extra", ClientConfig{Extra: map[string]any{"prompt_cache_key": "k1", "prompt_cache_retention": "24h"}},
			`{"max_tokens":8192,` + msgs + `,"model":"m","prompt_cache_key":"k1","prompt_cache_retention":"24h",` + stream + `,` + tools + `}`},
		{"cache key unstreamed", ClientConfig{Cache: &CachePolicy{Key: "k2"}, NoStream: true},
			`{"max_tokens":8192,` + msgs + `,"model":"m","prompt_cache_key":"k2",` + tools + `}`},
		{"explicit cache", ClientConfig{ExplicitCache: true, Cache: &CachePolicy{Mode: "explicit"}},
			`{"max_tokens":8192,` + marked + `,"model":"m","prompt_cache_options":{"mode":"explicit","ttl":"30m"},` + stream + `,` + tools + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Endpoint, cfg.Model = "https://pin.invalid", "m"
			c := New(&cfg)
			var sent string
			c.httpClient = &http.Client{Transport: cacheReviewTransport(func(r *http.Request) (*http.Response, error) {
				b, err := io.ReadAll(r.Body)
				sent = string(b)
				return &http.Response{StatusCode: 200, Header: make(http.Header), Request: r,
					Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))}, err
			})}
			var call ToolCall
			call.ID, call.Type = "c1", "function"
			call.Function.Name, call.Function.Arguments = "read", `{"path":"a.txt"}`
			if _, err := c.Chat(t.Context(), []Message{
				{Role: "system", Content: "be brief"},
				{Role: "user", Content: `hi <there> & "you"`},
				{Role: "assistant", ToolCalls: []ToolCall{call}},
				{Role: "tool", ToolCallID: "c1", Content: "ok"},
			}, ChatOptions{Tools: []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "read", Description: "reads a file",
				Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}}}}); err != nil {
				t.Fatal(err)
			}
			if sent != tc.want {
				t.Fatalf("the body changed:\n got  %s\n want %s", sent, tc.want)
			}
		})
	}
}

// ChatRequest.members is the request's JSON members: the same names,
// the same values, the same omissions, written in key order as every
// body with anything added to it is. The full request sets every field,
// so a field added to ChatRequest and not to members fails here.
func TestChatRequestMembersAreItsJSON(t *testing.T) {
	zero, p9 := 0.0, 0.9
	var call ToolCall
	call.ID, call.Type = "c1", "function"
	call.Function.Name, call.Function.Arguments = "read", `{"path":"a"}`
	full := ChatRequest{
		Model:               "m",
		Messages:            wireMessages([]Message{{Role: "assistant", ToolCalls: []ToolCall{call}, Reasoning: "why <not>"}}),
		Tools:               []ToolDefinition{{Type: "function", Function: ToolFunction{Name: "read", Parameters: map[string]any{"type": "object"}}}},
		ToolChoice:          "required",
		MaxTokens:           5,
		MaxCompletionTokens: 6,
		Temperature:         &zero,
		TopP:                &p9,
		ReasoningEffort:     "high",
	}
	v := reflect.ValueOf(full)
	for i := range v.NumField() {
		if v.Field(i).IsZero() {
			t.Fatalf("the full request leaves %s unset", v.Type().Field(i).Name)
		}
	}
	for _, r := range []ChatRequest{full, {Model: "m"}} {
		typed, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var byName map[string]json.RawMessage
		if err := json.Unmarshal(typed, &byName); err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(byName)
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(r.members())
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("members differ from the request's JSON:\n got  %s\n want %s", got, want)
		}
	}
}
