package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .

func timerEngine(t *testing.T) (*Engine, func()) {
	e, st, lg, kp, _ := setupEngine(t)
	e.SetTimers(NewStoreTimers(st))
	// .
	// .
	tm := cognitive.NewTIME(st, st)
	tm.RegisterOwner(NewTimerDeliveryOwner(e))
	t.Cleanup(tm.Stop)
	return e, func() { _ = tm; _ = lg; _ = kp }
}

func TestTimerSetDurationFiresToOutbox(t *testing.T) {
	e, _ := timerEngine(t)

	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "tea", "duration": "1s", "message": "your tea is ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "tea") || !strings.Contains(res, "set") {
		t.Fatalf("set must answer with the promise: %q", res)
	}

	// .
	// .
	st := engineStore(e)
	alarms, _ := st.DueAlarms("wall", time.Now().Add(time.Second).UnixMilli(), 10)
	if len(alarms) != 1 || alarms[0].AlarmID != "tea" {
		t.Fatalf("want one due 'tea' row, got %+v", alarms)
	}
	owner := NewTimerDeliveryOwner(e)
	r := owner.OnAlarm(ctxBG(), alarms[0].AlarmID, alarms[0].Clock, alarms[0].Deadline, alarms[0].Payload)
	if !r.Accepted {
		t.Fatal("delivery must accept (one-shot delete)")
	}

	msgs, _ := e.UndeliveredMessages()
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "your tea is ready") {
			found = true
		}
	}
	if !found {
		t.Fatal("fired timer message must reach the outbox verbatim")
	}
}

func TestTimerWhenRFC3339(t *testing.T) {
	e, _ := timerEngine(t)
	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "wake", "when": "2026-08-18T07:00:00-04:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "wake") {
		t.Fatalf("set answer: %q", res)
	}
	timers, _ := e.timers.ListTimers()
	if len(timers) != 1 {
		t.Fatalf("want 1 timer, got %d", len(timers))
	}
	want := time.Date(2026, 8, 18, 7, 0, 0, 0, time.FixedZone("", -4*3600)).UnixMilli()
	if timers[0].Deadline != want {
		t.Fatalf("deadline = %d, want %d (timezone must be honored)", timers[0].Deadline, want)
	}
}

func TestTimerCancelAndList(t *testing.T) {
	e, _ := timerEngine(t)
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "first-reminder", "duration": "10m"})
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "second-reminder", "duration": "20m", "message": "second"})

	list, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{"action": "list"})
	if !strings.Contains(list, "first-reminder") || !strings.Contains(list, "second") {
		t.Fatalf("list must show ids and messages: %q", list)
	}

	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{"action": "cancel", "id": "first-reminder"})
	list2, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{"action": "list"})
	// .
	// .
	if strings.Contains(list2, "first-reminder") || !strings.Contains(list2, "second-reminder") {
		t.Fatalf("cancel must remove exactly first-reminder: %q", list2)
	}
}

func TestTimerReplaceSameID(t *testing.T) {
	e, _ := timerEngine(t)
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "x", "duration": "10m"})
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "x", "duration": "1h"})
	timers, _ := e.timers.ListTimers()
	if len(timers) != 1 {
		t.Fatalf("same id must replace, got %d rows", len(timers))
	}
}

// .
// .
// .
func TestTimerDurationDeadlineMath(t *testing.T) {
	cases := map[string]time.Duration{
		"10m":   10 * time.Minute,
		"90s":   90 * time.Second,
		"1h30m": 90 * time.Minute,
		"250ms": 250 * time.Millisecond,
		"90":    90 * time.Second,
		"1.5":   1500 * time.Millisecond,
	}
	for in, want := range cases {
		got, err := parseDuration(in)
		if err != nil {
			t.Fatalf("parseDuration(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("parseDuration(%q) = %v, want %v", in, got, want)
		}
	}
	// .
	for _, bad := range []string{"10x", "m10", "tomorrow", "", "-5m"} {
		if _, err := parseDuration(bad); err == nil {
			t.Fatalf("parseDuration(%q) must refuse", bad)
		}
	}
}

func TestTimerBareSecondsAndErrors(t *testing.T) {
	e, _ := timerEngine(t)
	// .
	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "n", "duration": "90"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "n") {
		t.Fatalf("bare-seconds duration must parse: %q", res)
	}
	// .
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "z", "when": "2026-08-18T07:00:00Z", "duration": "5m"}); err == nil {
		t.Fatal("when+duration must refuse")
	}
	// .
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "z", "when": "tomorrow"}); err == nil {
		t.Fatal("bad when must refuse with guidance")
	}
	// .
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "explode"}); err == nil {
		t.Fatal("unknown action must refuse")
	}
}

func TestTimerSurvivesRestart(t *testing.T) {
	// .
	// .
	e1, _ := timerEngine(t)
	e1.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "persist", "duration": "500ms", "message": "still owed"})
	st := engineStore(e1)

	time.Sleep(700 * time.Millisecond)
	alarms, _ := st.DueAlarms("wall", time.Now().UnixMilli(), 10)
	if len(alarms) != 1 || alarms[0].AlarmID != "persist" {
		t.Fatalf("row must survive: %+v", alarms)
	}
	owner := NewTimerDeliveryOwner(e1)
	if r := owner.OnAlarm(ctxBG(), alarms[0].AlarmID, alarms[0].Clock, alarms[0].Deadline, alarms[0].Payload); !r.Accepted {
		t.Fatal("late fire must deliver")
	}
	msgs, _ := e1.UndeliveredMessages()
	ok := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "still owed") {
			ok = true
		}
	}
	if !ok {
		t.Fatal("late delivery must carry the message")
	}
}

func TestTimerPayloadVerbatim(t *testing.T) {
	e, _ := timerEngine(t)
	msg := "line1\n\"quoted\" — ünïcodé ✓"
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "uni", "duration": "1s", "message": msg})
	st := engineStore(e)
	alarms, _ := st.DueAlarms("wall", time.Now().Add(time.Second).UnixMilli(), 10)
	if len(alarms) != 1 {
		t.Fatalf("one row, got %d", len(alarms))
	}
	tag, got := decodeTimerPayload(alarms[0].Payload)
	if got != msg {
		t.Fatalf("message must ride verbatim inside the envelope, got %q", got)
	}
	if tag != "" {
		t.Fatalf("no tag set, got %q", tag)
	}
}

func TestTimerNotGatedAndHonestNil(t *testing.T) {
	// .
	e, _, _, _, _ := setupEngine(t)
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "list"}); err == nil {
		t.Fatal("nil backend must refuse, not silently succeed")
	}
}

// .

func ctxBG() context.Context { return context.Background() }

func engineStore(e *Engine) *store.Store { return e.store }

// .
// .
// .
func TestTimerFiringsReachResidentWindow(t *testing.T) {
	e, _ := timerEngine(t)
	st := engineStore(e)

	// .
	if err := st.AddConversationTurn("resident", "I'll check the build soon."); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOutboxMessage("timer_acme_ab12", "operator", "", "check the Acme build", nil); err != nil {
		t.Fatal(err)
	}
	// .
	if err := st.AddOutboxMessage("timer_old_cd34", "operator", "", "stale firing", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOutboxMessage("timerXnot_a_firing", "operator", "", "unrelated outbox row", nil); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	if err := st.BumpOutboxCreatedMs("timer_old_cd34", -60000); err != nil {
		t.Fatal(err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := st.BumpOutboxCreatedMs("timer_acme_ab12", +1); err != nil {
		t.Fatal(err)
	}

	last, err := st.LastTurnAtMs("resident")
	if err != nil {
		t.Fatal(err)
	}
	firings, err := st.TimerFiringsSince(last)
	if err != nil {
		t.Fatal(err)
	}
	if len(firings) != 1 || !strings.Contains(firings[0].Content, "Acme") {
		t.Fatalf("window must contain exactly the post-turn firing, got %+v", firings)
	}

	// .
	// .
	// .
	// .
	if err := st.BumpOutboxCreatedMs("timer_acme_ab12", -2); err != nil {
		t.Fatal(err)
	}
	if err := st.AddConversationTurn("resident", "on it — checking now."); err != nil {
		t.Fatal(err)
	}
	last2, _ := st.LastTurnAtMs("resident")
	firings2, _ := st.TimerFiringsSince(last2)
	if len(firings2) != 0 {
		t.Fatalf("firing must age out after the resident's next turn, got %d", len(firings2))
	}
}

// .
// .
// .
// .
func TestTimerTagStatusQueryEvery(t *testing.T) {
	e, _ := timerEngine(t)

	// .
	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "standup", "tag": "ops", "every": "24h",
		"when": "2026-08-18T09:00:00-04:00", "message": "standup notes",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"standup", "#ops", "every 24h"} {
		if !strings.Contains(res, want) {
			t.Fatalf("set answer must name id/tag/cadence (%q missing): %q", want, res)
		}
	}

	// .
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "tea2", "tag": "personal", "duration": "40m"})

	// .
	list, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{"action": "list"})
	for _, want := range []string{"standup", "#ops", "tea2", "pending", "in "} {
		if !strings.Contains(list, want) {
			t.Fatalf("list must show %q: %q", want, list)
		}
	}

	// .
	q, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "query", "tag": "ops"})
	if !strings.Contains(q, "standup") || strings.Contains(q, "tea2") {
		t.Fatalf("tag query must filter: %q", q)
	}
	// .
	q2, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "query", "query": "standup notes"})
	if !strings.Contains(q2, "standup") {
		t.Fatalf("text query must find: %q", q2)
	}

	// .
	st := engineStore(e)
	owner := NewTimerDeliveryOwner(e)
	if r := owner.OnAlarm(ctxBG(), "tea2", "wall", time.Now().UnixMilli(), encodeTimerPayload("personal", "")); !r.Accepted {
		t.Fatal("fire must deliver")
	}
	list2, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "query", "status": "fired"})
	if !strings.Contains(list2, "tea2") {
		t.Fatalf("fired alarms must be searchable: %q", list2)
	}

	// .
	alarms, _ := st.DueAlarms("wall", int64(1)<<62, 10)
	var rep int64
	for _, a := range alarms {
		if a.AlarmID == "standup" && a.RepeatEvery != nil {
			rep = *a.RepeatEvery
		}
	}
	if rep != 24*3600*1000 {
		t.Fatalf("every=24h must ride repeat_every, got %d", rep)
	}
}

// .
// .
// .
func TestTimerOwnerWakes(t *testing.T) {
	e, _ := timerEngine(t)
	wake := make(chan [3]string, 1)
	owner := NewTimerDeliveryOwner(e)
	t.Cleanup(owner.Stop)
	owner.OnWake = func(_ context.Context, id, tag, msg string) { wake <- [3]string{id, tag, msg} }

	r := owner.OnAlarm(ctxBG(), "wake7", "wall", time.Now().UnixMilli(), encodeTimerPayload("morning", "good morning"))
	if !r.Accepted {
		t.Fatal("floor must accept")
	}
	select {
	case got := <-wake:
		if got[0] != "wake7" || got[1] != "morning" || got[2] != "good morning" {
			t.Fatalf("wake carries id/tag/message, got %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnWake must be called after the floor")
	}
}

func TestTimerOwnerStopWaitsForWake(t *testing.T) {
	e, _ := timerEngine(t)
	owner := NewTimerDeliveryOwner(e)
	started := make(chan struct{})
	release := make(chan struct{})
	owner.OnWake = func(context.Context, string, string, string) {
		close(started)
		<-release
	}
	owner.OnAlarm(context.Background(), "owned", "wall", time.Now().UnixMilli(), "")
	<-started

	stopped := make(chan struct{})
	go func() {
		owner.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while its wake goroutine was running")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the wake exited")
	}
}

// .
// .
// .
// .
// .
func TestTimerSetWorksInSafeMode(t *testing.T) {
	e, st, lg, kp, _ := setupEngine(t)
	e.SetTimers(NewStoreTimers(st))
	e.SetSafeMode("test safe reason")

	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "safe-timer", "duration": "10m", "message": "still owed"}); err != nil {
		t.Fatalf("timer set must work in SAFE (store-side promise, not a mint): %v", err)
	}
	timers, _ := e.timers.ListTimers()
	if len(timers) != 1 {
		t.Fatal("the promise landed")
	}
	_ = lg
	_ = kp
}

// .
// .
// .
// .
func TestTimerDoubleDispatchSingleDelivery(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	owner := NewTimerDeliveryOwner(engine)
	t.Cleanup(owner.Stop)

	wakeCh := make(chan string, 4)
	owner.OnWake = func(_ context.Context, alarmID, tag, message string) { wakeCh <- alarmID }
	waitWakes := func(n int) int {
		for i := 0; i < n; i++ {
			select {
			case <-wakeCh:
			case <-time.After(2 * time.Second):
				return i
			}
		}
		return n
	}

	deadline := time.Now().Add(time.Hour).UnixMilli()
	payload := encodeTimerPayload("identity", "look at yourself")

	// .
	results := make(chan cognitive.AlarmResult, 2)
	for i := 0; i < 2; i++ {
		go func() {
			results <- owner.OnAlarm(context.Background(), "self_reflect", "wall", deadline, payload)
		}()
	}
	r1, r2 := <-results, <-results

	if !r1.Accepted || !r2.Accepted {
		t.Fatalf("both dispatches accept (the transition must proceed): %v %v", r1.Accepted, r2.Accepted)
	}
	if got := waitWakes(1); got != 1 {
		t.Fatalf("one firing = one wake; got %d", got)
	}
	msgs, _ := st.UndeliveredMessages()
	floors := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "[timer self_reflect") {
			floors++
		}
	}
	if floors != 1 {
		t.Fatalf("one firing = one floor notice; got %d", floors)
	}
	listed, err := NewStoreTimers(st).ListTimers()
	if err != nil {
		t.Fatal(err)
	}
	foundID := false
	for _, timer := range listed {
		if timer.Fired && timer.ID == "self_reflect" {
			foundID = true
			if timer.FiredAtMs == 0 {
				t.Fatal("fired timer lost its delivery time")
			}
		}
	}
	if !foundID {
		t.Fatalf("fired timer id with underscore was not preserved: %+v", listed)
	}

	// .
	next := deadline + 15*60*1000
	owner.OnAlarm(context.Background(), "self_reflect", "wall", next, payload)
	if got := waitWakes(1); got != 1 {
		t.Fatalf("a new firing must wake again; got %d wakes", got)
	}
}

func TestTimerDeliveryFailureDoesNotMarkFiringDelivered(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	owner := NewTimerDeliveryOwner(engine)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	const deadline = int64(42)
	if got := owner.OnAlarm(context.Background(), "retry", "wall", deadline, "message"); got.Accepted {
		t.Fatal("failed outbox delivery was accepted")
	}
}

func TestListTimersReturnsStoreFailure(t *testing.T) {
	_, st, _, _, _ := setupEngine(t)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStoreTimers(st).ListTimers(); err == nil {
		t.Fatal("closed store was reported as an empty timer list")
	}
}
