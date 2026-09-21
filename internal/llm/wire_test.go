package llm

// .
// .
// .
// .

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func f64(v float64) *float64 { return &v }

// .
// .
func captureServer(t *testing.T, response string) (*httptest.Server, *map[string]any) {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

const openAIResp = `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`
const anthResp = `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`

// .
// .
// .
func TestOpenAIWireSamplingAndExtra(t *testing.T) {
	srv, got := captureServer(t, openAIResp)
	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "m",
		Temperature: f64(0), TopP: f64(0.9),
		Extra: map[string]any{
			"repetition_penalty": 1.05,
			"model":              "OVERRIDE",
		},
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	b := *got
	if v, ok := b["temperature"].(float64); !ok || v != 0 {
		t.Fatalf(`"temperature": 0 must be SENT when set to zero, got %v (present %v)`, b["temperature"], ok)
	}
	if v, ok := b["top_p"].(float64); !ok || v != 0.9 {
		t.Fatalf("top_p must ride, got %v", b["top_p"])
	}
	if v, ok := b["repetition_penalty"].(float64); !ok || v != 1.05 {
		t.Fatalf("extra keys must merge verbatim, got %v", b["repetition_penalty"])
	}
	if b["model"] != "m" {
		t.Fatalf("typed fields must win over extra on collision, got model=%v", b["model"])
	}
}

// .
// .
func TestOpenAIWireOmitsUnsetSampling(t *testing.T) {
	srv, got := captureServer(t, openAIResp)
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	b := *got
	if _, present := b["temperature"]; present {
		t.Fatalf("unset temperature must be ABSENT, got %v", b["temperature"])
	}
	if _, present := b["top_p"]; present {
		t.Fatalf("unset top_p must be ABSENT, got %v", b["top_p"])
	}
}

// .
// .
func TestAnthropicWireSamplingNoExtra(t *testing.T) {
	srv, got := captureServer(t, anthResp)
	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic",
		Temperature: f64(0.3),
		Extra:       map[string]any{"repetition_penalty": 1.05},
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	b := *got
	if v, ok := b["temperature"].(float64); !ok || v != 0.3 {
		t.Fatalf("anthropic temperature must ride the typed request, got %v", b["temperature"])
	}
	if _, present := b["repetition_penalty"]; present {
		t.Fatal("extra must NOT apply on the anthropic path (typed API)")
	}
}

// .
// .
// .
// .
// .
func TestUnknownUsageIsNotZeroUsage(t *testing.T) {
	const okAnth = `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"`
	for _, tc := range []struct {
		name         string
		provider     string
		body         string
		wantReported bool
		wantTotal    int
	}{
		{"openai reports usage", "", `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`, true, 16},
		{"openai reports a GENUINE zero", "", `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, true, 0},
		{"openai omits usage entirely", "", `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`, false, 0},
		{"anthropic reports usage", "anthropic", okAnth + `,"usage":{"input_tokens":9,"output_tokens":3}}`, true, 12},
		{"anthropic omits usage", "anthropic", okAnth + `}`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: tc.provider})
			resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Usage.Reported != tc.wantReported {
				t.Fatalf("Reported = %v, want %v (usage %+v)", resp.Usage.Reported, tc.wantReported, resp.Usage)
			}
			if resp.Usage.TotalTokens != tc.wantTotal {
				t.Fatalf("TotalTokens = %d, want %d", resp.Usage.TotalTokens, tc.wantTotal)
			}
		})
	}
}

// .
// .
func TestAnthropicUsageCountsCachedInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":100,"cache_creation_input_tokens":7}}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.PromptTokens != 117 || resp.Usage.TotalTokens != 121 {
		t.Fatalf("cached input not counted toward the window: %+v", resp.Usage)
	}
	// .
	// .
	// .
	// .
	if resp.Usage.CachedPromptTokens != 100 {
		t.Fatalf("cache reads not separated for cost: %+v", resp.Usage)
	}
}

// .
// .
// .
// .
// .
func TestEveryDialectSendsTheOutputCeiling(t *testing.T) {
	for _, tc := range []struct {
		name, provider, field string
	}{
		{"openai-compatible", "", "max_tokens"},
		{"anthropic", "anthropic", "max_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewDecoder(r.Body).Decode(&body)
				if tc.provider == "anthropic" {
					io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
					return
				}
				io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
			}))
			defer srv.Close()
			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m",
				Provider: tc.provider, MaxOutputTokens: 4096})
			if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
				t.Fatal(err)
			}
			got, present := body[tc.field]
			if !present {
				t.Fatalf("%s omitted %s — the provider, not AII OS, would choose the ceiling: %v", tc.name, tc.field, body)
			}
			if got != float64(4096) {
				t.Fatalf("%s sent %s = %v, want 4096", tc.name, tc.field, got)
			}
		})
	}
}

// .
// .
// .
func TestDialectFallbackIsTheSharedConstant(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if body["max_tokens"] != float64(DefaultMaxOutputTokens) {
		t.Fatalf("fallback = %v, want the shared DefaultMaxOutputTokens %d", body["max_tokens"], DefaultMaxOutputTokens)
	}
}

// .
// .
// .
// .
// .
// .
func TestFailedCallIsRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"messages.19.content.0.thinking.thinking: Field required"}}`)
	}))
	defer srv.Close()

	buf := logsink.CaptureForTest(t)

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err == nil {
		t.Fatal("a 400 must be an error")
	}
	got := buf.String()
	if !strings.Contains(got, "call FAILED") || !strings.Contains(got, "llm.error") {
		t.Fatalf("a failed call left no record: %q", got)
	}
	// .
	// .
	if !strings.Contains(got, "anthropic") || !strings.Contains(got, "Field required") {
		t.Fatalf("the record does not identify the failure: %q", got)
	}
}

// .
// .
func TestSuccessfulCallIsSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	buf := logsink.CaptureForTest(t)

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "LLM call FAILED") {
		t.Fatalf("a successful call was logged as a failure: %q", buf.String())
	}
}

// .
// .
func TestFailureRecordIsBounded(t *testing.T) {
	if got := clip(strings.Repeat("x", 5000), 500); len(got) > 600 {
		t.Fatalf("clip did not bound the record: %d bytes", len(got))
	}
	if !strings.Contains(clip(strings.Repeat("x", 5000), 500), "more bytes") {
		t.Fatal("a clipped record must say that it was clipped")
	}
	if got := clip("short", 500); got != "short" {
		t.Fatalf("clip altered a short record: %q", got)
	}
}

// .
// .
// .
// .
func TestUnattendedCallCostIsRecorded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":900,"output_tokens":40,"cache_read_input_tokens":800}}`)
	}))
	defer srv.Close()

	buf := logsink.CaptureForTest(t)

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, _, err := c.ChatSimple(context.Background(), "system", "user"); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.Contains(got, "unattended call cost") || !strings.Contains(got, "llm.budget") {
		t.Fatalf("a facility pass left no cost record: %q", got)
	}
	// .
	if !strings.Contains(got, "1740") {
		t.Fatalf("cost record does not carry the total: %q", got)
	}
	// .
	if !strings.Contains(got, "800 cached") {
		t.Fatalf("cost record does not separate cached input: %q", got)
	}
}

// .
// .
// .
func TestUnattendedCostSilentWhenUnreported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()

	buf := logsink.CaptureForTest(t)

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, _, err := c.ChatSimple(context.Background(), "system", "user"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "Unattended call cost") {
		t.Fatalf("unknown spend was logged as if measured: %q", buf.String())
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestChatGPTOmitsTheOutputCeiling(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "event: response.completed\ndata: {\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":5,\"output_tokens\":1,\"total_tokens\":6}}}\n\n")
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "gpt-5.6-sol",
		Provider: "chatgpt", MaxOutputTokens: 8192})
	// .
	// .
	_, _ = c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if raw == "" {
		t.Fatal("no request reached the server")
	}
	if strings.Contains(raw, "max_output_tokens") {
		t.Fatalf("the ChatGPT backend was sent a parameter it rejects: %s", raw)
	}
}

// .
// .
// .
// .
// .
func TestChatStructuredAcceptsEitherChannel(t *testing.T) {
	envelope := `{"operations":[{"op":"upsert","id":"b1"}],"ring3_view":"you believe the build is green"}`

	for _, tc := range []struct {
		name        string
		body        string
		wantViaTool bool
	}{
		{
			name:        "substrate calls the tool",
			body:        `{"content":[{"type":"tool_use","id":"t1","name":"emit_consolidation","input":` + envelope + `}],"stop_reason":"tool_use"}`,
			wantViaTool: true,
		},
		{
			name:        "substrate answers in prose (weak tool calls)",
			body:        `{"content":[{"type":"text","text":` + strconv.Quote(envelope) + `}],"stop_reason":"end_turn"}`,
			wantViaTool: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			tool := ToolDefinition{Type: "function"}
			tool.Function.Name = "emit_consolidation"
			tool.Function.Parameters = map[string]interface{}{"type": "object"}

			c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
			payload, _, viaTool, err := c.ChatStructured(context.Background(), "system", "user", tool)
			if err != nil {
				t.Fatal(err)
			}
			if viaTool != tc.wantViaTool {
				t.Fatalf("viaTool = %v, want %v", viaTool, tc.wantViaTool)
			}
			// .
			// .
			var got map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &got); err != nil {
				t.Fatalf("payload from the %s channel does not parse: %v (%q)", tc.name, err, payload)
			}
			if got["ring3_view"] != "you believe the build is green" {
				t.Fatalf("payload lost content: %v", got)
			}
		})
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestOpenAIUsageCountsCachedInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4013,"completion_tokens":4,"total_tokens":4017,"prompt_tokens_details":{"cached_tokens":2048}}}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CachedPromptTokens != 2048 {
		t.Fatalf("cache reads not lifted out of prompt_tokens_details: %+v", resp.Usage)
	}
	// .
	// .
	// .
	if resp.Usage.PromptTokens != 4013 || resp.Usage.TotalTokens != 4017 {
		t.Fatalf("reporting a cache hit changed the context accounting: %+v", resp.Usage)
	}
}

// .
// .
// .
// .
// .
func TestOpenAIUsageWithoutCacheDetailsStaysZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m"})
	resp, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Usage.Reported {
		t.Fatalf("a usage object was sent; presence must still be recorded: %+v", resp.Usage)
	}
	if resp.Usage.CachedPromptTokens != 0 {
		t.Fatalf("invented a cache hit from an absent details object: %+v", resp.Usage)
	}
}

// .
// .
// .
// .
func TestAnthropicMarksTheConversationTailForCache(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":4}}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	_, err := c.Chat(context.Background(), []Message{
		{Role: "system", Content: "stable identity material"},
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
		{Role: "user", Content: "third"},
	}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := got["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("no messages sent: %+v", got)
	}
	last, _ := msgs[len(msgs)-1].(map[string]any)
	blocks, _ := last["content"].([]any)
	if len(blocks) == 0 {
		t.Fatalf("last message carried no content blocks: %+v", last)
	}
	tail, _ := blocks[len(blocks)-1].(map[string]any)
	if tail["cache_control"] == nil {
		t.Fatalf("the conversation tail carries no cache_control — every call in the turn re-pays for the whole history: %+v", tail)
	}

	// .
	// .
	// .
	n := 0
	var count func(any)
	count = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["cache_control"]; ok {
				n++
			}
			for _, e := range x {
				count(e)
			}
		case []any:
			for _, e := range x {
				count(e)
			}
		}
	}
	count(got)
	if n > 4 {
		t.Fatalf("%d cache breakpoints sent; the provider honours four", n)
	}
}

// .
// .
// .
func TestAnthropicNeverMarksAThinkingBlock(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":4}}`)
	}))
	defer srv.Close()
	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	_, err := c.Chat(context.Background(), []Message{
		{Role: "system", Content: "stable"},
		{Role: "user", Content: "go"},
		{Role: "assistant", Thinking: []ThinkingBlock{{Kind: "thinking", Text: "considering", Signature: "sig"}}},
	}, ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := got["messages"].([]any)
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		blocks, _ := mm["content"].([]any)
		for _, b := range blocks {
			bb, _ := b.(map[string]any)
			if bb["type"] == "thinking" || bb["type"] == "redacted_thinking" {
				if bb["cache_control"] != nil {
					t.Fatalf("cache_control placed on a thinking block — the provider refuses this and the turn is lost: %+v", bb)
				}
			}
		}
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestAnthropicLiftsTopLevelCombinatorsOutOfToolSchemas(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":4}}`)
	}))
	defer srv.Close()

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"before_ms": map[string]interface{}{"type": "integer"},
			"after_ms":  map[string]interface{}{"type": "integer"},
		},
		"anyOf": []interface{}{
			map[string]interface{}{"required": []interface{}{"before_ms"}},
			map[string]interface{}{"required": []interface{}{"after_ms"}},
		},
	}
	tool := ToolDefinition{Type: "function"}
	tool.Function.Name = "recall"
	tool.Function.Description = "Time-bounded retrieval."
	tool.Function.Parameters = schema

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}},
		ChatOptions{Tools: []ToolDefinition{tool}})
	if err != nil {
		t.Fatal(err)
	}

	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("want one tool: %+v", got["tools"])
	}
	sent, _ := tools[0].(map[string]any)
	in, _ := sent["input_schema"].(map[string]any)
	for _, k := range []string{"anyOf", "oneOf", "allOf"} {
		if _, bad := in[k]; bad {
			t.Fatalf("%s survived at the top level of input_schema — the provider refuses this and every call of the turn fails: %+v", k, in)
		}
	}
	// .
	// .
	desc, _ := sent["description"].(string)
	if !strings.Contains(desc, "before_ms") || !strings.Contains(desc, "after_ms") {
		t.Fatalf("the lifted constraint did not reach the description; the model no longer knows the rule: %q", desc)
	}
	// .
	props, _ := in["properties"].(map[string]any)
	if _, ok := props["before_ms"]; !ok {
		t.Fatalf("lifting the combinator damaged the schema: %+v", in)
	}
}

// .
// .
// .
func TestAnthropicDoesNotMutateTheCallersSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()

	schema := map[string]interface{}{
		"type":  "object",
		"anyOf": []interface{}{map[string]interface{}{"required": []interface{}{"a"}}},
	}
	tool := ToolDefinition{Type: "function"}
	tool.Function.Name = "t"
	tool.Function.Parameters = schema

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}},
		ChatOptions{Tools: []ToolDefinition{tool}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := schema["anyOf"]; !ok {
		t.Fatal("the anthropic dialect stripped the combinator from the CALLER's schema — the next OpenAI-path call would lose a constraint it can carry")
	}
}
