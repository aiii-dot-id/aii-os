package pluginhost

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestCalibrationSubscriptionAdmission(t *testing.T) {
	for _, raw := range []string{
		`[{"topic":"tool.called","operation":"observe","delivery":null}]`,
		`[{"topic":"tool.called","operation":"observe","delivery":""}]`,
		`[{"topic":"tool.called","operation":"observe","delivery":"unknown"}]`,
		`[{"topic":"turn.ended","operation":"observe","delivery":"calibration_v1"}]`,
		`[{"topic":"tool.called","operation":"observe","delivery":"calibration_v1","filter":{"session":"x"}}]`,
		`[{"topic":"tool.called","operation":"observe","delivery":"calibration_v1"},{"topic":"turn.ended","operation":"observe"}]`,
	} {
		if _, err := ParseSubscriptions([]byte(raw), []string{"observe"}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := ParseSubscriptions([]byte(`[{"topic":"tool.called","operation":"observe","delivery":"calibration_v1"}]`), []string{"observe"}); err != nil {
		t.Fatal(err)
	}
}

type calibrationProbe struct {
	accepted bool
	calls    int
}

func (p *calibrationProbe) Snapshot(g uint64, s ActingSession) map[string]any {
	return map[string]any{"version": 1, "available": true, "session_id": s.ID, "stream_id": "g", "emitted": 1, "settled": 0, "loss_count": 0}
}
func (p *calibrationProbe) Finish(g, n uint64, ok bool) { p.calls++; p.accepted = ok }
func TestCalibrationWasmOperationParity(t *testing.T) {
	for _, supervised := range []bool{false, true} {
		name := "inprocess"
		if supervised {
			name = "supervised"
		}
		t.Run(name, func(t *testing.T) {
			const id = "org.example.calibration"
			input := []byte(`{"type":"object","properties":{"stream_id":{"type":"string"},"sequence":{"type":"integer"},"topic":{"type":"string"},"at":{"type":"string"},"payload":{"type":"object"}},"required":["stream_id","sequence","topic","at","payload"],"additionalProperties":false}`)
			output := []byte(`{"type":"object","properties":{"accepted":{"type":"boolean"},"stream_id":{"type":"string"},"sequence":{"type":"integer"}},"required":["accepted","stream_id","sequence"],"additionalProperties":false}`)
			desc := []byte(`[{"id":"observe","input":"schemas/in.json","output":"schemas/out.json","effects":"write.local","capabilities":["ring4.kv"]},{"id":"plan"}]`)
			frame := []byte(`{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":{"accepted":true,"stream_id":"g","sequence":1}}}`)
			files := map[string][]byte{"interfaces/cal.core.v1.schema.json": desc, "schemas/in.json": input, "schemas/out.json": output, "variants/v/plugin.wasm": wasmgen.CannedResponder(frame), SubscriptionsFile: []byte(`[{"topic":"tool.called","operation":"observe","delivery":"calibration_v1"}]`)}
			manifest := packagetest.BuildManifestJSON(id, "0.1.0", []packagetest.InterfaceSpec{{ID: "cal.core", Version: 1, SchemaFile: "interfaces/cal.core.v1.schema.json", Methods: []string{"observe", "plan"}}}, []packagetest.VariantSpec{{ID: "v", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox", Entrypoint: "variants/v/plugin.wasm"}}, files, nil)
			opts := &Options{}
			if supervised {
				opts.WorkerBinary = workerBin
			}
			reg := newRegistry(t)
			pkg := writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files})
			ap, e := Activate(t.Context(), pkg, reg, opts)
			if e != nil {
				t.Fatal(e)
			}
			defer ap.Deactivate(context.Background())
			source := &calibrationProbe{}
			ap.SetCalibrationSource(source)
			sink := ToolNameFor(id, "observe")
			if res, e := reg.ExecuteModel(t.Context(), sink, nil); e != nil || res.ReasonCode != tools.ReasonHostOnly {
				t.Fatal(res, e)
			}
			args := map[string]any{"stream_id": "g", "sequence": 1, "topic": "tool.called", "at": "2026-09-24T00:00:00Z", "payload": map[string]any{}}
			d := &CalibrationDelivery{Owner: ap, Generation: ap.RuntimeGeneration(), Sequence: 1, Stream: "g", Source: source}
			res, e := reg.Execute(WithCalibrationDelivery(t.Context(), d), sink, args)
			d.Finish(false)
			if e != nil || res.Error != "" || !source.accepted || source.calls != 1 {
				t.Fatal(res, e, source)
			}
			bad := &CalibrationDelivery{Owner: ap, Generation: ap.RuntimeGeneration(), Sequence: 2, Stream: "g", Source: source}
			args["sequence"] = 2
			res, e = reg.Execute(WithCalibrationDelivery(t.Context(), bad), sink, args)
			if e != nil || res.ReasonCode != "CALIBRATION_ACK_REFUSED" || source.accepted {
				t.Fatal(res, e, source)
			}
			next, e := ActivateShadow(t.Context(), pkg, reg, opts)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Deactivate(context.Background())
			if e = ap.inFlight.acquire(t.Context()); e != nil {
				t.Fatal(e)
			}
			e = next.Redirect(ap)
			ap.inFlight.release()
			if !errors.Is(e, ErrHandOverBusy) || ap.superseded.Load() {
				t.Fatalf("busy handoff changed incumbent: %v", e)
			}
			if e = next.Redirect(ap); e != nil {
				t.Fatal(e)
			}
			res, e = reg.Execute(WithCalibrationDelivery(t.Context(), bad), sink, args)
			if e != nil || res.Error == "" {
				t.Fatalf("predecessor delivery survived handoff: %+v, %v", res, e)
			}
		})
	}
}

func TestCalibrationRechecksSAFEAfterWaiting(t *testing.T) {
	reg := newRegistry(t)
	ap := auditCommitActivation(t, reg)
	ap.admitted.Store(true)
	tool := ap.pending[0].tool
	if err := reg.RegisterDynamic(tool, ap.ID); err != nil {
		t.Fatal(err)
	}
	var safe atomic.Bool
	entered := make(chan struct{})
	var once sync.Once
	reg.SetSafeSource(func() (string, bool) { v := safe.Load(); once.Do(func() { close(entered) }); return "test", v })
	if err := ap.inFlight.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	done := make(chan tools.Result, 1)
	go func() { r, _ := reg.Execute(t.Context(), tool.Name(), nil); done <- r }()
	<-entered
	safe.Store(true)
	ap.inFlight.release()
	r := <-done
	if r.Error == "" || ap.inv.(*auditCommitInvoker).calls.Load() != 0 {
		t.Fatal(r)
	}
}

func TestCalibrationRedirectCannotOvertakeInvocation(t *testing.T) {
	reg := newRegistry(t)
	old := auditCommitActivation(t, reg)
	old.Subscriptions = []SubscriptionDecl{{Topic: TopicToolCalled, Operation: "observe", Delivery: CalibrationV1}}
	if err := old.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	next := auditCommitActivation(t, reg)
	if err := old.inFlight.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	err := next.Redirect(old)
	if !errors.Is(err, ErrHandOverBusy) {
		old.inFlight.release()
		t.Fatalf("busy update: %v", err)
	}
	active, ok := reg.Get("audit.declared")
	if !ok || active.(interface{ DispatchOwner() any }).DispatchOwner() != old {
		old.inFlight.release()
		t.Fatal("incumbent replaced while in flight")
	}
	if old.superseded.Load() || next.admitted.Load() {
		old.inFlight.release()
		t.Fatal("busy refusal changed ownership")
	}
	old.inFlight.release()
	if err = next.Redirect(old); err != nil {
		t.Fatal(err)
	}
	if !old.superseded.Load() {
		t.Fatal("predecessor not retired")
	}
}

func TestCalibrationRetirementKeepsCustodyWhileBusy(t *testing.T) {
	for _, quiet := range []bool{false, true} {
		t.Run(map[bool]string{false: "deactivate", true: "quiet"}[quiet], func(t *testing.T) {
			reg := newRegistry(t)
			ap := auditCommitActivation(t, reg)
			ap.Subscriptions = []SubscriptionDecl{{Topic: TopicToolCalled, Operation: "observe", Delivery: CalibrationV1}}
			if err := ap.Redirect(nil); err != nil {
				t.Fatal(err)
			}
			artifact := t.TempDir()
			ap.artifactDir = artifact
			if err := ap.inFlight.acquire(t.Context()); err != nil {
				t.Fatal(err)
			}
			close := ap.Deactivate
			if quiet {
				close = ap.CloseQuiet
			}
			err := close(t.Context())
			ap.inFlight.release()
			if !errors.Is(err, ErrRetirementBusy) {
				t.Fatal("busy retirement acknowledged", err)
			}
			if _, err := os.Stat(artifact); err != nil {
				t.Fatal("busy retirement released artifact", err)
			}
			if _, ok := reg.Get("audit.declared"); !ok {
				t.Fatal("busy refusal changed registry")
			}
			if err := close(t.Context()); err != nil {
				t.Fatal("idle retirement refused", err)
			}
			if _, err := os.Stat(artifact); !os.IsNotExist(err) {
				t.Fatal("idle retirement retained artifact", err)
			}
			reg.Deregister("audit.declared")
		})
	}
}

func TestChangedDispatchIsTypedAndExecutesNeitherVersion(t *testing.T) {
	reg := newRegistry(t)
	old := auditCommitActivation(t, reg)
	if err := old.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	selected, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	reg.SetSafeSource(func() (string, bool) { once.Do(func() { close(selected); <-proceed }); return "", false })
	done := make(chan tools.Result, 1)
	go func() { r, _ := reg.Execute(t.Context(), "audit.declared", nil); done <- r }()
	<-selected
	next := auditCommitActivation(t, reg)
	if err := next.Redirect(old); err != nil {
		close(proceed)
		t.Fatal(err)
	}
	close(proceed)
	r := <-done
	if r.ReasonCode != tools.ReasonDispatchTargetChanged || r.Error == "" {
		t.Fatal(r)
	}
	if old.inv.(*auditCommitInvoker).calls.Load() != 0 || next.inv.(*auditCommitInvoker).calls.Load() != 0 {
		t.Fatal("stale selection executed or replayed")
	}
	if _, ok := reg.Get("audit.declared"); !ok {
		t.Fatal("update left a registry gap")
	}
}

func TestWithdrawnDispatchIsTypedBeforeInvocation(t *testing.T) {
	reg := newRegistry(t)
	ap := auditCommitActivation(t, reg)
	if err := ap.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	if err := ap.inFlight.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	var allowed atomic.Bool
	allowed.Store(true)
	var checks atomic.Int32
	arrived := make(chan struct{})
	ap.Authorize(func() bool {
		v := allowed.Load()
		if checks.Add(1) == 1 {
			close(arrived)
		}
		return v
	})
	done := make(chan tools.Result, 1)
	go func() { r, _ := reg.Execute(t.Context(), "audit.declared", nil); done <- r }()
	<-arrived
	allowed.Store(false)
	ap.inFlight.release()
	r := <-done
	if r.Error == "" || r.ReasonCode != ReasonActivationWithdrawn || ap.inv.(*auditCommitInvoker).calls.Load() != 0 {
		t.Fatal(r)
	}
}
