package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type primarySchemaTool struct {
	name   string
	schema map[string]any
}

func (p primarySchemaTool) Name() string               { return p.name }
func (p primarySchemaTool) Description() string        { return "Find reusable evidence for a task" }
func (p primarySchemaTool) Parameters() map[string]any { return p.schema }
func (p primarySchemaTool) Discovery() tools.Discovery {
	return tools.Discovery{Plugin: "org.example.notes", Operation: "search", Family: "notes", Primary: true, Summary: p.Description(), Effects: "read.internal"}
}
func (p primarySchemaTool) Execute(context.Context, map[string]any) (tools.Result, error) {
	return tools.Result{Output: "[]"}, nil
}

func primarySchemaApp(t *testing.T) (*App, primarySchemaTool) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string","minLength":1},"scope":{"type":"object","properties":{"project":{"type":"string"}},"required":["project"],"additionalProperties":false},"kinds":{"type":"array","items":{"type":"string","enum":["procedure","observation"]},"maxItems":2}},"required":["query","scope"]}`), &schema); err != nil {
		t.Fatal(err)
	}
	p := primarySchemaTool{name: "pl_org_example_notes_search", schema: schema}
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	if err := reg.RegisterDynamic(p, "org.example.notes"); err != nil {
		t.Fatal(err)
	}
	return &App{toolReg: reg}, p
}

func TestPrimaryPluginActionUsesTheSameExplicitOffer(t *testing.T) {
	a, p := primarySchemaApp(t)
	h := &dashboard.WSHandler{}
	a.wirePluginHooks(h)
	if len(a.toolReg.Offered()) != 0 {
		t.Fatal("registering a primary operation offered it")
	}
	if err := h.PluginAct(dashboard.PluginAction{Action: "offer", ID: "org.example.notes"}); err != nil {
		t.Fatal(err)
	}
	got := a.toolReg.Offered()
	if len(got) != 1 || got[0] != p.name {
		t.Fatalf("plugin action selected %v", got)
	}
	if err := h.PluginAct(dashboard.PluginAction{Action: "release", ID: "org.example.notes"}); err != nil {
		t.Fatal(err)
	}
	for _, def := range a.buildToolDefinitions() {
		if def.Function.Name == p.name {
			t.Fatal("released tool remained in next provider tool array")
		}
	}
	if err := h.PluginAct(dashboard.PluginAction{Action: "release", ID: "notes"}); err == nil {
		t.Fatal("release inferred plugin from alias")
	}
	if err := h.PluginAct(dashboard.PluginAction{Action: "offer", ID: "notes"}); err == nil || len(a.toolReg.Offered()) != 0 {
		t.Fatal("family alias selected a plugin")
	}
	a.toolReg.SetToolEnabled(p.name, false)
	if err := h.PluginAct(dashboard.PluginAction{Action: "offer", ID: "org.example.notes"}); err == nil {
		t.Fatal("UI selection bypassed operator disable")
	}
}

// Capture the real dialect clients' HTTP bodies from the app's actual offered
// definitions. This proves wire shape, not acceptance by a paid endpoint.
func TestPrimaryPluginSchemasReachEveryProviderDialect(t *testing.T) {
	a, p := primarySchemaApp(t)
	if _, err := a.toolReg.OfferPrimary("org.example.notes"); err != nil {
		t.Fatal(err)
	}
	defs := a.buildToolDefinitions()
	found := false
	for _, d := range defs {
		if d.Function.Name == p.name {
			found = true
			if faults := geminiToolFaults(d); len(faults) != 0 {
				t.Fatalf("typed schema violates provider subset: %v", faults)
			}
			if !reflect.DeepEqual(d.Function.Parameters, p.schema) {
				t.Fatal("schema changed in app")
			}
		}
		if d.Function.Name == "memory" || d.Function.Name == "notes" {
			t.Fatal("untyped family wrapper added")
		}
	}
	if !found {
		t.Fatal("primary operation absent")
	}
	for _, provider := range []string{"openai", "anthropic", "gemini", "chatgpt"} {
		t.Run(provider, func(t *testing.T) {
			captured := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				captured <- body
				w.Header().Set("Content-Type", "application/json")
				switch provider {
				case "chatgpt":
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}]}}\n\n")
				case "gemini":
					fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
				case "anthropic":
					fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
				default:
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
				}
			}))
			defer server.Close()
			client := llm.New(&llm.ClientConfig{Endpoint: server.URL, Provider: provider, APIKey: "synthetic", Model: "fixture", NoStream: true, Retries: -1})
			if _, err := client.Chat(t.Context(), []llm.Message{{Role: "user", Content: "synthetic"}}, llm.ChatOptions{Tools: defs, ExactTools: true}); err != nil {
				t.Fatal(err)
			}
			body := <-captured
			var actual any
			for _, v := range body["tools"].([]any) {
				tool := v.(map[string]any)
				switch provider {
				case "openai":
					f := tool["function"].(map[string]any)
					if f["name"] == p.name {
						actual = f["parameters"]
					}
				case "anthropic":
					if tool["name"] == p.name {
						actual = tool["input_schema"]
					}
				case "gemini":
					for _, d := range tool["functionDeclarations"].([]any) {
						f := d.(map[string]any)
						if f["name"] == p.name {
							actual = f["parametersJsonSchema"]
						}
					}
				case "chatgpt":
					if tool["name"] == p.name {
						actual = tool["parameters"]
					}
				}
			}
			if !reflect.DeepEqual(actual, p.schema) {
				t.Fatalf("%s lost typed schema: got%v want%v", provider, actual, p.schema)
			}
			raw, _ := json.Marshal(body["tools"])
			if strings.Contains(string(raw), "checks them and says what it wanted") {
				t.Fatal("untyped retry-discovery wrapper reached provider")
			}
		})
	}
}
