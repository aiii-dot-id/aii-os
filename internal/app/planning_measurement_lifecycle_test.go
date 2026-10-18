package app

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// The same workload runs against immutable baseline and candidate source.
// Setup/build is excluded and reported separately; begin, delivery, grading,
// final status and pruning are included. It is deliberately not parallel.
func TestPlanningMeasurementLifecycle(t *testing.T) {
	if os.Getenv("AII_CALIBRATION_PLUGIN_SOURCE") == "" {
		t.Skip("requires exact planning source")
	}
	for _, tc := range []struct {
		name string
		n    int
		many bool
	}{{"zero", 0, false}, {"one", 1, false}, {"two_hundred_shared", 200, false}, {"two_hundred_sessions", 200, true}} {
		t.Run(tc.name, func(t *testing.T) {
			setup := time.Now()
			f := newNativePlanningFixture(t)
			setupElapsed := time.Since(setup)
			const id = "id.example.planning"
			call := func(session, mode, plan string, extra map[string]any) map[string]any {
				t.Helper()
				ctx := pluginhost.WithActingSession(t.Context(), pluginhost.ActingSession{ID: session})
				args := map[string]any{"mode": mode, "plan_id": plan}
				for k, v := range extra {
					args[k] = v
				}
				r, err := f.registry.ExecuteModel(ctx, pluginhost.ToolNameFor(id, "plan"), args)
				if err != nil || r.Error != "" {
					t.Fatalf("%s: %+v %v", mode, r, err)
				}
				if !strings.HasPrefix(r.Output, untrusted.Open) || !strings.HasSuffix(r.Output, untrusted.Close) {
					t.Fatal("model result lost trust boundary")
				}
				_, body, ok := strings.Cut(r.Output, "\n")
				if !ok {
					t.Fatal(r.Output)
				}
				body = strings.TrimSuffix(body, "\n"+untrusted.Close)
				var v map[string]any
				if err := json.Unmarshal([]byte(body), &v); err != nil {
					t.Fatal(err, r.Output)
				}
				return v
			}
			who := func(i int) string {
				if tc.many {
					return fmt.Sprintf("ws_%d", i)
				}
				return "ws"
			}
			receiptStats := func() map[string]int64 {
				t.Helper()
				out := map[string]int64{}
				rows, err := f.store.DB().Query(`SELECT operation,COUNT(*),COALESCE(SUM(LENGTH(CAST(receipt_json AS BLOB))),0) FROM plugin_receipts WHERE plugin_id=? GROUP BY operation`, id)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				for rows.Next() {
					var op string
					var count, size int64
					if err := rows.Scan(&op, &count, &size); err != nil {
						t.Fatal(err)
					}
					out[op] = count
					out["receipt_bytes"] += size
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return out
			}
			hostBefore := planningProcessUsage(os.Getpid())
			guestBefore := planningProcessTreeUsage(f.active.SupervisedPid())
			start := time.Now()
			for i := 0; i < tc.n; i++ {
				call(who(i), "begin", fmt.Sprintf("p%d", i), map[string]any{"family": "test", "declared_steps": 1, "budget_calls": 100, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
			}
			beginElapsed := time.Since(start)
			receiptsBefore := receiptStats()
			burst := time.Now()
			for i := 0; i < 64; i++ {
				f.app.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "read", "failed": false, "duration_ms": int64(1), "actor": "main", "session": who(i % max(1, tc.n))})
			}
			sub := f.app.subscribers[id]
			deadline := time.NewTimer(5 * time.Minute)
			defer deadline.Stop()
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for {
				snap := sub.Snapshot(f.active.RuntimeGeneration(), pluginhost.ActingSession{ID: "ws"})
				if snap["settled"] == uint64(64) {
					if snap["loss_count"] != uint64(0) {
						t.Fatal(snap)
					}
					break
				}
				select {
				case <-deadline.C:
					t.Fatal("delivery deadline", snap)
				case <-tick.C:
				}
			}
			burstElapsed := time.Since(burst)
			receiptsAfter := receiptStats()
			closeStart := time.Now()
			for i := 0; i < tc.n; i++ {
				plan := fmt.Sprintf("p%d", i)
				call(who(i), "apply", plan, map[string]any{"apply_id": "a", "step_id": "s1", "kind_class": "refinement", "from_class": "a", "to_class": "b", "count": 1})
				got := call(who(i), "grade", plan, map[string]any{"result_class": "passed"})
				want := 64
				cursor := 64
				if tc.many {
					want = 0
					cursor = 0
					for n := 0; n < 64; n++ {
						if n%tc.n == i {
							want++
							cursor = n + 1
						}
					}
				}
				if got["host_calibration"] != "complete" || got["observed_calls"] != float64(want) {
					t.Fatal(i, got, want)
				}
				measured := got["measurement"].(map[string]any)
				if measured["cursor"] != float64(cursor) {
					t.Fatal(i, measured, cursor)
				}
				if i == tc.n-1 {
					call(who(i), "status", plan, nil)
				}
			}
			closeElapsed := time.Since(closeStart)
			total := time.Since(start)
			hostAfter := planningProcessUsage(os.Getpid())
			guestAfter := planningProcessTreeUsage(f.active.SupervisedPid())
			delta := map[string]int64{}
			for k, v := range receiptsAfter {
				delta[k] = v - receiptsBefore[k]
			}
			report := map[string]any{"workload": "planning-lifecycle-v1", "case": tc.name, "plans": tc.n, "events": 64, "setup_ms": float64(setupElapsed.Microseconds()) / 1000, "begin_ms": float64(beginElapsed.Microseconds()) / 1000, "burst_ms": float64(burstElapsed.Microseconds()) / 1000, "close_ms": float64(closeElapsed.Microseconds()) / 1000, "total_ms": float64(total.Microseconds()) / 1000, "burst_receipts": delta, "total_receipts": receiptStats(), "host_process_delta": planningUsageDelta(hostBefore, hostAfter), "guest_process_delta": planningTreeDelta(guestBefore, guestAfter)}
			raw, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("PLANNING_LIFECYCLE %s", raw)
		})
	}
}

// Linux process counters include native framing and host storage/receipt work.
// They are neither logical KV payload sizes nor device energy measurements.
func planningProcessUsage(pid int) map[string]uint64 {
	if runtime.GOOS != "linux" || pid <= 0 {
		return nil
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return nil
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return nil
	}
	out := map[string]uint64{}
	for key, i := range map[string]int{"user_ticks": 11, "system_ticks": 12, "start_ticks": 19} {
		n, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return nil
		}
		out[key] = n
	}
	raw, err = os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		if err == nil {
			out[key] = n
		}
	}
	return out
}
func planningUsageDelta(a, b map[string]uint64) map[string]uint64 {
	if a == nil || b == nil || a["start_ticks"] != b["start_ticks"] {
		return nil
	}
	out := map[string]uint64{}
	for k, v := range b {
		if k != "start_ticks" && v >= a[k] {
			out[k] = v - a[k]
		}
	}
	return out
}

// The supervisor PID may be a sandbox launcher; include its owned descendants
// rather than treating an idle launcher as the native plugin's resource use.
func planningProcessTreeUsage(root int) map[int]map[string]uint64 {
	if runtime.GOOS != "linux" || root <= 0 {
		return nil
	}
	out := map[int]map[string]uint64{}
	todo := []int{root}
	for len(todo) > 0 {
		pid := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if _, seen := out[pid]; seen {
			continue
		}
		u := planningProcessUsage(pid)
		if u == nil {
			return nil
		}
		out[pid] = u
		threads, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
		if err != nil {
			return nil
		}
		for _, thread := range threads {
			raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, thread.Name()))
			if err != nil {
				continue
			}
			for _, child := range strings.Fields(string(raw)) {
				n, err := strconv.Atoi(child)
				if err == nil {
					todo = append(todo, n)
				}
			}
		}
	}
	return out
}
func planningTreeDelta(a, b map[int]map[string]uint64) map[string]uint64 {
	if a == nil || b == nil {
		return nil
	}
	out := map[string]uint64{}
	for pid, first := range a {
		last, ok := b[pid]
		if !ok {
			return nil
		}
		delta := planningUsageDelta(first, last)
		if delta == nil {
			return nil
		}
		for k, v := range delta {
			out[k] += v
		}
	}
	for pid, last := range b {
		if _, ok := a[pid]; !ok {
			for k, v := range last {
				if k != "start_ticks" {
					out[k] += v
				}
			}
		}
	}
	out["processes"] = uint64(len(b))
	return out
}
