package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// AN ALARM REMEMBERS THE TURN THAT SET IT: the source of that turn rides
// the alarm's payload, and the firing carries it to the wake's turn
// (TimerWake.SetIn), so a reminder the operator asked for keeps their
// provenance when it fires. An alarm set in a turn that named no path
// remembers none.
func TestAnAlarmRemembersTheTurnThatSetIt(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	e.timers = NewStoreTimers(st)
	operator := interaction.WithTurnSource(context.Background(), "operator")
	if _, err := e.ExecuteAction(operator, "verb", "timer", map[string]any{"action": "set", "id": "t_op", "duration": "10m", "message": "tea"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ExecuteAction(context.Background(), "verb", "timer", map[string]any{"action": "set", "id": "t_none", "duration": "10m", "message": "tea"}); err != nil {
		t.Fatal(err)
	}
	alarms, err := st.DueAlarms("wall", int64(1)<<62, 10)
	if err != nil {
		t.Fatal(err)
	}
	payloads := map[string]string{}
	for _, a := range alarms {
		payloads[a.AlarmID] = a.Payload
	}
	if p := decodeTimerPayloadWhole(payloads["t_op"]); p.SetIn != "operator" || p.Message != "tea" {
		t.Fatalf("the operator's alarm does not remember its turn: %+v (%s)", p, payloads["t_op"])
	}
	if p := decodeTimerPayloadWhole(payloads["t_none"]); p.SetIn != "" {
		t.Fatalf("an alarm set in an unnamed turn remembers %q", p.SetIn)
	}
	if strings.Contains(payloads["t_none"], "set_in") {
		t.Fatalf("an empty provenance is written: %s", payloads["t_none"])
	}
}
