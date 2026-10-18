package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestPlanningSharedMeasurementStorageFailures(t *testing.T) {
	f := newNativePlanningFixture(t)
	const id = "id.example.planning"
	const stateKey = "t3/calibration"
	ctx := pluginhost.WithActingSession(t.Context(), pluginhost.ActingSession{ID: "ws"})
	call := func(mode, p string, extra map[string]any) map[string]any {
		t.Helper()
		args := map[string]any{"mode": mode, "plan_id": p}
		for k, v := range extra {
			args[k] = v
		}
		r, err := f.registry.Execute(ctx, pluginhost.ToolNameFor(id, "plan"), args)
		if err != nil || r.Error != "" {
			t.Fatal(mode, r, err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(r.Output), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	begin := func(p string) {
		call("begin", p, map[string]any{"family": "f", "declared_steps": 1, "budget_calls": 10, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
	}
	apply := func(p string) {
		call("apply", p, map[string]any{"apply_id": "a", "step_id": "s1", "kind_class": "refinement", "from_class": "a", "to_class": "b", "count": 1})
	}
	begin("p")
	apply("p")
	before, found, err := f.store.PluginKVGet(store.PluginScope{PluginID: id}, stateKey)
	if err != nil || !found {
		t.Fatal("fixture did not load shared-record candidate", err)
	}
	// Owned SQLite fixture only. Force the actual broker transaction to fail
	// before its UPSERT; the persistent source must remain byte-for-byte intact.
	f.store.DB().SetMaxOpenConns(1)
	_, err = f.store.DB().Exec(`CREATE TEMP TRIGGER planning_write_failure BEFORE INSERT ON plugin_kv
 WHEN NEW.plugin_id='id.example.planning' AND NEW.key='t3/calibration'
 BEGIN SELECT RAISE(ABORT, 'injected planner write failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	emit := func() {
		f.app.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "read", "failed": false, "duration_ms": int64(1), "actor": "main", "session": "ws"})
	}
	emit()
	sub := f.app.subscribers[id]
	awaitEvents(t, "failed observation settled", func() bool {
		return sub.Snapshot(f.active.RuntimeGeneration(), pluginhost.ActingSession{ID: "ws"})["settled"] == uint64(1)
	})
	snap := sub.Snapshot(f.active.RuntimeGeneration(), pluginhost.ActingSession{ID: "ws"})
	if snap["loss_count"] != uint64(1) {
		t.Fatal("failed write acknowledged", snap)
	}
	after, found, err := f.store.PluginKVGet(store.PluginScope{PluginID: id}, stateKey)
	if err != nil || !found || after != before {
		t.Fatal("failed transaction changed counter", err)
	}
	if _, err = f.store.DB().Exec(`DROP TRIGGER planning_write_failure`); err != nil {
		t.Fatal(err)
	}
	emit()
	awaitEvents(t, "recovered observation settled", func() bool {
		return sub.Snapshot(f.active.RuntimeGeneration(), pluginhost.ActingSession{ID: "ws"})["settled"] == uint64(2)
	})
	got := call("grade", "p", map[string]any{"result_class": "passed"})
	if got["host_calibration"] != "unresolved" || got["observed_calls"] != nil {
		t.Fatal("recovery erased delivery loss", got)
	}
	begin("q")
	if err = f.store.PluginKVPut(store.PluginScope{PluginID: id}, stateKey, `{"version":2}`, false, 256, 1<<20); err != nil {
		t.Fatal(err)
	}
	got = call("status", "q", nil)
	m := got["measurement"].(map[string]any)
	if m["count"] != nil || m["cursor"] != nil {
		t.Fatal("corruption became measured zero", got)
	}
	apply("q")
	got = call("grade", "q", map[string]any{"result_class": "passed"})
	if got["state"] != "closed" || got["host_calibration"] != "unresolved" || got["observed_calls"] != nil {
		t.Fatal("unresolved escape blocked", got)
	}
	after, found, err = f.store.PluginKVGet(store.PluginScope{PluginID: id}, stateKey)
	if err != nil || !found || after != `{"version":2}` {
		t.Fatal("corrupt evidence overwritten", err)
	}
	call("status", "p", nil)
	call("status", "q", nil)
	t.Log("real broker write refusal preserved bytes and recorded loss; native readiness recovered; corrupt evidence stayed intact while business completion and closed reads remained available")
}

func TestPlanningSharedMeasurementRollover(t *testing.T) {
	f := newNativePlanningFixture(t)
	const id = "id.example.planning"
	ctx := pluginhost.WithActingSession(t.Context(), pluginhost.ActingSession{ID: "ws"})
	call := func(mode, p string, extra map[string]any) map[string]any {
		t.Helper()
		args := map[string]any{"mode": mode, "plan_id": p}
		for k, v := range extra {
			args[k] = v
		}
		r, err := f.registry.Execute(ctx, pluginhost.ToolNameFor(id, "plan"), args)
		if err != nil || r.Error != "" {
			t.Fatal(mode, r, err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(r.Output), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	begin := func(p string) {
		call("begin", p, map[string]any{"family": "f", "declared_steps": 1, "budget_calls": 100, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
	}
	apply := func(p string) {
		call("apply", p, map[string]any{"apply_id": "a", "step_id": "s1", "kind_class": "refinement", "from_class": "a", "to_class": "b", "count": 1})
	}
	for i := 0; i < 200; i++ {
		begin(fmt.Sprintf("p%d", i))
	}
	f.app.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "read", "failed": false, "duration_ms": int64(1), "actor": "main", "session": "ws"})
	sub := f.app.subscribers[id]
	awaitEvents(t, "old stream delivered", func() bool {
		return sub.Snapshot(f.active.RuntimeGeneration(), pluginhost.ActingSession{ID: "ws"})["settled"] == uint64(1)
	})
	next, err := pluginhost.ActivateShadow(t.Context(), f.archive, f.registry, f.options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { next.CloseQuiet(context.Background()) })
	if err = next.Redirect(f.active); err != nil {
		t.Fatal(err)
	}
	f.app.startSubscriber(next)
	// Complete one plan unresolved so the existing bounded archive policy can
	// reclaim a hot slot. Other unfinished plans remain open through rollover.
	apply("p0")
	got := call("grade", "p0", map[string]any{"result_class": "passed"})
	if got["host_calibration"] != "unresolved" {
		t.Fatal(got)
	}
	started := time.Now()
	begin("new")
	elapsed := time.Since(started)
	for i := 1; i < 200; i++ {
		got := call("status", fmt.Sprintf("p%d", i), nil)
		m := got["measurement"].(map[string]any)
		if got["state"] != "open" || got["host_calibration"] != "unresolved" || m["count"] != float64(1) || m["cursor"] != float64(1) {
			t.Fatal(i, got)
		}
	}
	apply("p1")
	got = call("grade", "p1", map[string]any{"result_class": "passed"})
	if got["host_calibration"] != "unresolved" {
		t.Fatal(got)
	}
	got = call("status", "p0", nil)
	if got["state"] != "closed" {
		t.Fatal("archived result lost", got)
	}
	t.Logf("NATIVE_ROLLOVER old_open=199 new_begin_ms=%.3f all partial counts/cursors retained; business completion and archived reads passed", float64(elapsed.Microseconds())/1000)
}
