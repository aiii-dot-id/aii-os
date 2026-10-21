package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// declaredOp is a plugin operation as a package declares it: its tool
// name, its operation id, whether it reads only, whether its author marked
// it primary, and the arguments it requires. Each call is counted.
type declaredOp struct {
	name, op string
	read     bool
	primary  bool
	required []string
	ran      *atomic.Int32
}

func (o declaredOp) Name() string        { return o.name }
func (o declaredOp) Description() string { return o.op }
func (o declaredOp) Parameters() map[string]any {
	p := map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}
	if len(o.required) > 0 {
		p["required"] = o.required
	}
	return p
}
func (o declaredOp) Discovery() tools.Discovery {
	return tools.Discovery{Plugin: "org.example.memory", Operation: o.op, Summary: o.op, Primary: o.primary}
}
func (o declaredOp) ReadOnly() bool   { return o.read }
func (o declaredOp) ReplaySafe() bool { return o.read }
func (o declaredOp) Execute(context.Context, map[string]any) (tools.Result, error) {
	if o.ran != nil {
		o.ran.Add(1)
	}
	return tools.Result{Output: "ran " + o.op}, nil
}

// primaryOp is fakeOp with its author's primary mark.
type primaryOp struct{ fakeOp }

func (*primaryOp) Discovery() tools.Discovery { return tools.Discovery{Primary: true} }

// turnScript answers each request that may call tools with the next
// response in its queue — a nil response is the turn's answer, "done", as
// is any request once the queue is empty. It keeps the tool names every
// request carried and the observations handed back.
type turnScript struct {
	mu        sync.Mutex
	responses [][]llm.ToolCall
	sent      [][]string        // the tool names of each request, in order
	observed  map[string]string // tool call id → the observation handed back
}

func (s *turnScript) Chat(_ context.Context, msgs []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		if m.Role == "tool" {
			s.observed[m.ToolCallID] = m.Content
		}
	}
	if opts.DisableTools || len(s.responses) == 0 {
		return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}}, nil
	}
	var names []string
	for _, d := range opts.Tools {
		names = append(names, d.Function.Name)
	}
	s.sent = append(s.sent, names)
	next := s.responses[0]
	s.responses = s.responses[1:]
	if next == nil {
		return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}}, nil
	}
	return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: next}}}}, nil
}

func scriptCall(id, name string, args map[string]any) llm.ToolCall {
	raw, _ := json.Marshal(args)
	var tc llm.ToolCall
	tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = id, "function", name, string(raw)
	return tc
}

// toolRecords is every recorded result of tool, oldest first: its outcome
// and its reason code.
func toolRecords(t *testing.T, a *App, tool string) [][2]string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT outcome, COALESCE(json_extract(details, '$.reason_code'), '') FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')=? ORDER BY rowid`, tool)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var r [2]string
		if err := rows.Scan(&r[0], &r[1]); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// dyadApp is an app on a fresh registry whose plugins declare what the
// Memory and Planning plugins do: Memory's store and search, and
// Planning's review, marked primary; Memory's recent, not. Nothing is
// offered, selected or remembered: there is no such thing.
func dyadApp(t *testing.T) (*App, map[string]*atomic.Int32) {
	t.Helper()
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	a.engine = identity.NewEngine(a.store, nil, ring.NewManager(), toolDiscovererAdapter{a.toolReg}, identity.Ports{})
	ran := map[string]*atomic.Int32{}
	for _, op := range []struct {
		plugin, name, op string
		primary          bool
		required         []string
	}{
		{"id.aiii.memory", "pl_id_aiii_memory_store", "store", true, []string{"text"}},
		{"id.aiii.memory", "pl_id_aiii_memory_search", "search", true, nil},
		{"id.aiii.memory", "pl_id_aiii_memory_recent", "recent", false, []string{"text"}},
		{"id.aiii.planning", "pl_id_aiii_planning_review", "review", true, nil},
	} {
		n := &atomic.Int32{}
		ran[op.name] = n
		if err := a.toolReg.RegisterDynamic(declaredOp{name: op.name, op: op.op, primary: op.primary, required: op.required, ran: n}, op.plugin); err != nil {
			t.Fatal(err)
		}
	}
	return a, ran
}

func runScript(t *testing.T, a *App, model *turnScript) {
	t.Helper()
	loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, testLoop(conversation.Config{}))
	if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "keep what you learned and review the work"}}); err != nil {
		t.Fatal(err)
	}
}

// THE DYAD IS CALLABLE IN THE FIRST RESPONSE OF A TURN. A fresh identity
// could make many calls and no successful Memory or Planning call: the
// operations were inspect-only until offered, an
// offer was callable only from the next response, and the seats were
// withheld when a release changed a schema. Now the first request of the
// first turn lists Memory's store and search and Planning's review, and
// the model's first response calls all three by their tool names and each
// runs, recorded succeeded.
func TestTheDyadIsCallableInTheFirstResponseOfATurn(t *testing.T) {
	a, ran := dyadApp(t)
	primaries := []string{"pl_id_aiii_memory_store", "pl_id_aiii_memory_search", "pl_id_aiii_planning_review"}
	model := &turnScript{observed: map[string]string{}, responses: [][]llm.ToolCall{
		{scriptCall("store", primaries[0], map[string]any{"text": "the shorter way"}),
			scriptCall("search", primaries[1], map[string]any{"text": "shorter"}),
			scriptCall("review", primaries[2], nil)},
		nil,
	}}
	runScript(t, a, model)
	if len(model.sent) == 0 {
		t.Fatal("no request was sent")
	}
	for _, name := range primaries {
		if !slices.Contains(model.sent[0], name) {
			t.Fatalf("the first request must list %s: %v", name, model.sent[0])
		}
		if ran[name].Load() != 1 {
			t.Fatalf("%s must run in the first response: ran %d", name, ran[name].Load())
		}
		if got := toolRecords(t, a, name); len(got) != 1 || got[0][0] != "succeeded" {
			t.Fatalf("%s must be recorded succeeded: %v", name, got)
		}
	}
	if slices.Contains(model.sent[0], "pl_id_aiii_memory_recent") {
		t.Fatalf("a non-primary operation was listed: %v", model.sent[0])
	}
	if len(model.sent[0]) > PromptToolDefinitionCeiling {
		t.Fatalf("the first request carries %d definitions, past the ceiling %d", len(model.sent[0]), PromptToolDefinitionCeiling)
	}
}

// A NON-PRIMARY IS NOT LISTED AND IS ONE CALL AWAY. Called by its tool
// name, which no request showed, it is refused, typed, in one line naming
// the call route, and nothing runs; the same response's tools action=call
// runs it through the same execution path. Its schema refusing the call's
// arguments, the answer carries the operation's card.
func TestANonPrimaryIsReachedThroughTheCallRoute(t *testing.T) {
	a, ran := dyadApp(t)
	const recent = "pl_id_aiii_memory_recent"
	model := &turnScript{observed: map[string]string{}, responses: [][]llm.ToolCall{
		{scriptCall("direct", recent, map[string]any{"text": "x"}),
			scriptCall("routed", "tools", map[string]any{"action": "call", "name": recent, "args": map[string]any{"text": "x"}}),
			scriptCall("encoded", "tools", map[string]any{"action": "call", "name": "recent", "args": `{"text":"y"}`}),
			scriptCall("missing", "tools", map[string]any{"action": "call", "name": recent, "args": map[string]any{}})},
		nil,
	}}
	runScript(t, a, model)
	if slices.Contains(model.sent[0], recent) {
		t.Fatalf("a non-primary was listed: %v", model.sent[0])
	}
	direct := model.observed["direct"]
	if !strings.Contains(direct, "is not in your tool list; call it with tools action=call name="+recent+" args={…}") || strings.Contains(direct, "\n") {
		t.Fatalf("a direct call to an unlisted operation must be refused in one line naming the route: %q", direct)
	}
	if got := toolRecords(t, a, recent); len(got) != 1 || got[0] != [2]string{"refused", tools.ReasonNotInToolList} {
		t.Fatalf("the direct call's record: %v", got)
	}
	if got := model.observed["routed"]; !strings.Contains(got, "ran recent") || strings.HasPrefix(got, "Error") {
		t.Fatalf("the routed call must run: %q", got)
	}
	if got := model.observed["encoded"]; !strings.Contains(got, "ran recent") {
		t.Fatalf("a call by operation id with its args encoded as JSON text must run: %q", got)
	}
	if ran[recent].Load() != 2 {
		t.Fatalf("the routed calls must run, the direct and the malformed ones not: ran %d", ran[recent].Load())
	}
	missing := model.observed["missing"]
	for _, want := range []string{"missing required text", "recent — tool name `" + recent + "`", "arguments (JSON Schema):", `"required":["text"]`, "To call it: tools action=call name=" + recent} {
		if !strings.Contains(missing, want) {
			t.Fatalf("a schema refusal must carry the card (%q):\n%s", want, missing)
		}
	}
	// The facade's other actions are the verb's, unrouted.
	if obs := a.executeToolCall(context.Background(), scriptCall("brief", "tools", map[string]any{"action": "brief"})); obs.Failed || !strings.Contains(obs.Text, "Plugin operations beside you") {
		t.Fatalf("tools action=brief: %+v", obs)
	}
	for _, c := range []struct{ args map[string]any }{
		{map[string]any{"action": "call"}},
		{map[string]any{"action": "call", "name": "nothing.here"}},
		{map[string]any{"action": "call", "name": "read", "args": map[string]any{"file_path": "x"}}},
	} {
		if obs := a.executeToolCall(context.Background(), scriptCall("bad", "tools", c.args)); !obs.Failed || !obs.Refused {
			t.Fatalf("%v must be refused: %+v", c.args, obs)
		}
	}
	if obs := a.executeToolCall(context.Background(), scriptCall("bad", "tools", map[string]any{"action": "call", "name": recent, "args": 7})); obs.ReasonCode != tools.ReasonArgumentsRequired || !strings.Contains(obs.Text, "arguments (JSON Schema):") {
		t.Fatalf("args that are not an object are refused with the card: %+v", obs)
	}
}

// AN OPERATOR-DISABLED OR SAFE-HIDDEN PRIMARY IS NEITHER LISTED NOR
// CALLABLE, by its tool name or through the route, each refusal with its
// code; the hint counts it nowhere.
func TestAHiddenPrimaryIsNeitherListedNorCallable(t *testing.T) {
	for _, hide := range []string{tools.ReasonOperatorDisabled, tools.ReasonSafeSuspended} {
		t.Run(hide, func(t *testing.T) {
			a, ran := dyadApp(t)
			const store = "pl_id_aiii_memory_store"
			if hide == tools.ReasonOperatorDisabled {
				a.toolReg.SetToolEnabled(store, false)
			} else {
				a.toolReg.SetSafeSource(func() (string, bool) { return "fixture", true })
			}
			for _, d := range a.buildToolDefinitions() {
				if d.Function.Name == store {
					t.Fatal("a hidden primary was listed")
				}
			}
			for _, tc := range []llm.ToolCall{
				scriptCall("direct", store, map[string]any{"text": "x"}),
				scriptCall("routed", "tools", map[string]any{"action": "call", "name": store, "args": map[string]any{"text": "x"}}),
			} {
				if obs := a.executeToolCall(context.Background(), tc); !obs.Refused || obs.ReasonCode != hide {
					t.Fatalf("%s: want refused %s, got %+v", tc.ID, hide, obs)
				}
			}
			if ran[store].Load() != 0 {
				t.Fatal("a hidden primary ran")
			}
		})
	}
}

// A ROUTED CALL IS ITS OPERATION'S CALL to every classifier, as a door's
// was: a search through tools action=call is parallel and repeatable after
// a fold exactly as the same search by its tool name, a store neither,
// and the turn's meter counts the two alike. A call the route refuses is
// the facade, which every classifier answers closed.
func TestARoutedCallClassifiesAsItsOperation(t *testing.T) {
	reg := tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	for _, op := range []declaredOp{{op: "memory.store"}, {op: "memory.search", read: true}} {
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
		target string
		read   bool
	}{
		{"pl_org_example_memory_memory_search", true},
		{"pl_org_example_memory_memory_store", false},
	} {
		routed := scriptCall("r", "tools", map[string]any{"action": "call", "name": c.target, "args": map[string]any{"text": "x"}})
		direct := scriptCall("d", c.target, map[string]any{"text": "x"})
		for _, k := range classifiers {
			if got, byName := k.ask(routed), k.ask(direct); got != byName || byName != c.read {
				t.Errorf("%s routed: %s=%v; by its tool name %v; declared %v", c.target, k.name, got, byName, c.read)
			}
		}
		rc, rr := meter(routed)
		dc, dr := meter(direct)
		if rc != dc || rr != dr {
			t.Errorf("%s: the meter counted the routed call as %d call(s), %d read(s); the direct call as %d, %d", c.target, rc, rr, dc, dr)
		}
	}
	refused := scriptCall("x", "tools", map[string]any{"action": "call", "name": "invent"})
	for _, k := range classifiers {
		if k.ask(refused) {
			t.Errorf("a refused routed call: %s=true, want false", k.name)
		}
	}
}
