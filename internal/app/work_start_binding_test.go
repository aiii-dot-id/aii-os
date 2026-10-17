package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestChildWorkStartCannotRedirectPluginScope(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	if err := a.store.StartWorkSession(t.Context(), "ws_parent", "parent task"); err != nil {
		t.Fatal(err)
	}
	if _, added, err := a.store.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{Kind: identity.SubagentWorkKind, Payload: "{}", DedupKey: "ws_child", Source: "identity", LeaseMs: 1000}, 8, "ws_child", store.SubagentDescription("child task")); err != nil || !added {
		t.Fatalf("child enqueue: added=%v err=%v", added, err)
	}
	ctx := context.WithValue(t.Context(), identity.SubagentWorkSession{}, "ws_child")
	call := llm.ToolCall{}
	call.Function.Name, call.Function.Arguments = "work", `{"action":"start","description":"unrelated session"}`
	obs := a.executeToolCall(ctx, call)
	if !obs.Failed || !strings.Contains(obs.Text, "rejected_before_effect (start.worker_session)") || obs.WorkSession != "ws_child" {
		t.Fatalf("child start was not refused on its bound session: %+v", obs)
	}
	probe := &calibrationBlockingTool{entered: make(chan pluginhost.ActingSession, 1), release: make(chan struct{})}
	close(probe.release)
	a.toolReg.Register(probe)
	for _, seat := range []struct {
		ctx context.Context
		id  string
	}{{t.Context(), "ws_parent"}, {ctx, "ws_child"}} {
		obs = a.executeToolCall(seat.ctx, toolCallFor(probe.Name()))
		if obs.Failed {
			t.Fatal(obs)
		}
		select {
		case captured := <-probe.entered:
			if captured.ID != seat.id || obs.WorkSession != seat.id {
				t.Fatalf("plugin context redirected: captured=%+v observation=%+v want=%s", captured, obs, seat.id)
			}
		default:
			t.Fatal("plugin context probe did not execute")
		}
	}
}
