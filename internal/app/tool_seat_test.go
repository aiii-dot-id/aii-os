package app

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// Every observation a tool call returns to the loop names the work session
// it acted within, or why there is none — a physical tool's, a verb's, a
// refusal's alike — and that is one reading, the one its start resolved
// (startedSeat, F1): the resident's active session, or a sub-agent's own.
func TestEveryToolObservationNamesTheSessionItActedWithin(t *testing.T) {
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	call := func(name, args string) llm.ToolCall {
		var tc llm.ToolCall
		tc.Function.Name, tc.Function.Arguments = name, args
		return tc
	}
	calls := map[string]llm.ToolCall{
		"physical":  call("ls", `{"path":`+strconv.Quote(dir)+`}`),
		"verb":      call("work", `{"action":"status"}`),
		"refused":   call("no_such_tool", `{}`),
		"malformed": call("ls", `{"path":`),
	}
	n := 0
	check := func(ctx context.Context, actor, wantSession, wantReason string) {
		t.Helper()
		for kind, tc := range calls {
			n++
			obs := startedExecute(t, a, ctx, actor, fmt.Sprintf("t_seat_%d", n), tc, nil)
			if obs.WorkSession != wantSession || obs.SessionReason != wantReason {
				t.Errorf("%s call: session %q reason %q, want %q %q (%s)", kind, obs.WorkSession, obs.SessionReason, wantSession, wantReason, obs.Text)
			}
		}
	}
	check(t.Context(), "", "", "no_session")
	if err := a.store.StartWorkSession(t.Context(), "ws_seat", "the work in hand"); err != nil {
		t.Fatal(err)
	}
	check(t.Context(), "", "ws_seat", "")
	child := context.WithValue(t.Context(), identity.SubagentWorkSession{}, "ws_child")
	// A child whose session the record does not hold acts in none, and says
	// why: the start row's answer, which its execution now takes (F1).
	check(child, "ws_child", "", "acting_session_unavailable")
	if _, err := a.store.DB().Exec(`INSERT INTO work_queue (id, kind, payload, dedup_key, source, state) VALUES ('wq_child', ?, '{}', 'ws_child', 'identity', 'DONE')`, identity.SubagentWorkKind); err != nil {
		t.Fatal(err)
	}
	if err := a.store.StartWorkSession(t.Context(), "ws_child", store.SubagentDescription("a bounded child task")); err != nil {
		t.Fatal(err)
	}
	check(child, "ws_child", "ws_child", "")
}
