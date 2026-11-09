package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// A TIME CONDITION IS KEPT AS THE IDENTITY'S OWN ALARM: set with
// resumes_on time:<ms>, the session's alarm is armed at that moment, as a
// timer of theirs, remembering the turn that set it; any other condition
// cancels it.
func TestATimeConditionIsKeptAsTheIdentitysAlarm(t *testing.T) {
	e, st, lg, kp, _ := setupEngine(t)
	e.timers = NewStoreTimers(st)
	ctx := interaction.WithTurnSource(interaction.WithTurn(context.Background(), "turn-1"), "operator")
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "keep a promise"}); err != nil {
		t.Fatal(err)
	}
	ws, _ := st.ActiveWorkSession()
	recordPromise(t, e, st, lg, kp, "in_1")
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "promise": "in_1", "resumes_on": "time:1800000000000"}); err != nil {
		t.Fatal(err)
	}
	alarm, found, err := st.AlarmByID(PromiseAlarmID(ws.ID))
	if err != nil || !found || alarm.Deadline != 1800000000000 || alarm.OwnerName != "timers" {
		t.Fatalf("the time condition is not the identity's alarm: %+v %v %v", alarm, found, err)
	}
	p := decodeTimerPayloadWhole(alarm.Payload)
	if p.Tag != PromiseAlarmTag || p.SetIn != "operator" || !strings.Contains(p.Message, "Work session "+ws.ID+" resumes now") {
		t.Fatalf("the alarm does not say what it is: %+v", p)
	}
	if _, err := e.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "update", "resumes_on": "word"}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := st.AlarmByID(PromiseAlarmID(ws.ID)); found {
		t.Fatal("the alarm outlived its condition")
	}
}
