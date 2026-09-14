package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// .
// .
// .
// .
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
// .
// .
func TestAnEmptyToolResultStillCarriesItsOutputKey(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		// .
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, streamOneItem+"\n"+streamTerminal)
	}))
	defer srv.Close()

	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "gpt-x", Provider: "chatgpt",
	})
	// .
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

	// .
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
