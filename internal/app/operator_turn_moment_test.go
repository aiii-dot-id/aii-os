package app

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// AN OPERATOR'S TURN DURING WHICH THE MOMENT COMES: a session waits on
// time:T with its alarm set; the operator's message opens a turn before
// T; T comes while it runs, so the firing is queued and the alarm row is
// gone; the turn's only work call is an update of the focus, with no
// resumes_on, and it ends by itself. Then: the queued wake must meet the
// condition, or the work must continue — never idle with a condition no
// alarm serves.
func TestAnOperatorsTurnDuringWhichTheMomentComesDoesNotIdleTheWork(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	logs := logsink.CaptureForTest(t)
	var mu sync.Mutex
	var sessionID string
	var at int64
	errs := make(chan error, 2)
	fireCtx := goldenTurnCtx(t)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		mu.Lock()
		defer mu.Unlock()
		switch n {
		case 1: // the operator's turn: an update of the focus, no resumes_on
			_, _ = io.WriteString(w, callReply("", toolCall("w1", "work", `{"action":"update","focus":"the gate failed; four repairs"}`)))
		case 2: // the moment comes while the turn runs: the alarm fires, its wake is queued, its row goes
			time.Sleep(time.Until(time.UnixMilli(at)) + 100*time.Millisecond)
			// The identity's own timer fires it in its own time, even between
			// a look at the row and this firing; a row still standing is
			// fired here, as TIME would, and a row it has fired is gone.
			if err := fireAlarm(app, fireCtx, sessionID); err != nil {
				if _, found, _ := app.store.AlarmByID(identity.PromiseAlarmID(sessionID)); found {
					errs <- err
				}
			}
			_, _ = io.WriteString(w, textReply("noted"))
		default:
			_, _ = io.WriteString(w, textReply("noted"))
		}
	})
	mu.Lock()
	at = time.Now().UTC().Add(1500 * time.Millisecond).UnixMilli()
	mu.Unlock()
	ws := sessionUnder(t, app, "time:"+strconv.FormatInt(at, 10))
	mu.Lock()
	sessionID = ws.ID
	mu.Unlock()
	operatorTurn2(t, app, "Status?")
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	afterTurn := resumesOnOf(t, app)
	resumesNow := "Work session " + ws.ID + " resumes now: the moment you named with resumes_on has come."
	waitUntil(t, func() bool {
		return strings.Contains(logs.String(), "wakes nothing") || sawRequest(obs, resumesNow)() || strings.Contains(logs.String(), "continuation.start")
	}, "the queued wake's outcome, or a continuation")
	time.Sleep(2 * time.Second)
	out := logs.String()
	refused := strings.Contains(out, "wakes nothing")
	resumed := sawRequest(obs, resumesNow)()
	continued := strings.Contains(out, "continuation.start")
	afterWake := resumesOnOf(t, app)
	t.Logf("after the operator's turn the condition is %q; the queued wake: refused=%v, resumed=%v; a continuation followed: %v; the condition now: %q", afterTurn, refused, resumed, continued, afterWake)
	if !resumed && !continued {
		t.Fatalf("THE WORK IS IDLE: after the operator's turn the condition was %q, the queued wake was refused=%v, nothing followed, and the condition is %q (alarm %s)", afterTurn, refused, afterWake, identity.PromiseAlarmID(ws.ID))
	}
}
