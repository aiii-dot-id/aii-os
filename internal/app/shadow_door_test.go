package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// Legacy category calls from retained history still route, while new turns
// use explicitly offered operations with their own schemas.
func TestLegacyMemoryDoorRoutesWithoutEnteringNewToolLists(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "ShadowDoorTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	const id = "org.example.mem"
	installPluginDir(t, dir, id, buildResponderPkgWithOps(t, dir, id,
		[]string{"memory.store", "memory.search", "memory.recent", "memory.reflect"}))
	t.Chdir(dir)
	a := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	})
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if len(a.plugins) != 1 {
		t.Fatalf("the plugin did not activate: %d active", len(a.plugins))
	}

	// Built TWICE, and judged on the second: a door is rendered every turn,
	// and one that quietly offered its operations while rendering would
	// leak them into the turn after this one rather than this one.
	a.buildToolDefinitions()
	var door *string
	var ops int
	for _, d := range a.buildToolDefinitions() {
		if d.Function.Name == "memory" {
			n := d.Function.Name
			door = &n
		}
		if strings.HasPrefix(d.Function.Name, "pl_") {
			ops++
		}
	}
	if door != nil {
		t.Fatal("legacy untyped category wrapper entered a new turn")
	}
	if ops != 0 {
		t.Errorf("the plugin's own operations entered the tool list beside the door (%d of them): installed plugins are not prompt context", ops)
	}

	// And it reaches the operation. Which one is asked of the registry
	// itself — the fixture answers every operation the same way, so the
	// resolution is where the truth is — and then the call is made and
	// must succeed, which an unregistered name could not.
	target, err := a.toolReg.ShadowTool("memory", "store")
	if err != nil || target != "pl_org_example_mem_memory_store" {
		t.Fatalf("the door resolved store to %q (%v), not the plugin's own operation", target, err)
	}
	obs := a.callDoor(t, "memory", `{"action":"store","args":{"text":"a working note"}}`)
	if obs.Failed {
		t.Fatalf("the door did not reach memory.store: %q", obs.Text)
	}
	// An action it does not have is refused in the door's own words.
	bad := a.callDoor(t, "memory", `{"action":"invent"}`)
	if !bad.Failed || !strings.Contains(bad.Text, "has no action") {
		t.Fatalf("an action the door does not have must be refused by name: %q", bad.Text)
	}
	// And no action at all names what there is.
	none := a.callDoor(t, "memory", `{}`)
	if !none.Failed || !strings.Contains(none.Text, "needs an action") {
		t.Fatalf("a door called with no action must say what it has: %q", none.Text)
	}
}

// callDoor issues one tool call the way a provider does.
func (a *App) callDoor(t *testing.T, name, args string) conversation.Observation {
	t.Helper()
	var tc llm.ToolCall
	tc.Function.Name, tc.Function.Arguments = name, args
	return a.executeToolCall(t.Context(), tc)
}

// An installed family contributes no hidden category wrapper or per-turn
// prose. Only explicit operation selections spend prompt seats.
func TestAnUnselectedFamilyAddsNoCategoryPromptCost(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "ShadowCostTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	const id = "org.example.mem"
	installPluginDir(t, dir, id, buildResponderPkgWithOps(t, dir, id,
		[]string{"memory.store", "memory.search", "memory.recent", "memory.get", "memory.update", "memory.evict", "memory.stats", "memory.health", "memory.recall"}))
	t.Chdir(dir)
	a := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	})
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)

	var doorBytes, allBytes int
	for _, d := range a.buildToolDefinitions() {
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		allBytes += len(raw)
		if d.Function.Name == "memory" {
			doorBytes = len(raw)
		}
	}
	if doorBytes != 0 {
		t.Fatalf("unselected family added %d wrapper bytes to %d-byte prompt", doorBytes, allBytes)
	}
}

func TestACallToADoorSaysWhyItIsNotThere(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "DoorReasonTest",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	// The resident's memory publishes `store` AND `memory.store`, which
	// flatten to one action; a rival publishes the same family.
	const mine, rival = "org.example.memory", "org.example.rival"
	installPluginDir(t, dir, mine, buildResponderPkgWithOps(t, dir, mine,
		[]string{"store", "memory.store", "memory.search", "memory.recent"}))
	installPluginDir(t, dir, rival, buildResponderPkgWithOps(t, dir, rival,
		[]string{"memory.store", "memory.search", "memory.recent"}))
	t.Chdir(dir)
	a := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db")},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Plugins:    PluginsConfig{Autoload: "T0"},
		Agency:     defaultConfig().Agency,
	})
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, a)
	if a.activePlugin(mine) == nil || a.activePlugin(rival) == nil {
		t.Fatalf("both plugins must be active: %d active", len(a.plugins))
	}
	a.resetTurnMeter()
	refused := func(want string) {
		t.Helper()
		obs := a.callDoor(t, "memory", `{"action":"search","args":{"query":"x"}}`)
		if !obs.Failed || obs.ReasonCode != want {
			t.Fatalf("want a refusal coded %q, got %q: %q", want, obs.ReasonCode, obs.Text)
		}
	}

	refused(tools.ReasonDoorConflicted)

	// The rival leaves and the door is there: no action named twice in
	// what the identity is shown, and every operation reachable through it.
	if err := a.activePlugin(rival).Deactivate(t.Context()); err != nil {
		t.Fatal(err)
	}
	var enum []any
	for _, d := range a.toolReg.Shadows() {
		if d.Name == "memory" {
			for _, action := range d.Actions {
				enum = append(enum, action.Action)
			}
		}
	}
	seen, reached := map[string]bool{}, map[string]bool{}
	for _, v := range enum {
		action, _ := v.(string)
		if seen[action] {
			t.Errorf("the door offers %q twice: %v", action, enum)
		}
		seen[action] = true
		target, err := a.toolReg.ShadowTool("memory", action)
		if err != nil {
			t.Fatalf("%q does not resolve: %v", action, err)
		}
		reached[target] = true
		if obs := a.callDoor(t, "memory", fmt.Sprintf(`{"action":%q}`, action)); obs.Failed {
			t.Errorf("%q did not run: %q", action, obs.Text)
		}
	}
	if len(reached) != 4 {
		t.Errorf("the door reaches %d of the family's four operations: %v", len(reached), enum)
	}

	// The operator switches off part of its minimum: the door gives that
	// operation's cause as a code, in the sentence its own name is refused
	// with.
	const recent = "pl_org_example_memory_memory_recent"
	a.toolReg.SetToolEnabled(recent, false)
	refused(tools.ReasonOperatorDisabled)
	direct := a.callDoor(t, recent, `{}`)
	if _, _, reason, _ := a.toolReg.State(recent); !strings.Contains(direct.Text, reason) ||
		!strings.Contains(a.callDoor(t, "memory", `{"action":"search"}`).Text, reason) {
		t.Errorf("the door and its target do not give the same reason %q: %q", reason, direct.Text)
	}
	a.toolReg.SetToolEnabled(recent, true)

	a.enterSafe("test: integrity lost")
	refused(tools.ReasonSafeSuspended)
	a.resetModeForTest()

	if err := a.activePlugin(mine).Deactivate(t.Context()); err != nil {
		t.Fatal(err)
	}
	refused(tools.ReasonDoorAbsent)

	a.turn.mu.Lock()
	calls := a.turn.turnCalls
	a.turn.mu.Unlock()
	if calls != len(enum) {
		t.Errorf("the meter counted %d calls; %d ran", calls, len(enum))
	}

	dark := a.callDoor(t, "voice", `{"action":"speak"}`)
	nobody := a.callDoor(t, "nosuchname", `{"action":"speak"}`)
	if !dark.Failed || dark.ReasonCode != "" || dark.Text != strings.ReplaceAll(nobody.Text, "nosuchname", "voice") {
		t.Errorf("a dark category answered %+v; an undeclared name answers %+v", dark, nobody)
	}
}

// declaredOp is a plugin operation that says what it is, so every
// classifier has a declared answer to give for it.
type declaredOp struct {
	name, op string
	read     bool
}

func (o declaredOp) Name() string        { return o.name }
func (o declaredOp) Description() string { return o.op }
func (o declaredOp) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (o declaredOp) Discovery() tools.Discovery {
	return tools.Discovery{Plugin: "org.example.memory", Operation: o.op, Summary: o.op}
}
func (o declaredOp) ReadOnly() bool   { return o.read }
func (o declaredOp) ReplaySafe() bool { return o.read }
func (o declaredOp) Execute(context.Context, map[string]any) (tools.Result, error) {
	return tools.Result{Output: "ran " + o.op}, nil
}

func TestADoorCallClassifiesAsItsTarget(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	for _, op := range []declaredOp{{op: "memory.store"}, {op: "memory.search", read: true}, {op: "memory.recent", read: true}} {
		op.name = "pl_org_example_memory_" + strings.ReplaceAll(op.op, ".", "_")
		if err := reg.RegisterDynamic(op, "org.example.memory"); err != nil {
			t.Fatal(err)
		}
	}
	a := &App{toolReg: reg}
	classifiers := []struct {
		name string
		ask  func(llm.ToolCall) bool
	}{
		{"ParallelSafe", appToolExecutor{a: a}.ParallelSafe},
		{"ReplaySafe", replaySafeHook(reg)},
	}
	call := func(name, args string) llm.ToolCall {
		var tc llm.ToolCall
		tc.Function.Name, tc.Function.Arguments = name, args
		return tc
	}
	meter := func(tc llm.ToolCall) (calls, reads int) {
		a.resetTurnMeter()
		if obs := a.executeToolCall(t.Context(), tc); obs.Failed {
			t.Fatalf("%s did not run: %q", tc.Function.Name, obs.Text)
		}
		a.turn.mu.Lock()
		defer a.turn.mu.Unlock()
		return a.turn.turnCalls, a.turn.turnReadOnly
	}
	for _, c := range []struct {
		action, target string
		read           bool
	}{
		{"search", "pl_org_example_memory_memory_search", true},
		{"store", "pl_org_example_memory_memory_store", false},
	} {
		door := call("memory", fmt.Sprintf(`{"action":%q,"args":{"text":"x"}}`, c.action))
		direct := call(c.target, `{"text":"x"}`)
		for _, k := range classifiers {
			want := c.read
			if got, byName := k.ask(door), k.ask(direct); got != byName || byName != want {
				t.Errorf("%s through the door: %s=%v; by its own name %v; declared %v", c.action, k.name, got, byName, want)
			}
		}
		if _, err := reg.Offer(c.target); err != nil {
			t.Fatal(err)
		}
		dc, dr := meter(door)
		nc, nr := meter(direct)
		if dc != nc || dr != nr {
			t.Errorf("%s: the meter counted the door as %d call(s), %d read(s); the direct call as %d, %d", c.action, dc, dr, nc, nr)
		}
	}
	// A door that refuses is the unknown name it is: serial and not
	// repeatable — every classifier answers it closed.
	refused := call("memory", `{"action":"invent"}`)
	for _, k := range classifiers {
		if k.ask(refused) {
			t.Errorf("a refused door call: %s=true, want false", k.name)
		}
	}
}

func TestADoorAndItsTargetRefuseWithOneCode(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	for _, op := range []declaredOp{{op: "memory.store"}, {op: "memory.search", read: true}, {op: "memory.recent", read: true}} {
		op.name = "pl_org_example_memory_" + strings.ReplaceAll(op.op, ".", "_")
		if err := reg.RegisterDynamic(op, "org.example.memory"); err != nil {
			t.Fatal(err)
		}
	}
	safe := false
	reg.SetSafeSource(func() (string, bool) { return "test: integrity lost", safe })
	a := &App{toolReg: reg}
	const search = "pl_org_example_memory_memory_search"
	door := func() conversation.Observation {
		return a.callDoor(t, "memory", `{"action":"search","args":{"query":"x"}}`)
	}
	direct := func() conversation.Observation { return a.callDoor(t, search, `{"query":"x"}`) }

	if obs := direct(); !obs.Failed || obs.ReasonCode != tools.ReasonInspectOnly {
		t.Errorf("an operation not offered: want a refusal coded %q, got %q: %q", tools.ReasonInspectOnly, obs.ReasonCode, obs.Text)
	}
	if obs := door(); obs.Failed {
		t.Errorf("the door must reach an operation that is not offered: %q", obs.Text)
	}

	for _, c := range []struct {
		cause      string
		code       string
		hide, show func()
	}{
		{"the operator's switch", tools.ReasonOperatorDisabled,
			func() { reg.SetToolEnabled(search, false) }, func() { reg.SetToolEnabled(search, true) }},
		{"SAFE", tools.ReasonSafeSuspended, func() { safe = true }, func() { safe = false }},
	} {
		c.hide()
		d, n := door(), direct()
		if !d.Failed || !n.Failed || d.ReasonCode != c.code || n.ReasonCode != c.code {
			t.Errorf("hidden by %s: the door refused %q (%q), the direct call %q (%q); want both %q",
				c.cause, d.ReasonCode, d.Text, n.ReasonCode, n.Text, c.code)
		}
		c.show()
	}
}
