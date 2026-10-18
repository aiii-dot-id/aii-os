package tools

import (
	"context"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// opTool is a plugin operation as the registry sees one: a dynamic
// tool that carries its Discovery.
type opTool struct {
	name string
	disc Discovery
}

func (o opTool) Name() string        { return o.name }
func (o opTool) Description() string { return o.disc.Summary + " (described)" }
func (o opTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}
}
func (o opTool) Execute(context.Context, map[string]any) (Result, error) {
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

// Installed plugins are not prompt context: a plugin operation is born
// inspect-only, absent from the prompt's set and from Discover, until
// offered; the offer is bounded; a released seat is free again; a
// deactivation clears its seat.
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
	if st, _, _, ok := r.State(mem[1]); !ok || st != StateInspectOnly {
		t.Fatalf("state of a fresh plugin operation = %q ok=%v, want inspect-only", st, ok)
	}
	if _, _, _, ok := r.State("read"); ok {
		t.Fatal("a builtin has no plugin discovery state")
	}

	// Offer by operation id (unique) and by tool name; the ninth refuses.
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

func TestADisabledBuiltinIsWithheldFromTheModel(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	ops := registerPlugin(t, r, "com.example.memory", "memory.recall", "memory.forget")
	if _, err := r.Offer(ops[0]); err != nil {
		t.Fatal(err)
	}
	before := r.PromptNames()
	if !contains(before, "shell") || !contains(before, ops[0]) || contains(before, ops[1]) {
		t.Fatalf("the prompt's set before the switch: %v", before)
	}
	r.SetToolEnabled("shell", false)
	want := slices.DeleteFunc(slices.Clone(before), func(n string) bool { return n == "shell" })
	var defs, listed []string
	for _, d := range r.ToolDefinitions() {
		defs = append(defs, d.Function.Name)
	}
	for _, info := range r.Discover(2) {
		listed = append(listed, info.Name)
	}
	if got := r.PromptNames(); !slices.Equal(got, want) || !slices.Equal(defs, want) || !slices.Equal(listed, want) {
		t.Fatalf("with the shell off:\nprompt set %v\ndefinitions %v\nlisting %v\nwant %v", got, defs, listed, want)
	}
	if !contains(r.Names(), "shell") {
		t.Fatal("the operator's toggle list lost the shell")
	}
	if res, err := r.ExecuteModel(context.Background(), "shell", map[string]any{"command": "echo on"}); err != nil || res.ReasonCode != ReasonOperatorDisabled {
		t.Fatalf("a call to the withheld shell must be refused with %s: %+v, %v", ReasonOperatorDisabled, res, err)
	}
	r.SetToolEnabled("shell", true)
	if got := r.PromptNames(); !slices.Equal(got, before) {
		t.Fatalf("re-enabled, the prompt's set is %v, want %v", got, before)
	}
}

// The brief is families and counts; a search ranks by the descriptor
// text; show is the one place a schema appears; hidden operations are
// counted as unavailable and never listed.
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
	if !strings.Contains(card.Receipt, "check the returned data") || !strings.Contains(card.Receipt, "scope and coverage") {
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

// An operation id two plugins declare resolves to neither: the error
// names both, and the identity chooses by tool name.
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

// A host op (a channel adapter's plumbing) is neither discoverable nor
// offerable — it is the host's, and the tools organ never shows it.
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

func TestTwoOffersOfOneNameTakeOneSeat(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	mem := registerPlugin(t, r, "com.example.memory", "memory.recall")
	r.persistMu.Lock()
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := r.Offer(mem[0])
			errs <- err
		}()
	}
	for deadline := time.Now().Add(10 * time.Second); parkedInChangeStanding() < 2; {
		if time.Now().After(deadline) {
			r.persistMu.Unlock()
			t.Fatalf("the two offers never both waited their turn (%d did)", parkedInChangeStanding())
		}
		runtime.Gosched()
	}
	r.persistMu.Unlock()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("offer: %v", err)
		}
	}
	if got := r.Offered(); !slices.Equal(got, []string{mem[0]}) {
		t.Fatalf("two offers of one name: the order is %v, want one seat", got)
	}
	if _, err := r.Release(mem[0]); err != nil {
		t.Fatal(err)
	}
	if got := r.Offered(); len(got) != 0 {
		t.Fatalf("one release left %v in the offer", got)
	}
	if st, _, _, _ := r.State(mem[0]); st != StateInspectOnly {
		t.Fatalf("after its release the operation is %q, want inspect-only", st)
	}
}

// parkedInChangeStanding counts the goroutines waiting on the standing
// offer's lock inside changeStanding.
func parkedInChangeStanding() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "(*Registry).changeStanding") && strings.Contains(g, "sync.(*Mutex).Lock") {
			n++
		}
	}
	return n
}
