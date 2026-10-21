package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakePlane is a discoverer with a plugin side: two families, one
// primary operation and one other.
type fakePlane struct{}

func (f *fakePlane) Discover(int) []ToolInfo {
	return []ToolInfo{{Name: "read", Description: "read a file"}}
}
func (f *fakePlane) Brief() ToolBrief {
	return ToolBrief{Total: 4, Unavailable: 1,
		Families: []ToolFamily{{Name: "memory", Count: 3, Names: []string{"memory.store", "memory.recall", "memory.forget"}}, {Name: "weather", Count: 1, Names: []string{"weather.current"}}}}
}
func (f *fakePlane) Search(query string, limit int) []ToolHit {
	if strings.Contains(query, "recall") {
		return []ToolHit{{Name: "pl_m_memory_recall", Operation: "memory.recall", Plugin: "com.example.memory", Summary: "Recall the closest texts", Effects: "read.internal"},
			{Name: "pl_m_memory_store", Operation: "memory.store", Plugin: "com.example.memory", Summary: "Keep a text", Effects: "write.local", Primary: true}}
	}
	return nil
}
func (f *fakePlane) Show(ref string) (ToolCard, error) {
	switch ref {
	case "memory.recall":
		return ToolCard{Name: "pl_m_memory_recall", Operation: "memory.recall", Plugin: "com.example.memory", Version: "0.1.0", Tier: "T1", Family: "memory", Summary: "Recall the closest texts", Effects: "read.internal", Capabilities: []string{"ring4.kv"}, MaxResultBytes: 4096, Receipt: "no host effect", Parameters: map[string]any{"type": "object"}}, nil
	case "memory.store":
		return ToolCard{Name: "pl_m_memory_store", Operation: "memory.store", Plugin: "com.example.memory", Primary: true}, nil
	case "weather.current":
		return ToolCard{Name: "pl_w_weather_current", Operation: "weather.current", Reason: "disabled by your operator"}, nil
	}
	return ToolCard{}, fmt.Errorf("%s is not a plugin operation installed beside you", ref)
}

// The tools organ's plugin side speaks the C tool plane's layers (S6):
// the plain call carries the brief after the organs;
// brief, search and show are bounded views; a card ends with how to call
// its operation — by tool name when it is listed, through action=call
// when it is not; every failure is named. No action offers or releases.
func TestToolsOrganBriefsSearchesAndShows(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	engine.toolDisc = &fakePlane{}
	call := func(args map[string]any) (string, error) {
		return engine.ExecuteAction(context.Background(), "verb", "tools", args)
	}

	plain, err := call(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Your organs", "Tools in your sandbox", "Plugin operations beside you: 4 across 2 families (1 unavailable)", "memory — 3: memory.store, memory.recall, memory.forget", "primary operations first; they are in your tool list", "action=call name=<operation> args={…}", "SKILLS.md", "receipt says"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("the plain call lacks %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "JSON Schema") || strings.Contains(plain, "offer") {
		t.Fatalf("the brief carries no schema and no offer:\n%s", plain)
	}

	brief, _ := call(map[string]any{"action": "brief"})
	if strings.Contains(brief, "Your organs") || !strings.Contains(brief, "weather — 1: weather.current") {
		t.Fatalf("action=brief is the plugin brief alone:\n%s", brief)
	}

	if _, err := call(map[string]any{"action": "search"}); err == nil {
		t.Fatal("search without a query is an error")
	}
	hits, _ := call(map[string]any{"action": "search", "query": "recall what I stored"})
	if !strings.Contains(hits, "memory.recall — Recall the closest texts [read.internal; plugin com.example.memory] → tools action=show name=memory.recall\n") ||
		!strings.Contains(hits, "memory.store — Keep a text [write.local; plugin com.example.memory] → tools action=show name=memory.store [primary]") {
		t.Fatalf("search renders names and one-liners with the show hint:\n%s", hits)
	}
	none, _ := call(map[string]any{"action": "search", "query": "launch rockets"})
	if !strings.Contains(none, "No plugin operation matches") {
		t.Fatalf("an empty search says so:\n%s", none)
	}

	card, err := call(map[string]any{"action": "show", "name": "memory.recall"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"memory.recall — tool name `pl_m_memory_recall`\n", "plugin com.example.memory 0.1.0 (T1); family memory; effects read.internal; capabilities ring4.kv", "receipt rule: no host effect", "result bound: 4096 bytes", `arguments (JSON Schema): {"type":"object"}`, "To call it: tools action=call name=pl_m_memory_recall args={…}"} {
		if !strings.Contains(card, want) {
			t.Fatalf("show lacks %q:\n%s", want, card)
		}
	}
	// Outside a turn a primary is what a request lists.
	if primary, _ := call(map[string]any{"action": "show", "name": "memory.store"}); !strings.Contains(primary, "(primary)") || !strings.HasSuffix(primary, "It is in your tool list: call it by its tool name.") {
		t.Fatalf("a primary's card:\n%s", primary)
	}
	if hidden, _ := call(map[string]any{"action": "show", "name": "weather.current"}); !strings.Contains(hidden, "(unavailable: disabled by your operator)") || !strings.HasSuffix(hidden, "It cannot be called while it is unavailable.") {
		t.Fatalf("a hidden operation's card:\n%s", hidden)
	}
	if _, err := call(map[string]any{"action": "show", "name": "nothing.here"}); err == nil {
		t.Fatal("show of an unknown operation is an error")
	}
	for _, gone := range []string{"offer", "release", "dance"} {
		if _, err := call(map[string]any{"action": gone, "name": "memory.recall"}); err == nil || !strings.Contains(err.Error(), "organs, brief, search, show, call") {
			t.Fatalf("action=%s must be an unknown action naming the five: %v", gone, err)
		}
	}
	// action=call is the host's route; reaching the verb means no host
	// routed it, and it says so.
	if _, err := call(map[string]any{"action": "call", "name": "memory.recall"}); err == nil || !strings.Contains(err.Error(), "no plugin host") {
		t.Fatalf("an unrouted call: %v", err)
	}
}

// Without a plugin side, the organ answers plainly: nothing installed,
// and the organs and sandbox tools render as before.
func TestToolsOrganWithoutAPluginSide(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	out, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{"action": "brief"})
	if err != nil || out != "No plugin operations are installed beside you." {
		t.Fatalf("brief without a plane: %q %v", out, err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{"action": "show", "name": "x"}); err == nil {
		t.Fatal("show without a plane is an error")
	}
	plain, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{"depth": 2})
	if err != nil || !strings.Contains(plain, "Your organs") || strings.Contains(plain, "Plugin operations beside you") {
		t.Fatalf("the plain call without plugins is the old orientation:\n%s", plain)
	}
}
