package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// A MODEL IS ASKED TO THINK IN THE SHAPE THE REGISTRY NAMES FOR IT.
//
// Readable reasoning can only be asked of a model that is asked to think,
// and a live run found two groups that were not. A model the shipped registry
// had no row for was sent no thinking object: claude-fable-5-1 and
// claude-sonnet-5-5, both of the family whose every other member is adaptive.
// And a model whose row names the vendor's "enabled" shape was sent none
// either, whatever budget its entry set, because the client built a thinking
// object for "adaptive" and for an entry's own word "budget" and for no
// other word.

// The shipped registry names a shape for each of these.
func TestTheShippedRegistryNamesAThinkingShape(t *testing.T) {
	eff := newEffectiveCaps(nil)
	for model, want := range map[string]string{
		"claude-opus-5-5":          "adaptive",
		"claude-sonnet-5-5":        "adaptive",
		"claude-fable-5-1":         "adaptive",
		"claude-fable-5":           "adaptive",
		"claude-haiku-4-5":         "enabled",
		"claude-opus-4-5-20251101": "enabled",
	} {
		c, ok := eff.capabilityFor(model)
		if !ok || c.Thinking != want {
			t.Errorf("%s: the shipped registry says %q (row present %v), want %q", model, c.Thinking, ok, want)
		}
	}
}

// thinkingOnTheWire resolves a client for the model through the shipped
// registry and an entry of the vendor's API, sends one request to a server
// that keeps its body, and returns the request's thinking object.
func thinkingOnTheWire(t *testing.T, model string, budget int) (map[string]any, bool) {
	t.Helper()
	return thinkingOnTheWireOf(t, model, budget, "")
}

// thinkingOnTheWireOf is thinkingOnTheWire for an entry that names a
// thinking mode of its own (the provider card's THINKING SHAPE).
func thinkingOnTheWireOf(t *testing.T, model string, budget int, mode string) (map[string]any, bool) {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	entry := providerEntry{Name: "vendor-api", APIType: "anthropic", URL: srv.URL, APIKey: "sk-test",
		DefaultModel: model, Models: []string{model}, Default: true, ThinkingBudget: budget, ThinkingMode: mode}
	writeTestProviders(t, dir, entry)
	a := New(&Config{LLM: LLMConfig{Provider: entry.Name, Model: model, TimeoutSeconds: 10, Retries: -1}, SourcePath: filepath.Join(dir, "config.json")})
	cc, _, err := a.resolveLLM()
	if err != nil {
		t.Fatalf("%s: resolveLLM: %v", model, err)
	}
	if _, err := llm.New(&cc).Chat(context.Background(), []llm.Message{{Role: "user", Content: "hi"}}, llm.ChatOptions{}); err != nil {
		t.Fatalf("%s: %v", model, err)
	}
	if got, _ := body["model"].(string); got != model {
		t.Fatalf("the request named model %q, want %q", got, model)
	}
	think, present := body["thinking"].(map[string]any)
	return think, present
}

func TestTheRegistrysShapeIsWhatTheRequestCarries(t *testing.T) {
	// The two models the registry had no row for: adaptive, and the default
	// of readable reasoning rides it.
	for _, model := range []string{"claude-sonnet-5-5", "claude-fable-5-1"} {
		think, present := thinkingOnTheWire(t, model, 0)
		if !present || think["type"] != "adaptive" || think["display"] != "summarized" || len(think) != 2 {
			t.Errorf("%s: thinking on the wire = %v (present %v), want adaptive with display summarized and nothing else", model, think, present)
		}
	}
	// The vendor's "enabled" shape, with the budget the entry sets.
	for _, model := range []string{"claude-haiku-4-5", "claude-opus-4-5-20251101"} {
		think, present := thinkingOnTheWire(t, model, 2048)
		if !present || think["type"] != "enabled" || think["budget_tokens"] != float64(2048) || think["display"] != "summarized" || len(think) != 3 {
			t.Errorf("%s with a budget: thinking on the wire = %v (present %v), want enabled, budget_tokens 2048, display summarized", model, think, present)
		}
		// No budget is invented: an entry that sets none asks for no thinking.
		if think, present := thinkingOnTheWire(t, model, 0); present {
			t.Errorf("%s with no budget: a thinking object was sent: %v", model, think)
		}
	}
	// A budget is the "enabled" shape's and no other's: an adaptive model's
	// request does not take one from the entry.
	if think, present := thinkingOnTheWire(t, "claude-opus-5-5", 2048); !present || think["type"] != "adaptive" || think["budget_tokens"] != nil {
		t.Errorf("claude-opus-5-5 with an entry budget: thinking on the wire = %v (present %v), want adaptive and no budget", think, present)
	}
	// And a model the registry has never heard of is still sent none: one
	// entry serves many models, and the shape a model rejects is a 400.
	if think, present := thinkingOnTheWire(t, "claude-not-yet-catalogued-9", 2048); present {
		t.Errorf("an uncatalogued model was sent a thinking object: %v", think)
	}
}

func TestAnEntryThatSaysOffSendsNoThinkingToAnyModel(t *testing.T) {
	for _, tc := range []struct {
		model  string
		budget int
	}{
		{"claude-opus-5-5", 0},
		{"claude-sonnet-5-5", 0},
		{"claude-fable-5-1", 0},
		{"claude-haiku-4-5", 2048},
		{"claude-opus-4-5-20251101", 2048},
		{"claude-not-yet-catalogued-9", 2048},
	} {
		if think, present := thinkingOnTheWireOf(t, tc.model, tc.budget, "off"); present {
			t.Errorf("%s: the entry says off and the request carried a thinking object: %v", tc.model, think)
		}
	}
	// What off is not. An entry that names a shape still yields to the
	// row, in both directions: the wrong shape is a 400.
	if think, present := thinkingOnTheWireOf(t, "claude-opus-5-5", 2048, "budget"); !present || think["type"] != "adaptive" || think["budget_tokens"] != nil {
		t.Errorf("an entry that says budget, on an adaptive model: thinking on the wire = %v (present %v), want the row's adaptive", think, present)
	}
	if think, present := thinkingOnTheWireOf(t, "claude-haiku-4-5", 2048, "adaptive"); !present || think["type"] != "enabled" || think["budget_tokens"] != float64(2048) {
		t.Errorf("an entry that says adaptive, on a model of the earlier shape: thinking on the wire = %v (present %v), want the row's enabled with the budget", think, present)
	}

	if think, present := thinkingOnTheWireOf(t, "claude-opus-5-5", 0, ""); !present || think["type"] != "adaptive" || think["display"] != "summarized" {
		t.Errorf("an entry that says nothing, on an adaptive model: thinking on the wire = %v (present %v), want adaptive with the display", think, present)
	}
}

// The same rule where it is decided, without a request.
func TestOffIsNotOverriddenByARow(t *testing.T) {
	eff := newEffectiveCaps(nil)
	for _, model := range []string{"claude-opus-5-5", "claude-fable-5-1", "claude-haiku-4-5", "some-unlisted-model"} {
		if got := thinkingShapeFor(eff, providerEntry{ThinkingMode: "off"}, model); got != "off" {
			t.Errorf("%s: an entry that says off resolves to %q, want off", model, got)
		}
	}
	if got := thinkingShapeFor(eff, providerEntry{}, "claude-opus-5-5"); got != "adaptive" {
		t.Errorf("an entry that says nothing takes the row's shape, got %q", got)
	}
}
