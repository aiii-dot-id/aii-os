package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// Every observation a tool call returns to the loop names the work session
// it acted within, or why there is none — a physical tool's, a verb's, a
// refusal's alike — and that is one reading, taken before the call ran
// (callSeat): the resident's active session, or a sub-agent's own.
func TestEveryToolObservationNamesTheSessionItActedWithin(t *testing.T) {
	a, dir := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	run := appToolExecutor{a}
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
	check := func(ctx context.Context, wantSession, wantReason string) {
		t.Helper()
		for kind, tc := range calls {
			obs := run.Execute(ctx, tc)
			if obs.WorkSession != wantSession || obs.SessionReason != wantReason {
				t.Errorf("%s call: session %q reason %q, want %q %q (%s)", kind, obs.WorkSession, obs.SessionReason, wantSession, wantReason, obs.Text)
			}
		}
	}
	check(t.Context(), "", "no_session")
	if err := a.store.StartWorkSession(t.Context(), "ws_seat", "the work in hand"); err != nil {
		t.Fatal(err)
	}
	check(t.Context(), "ws_seat", "")
	check(context.WithValue(t.Context(), identity.SubagentWorkSession{}, "ws_child"), "ws_child", "")
}
