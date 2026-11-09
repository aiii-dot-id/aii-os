package app

import (
	"errors"
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

// AN UPDATE AFTER THE FIRING IS QUEUED: a session waits on time:T with
// its alarm set; the operator's message opens a turn before T; T comes
// while it runs, so the firing is queued and the alarm row is gone; THEN,
// in that same turn, work update with state= and standing= and no
// resumes_on; the turn ends by itself. An update that names no resumes_on
// leaves the condition and its firing as they are: the queued wake meets
// the condition, or the work continues — never idle.
func TestAnUpdateAfterTheFiringIsQueuedLeavesTheConditionAndItsWake(t *testing.T) {
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
		case 1: // the moment comes while the turn runs: the alarm fires, its wake is queued, its row goes; THEN the update
			time.Sleep(time.Until(time.UnixMilli(at)) + 100*time.Millisecond)
			// The app's own clock runs and may reach the moment first, even
			// between a look at the row and this firing; a row it has fired
			// is gone, which is the case under test.
			if err := fireAlarm(app, fireCtx, sessionID); err != nil {
				if _, found, _ := app.store.AlarmByID(identity.PromiseAlarmID(sessionID)); found {
					errs <- err
				}
			}
			if _, found, _ := app.store.AlarmByID(identity.PromiseAlarmID(sessionID)); found {
				errs <- errors.New("the alarm row still stands after its firing: the wake was not queued")
			}
			_, _ = io.WriteString(w, callReply("", toolCall("w1", "work", `{"action":"update","state":"997d47d9 deployed; live-checked","standing":"Running the whole build; a78f8695 landed, not yet deployed here"}`)))
		default: // the turn ends by itself
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
	if afterTurn != "time:"+strconv.FormatInt(at, 10) && !resumed && !continued {
		t.Fatalf("THE UPDATE MOVED THE CONDITION: after an update with no resumes_on the condition was %q", afterTurn)
	}
	if !resumed && !continued {
		t.Fatalf("THE WORK IS IDLE: after the operator's turn the condition was %q, the queued wake was refused=%v, nothing followed, and the condition is %q (alarm %s)", afterTurn, refused, afterWake, identity.PromiseAlarmID(ws.ID))
	}
}
