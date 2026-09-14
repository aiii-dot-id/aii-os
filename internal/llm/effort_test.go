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

// .
func TestWirePlanSeparatesWhereFromWhetherFromWhat(t *testing.T) {
	openai := []string{"minimal", "low", "medium", "high"}

	for _, c := range []struct {
		name      string
		dialect   Dialect
		requested string
		levels    []string
		wantSent  bool
		wantWire  string
		wantField string
	}{
		{"nothing asked for", DialectResponses, "", openai, false, "", "reasoning.effort"},
		{"a level this provider takes", DialectResponses, "high", openai, true, "high", "reasoning.effort"},
		{"a level it does not", DialectResponses, "max", openai, false, "", "reasoning.effort"},
		{"anthropic puts it elsewhere", DialectAnthropic, "max", []string{"low", "high", "max"}, true, "max", "output_config.effort"},
		{"chat completions field", DialectOpenAI, "low", openai, true, "low", "reasoning_effort"},
		// .
		// .
		{"uncatalogued provider still sends", DialectOpenAI, "whatever", nil, true, "whatever", "reasoning_effort"},
		// .
		// .
		// .
		// .
		{"model with no effort parameter", DialectAnthropic, "high", []string{}, false, "", "output_config.effort"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := PlanEffort(c.dialect, c.requested, c.levels)
			if p.Sent != c.wantSent {
				t.Fatalf("Sent = %v, want %v (%s)", p.Sent, c.wantSent, p.Summary())
			}
			if p.Wire != c.wantWire {
				t.Fatalf("Wire = %q, want %q", p.Wire, c.wantWire)
			}
			if p.Field != c.wantField {
				t.Fatalf("Field = %q, want %q", p.Field, c.wantField)
			}
			if !p.Sent && c.requested != "" && p.Note == "" {
				t.Fatal("a refusal must say why, or the operator cannot act on it")
			}
			// .
			if !p.Sent && c.requested != "" && !strings.Contains(p.Summary(), "NOT SENT") {
				t.Fatalf("a refused level must read as refused: %q", p.Summary())
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
func TestARefusedEffortNeverReachesTheProvider(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, streamOneItem+"\n"+streamTerminal)
	}))
	defer srv.Close()

	c := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "gpt-x", Provider: "chatgpt",
		ReasoningEffort: "max",
		EffortLevels:    []string{"minimal", "low", "medium", "high"},
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if r, present := body["reasoning"]; present {
		t.Fatalf("a level this provider does not accept was sent anyway: reasoning=%v", r)
	}

	// .
	body = nil
	c2 := New(&ClientConfig{
		Endpoint: srv.URL, APIKey: "k", Model: "gpt-x", Provider: "chatgpt",
		ReasoningEffort: "high",
		EffortLevels:    []string{"minimal", "low", "medium", "high"},
	})
	if _, err := c2.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	r, present := body["reasoning"]
	if !present {
		t.Fatal("an accepted level must reach the provider")
	}
	obj, _ := r.(map[string]any)
	if obj["effort"] != "high" {
		t.Fatalf("reasoning.effort = %v, want high", obj["effort"])
	}
}
