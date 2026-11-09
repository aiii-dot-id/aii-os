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

// THE VARIANT: the condition
// is set in a timer wake's own turn (the wake for moment A names moment
// B), then an operator's message opens a turn before B; B comes while it
// runs (the timer fires, the wake is queued, the row goes); the turn's
// only work call is an update of the focus, no resumes_on; it ends by
// itself. Then the queued wake must meet the condition, or the work must
// continue — never idle with a condition no alarm serves.
func TestAnOperatorsTurnDuringAMomentSetByAWakeDoesNotIdleTheWork(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	pressStage(t, app, store.StageTrusted)
	logs := logsink.CaptureForTest(t)
	app.executor.Stop()
	fireCtx := goldenTurnCtx(t)
	var mu sync.Mutex
	var sessionID string
	var b int64
	errs := make(chan error, 2)
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		mu.Lock()
		defer mu.Unlock()
		switch n {
		case 1: // A's wake: the identity names B, two seconds ahead
			b = time.Now().UTC().Add(2000 * time.Millisecond).UnixMilli()
			_, _ = io.WriteString(w, callReply("", toolCall("w1", "work", `{"action":"update","resumes_on":"time:`+strconv.FormatInt(b, 10)+`"}`)))
		case 2:
			_, _ = io.WriteString(w, textReply("noted"))
		case 3: // the operator's turn: an update of the focus, no resumes_on
			_, _ = io.WriteString(w, callReply("", toolCall("w2", "work", `{"action":"update","focus":"the gate failed; four repairs"}`)))
		case 4: // B comes while the operator's turn runs
			time.Sleep(time.Until(time.UnixMilli(b)) + 100*time.Millisecond)
			if _, found, _ := app.store.AlarmByID(identity.PromiseAlarmID(sessionID)); found {
				if err := fireAlarm(app, fireCtx, sessionID); err != nil {
					errs <- err
				}
			}
			_, _ = io.WriteString(w, textReply("noted"))
		default:
			_, _ = io.WriteString(w, textReply("noted"))
		}
	})
	a := time.Now().UTC().Add(300 * time.Millisecond).UnixMilli()
	ws := sessionUnder(t, app, "time:"+strconv.FormatInt(a, 10))
	mu.Lock()
	sessionID = ws.ID
	mu.Unlock()
	time.Sleep(time.Until(time.UnixMilli(a)) + 100*time.Millisecond)
	if err := fireAlarm(app, fireCtx, ws.ID); err != nil {
		t.Fatal(err)
	}
	wake := func(which string) {
		t.Helper()
		w, err := app.store.ClaimWork([]string{identity.TimerWakeWorkKind}, time.Now().UnixMilli())
		if err != nil || w == nil {
			t.Fatalf("no queued wake for %s: %+v %v", which, w, err)
		}
		if err := (&timerWakeHandler{app}).RunWork(goldenTurnCtx(t), w); err != nil {
			t.Fatalf("the wake for %s: %v", which, err)
		}
		if err := app.store.CompleteWork(w.ID); err != nil {
			t.Fatal(err)
		}
	}
	wake("A") // sets B in the wake's own turn
	mu.Lock()
	want := "time:" + strconv.FormatInt(b, 10)
	mu.Unlock()
	if on := resumesOnOf(t, app); on != want {
		t.Fatalf("A's wake did not leave B standing: %q", on)
	}
	operatorTurn2(t, app, "Status?")
	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
	afterTurn := resumesOnOf(t, app)
	wake("B")
	resumesNow := "Work session " + ws.ID + " resumes now: the moment you named with resumes_on has come."
	refused := strings.Contains(logs.String(), "wakes nothing")
	resumed := sawRequest(obs, resumesNow)()
	afterWake := resumesOnOf(t, app)
	t.Logf("after the operator's turn the condition is %q (set %q); B's queued wake: refused=%v, resumed=%v; the condition now: %q", afterTurn, want, refused, resumed, afterWake)
	if !resumed {
		t.Fatalf("THE WORK IS IDLE: after the operator's turn the condition was %q (B was %q), B's queued wake was refused=%v, and the condition is %q", afterTurn, want, refused, afterWake)
	}
}
