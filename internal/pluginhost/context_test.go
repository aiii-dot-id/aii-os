package pluginhost

import (
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestContextResultBoundAndShape(t *testing.T) {
	for _, raw := range []string{`{"text":"","detail_route":""}`, `{"text":"lesson","detail_route":"search id=note"}`} {
		if _, err := ReadContextResult(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{`{}`, `{"text":"x"}`, `{"text":null,"detail_route":""}`, `{"text":"x","detail_route":""}`, `{"text":"","detail_route":"x"}`, `{"text":"x","detail_route":"r","other":1}`, `{"text":"x","text":"y","detail_route":"r"}`, `{"text":"x","detail_route":"` + strings.Repeat("r", 513) + `"}`, `{"text":"` + strings.Repeat("x", 4096) + `","detail_route":"r"}`} {
		if _, err := ReadContextResult(raw); err == nil {
			t.Fatalf("accepted invalid result: %s", raw)
		}
	}
}

func TestContextDescriptorIsHostOnlyAndStrict(t *testing.T) {
	valid := `[{"id":"ping","context":true,"effects":"read.internal","capabilities":[],"input":"schemas/in.json","output":"schemas/out.json","max_result_bytes":4096}]`
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.context", []byte(valid), []byte(`{"type":"object"}`), []byte(`{"type":"object"}`))
	ap, e := Activate(t.Context(), pkg, reg, supervisedOpts(Options{}))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	if len(ap.ToolNames) != 1 {
		t.Fatal(ap.ToolNames)
	}
	name := ap.ToolNames[0]
	for _, n := range reg.Names() {
		if n == name {
			t.Fatal("context advertised as model tool")
		}
	}
	r, e := reg.ExecuteModel(context.Background(), name, nil)
	if e != nil || r.ReasonCode != tools.ReasonHostOnly {
		t.Fatal(r, e)
	}
	for _, bad := range []string{
		strings.Replace(valid, `"read.internal"`, `"write.local"`, 1),
		strings.Replace(valid, `"context":true`, `"context":true,"primary":true`, 1),
		strings.Replace(valid, `"context":true`, `"context":true,"operator_confirms":true`, 1),
		strings.Replace(valid, `"capabilities":[],`, "", 1),
		strings.Replace(valid, `"input":"schemas/in.json",`, "", 1),
		strings.Replace(valid, `"output":"schemas/out.json",`, "", 1),
		strings.Replace(valid, `4096`, `4097`, 1),
		strings.Replace(valid, `4096`, `0`, 1),
	} {
		p := contractPkg(t, "org.example.invalidcontext", []byte(bad), []byte(`{"type":"object"}`), []byte(`{"type":"object"}`))
		if a, e := Activate(t.Context(), p, newRegistry(t), nil); e == nil {
			_ = a.Deactivate(t.Context())
			t.Fatalf("accepted invalid descriptor %s", bad)
		}
	}
}

type contextGrant struct {
	recordingProposer
	denied atomic.Bool
}

func (g *contextGrant) AdmitOperation(_, _, _ string) error {
	if g.denied.Load() {
		return errors.New("fixture grant withdrawn")
	}
	return nil
}
func TestContextSelectionGrantAndSafeAreRecheckedAfterOperationGate(t *testing.T) {
	for _, kind := range []string{"disabled", "released", "safe", "grant", "owner"} {
		t.Run(kind, func(t *testing.T) {
			reg := newRegistry(t)
			ap := auditCommitActivation(t, reg)
			gate := &contextGrant{}
			var safe atomic.Bool
			reg.SetSafeSource(func() (string, bool) { return "fixture", safe.Load() })
			ctxTool := ap.newOperationTool("audit.context", "context", "local context", &opDescriptor{context: true, effects: broker.EffectsReadInternal}, tools.Discovery{})
			ctxTool.acts = gate
			ap.pending = append(ap.pending, pendingTool{tool: ctxTool, hostOp: true})
			if e := ap.Redirect(nil); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
			if _, e := reg.Offer("audit.declared"); e != nil {
				t.Fatal(e)
			}
			if e := ap.inFlight.acquire(t.Context()); e != nil {
				t.Fatal(e)
			}
			arrived := make(chan struct{})
			var once sync.Once
			ap.Authorize(func() bool { once.Do(func() { close(arrived) }); return true })
			done := make(chan tools.Result, 1)
			go func() {
				r, e := reg.Execute(tools.WithContextSelection(tools.WithDispatchOwner(t.Context(), ap)), "audit.context", nil)
				if e != nil {
					r.Error = e.Error()
				}
				done <- r
			}()
			auditAuthorizationWait(t, arrived, "context did not arrive at gate")
			want := "CONTEXT_NOT_SELECTED"
			switch kind {
			case "disabled":
				reg.SetToolEnabled("audit.declared", false)
			case "released":
				if _, e := reg.Release("audit.declared"); e != nil {
					t.Fatal(e)
				}
			case "safe":
				safe.Store(true)
				want = tools.ReasonSafeSuspended
			case "grant":
				gate.denied.Store(true)
				want = ReasonOperatorGrant
			case "owner":
				reg.Deregister("audit.context")
				want = tools.ReasonDispatchTargetChanged
			}
			ap.inFlight.release()
			r := <-done
			if r.ReasonCode != want {
				t.Fatalf("got %+v want %s", r, want)
			}
			if ap.inv.(*auditCommitInvoker).calls.Load() != 0 {
				t.Fatal("changed selection/grant reached guest")
			}
		})
	}
}

func TestContextRuntimeExcludesResidentAndNonWasm(t *testing.T) {
	desc := &descriptorSet{ops: map[string]*opDescriptor{"context": {context: true}}}
	m := &packagefmt.Manifest{Interfaces: &packagefmt.InterfaceSet{}}
	if e := validateContextRuntime(desc, m, "wasm_component", false); e == nil {
		t.Fatal("in-process context admitted")
	}
	if e := validateContextRuntime(desc, m, "wasm_component", true); e != nil {
		t.Fatal(e)
	}
	for _, runtime := range []string{"native_t3_component", "wasm_aot_component", ""} {
		if e := validateContextRuntime(desc, m, runtime, true); e == nil {
			t.Fatal("unsupported runtime admitted", runtime)
		}
	}
	for _, family := range []string{SessionLaneFamily, "channel_adapter"} {
		m.PluginFamily = family
		if e := validateContextRuntime(desc, m, "wasm_component", true); e == nil {
			t.Fatal("resident family admitted", family)
		}
	}
	m.PluginFamily = "tool_bridge"
	for _, id := range []string{ChannelInterfaceID, EmbeddingsInterfaceID} {
		m.Interfaces.Optional = []packagefmt.InterfaceDecl{{ID: id}}
		if e := validateContextRuntime(desc, m, "wasm_component", true); e == nil {
			t.Fatal("resident interface admitted", id)
		}
	}
}

func TestContextDispatchedTimeoutRecoversWithoutRetryOrStoreMutation(t *testing.T) {
	reg := newRegistry(t)
	st := newBrokerStore(t)
	h, e := broker.New(broker.Config{Store: st})
	if e != nil {
		t.Fatal(e)
	}
	guest := wasmgen.LoopForLongRequest(1000, []byte(`{"jsonrpc":"2.0","id":"h1","result":{"output":"ordinary call recovered"}}`))
	ap, e := Activate(t.Context(), writePkg(t, pkgSpec("org.example.contexttimeout", "0.1.0", []string{"search"}, guest)), reg, supervisedOpts(Options{Broker: h}))
	if e != nil {
		t.Fatal(e)
	}
	defer ap.Deactivate(context.Background())
	ordinary := ap.ToolNames[0]
	hook := ap.newOperationTool("fixture.context", "context", "fixture context", &opDescriptor{context: true, effects: broker.EffectsReadInternal}, tools.Discovery{})
	if e = reg.RegisterHostOp(hook, ap.ID); e != nil {
		t.Fatal(e)
	}
	defer reg.Deregister(hook.Name())
	if _, e = reg.Offer(ordinary); e != nil {
		t.Fatal(e)
	}
	first := ap.SupervisedPid()
	ctx, cancel := context.WithTimeout(tools.WithContextSelection(tools.WithDispatchOwner(t.Context(), ap)), 250*time.Millisecond)
	_, e = reg.Execute(ctx, hook.Name(), map[string]any{"query": strings.Repeat("x", 1500), "max_bytes": 4096})
	cancel()
	var timeout *supervisor.InvokeTimeoutError
	if !errors.As(e, &timeout) {
		t.Fatalf("expected dispatched timeout: %v", e)
	}
	pollUntil(t, "normal worker recovery", func() bool { return ap.SupervisedPid() != 0 && ap.SupervisedPid() != first })
	r, e := reg.Execute(t.Context(), ordinary, nil)
	if e != nil || r.Failed() || r.Output != `{"output":"ordinary call recovered"}` {
		t.Fatal(r, e)
	}
	var rows int
	if e = st.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_kv)+(SELECT COUNT(*) FROM plugin_memories)+(SELECT COUNT(*) FROM memory_access)`).Scan(&rows); e != nil || rows != 0 {
		t.Fatal("timeout mutated storage", rows, e)
	}
	if ap.Restarts() != 0 {
		t.Fatal("timeout reclassified as crash")
	}
}
