package tools

import (
	"context"
	"strings"
	"testing"
)

// .
// .
type opTool struct {
	name string
	disc Discovery
}

func (o opTool) Name() string        { return o.name }
func (o opTool) Description() string { return o.disc.Summary + " (described)" }
func (o opTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"q": map[string]interface{}{"type": "string"}}}
}
func (o opTool) Execute(context.Context, map[string]interface{}) (Result, error) {
	return Result{Output: "ran " + o.name}, nil
}
func (o opTool) Discovery() Discovery { return o.disc }

func registerPlugin(t *testing.T, r *Registry, plugin string, ops ...string) []string {
	t.Helper()
	var names []string
	for _, op := range ops {
		name := "pl_" + strings.NewReplacer(".", "_", "-", "_").Replace(plugin) + "_" + strings.ReplaceAll(op, ".", "_")
		summary := "Operation " + op + " of " + plugin
		if op == "memory.recall" {
			summary = "Recall the stored texts closest to a query"
		}
		if err := r.RegisterDynamic(opTool{name: name, disc: Discovery{Plugin: plugin, Version: "0.1.0", Tier: "T1", Operation: op, Summary: summary, Effects: "read.internal", Capabilities: []string{"ring4.kv"}, MaxResultBytes: 4096, Keywords: []string{"remember"}}}, plugin); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	return names
}

// .
// .
// .
// .
func TestPluginOperationsAreBornInspectOnlyAndTheOfferIsBounded(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	base := len(r.PromptNames())
	mem := registerPlugin(t, r, "com.example.memory", "memory.remember", "memory.recall", "memory.forget")
	registerPlugin(t, r, "com.example.weather", "weather.current", "weather.forecast", "weather.alerts", "weather.radar", "weather.history", "weather.marine", "weather.air")
	if got := len(r.PromptNames()); got != base {
		t.Fatalf("plugin operations reached the prompt set before any offer: %d vs %d", got, base)
	}
	for _, info := range r.Discover(2) {
		if strings.HasPrefix(info.Name, "pl_") {
			t.Fatalf("Discover listed an unoffered plugin operation: %s", info.Name)
		}
	}
	if st, _, ok := r.State(mem[1]); !ok || st != StateInspectOnly {
		t.Fatalf("state of a fresh plugin operation = %q ok=%v, want inspect-only", st, ok)
	}
	if _, _, ok := r.State("read"); ok {
		t.Fatal("a builtin has no plugin discovery state")
	}

	// .
	if name, err := r.Offer("memory.recall"); err != nil || name != mem[1] {
		t.Fatalf("offer by operation id: %q %v", name, err)
	}
	if _, err := r.Offer(mem[1]); err != nil {
		t.Fatalf("re-offering an offered operation is not an error: %v", err)
	}
	for _, op := range []string{"weather.current", "weather.forecast", "weather.alerts", "weather.radar", "weather.history", "weather.marine", "weather.air"} {
		if _, err := r.Offer(op); err != nil {
			t.Fatalf("offer %s: %v", op, err)
		}
	}
	if len(r.Offered()) != MaxOffered {
		t.Fatalf("offered = %d, want %d", len(r.Offered()), MaxOffered)
	}
	if _, err := r.Offer("memory.remember"); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatalf("the ninth offer must refuse naming the bound, got %v", err)
	}
	if got := len(r.PromptNames()); got != base+MaxOffered {
		t.Fatalf("prompt set = %d, want base %d + %d offered", got, base, MaxOffered)
	}
	if _, err := r.Release("weather.air"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Offer("memory.remember"); err != nil {
		t.Fatalf("a released seat is free: %v", err)
	}
	if _, err := r.Release("memory.forget"); err == nil {
		t.Fatal("releasing an operation that is not offered is an error")
	}
	r.Deregister(mem[1])
	for _, n := range r.Offered() {
		if n == mem[1] {
			t.Fatal("a deactivated operation kept its seat")
		}
	}
	if _, err := r.Offer("nothing.here"); err == nil {
		t.Fatal("an unknown operation cannot be offered")
	}
}

// .
// .
// .
func TestBriefSearchAndShowAreBoundedViews(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	mem := registerPlugin(t, r, "com.example.memory", "memory.remember", "memory.recall", "memory.forget")
	wx := registerPlugin(t, r, "com.example.weather", "weather.current", "weather.forecast")
	if _, err := r.Offer("memory.recall"); err != nil {
		t.Fatal(err)
	}
	r.SetToolEnabled(wx[1], false)

	b := r.Brief()
	if b.Total != 4 || b.Offered != 1 || b.Unavailable != 1 {
		t.Fatalf("brief counts: %+v", b)
	}
	if len(b.Families) != 2 || b.Families[0].Name != "memory" || b.Families[0].Count != 3 || b.Families[1].Name != "weather" || b.Families[1].Count != 1 {
		t.Fatalf("brief families: %+v", b.Families)
	}
	if len(b.OfferedNames) != 1 || !strings.HasPrefix(b.OfferedNames[0], "memory.recall (") {
		t.Fatalf("brief offered names: %v", b.OfferedNames)
	}
	for _, f := range b.Families {
		for _, n := range f.Names {
			if n == "weather.forecast" {
				t.Fatal("a disabled operation was listed")
			}
		}
	}

	hits := r.Search("recall a memory", 0)
	if len(hits) == 0 || hits[0].Name != mem[1] || hits[0].State != StateOffered {
		t.Fatalf("search must rank memory.recall first: %+v", hits)
	}
	for _, h := range hits {
		if h.Name == wx[1] {
			t.Fatal("search returned a hidden operation")
		}
	}
	if len(r.Search("", 0)) != 0 {
		t.Fatal("an empty query finds nothing")
	}
	if len(r.Search("weather", 1)) != 1 {
		t.Fatal("the limit bounds a search")
	}

	card, err := r.Show("memory.recall")
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != mem[1] || card.Family != "memory" || card.Plugin != "com.example.memory" || card.Version != "0.1.0" || card.Tier != "T1" || card.State != StateOffered || card.MaxResultBytes != 4096 {
		t.Fatalf("card: %+v", card)
	}
	if card.Parameters == nil || card.Parameters["type"] != "object" {
		t.Fatalf("show carries the schema: %+v", card.Parameters)
	}
	if !strings.Contains(card.Receipt, "plugin's own text") {
		t.Fatalf("show carries the receipt rule for %s: %q", card.Effects, card.Receipt)
	}
	hidden, err := r.Show(wx[1])
	if err != nil || hidden.State != StateHidden || !strings.Contains(hidden.Reason, "operator") {
		t.Fatalf("a hidden operation shows as hidden with its reason: %+v %v", hidden, err)
	}
	if _, err := r.Offer(wx[1]); err == nil {
		t.Fatal("a hidden operation cannot be offered")
	}
	if _, err := r.Show("nothing.here"); err == nil {
		t.Fatal("show of an unknown operation is an error")
	}
}

// .
// .
func TestAmbiguousOperationIdIsNamedNotGuessed(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	a := registerPlugin(t, r, "com.example.a", "memory.recall")
	registerPlugin(t, r, "com.example.b", "memory.recall")
	if _, err := r.Offer("memory.recall"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("ambiguous id must refuse naming the candidates: %v", err)
	}
	if name, err := r.Offer(a[0]); err != nil || name != a[0] {
		t.Fatalf("offer by tool name: %q %v", name, err)
	}
}

// .
// .
func TestHostOpsAreOutsideDiscovery(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	if err := r.RegisterHostOp(opTool{name: "pl_x_send", disc: Discovery{Operation: "channel.send"}}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	if r.HasDynamic() {
		t.Fatal("a host op is not a discoverable plugin operation")
	}
	if b := r.Brief(); b.Total != 0 {
		t.Fatalf("brief counted a host op: %+v", b)
	}
	if _, err := r.Offer("pl_x_send"); err == nil {
		t.Fatal("a host op cannot be offered")
	}
	registerPlugin(t, r, "org.example.y", "y.op")
	if !r.HasDynamic() {
		t.Fatal("a registered plugin operation makes the posture due")
	}
}
