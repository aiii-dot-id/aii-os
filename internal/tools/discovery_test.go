package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
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
	return registerOps(t, r, plugin, false, ops)
}

// registerPrimary is registerPlugin with every operation marked primary
// by its author.
func registerPrimary(t *testing.T, r *Registry, plugin string, ops ...string) []string {
	t.Helper()
	return registerOps(t, r, plugin, true, ops)
}

func registerOps(t *testing.T, r *Registry, plugin string, primary bool, ops []string) []string {
	t.Helper()
	var names []string
	for _, op := range ops {
		o := opWith(plugin, op, func(d *Discovery) { d.Primary = primary })
		if op == "memory.recall" {
			o.disc.Summary = "Recall the stored texts closest to a query"
		}
		if err := r.RegisterDynamic(o, plugin); err != nil {
			t.Fatal(err)
		}
		names = append(names, o.name)
	}
	return names
}

// opWith is registerPlugin's operation, declared the same way, with one
// change applied.
func opWith(plugin, op string, change func(*Discovery)) opTool {
	name := "pl_" + strings.NewReplacer(".", "_", "-", "_").Replace(plugin) + "_" + strings.ReplaceAll(op, ".", "_")
	d := Discovery{Plugin: plugin, Version: "0.1.0", Tier: "T1", Operation: op, Summary: "Operation " + op + " of " + plugin,
		Effects: "read.internal", Capabilities: []string{"ring4.kv"}, MaxResultBytes: 4096, Keywords: []string{"remember"}}
	if change != nil {
		change(&d)
	}
	return opTool{name: name, disc: d}
}

// listed is PromptNames with room for everything.
func listed(r *Registry) []string {
	names, _ := r.PromptNames(1 << 10)
	return names
}

// WHAT A REQUEST LISTS IS DERIVED. An active plugin's
// primary operations are in the prompt's set the moment they register,
// with no call by anyone; its other operations are not, and neither is in
// Discover, the sandbox listing. Deactivated, a primary leaves the set.
func TestPrimaryOperationsAreListedWithoutAnyCall(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	base := len(listed(r))
	primary := registerPrimary(t, r, "com.example.memory", "memory.store", "memory.search")
	other := registerPlugin(t, r, "com.example.memory", "memory.recent", "memory.forget")
	got := listed(r)
	if len(got) != base+2 || !slices.Contains(got, primary[0]) || !slices.Contains(got, primary[1]) || slices.Contains(got, other[0]) || slices.Contains(got, other[1]) {
		t.Fatalf("the prompt set must be the builtins and the two primaries: %v", got)
	}
	if !slices.IsSorted(got) {
		t.Fatalf("the prompt set is not in a stable order: %v", got)
	}
	for _, info := range r.Discover(2) {
		if strings.HasPrefix(info.Name, "pl_") {
			t.Fatalf("Discover lists the sandbox tools, not %s", info.Name)
		}
	}
	if code, reason, ok := r.Unavailable(other[0]); !ok || code != "" || reason != "" {
		t.Fatalf("a non-primary operation is callable (through tools action=call): %q %q %v", code, reason, ok)
	}
	if _, _, ok := r.Unavailable("read"); ok {
		t.Fatal("a builtin is not a plugin operation")
	}
	r.Deregister(primary[0])
	if slices.Contains(listed(r), primary[0]) {
		t.Fatal("a deactivated primary stayed listed")
	}
}

// A HIDDEN PRIMARY IS NEITHER LISTED NOR CALLABLE: disabled by the
// operator, or suspended in SAFE, each with its code; enabled again, it is
// listed again.
func TestAHiddenPrimaryIsNeitherListedNorCallable(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	ops := registerPrimary(t, r, "com.example.memory", "memory.store", "memory.search")
	r.SetToolEnabled(ops[0], false)
	if slices.Contains(listed(r), ops[0]) || !slices.Contains(listed(r), ops[1]) {
		t.Fatalf("a disabled primary must leave the list alone: %v", listed(r))
	}
	if code, reason, _ := r.Unavailable(ops[0]); code != ReasonOperatorDisabled || !strings.Contains(reason, "operator") {
		t.Fatalf("disabled: %q %q", code, reason)
	}
	if res, err := r.ExecuteModel(context.Background(), ops[0], nil); err != nil || res.ReasonCode != ReasonOperatorDisabled {
		t.Fatalf("a call to a disabled primary: %+v %v", res, err)
	}
	r.SetToolEnabled(ops[0], true)
	safe := true
	r.SetSafeSource(func() (string, bool) { return "integrity unverified", safe })
	if names := listed(r); slices.Contains(names, ops[0]) || slices.Contains(names, ops[1]) {
		t.Fatalf("SAFE must suspend the primaries: %v", names)
	}
	if code, _, _ := r.Unavailable(ops[1]); code != ReasonSafeSuspended {
		t.Fatalf("SAFE: %q", code)
	}
	if res, err := r.ExecuteModel(context.Background(), ops[1], nil); err != nil || res.ReasonCode != ReasonSafeSuspended {
		t.Fatalf("a call in SAFE: %+v %v", res, err)
	}
	safe = false
	if names := listed(r); !slices.Contains(names, ops[0]) || !slices.Contains(names, ops[1]) {
		t.Fatalf("out of SAFE, both are listed again: %v", names)
	}
}

// THE LIST HAS A CEILING, ACTIVATION HAS NONE. However many plugins mark
// operations primary, the prompt's set holds room names: the builtins,
// then primaries in tool-name order; the rest are counted, not listed,
// and remain callable.
func TestPrimariesPastTheRoomAreCountedNotListed(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	builtins := len(listed(r))
	var all []string
	for i := range 10 {
		all = append(all, registerPrimary(t, r, fmt.Sprintf("org.example.p%02d", i), "probe.ping", "probe.status")...)
	}
	room := builtins + 5
	names, unlisted := r.PromptNames(room)
	if len(names) != room || unlisted != len(all)-5 {
		t.Fatalf("PromptNames(%d) = %d names, %d unlisted; want %d and %d", room, len(names), unlisted, room, len(all)-5)
	}
	slices.Sort(all)
	for i, name := range all {
		if slices.Contains(names, name) != (i < 5) {
			t.Fatalf("the first five primaries in tool-name order are listed, not others: %v", names)
		}
	}
	if code, _, ok := r.Unavailable(all[len(all)-1]); !ok || code != "" {
		t.Fatalf("an unlisted primary stays callable: %q %v", code, ok)
	}
	if defs := r.ToolDefinitions(room); len(defs) != room {
		t.Fatalf("definitions = %d, want %d", len(defs), room)
	}
}

// A LIVE UPDATE MOVES THE LIST WITH THE AUTHOR'S MARK: an operation the
// new release still marks primary stays listed, one it no longer marks
// leaves, one it newly marks arrives — derived, with nothing to carry.
func TestALiveUpdateMovesTheListWithTheMark(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	const plugin = "com.example.memory"
	ops := registerPrimary(t, r, plugin, "memory.store", "memory.search")
	recent := opWith(plugin, "memory.recent", func(d *Discovery) { d.Primary = true; d.Version = "0.2.0" })
	store := opWith(plugin, "memory.store", func(d *Discovery) { d.Primary = true; d.Version = "0.2.0" })
	search := opWith(plugin, "memory.search", func(d *Discovery) { d.Version = "0.2.0" })
	if err := r.SupersedeOrigin(plugin, []Tool{store, search, recent}, nil); err != nil {
		t.Fatal(err)
	}
	names := listed(r)
	if !slices.Contains(names, ops[0]) || slices.Contains(names, ops[1]) || !slices.Contains(names, recent.name) {
		t.Fatalf("after the update: %v", names)
	}
}

func TestADisabledBuiltinIsWithheldFromTheModel(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	primary := registerPrimary(t, r, "com.example.memory", "memory.recall")
	other := registerPlugin(t, r, "com.example.memory", "memory.forget")
	before := listed(r)
	if !slices.Contains(before, "shell") || !slices.Contains(before, primary[0]) || slices.Contains(before, other[0]) {
		t.Fatalf("the prompt's set before the switch: %v", before)
	}
	r.SetToolEnabled("shell", false)
	want := slices.DeleteFunc(slices.Clone(before), func(n string) bool { return n == "shell" })
	var defs, sandbox []string
	for _, d := range r.ToolDefinitions(1 << 10) {
		defs = append(defs, d.Function.Name)
	}
	for _, info := range r.Discover(2) {
		sandbox = append(sandbox, info.Name)
	}
	if got := listed(r); !slices.Equal(got, want) || !slices.Equal(defs, want) || slices.Contains(sandbox, "shell") || len(sandbox) != len(want)-1 {
		t.Fatalf("with the shell off:\nprompt set %v\ndefinitions %v\nlisting %v\nwant %v", got, defs, sandbox, want)
	}
	if !slices.Contains(r.Names(), "shell") {
		t.Fatal("the operator's toggle list lost the shell")
	}
	if res, err := r.ExecuteModel(context.Background(), "shell", map[string]any{"command": "echo on"}); err != nil || res.ReasonCode != ReasonOperatorDisabled {
		t.Fatalf("a call to the withheld shell must be refused with %s: %+v, %v", ReasonOperatorDisabled, res, err)
	}
	r.SetToolEnabled("shell", true)
	if got := listed(r); !slices.Equal(got, before) {
		t.Fatalf("re-enabled, the prompt's set is %v, want %v", got, before)
	}
}

// The brief is families and counts, each family naming its primaries
// first; a search ranks by the descriptor text; show is the one place a
// schema appears; hidden operations are counted as unavailable and never
// listed.
func TestBriefSearchAndShowAreBoundedViews(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	mem := registerPlugin(t, r, "com.example.memory", "memory.forget", "memory.recall")
	mem = append(mem, registerPrimary(t, r, "com.example.memory", "memory.store")...)
	wx := registerPlugin(t, r, "com.example.weather", "weather.current", "weather.forecast")
	r.SetToolEnabled(wx[1], false)

	b := r.Brief()
	if b.Total != 4 || b.Unavailable != 1 {
		t.Fatalf("brief counts: %+v", b)
	}
	if len(b.Families) != 2 || b.Families[0].Name != "memory" || b.Families[0].Count != 3 || b.Families[1].Name != "weather" || b.Families[1].Count != 1 {
		t.Fatalf("brief families: %+v", b.Families)
	}
	if got := b.Families[0].Names; !slices.Equal(got, []string{"memory.store", "memory.forget", "memory.recall"}) {
		t.Fatalf("a family names its primary first: %v", got)
	}
	for _, f := range b.Families {
		if slices.Contains(f.Names, "weather.forecast") {
			t.Fatal("a disabled operation was listed")
		}
	}

	hits := r.Search("recall a memory", 0)
	if len(hits) == 0 || hits[0].Name != mem[1] || hits[0].Primary {
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
	if card.Name != mem[1] || card.Family != "memory" || card.Plugin != "com.example.memory" || card.Version != "0.1.0" || card.Tier != "T1" || card.Reason != "" || card.Primary || card.MaxResultBytes != 4096 {
		t.Fatalf("card: %+v", card)
	}
	if card.Parameters == nil || card.Parameters["type"] != "object" {
		t.Fatalf("show carries the schema: %+v", card.Parameters)
	}
	if !strings.Contains(card.Receipt, "check the returned data") || !strings.Contains(card.Receipt, "scope and coverage") {
		t.Fatalf("show carries the receipt rule for %s: %q", card.Effects, card.Receipt)
	}
	if store, err := r.Show("memory.store"); err != nil || !store.Primary {
		t.Fatalf("a primary's card says so: %+v %v", store, err)
	}
	hidden, err := r.Show(wx[1])
	if err != nil || !strings.Contains(hidden.Reason, "operator") {
		t.Fatalf("a hidden operation shows with its reason: %+v %v", hidden, err)
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
	if _, err := r.Resolve("memory.recall"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("ambiguous id must refuse naming the candidates: %v", err)
	}
	if name, err := r.Resolve(a[0]); err != nil || name != a[0] {
		t.Fatalf("resolve by tool name: %q %v", name, err)
	}
}

// A host op (a channel adapter's plumbing) is neither discoverable nor
// listed — it is the host's, and the tools organ never shows it, even
// when its author marked it primary.
func TestHostOpsAreOutsideDiscovery(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	if err := r.RegisterHostOp(opTool{name: "pl_x_send", disc: Discovery{Operation: "channel.send", Primary: true}}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	if r.HasDynamic() {
		t.Fatal("a host op is not a discoverable plugin operation")
	}
	if b := r.Brief(); b.Total != 0 {
		t.Fatalf("brief counted a host op: %+v", b)
	}
	if slices.Contains(listed(r), "pl_x_send") {
		t.Fatal("a host op was listed")
	}
	if _, err := r.Resolve("pl_x_send"); err == nil {
		t.Fatal("a host op cannot be resolved for a call")
	}
	registerPlugin(t, r, "org.example.y", "y.op")
	if !r.HasDynamic() {
		t.Fatal("a registered plugin operation makes the posture due")
	}
}
