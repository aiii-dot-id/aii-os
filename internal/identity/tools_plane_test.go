package identity

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakePlane is a discoverer with a plugin side: two families, one
// offered operation, a bounded offer of two seats.
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
	for _, chosen := range f.offered {
		if strings.HasSuffix(chosen, "("+ref+")") {
			return ToolCard{Name: ref, State: "offered"}, nil
		}
	}
	if ref != "memory.recall" {
		return ToolCard{}, fmt.Errorf("%s is not a plugin operation installed beside you", ref)
	}
	return ToolCard{Name: "pl_m_memory_recall", Operation: "memory.recall", Plugin: "com.example.memory", Version: "0.1.0", Tier: "T1", Family: "memory", Summary: "Recall the closest texts", Effects: "read.internal", Capabilities: []string{"ring4.kv"}, MaxResultBytes: 4096, State: "inspect-only", Receipt: "no host effect", Parameters: map[string]any{"type": "object"}}, nil
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

// The tools organ's plugin side speaks the C tool plane's five layers
// (S6): the plain call carries the brief after the organs; brief,
// search and show are bounded views; offer and release move one
// operation between inspect-only and callable; every failure is named.
func TestToolsOrganBriefsSearchesShowsAndOffers(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	plane := &fakePlane{}
	engine.toolDisc = plane
	call := func(args map[string]any) (string, error) {
		return engine.ExecuteAction(context.Background(), "verb", "tools", args)
	}

	plain, err := call(map[string]any{})
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

	brief, _ := call(map[string]any{"action": "brief"})
	if strings.Contains(brief, "Your organs") || !strings.Contains(brief, "weather — 1: weather.current") {
		t.Fatalf("action=brief is the plugin brief alone:\n%s", brief)
	}

	if _, err := call(map[string]any{"action": "search"}); err == nil {
		t.Fatal("search without a query is an error")
	}
	hits, _ := call(map[string]any{"action": "search", "query": "recall what I stored"})
	if !strings.Contains(hits, "memory.recall — Recall the closest texts [read.internal, inspect-only; plugin com.example.memory] → tools action=show name=memory.recall") {
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
	for _, want := range []string{"memory.recall — callable as `pl_m_memory_recall` (inspect-only)", "plugin com.example.memory 0.1.0 (T1); family memory; effects read.internal; capabilities ring4.kv", "receipt rule: no host effect", "result bound: 4096 bytes", `arguments (JSON Schema): {"type":"object"}`, "tools action=offer name=memory.recall"} {
		if !strings.Contains(card, want) {
			t.Fatalf("show lacks %q:\n%s", want, card)
		}
	}
	if _, err := call(map[string]any{"action": "show", "name": "nothing.here"}); err == nil {
		t.Fatal("show of an unknown operation is an error")
	}

	offered, err := call(map[string]any{"action": "offer", "name": "memory.recall"})
	if err != nil || (!strings.Contains(offered, "callable from your next turn as `pl_memory.recall`") || !strings.Contains(offered, "choice is saved across restarts") || !strings.Contains(offered, "tools action=show reports current availability") || strings.Contains(offered, "stays offered")) {
		t.Fatalf("offer: %q %v", offered, err)
	}
	if _, err := call(map[string]any{"action": "offer", "name": "memory.forget"}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(map[string]any{"action": "offer", "name": "weather.current"}); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("a full offer refuses by name: %v", err)
	}
	after, _ := call(map[string]any{"action": "brief"})
	if !strings.Contains(after, "(2 offered, 1 unavailable)") || !strings.Contains(after, "Offered now, callable by tool name: memory.recall (pl_memory.recall); memory.forget (pl_memory.forget)") {
		t.Fatalf("the brief shows the offer:\n%s", after)
	}
	released, err := call(map[string]any{"action": "release", "name": "memory.recall"})
	if err != nil || !strings.Contains(released, "removed from your selection") || strings.Contains(released, "inspect-only") {
		t.Fatalf("release: %q %v", released, err)
	}
	if _, err := call(map[string]any{"action": "release", "name": "memory.recall"}); err == nil {
		t.Fatal("releasing twice is an error")
	}
	if _, err := call(map[string]any{"action": "dance"}); err == nil {
		t.Fatal("an unknown action is an error")
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
	if _, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{"action": "offer", "name": "x"}); err == nil {
		t.Fatal("offer without a plane is an error")
	}
	plain, err := engine.ExecuteAction(context.Background(), "verb", "tools", map[string]any{"depth": 2})
	if err != nil || !strings.Contains(plain, "Your organs") || strings.Contains(plain, "Plugin operations beside you") {
		t.Fatalf("the plain call without plugins is the old orientation:\n%s", plain)
	}
}

type primaryPlane struct {
	fakePlane
	selected string
}

func (p *primaryPlane) Show(name string) (ToolCard, error) {
	if p.selected != "" && (name == "pl_notes_search" || name == "pl_notes_store") {
		return ToolCard{Name: name, State: "offered"}, nil
	}
	return p.fakePlane.Show(name)
}
func (p *primaryPlane) OfferPrimary(plugin string) ([]string, error) {
	p.selected = plugin
	return []string{"pl_notes_search", "pl_notes_store"}, nil
}
func TestToolsOfferAcceptsOneExactPluginSelector(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	p := &primaryPlane{}
	engine.toolDisc = p
	got, err := engine.verbTools(t.Context(), map[string]any{"action": "offer", "plugin": "org.example.notes"})
	if err != nil || p.selected != "org.example.notes" || !strings.Contains(got, "pl_notes_search, pl_notes_store") || (!strings.Contains(got, "Each is callable by its own tool name") || !strings.Contains(got, "choice is saved across restarts") || !strings.Contains(got, "tools action=show reports current availability") || strings.Contains(got, "stay offered")) {
		t.Fatalf("selection response: %s %v", got, err)
	}
	p.selected = ""
	if _, err := engine.verbTools(t.Context(), map[string]any{"action": "offer", "plugin": "org.example.notes", "name": "search"}); err == nil || p.selected != "" {
		t.Fatal("ambiguous selector was not refused before changing offer")
	}
	engine.toolDisc = &fakePlane{}
	if _, err := engine.verbTools(t.Context(), map[string]any{"action": "offer", "plugin": "org.example.notes"}); err == nil {
		t.Fatal("old plane silently accepted unsupported selection")
	}
}

type waitingPlane struct{ primaryPlane }

func (p *waitingPlane) Show(name string) (ToolCard, error) {
	return ToolCard{Name: name, State: "inspect-only", Reason: "waiting for free offer space"}, nil
}
func TestSavedOfferReportsCurrentAvailability(t *testing.T) {
	for _, args := range []map[string]any{{"action": "offer", "name": "search"}, {"action": "offer", "plugin": "org.example.notes"}} {
		engine, _, _, _, _ := setupEngine(t)
		engine.toolDisc = &waitingPlane{}
		out, err := engine.verbTools(t.Context(), args)
		if err != nil || !strings.Contains(out, "waiting for free offer space") || strings.Contains(out, "is callable") || strings.Contains(out, "Each is callable") {
			t.Fatalf("saved waiting choice falsely offered: %s %v", out, err)
		}
	}
}

type hiddenReleasePlane struct {
	fakePlane
	reason string
}

func (p *hiddenReleasePlane) Show(name string) (ToolCard, error) {
	return ToolCard{Name: name, State: "hidden", Reason: p.reason}, nil
}
func TestReleaseReceiptDoesNotClaimAvailability(t *testing.T) {
	for _, reason := range []string{"disabled by your operator", "suspended in safe mode"} {
		engine, _, _, _, _ := setupEngine(t)
		p := &hiddenReleasePlane{reason: reason}
		p.offered = []string{"search (pl_search)"}
		engine.toolDisc = p
		out, err := engine.verbTools(t.Context(), map[string]any{"action": "release", "name": "search"})
		card, _ := p.Show("pl_search")
		if err != nil || !strings.Contains(out, "removed from your selection") || strings.Contains(out, "inspect-only") || card.State != "hidden" || len(p.offered) != 0 {
			t.Fatalf("release receipt misstated hidden availability: %s %v", out, err)
		}
	}
}
