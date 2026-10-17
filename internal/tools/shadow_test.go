package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeOp is one plugin operation as the registry holds it.
type fakeOp struct {
	disc Discovery
}

func (f fakeOp) Discovery() Discovery       { return f.disc }
func (f fakeOp) Name() string               { return f.disc.Operation }
func (f fakeOp) Description() string        { return f.disc.Summary }
func (f fakeOp) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (f fakeOp) Execute(ctx context.Context, args map[string]any) (Result, error) {
	return Result{Output: "ran " + f.disc.Operation}, nil
}

func memoryRegistry(t *testing.T, ops ...string) *Registry {
	t.Helper()
	r := &Registry{tools: map[string]Tool{}, sources: map[string]string{}}
	for _, op := range ops {
		name := "pl_id_example_memory_" + strings.TrimPrefix(op, "memory.")
		r.tools[name] = fakeOp{disc: Discovery{
			Plugin: "id.example.memory", Operation: op, Family: FamilyOf(op),
			Summary: "the author's line about " + op,
		}}
		r.sources[name] = "id.example.memory"
	}
	return r
}

// A DOOR APPEARS ONLY WHERE A PLUGIN FILLS IT, and it is the family's own
// operations that fill it — including ones the host never heard of, because
// an author who adds an operation gets it in the door without asking anyone.
func TestAMemoryDoorAppearsWhenAPluginFillsIt(t *testing.T) {
	if got := (&Registry{tools: map[string]Tool{}, sources: map[string]string{}}).Shadows(); len(got) != 0 {
		t.Fatalf("a door appeared with no plugin behind it: %+v", got)
	}
	// The minimum, and nothing else: the door is there.
	r := memoryRegistry(t, "memory.store", "memory.search", "memory.recent")
	doors := r.Shadows()
	if len(doors) != 1 || doors[0].Name != "memory" {
		t.Fatalf("the memory door did not appear: %+v", doors)
	}
	if len(doors[0].Actions) != 3 {
		t.Fatalf("the door does not carry the family's operations: %+v", doors[0].Actions)
	}
	for _, a := range doors[0].Actions {
		if !strings.Contains(a.Summary, "the author's line") {
			t.Errorf("an action lost its author's description: %+v", a)
		}
		if strings.Contains(a.Action, ".") {
			t.Errorf("an action still carries the family the door already names: %q", a.Action)
		}
	}
	// One short of the minimum: no door at all.
	if got := memoryRegistry(t, "memory.store", "memory.search").Shadows(); len(got) != 0 {
		t.Fatalf("a door appeared without its minimum: %+v", got)
	}
	// Operations the host never declared ride along.
	extra := memoryRegistry(t, "memory.store", "memory.search", "memory.recent", "memory.reflect")
	found := false
	for _, a := range extra.Shadows()[0].Actions {
		if a.Action == "reflect" {
			found = true
		}
	}
	if !found {
		t.Error("an operation the author added is not under the door")
	}
}

// A DOOR RESOLVES TO THE OPERATION IT STANDS FOR, and refuses in its own
// words when it cannot — a shadow must never fall through to "unknown tool".
func TestAMemoryDoorResolvesToTheOperationBehindIt(t *testing.T) {
	r := memoryRegistry(t, "memory.store", "memory.search", "memory.recent")
	got, err := r.ShadowTool("memory", "store")
	if err != nil || got != "pl_id_example_memory_store" {
		t.Fatalf("store did not resolve to its operation: %q %v", got, err)
	}
	_, err = r.ShadowTool("memory", "invent")
	if err == nil || !strings.Contains(err.Error(), "has no action") || !strings.Contains(err.Error(), "store") {
		t.Fatalf("an action that does not exist must be refused by name, and say what there is: %v", err)
	}
	_, err = (&Registry{tools: map[string]Tool{}, sources: map[string]string{}}).ShadowTool("memory", "store")
	if err == nil || !strings.Contains(err.Error(), "no memory is available") {
		t.Fatalf("a door with no plugin must say so: %v", err)
	}
}

func TestOnlyLitCategoriesRender(t *testing.T) {
	var lit, declared int
	for _, c := range categories {
		declared++
		if c.Enabled {
			lit++
			if len(c.Minimum) == 0 {
				t.Errorf("category %q is lit with no minimum: its door would mean nothing", c.Family)
			}
		}
		if c.Blurb == "" {
			t.Errorf("category %q has no line saying what it is for", c.Family)
		}
	}
	if lit != 1 || declared < 3 {
		t.Fatalf("%d of %d categories are lit; memory alone is lit today", lit, declared)
	}
	// AND A DARK CATEGORY STAYS DARK EVEN WHEN A PLUGIN FILLS IT. This is
	// the whole of "declared, and lit separately": the voice plugin is
	// installed on this machine and publishes its family, and no door
	// appears until someone decides it should.
	for _, c := range categories {
		if c.Enabled || len(c.Minimum) == 0 {
			continue
		}
		r := &Registry{tools: map[string]Tool{}, sources: map[string]string{}}
		for _, op := range c.Minimum {
			name := "pl_test_" + strings.ReplaceAll(op, ".", "_")
			r.tools[name] = fakeOp{disc: Discovery{Plugin: "id.test." + c.Family, Operation: op, Family: FamilyOf(op), Summary: "filled"}}
			r.sources[name] = "id.test." + c.Family
		}
		for _, d := range r.Shadows() {
			if d.Name == c.Family {
				t.Errorf("category %q is dark and rendered a door anyway: %+v", c.Family, d)
			}
		}
	}
}

func TestADoorIsBounded(t *testing.T) {
	ops := []string{"memory.store", "memory.search", "memory.recent"}
	for i := 0; i < maxShadowActions+5; i++ {
		ops = append(ops, "memory.extra"+string(rune('a'+i)))
	}
	door := memoryRegistry(t, ops...).Shadows()[0]
	if len(door.Actions) > maxShadowActions {
		t.Fatalf("the door rendered %d actions, over its bound of %d", len(door.Actions), maxShadowActions)
	}
	if door.Overflow == 0 || !strings.Contains(door.Description, "tools brief") {
		t.Fatalf("an over-full door must carry its minimum and say where the rest are: %+v", door)
	}
	for _, want := range []string{"store", "search", "recent"} {
		found := false
		for _, a := range door.Actions {
			if a.Action == want {
				found = true
			}
		}
		if !found {
			t.Errorf("an over-full door dropped %q, which is its minimum", want)
		}
	}
}

// TWO PLUGINS IN ONE FAMILY IS NOT A DOOR. Their operations merge under
// one name — two `store` actions, and whichever sorted first would win — so
// the identity would be storing into one of two memories without knowing
// which, and reading back from the other. A door that quietly picks is
// worse than no door: this refuses, names both, and leaves the choice with
// the operator.
func TestTwoPluginsInOneFamilyGetNoDoor(t *testing.T) {
	r := &Registry{tools: map[string]Tool{}, sources: map[string]string{}}
	for _, plugin := range []string{"id.example.memory", "id.rival.memory"} {
		for _, op := range []string{"memory.store", "memory.search", "memory.recent"} {
			name := "pl_" + strings.ReplaceAll(plugin, ".", "_") + "_" + strings.TrimPrefix(op, "memory.")
			r.tools[name] = fakeOp{disc: Discovery{Plugin: plugin, Operation: op, Family: FamilyOf(op), Summary: "s"}}
			r.sources[name] = plugin
		}
	}
	doors := r.Shadows()
	if len(doors) != 1 {
		t.Fatalf("want one refused door, got %d: %+v", len(doors), doors)
	}
	d := doors[0]
	if !d.Conflicted() {
		t.Fatalf("TWO MEMORIES RENDERED ONE DOOR: %+v", d)
	}
	if len(d.Actions) != 0 {
		t.Errorf("a refused door still offers actions: %+v", d.Actions)
	}
	if len(d.Conflict) != 2 || d.Conflict[0] != "id.example.memory" || d.Conflict[1] != "id.rival.memory" {
		t.Errorf("the refusal does not name both plugins, in a stable order: %v", d.Conflict)
	}
	_, err := r.ShadowTool("memory", "store")
	if err == nil || !strings.Contains(err.Error(), "more than one plugin") ||
		!strings.Contains(err.Error(), "id.rival.memory") {
		t.Fatalf("a call into a contested family must refuse and say why: %v", err)
	}
	// One of them leaves, and the door is there again.
	for name := range r.tools {
		if strings.Contains(name, "rival") {
			delete(r.tools, name)
			delete(r.sources, name)
		}
	}
	back := r.Shadows()
	if len(back) != 1 || back[0].Conflicted() || len(back[0].Actions) != 3 {
		t.Fatalf("with one plugin left the door must come back whole: %+v", back)
	}
}

// NO DOOR HOLDS TWO ACTIONS OF ONE NAME. An undotted method takes its
// plugin id's last segment as its family, so a memory plugin publishing
// `store` and `memory.store` put two actions called `store` in one door:
// the enum repeated itself, a call reached whichever sorted first, and the
// other operation was unreachable through the door. Each keeps its tool
// name instead (actionName's promise) — neither is picked, both are there.
func TestADoorNeverHoldsTwoActionsOfOneName(t *testing.T) {
	r := &Registry{tools: map[string]Tool{}, sources: map[string]string{}}
	for _, op := range []string{"store", "memory.store", "memory.search", "memory.recent"} {
		name := "pl_org_example_memory_" + strings.ReplaceAll(op, ".", "_")
		r.tools[name] = fakeOp{disc: Discovery{Plugin: "org.example.memory", Operation: op, Summary: "s"}}
		r.sources[name] = "org.example.memory"
	}
	doors := r.Shadows()
	if len(doors) != 1 || doors[0].Name != "memory" || doors[0].Conflicted() {
		t.Fatalf("the family must render one memory door: %+v", doors)
	}
	seen, reached := map[string]bool{}, map[string]bool{}
	for _, a := range doors[0].Actions {
		if seen[a.Action] {
			t.Errorf("the door holds two actions named %q: %+v", a.Action, doors[0].Actions)
		}
		seen[a.Action] = true
		tool, err := r.ShadowTool("memory", a.Action)
		if err != nil || tool != a.Tool {
			t.Errorf("action %q reached %q (%v), not its own operation %q", a.Action, tool, err, a.Tool)
		}
		reached[tool] = true
	}
	for name := range r.tools {
		if !reached[name] {
			t.Errorf("%s is in the family and unreachable through the door: %+v", name, doors[0].Actions)
		}
	}
}

func TestADoorThatIsNotThereSaysWhy(t *testing.T) {
	codeOf := func(t *testing.T, err error) string {
		t.Helper()
		var refusal *DoorError
		if !errors.As(err, &refusal) {
			t.Fatalf("the door's refusal is not a *DoorError: %v", err)
		}
		if refusal.Door != "memory" {
			t.Errorf("the refusal names door %q, not memory", refusal.Door)
		}
		return refusal.Code
	}
	full := func() *Registry {
		r := memoryRegistry(t, "memory.store", "memory.search", "memory.recent")
		r.disabled = map[string]bool{}
		return r
	}

	t.Run("no plugin publishes it", func(t *testing.T) {
		_, err := (&Registry{tools: map[string]Tool{}, sources: map[string]string{}}).ShadowTool("memory", "search")
		if got := codeOf(t, err); got != ReasonDoorAbsent {
			t.Errorf("code %q, want %q: %v", got, ReasonDoorAbsent, err)
		}
		_, err = memoryRegistry(t, "memory.store", "memory.search").ShadowTool("memory", "search")
		if got := codeOf(t, err); got != ReasonDoorAbsent || !strings.Contains(err.Error(), "memory.recent") {
			t.Errorf("a door short of its minimum must name what is missing: %q %v", got, err)
		}
	})
	t.Run("two plugins publish it", func(t *testing.T) {
		r := full()
		name := "pl_id_rival_memory_store"
		r.tools[name] = fakeOp{disc: Discovery{Plugin: "id.rival.memory", Operation: "memory.store", Family: "memory", Summary: "s"}}
		r.sources[name] = "id.rival.memory"
		_, err := r.ShadowTool("memory", "search")
		if got := codeOf(t, err); got != ReasonDoorConflicted {
			t.Errorf("code %q, want %q: %v", got, ReasonDoorConflicted, err)
		}
	})
	t.Run("the operator disabled one of its minimum", func(t *testing.T) {
		r := full()
		r.SetToolEnabled("pl_id_example_memory_recent", false)
		_, err := r.ShadowTool("memory", "search")
		if got := codeOf(t, err); got != ReasonOperatorDisabled {
			t.Errorf("code %q, want %q: %v", got, ReasonOperatorDisabled, err)
		}
		if _, _, reason, _ := r.State("pl_id_example_memory_recent"); !strings.Contains(err.Error(), reason) {
			t.Errorf("the door does not give its target's reason %q: %v", reason, err)
		}
	})
	t.Run("SAFE suspends it", func(t *testing.T) {
		r := full()
		r.SetSafeSource(func() (string, bool) { return "integrity lost", true })
		_, err := r.ShadowTool("memory", "search")
		if got := codeOf(t, err); got != ReasonSafeSuspended {
			t.Errorf("code %q, want %q: %v", got, ReasonSafeSuspended, err)
		}
		if _, _, reason, _ := r.State("pl_id_example_memory_store"); !strings.Contains(err.Error(), reason) {
			t.Errorf("the door does not give its target's reason %q: %v", reason, err)
		}
	})
	t.Run("an operation no plugin publishes outranks a hidden one", func(t *testing.T) {
		// Switching memory.store back on would not bring the door back:
		// nobody publishes memory.recent. The refusal must not blame the
		// operator's switch.
		r := memoryRegistry(t, "memory.store", "memory.search")
		r.disabled = map[string]bool{}
		r.SetToolEnabled("pl_id_example_memory_store", false)
		_, err := r.ShadowTool("memory", "search")
		if got := codeOf(t, err); got != ReasonDoorAbsent || !strings.Contains(err.Error(), "memory.recent") {
			t.Errorf("code %q, want %q naming memory.recent: %v", got, ReasonDoorAbsent, err)
		}
	})
	t.Run("every hidden cause is named", func(t *testing.T) {
		// Leaving SAFE alone would not bring the door back: the operator
		// has switched off memory.recent as well. Both causes are said.
		r := full()
		r.SetToolEnabled("pl_id_example_memory_recent", false)
		r.SetSafeSource(func() (string, bool) { return "integrity lost", true })
		_, err := r.ShadowTool("memory", "search")
		if got := codeOf(t, err); got != ReasonSafeSuspended {
			t.Errorf("code %q, want %q (the first missing operation's cause): %v", got, ReasonSafeSuspended, err)
		}
		for _, name := range []string{"pl_id_example_memory_store", "pl_id_example_memory_recent"} {
			if _, _, reason, _ := r.State(name); !strings.Contains(err.Error(), reason) {
				t.Errorf("the door does not give %s's reason %q: %v", name, reason, err)
			}
		}
	})
	t.Run("the operator hid an action outside its minimum", func(t *testing.T) {
		// The door is there and the operation exists: a call to it is
		// told it is hidden and why, as its own tool name would be — not
		// that the door has no such action.
		r := memoryRegistry(t, "memory.store", "memory.search", "memory.recent", "memory.stats")
		r.disabled = map[string]bool{}
		const stats = "pl_id_example_memory_stats"
		r.SetToolEnabled(stats, false)
		if tool, err := r.ShadowTool("memory", "search"); err != nil || tool != "pl_id_example_memory_search" {
			t.Fatalf("the door must still be there: %q %v", tool, err)
		}
		_, _, reason, _ := r.State(stats)
		for _, action := range []string{"stats", stats} {
			_, err := r.ShadowTool("memory", action)
			if got := codeOf(t, err); got != ReasonOperatorDisabled || !strings.Contains(err.Error(), reason) {
				t.Errorf("action %q: code %q, want %q with its reason %q: %v", action, got, ReasonOperatorDisabled, reason, err)
			}
		}
	})
	t.Run("the call names no action it has", func(t *testing.T) {
		for _, action := range []string{"", "invent"} {
			_, err := full().ShadowTool("memory", action)
			if got := codeOf(t, err); got != ReasonDoorAction {
				t.Errorf("action %q: code %q, want %q: %v", action, got, ReasonDoorAction, err)
			}
		}
	})
}
