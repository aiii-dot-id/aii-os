package app

import (
	"net/http"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// ONE DENOMINATOR: the status line and TURN_SUMMARY count the
// same calls, and the refusals before running are their own part in
// both. One turn: a work start that ran, and an update refused before
// effect (the promise rule).
func TestTheStatusLineAndTheSummaryCountTheSameCalls(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	logs := logsink.CaptureForTest(t)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		if n == 1 {
			_, _ = w.Write([]byte(callReply("",
				toolCall("w1", "work", `{"action":"start","description":"count the calls"}`),
				toolCall("w2", "work", `{"action":"update","promise":"in_nothing_open"}`))))
			return
		}
		_, _ = w.Write([]byte(textReply("noted")))
	})
	operatorTurn2(t, app, "count your calls")
	waitUntil(t, func() bool { return strings.Contains(logs.String(), "TURN_SUMMARY") }, "the turn's summary")
	_ = obs
	out := logs.String()
	if !strings.Contains(out, `TURN_SUMMARY calls=2 refused=1 `) {
		t.Fatalf("the summary does not count two calls, one refused:\n%s", out)
	}
	if !strings.Contains(out, `status="[status · calls 2: 1 ok, 1 failed, 1 refused before running ·`) {
		t.Fatalf("the status line does not name the refused call among two:\n%s", out)
	}
}
