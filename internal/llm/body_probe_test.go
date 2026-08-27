package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
