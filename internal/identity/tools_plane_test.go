package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// .
// .
type fakePlane struct {
	offered []string
}

func (f *fakePlane) Discover(int) []ToolInfo {
	return []ToolInfo{{Name: "read", Description: "read a file"}}
}
func (f *fakePlane) Brief() ToolBrief {
	return ToolBrief{Total: 4, Offered: len(f.offered), Unavailable: 1,
		Families:     []ToolFamily{{Name: "memory", Count: 3, Names: []string{"memory.remember", "memory.recall", "memory.forget"}}, {Name: "weather", Count: 1, Names: []string{"weather.current"}}},
		OfferedNames: f.offered}
}
func (f *fakePlane) Search(query string, limit int) []ToolHit {
	if strings.Contains(query, "recall") {
		return []ToolHit{{Name: "pl_m_memory_recall", Operation: "memory.recall", Plugin: "com.example.memory", Summary: "Recall the closest texts", Effects: "read.internal", State: "inspect-only"}}
	}
	return nil
}
func (f *fakePlane) Show(ref string) (ToolCard, error) {
	if ref != "memory.recall" {
		return ToolCard{}, fmt.Errorf("%s is not a plugin operation installed beside you", ref)
	}
	return ToolCard{Name: "pl_m_memory_recall", Operation: "memory.recall", Plugin: "com.example.memory", Version: "0.1.0", Tier: "T1", Family: "memory", Summary: "Recall the closest texts", Effects: "read.internal", Capabilities: []string{"ring4.kv"}, MaxResultBytes: 4096, State: "inspect-only", Receipt: "no host effect", Parameters: map[string]interface{}{"type": "object"}}, nil
}
func (f *fakePlane) Offer(ref string) (string, error) {
	if len(f.offered) >= 2 {
		return "", fmt.Errorf("the offer is full (2 of 2): release one first")
	}
	f.offered = append(f.offered, ref+" (pl_"+ref+")")
	return "pl_" + ref, nil
}
func (f *fakePlane) Release(ref string) (string, error) {
	for i, o := range f.offered {
		if strings.HasPrefix(o, ref+" ") {
			f.offered = append(f.offered[:i], f.offered[i+1:]...)
			return "pl_" + ref, nil
		}
	}
	return "", fmt.Errorf("%s is not in the offer", ref)
}

// .
// .
// .
// .
func TestToolsOrganBriefsSearchesShowsAndOffers(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	plane := &fakePlane{}
	engine.toolDisc = plane
	call := func(args map[string]interface{}) (string, error) {
		return engine.ExecuteAction(context.Background(), "verb", "tools", args)
	}

	plain, err := call(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Your organs", "Tools in your sandbox", "Plugin operations beside you: 4 across 2 families (0 offered, 1 unavailable)", "memory — 3: memory.remember, memory.recall, memory.forget", "SKILLS.md", "receipt says"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("the plain call lacks %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "JSON Schema") {
		t.Fatal("the brief must never carry a schema")
	}

	brief, _ := call(map[string]interface{}{"action": "brief"})
	if strings.Contains(brief, "Your organs") || !strings.Contains(brief, "weather — 1: weather.current") {
		t.Fatalf("action=brief is the plugin brief alone:\n%s", brief)
	}

	if _, err := call(map[string]interface{}{"action": "search"}); err == nil {
		t.Fatal("search without a query is an error")
	}
	hits, _ := call(map[string]interface{}{"action": "search", "query": "recall what I stored"})
	if !strings.Contains(hits, "memory.recall — Recall the closest texts [read.internal, inspect-only; plugin com.example.memory] → tools action=show name=memory.recall") {
		t.Fatalf("search renders names and one-liners with the show hint:\n%s", hits)
	}
	none, _ := call(map[string]interface{}{"action": "search", "query": "launch rockets"})
	if !strings.Contains(none, "No plugin operation matches") {
		t.Fatalf("an empty search says so:\n%s", none)
	}

	card, err := call(map[string]interface{}{"action": "show", "name": "memory.recall"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"memory.recall — callable as `pl_m_memory_recall` (inspect-only)", "plugin com.example.memory 0.1.0 (T1); family memory; effects read.internal; capabilities ring4.kv", "receipt rule: no host effect", "result bound: 4096 bytes", `arguments (JSON Schema): {"type":"object"}`, "tools action=offer name=memory.recall"} {
		if !strings.Contains(card, want) {
			t.Fatalf("show lacks %q:\n%s", want, card)
		}
	}
	if _, err := call(map[string]interface{}{"action": "show", "name": "nothing.here"}); err == nil {
		t.Fatal("show of an unknown operation is an error")
	}

	offered, err := call(map[string]interface{}{"action": "offer", "name": "memory.recall"})
	if err != nil || !strings.Contains(offered, "callable from your next turn as `pl_memory.recall`") {
		t.Fatalf("offer: %q %v", offered, err)
	}
	if _, err := call(map[string]interface{}{"action": "offer", "name": "memory.forget"}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(map[string]interface{}{"action": "offer", "name": "weather.current"}); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("a full offer refuses by name: %v", err)
	}
	after, _ := call(map[string]interface{}{"action": "brief"})
	if !strings.Contains(after, "(2 offered, 1 unavailable)") || !strings.Contains(after, "Offered now, callable by tool name: memory.recall (pl_memory.recall); memory.forget (pl_memory.forget)") {
		t.Fatalf("the brief shows the offer:\n%s", after)
	}
	released, err := call(map[string]interface{}{"action": "release", "name": "memory.recall"})
	if err != nil || !strings.Contains(released, "inspect-only again") {
		t.Fatalf("release: %q %v", released, err)
	}
	if _, err := call(map[string]interface{}{"action": "release", "name": "memory.recall"}); err == nil {
		t.Fatal("releasing twice is an error")
	}
	if _, err := call(map[string]interface{}{"action": "dance"}); err == nil {
		t.Fatal("an unknown action is an error")
	}
}

// .
// .
func TestToolsOrganWithoutAPluginSide(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	out, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]interface{}{"action": "brief"})
	if err != nil || out != "No plugin operations are installed beside you." {
		t.Fatalf("brief without a plane: %q %v", out, err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]interface{}{"action": "offer", "name": "x"}); err == nil {
		t.Fatal("offer without a plane is an error")
	}
	plain, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]interface{}{"depth": 2})
	if err != nil || !strings.Contains(plain, "Your organs") || strings.Contains(plain, "Plugin operations beside you") {
		t.Fatalf("the plain call without plugins is the old orientation:\n%s", plain)
	}
}
