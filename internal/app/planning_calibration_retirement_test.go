package app

import (
	"context"
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlanningCalibrationRetirementDrainsAdmittedWrite(t *testing.T) {
	var hold atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	defer resume()
	f := newNativePlanningFixtureWithBroker(t, func(cfg *broker.Config) {
		cfg.InSAFE = func() bool {
			if hold.CompareAndSwap(true, false) {
				close(entered)
				<-release
			}
			return false
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	const id = "id.example.planning"
	// Reach the last permitted child without pending host calls, then fail
	// that child while its final KV write is admitted but not yet executed.
	for i := 0; i < 5; i++ {
		old := f.active.SupervisedPid()
		oldGen := f.active.RuntimeGeneration()
		if killed, err := f.active.KillSupervisedChild(); err != nil || !killed {
			t.Fatalf("no live child was killed: %v", err)
		}
		for f.active.SupervisedPid() == 0 || f.active.SupervisedPid() == old || f.active.RuntimeGeneration() <= oldGen {
			select {
			case <-ctx.Done():
				t.Fatal("restart", ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	actx := pluginhost.WithActingSession(ctx, pluginhost.ActingSession{ID: "ws"})
	begin := func(plan string) {
		t.Helper()
		r, err := f.registry.Execute(actx, pluginhost.ToolNameFor(id, "plan"), map[string]any{"mode": "begin", "plan_id": plan, "family": "f", "declared_steps": 1, "budget_calls": 10, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
		if err != nil || r.Error != "" {
			t.Fatal(r, err)
		}
	}
	begin("old")
	readState := func() map[string]any {
		t.Helper()
		raw, found, err := f.store.PluginKVGet(store.PluginScope{PluginID: id}, "t3/calibration")
		if err != nil || !found {
			t.Fatal(err)
		}
		var out map[string]any
		if json.Unmarshal([]byte(raw), &out) != nil {
			t.Fatal(raw)
		}
		return out
	}
	oldStream := readState()["stream_id"]
	hold.Store(true)
	f.app.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "read", "failed": false, "duration_ms": int64(1), "actor": "main", "session": "ws"})
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("no admitted write")
	}
	if killed, err := f.active.KillSupervisedChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	select {
	case <-f.active.Terminated():
	case <-ctx.Done():
		t.Fatal("no terminal failure")
	}
	old := &pluginhost.Activation{ID: id, Plugin: f.active}
	old.AdmitForTest()
	retired, err := newPluginRuntime(f.app).Stop(ctx, old)
	if err != nil || retired.Established || len(retired.Residue) == 0 {
		t.Fatalf("retirement must retain in-flight custody: %+v %v", retired, err)
	}
	if f.active.WaitIdle(0) {
		t.Fatal("fixture lost its blocked invocation")
	}
	resume()
	awaitEvents(t, "old host mutation drained", func() bool { return f.active.WaitIdle(0) })
	retired, err = newPluginRuntime(f.app).Stop(ctx, old)
	if err != nil || !retired.Established {
		t.Fatal("drained retirement failed", retired, err)
	}
	next, err := pluginhost.ActivateShadow(ctx, f.archive, f.registry, f.options)
	if err != nil {
		t.Fatal(err)
	}
	defer next.CloseQuiet(context.Background())
	if err = next.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	f.app.startSubscriber(next)
	begin("new")
	newStream := readState()["stream_id"]
	if newStream == oldStream {
		t.Fatal("fixture did not start a new stream")
	}
	if got := readState()["stream_id"]; got != newStream {
		t.Fatal("successor stream changed", got)
	}
	f.app.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "read", "failed": false, "duration_ms": int64(1), "actor": "main", "session": "ws"})
	sub := f.app.subscribers[id]
	awaitEvents(t, "successor measurement accepted", func() bool {
		return sub.Snapshot(next.RuntimeGeneration(), pluginhost.ActingSession{ID: "ws"})["settled"] == uint64(1)
	})
	if got := readState()["stream_id"]; got != newStream {
		t.Fatal("stale write reached successor", got)
	}
	raw, found, err := f.store.PluginKVGet(store.PluginScope{PluginID: id}, "t3/plan/old")
	if err != nil || !found {
		t.Fatal(err)
	}
	var oldPlan struct {
		HostCalibration string `json:"host_calibration"`
		Measurement     struct {
			Count uint64 `json:"count"`
		} `json:"measurement"`
	}
	if err = json.Unmarshal([]byte(raw), &oldPlan); err != nil || oldPlan.HostCalibration != "unresolved" || oldPlan.Measurement.Count != 1 {
		t.Fatal("rollover lost drained old facts", raw, err)
	}
	t.Log("terminal retirement stayed pending until the admitted host write drained; successor stream and old partial facts preserved")
}
