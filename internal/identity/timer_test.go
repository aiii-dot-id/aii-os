package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The timer verb: requested (never gated), owed
// (durable + delivered), simple (when|duration + message).

func timerEngine(t *testing.T) (*Engine, func()) {
	e, st, lg, kp, _ := setupEngine(t)
	e.timers = NewStoreTimers(st)
	// a real TIME with the delivery owner registered, not started —
	// tests drive dispatch directly or via a short-lived scheduler.
	tm := cognitive.NewTIME(st, st)
	tm.RegisterOwner(NewTimerDeliveryOwner(e))
	t.Cleanup(tm.Stop)
	return e, func() { _ = tm; _ = lg; _ = kp }
}

func TestTimerSetDurationFiresToOutbox(t *testing.T) {
	e, _ := timerEngine(t)

	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "tea", "duration": "1s", "message": "your tea is ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "tea") || !strings.Contains(res, "set") {
		t.Fatalf("set must answer with the promise: %q", res)
	}

	// Fire it: due row dispatch through the delivery owner.
	// (The scheduler would do this; directly is deterministic.)
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
	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
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
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "first-reminder", "duration": "10m"})
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "second-reminder", "duration": "20m", "message": "second"})

	list, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{"action": "list"})
	if !strings.Contains(list, "first-reminder") || !strings.Contains(list, "second") {
		t.Fatalf("list must show ids and messages: %q", list)
	}

	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{"action": "cancel", "id": "first-reminder"})
	list2, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{"action": "list"})
	// Distinctive ids: a bare "a" assertion false-positives on weekday names
	// ("Sat") inside the formatted timestamp. Assert on full identity.
	if strings.Contains(list2, "first-reminder") || !strings.Contains(list2, "second-reminder") {
		t.Fatalf("cancel must remove exactly first-reminder: %q", list2)
	}
}

func TestTimerReplaceSameID(t *testing.T) {
	e, _ := timerEngine(t)
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "x", "duration": "10m"})
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "x", "duration": "1h"})
	timers, _ := e.timers.ListTimers()
	if len(timers) != 1 {
		t.Fatalf("same id must replace, got %d rows", len(timers))
	}
}

// The parseDuration regression: unit durations are the PRIMARY form —
// their deadline math is asserted directly (the original bug: Sscanf
// parsed "10m" as 10 seconds, "1h30m" as 1).
func TestTimerDurationDeadlineMath(t *testing.T) {
	cases := map[string]time.Duration{
		"10m":   10 * time.Minute,
		"90s":   90 * time.Second,
		"1h30m": 90 * time.Minute,
		"250ms": 250 * time.Millisecond,
		"90":    90 * time.Second, // bare = seconds
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
	// Non-durations refuse — no partial-parse acceptance.
	for _, bad := range []string{"10x", "m10", "tomorrow", "", "-5m"} {
		if _, err := parseDuration(bad); err == nil {
			t.Fatalf("parseDuration(%q) must refuse", bad)
		}
	}
}

func TestTimerBareSecondsAndErrors(t *testing.T) {
	e, _ := timerEngine(t)
	// bare number = seconds
	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "n", "duration": "90"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "n") {
		t.Fatalf("bare-seconds duration must parse: %q", res)
	}
	// both when and duration = error
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "z", "when": "2026-08-18T07:00:00Z", "duration": "5m"}); err == nil {
		t.Fatal("when+duration must refuse")
	}
	// bad RFC3339
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "z", "when": "tomorrow"}); err == nil {
		t.Fatal("bad when must refuse with guidance")
	}
	// unknown action
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "explode"}); err == nil {
		t.Fatal("unknown action must refuse")
	}
}

func TestTimerRowOutlivesItsDeadlineAndDeliversLate(t *testing.T) {
	// set on engine 1; with no scheduler the row outlives its deadline,
	// and a late dispatch over the SAME store delivers it, message intact
	// (the restart promise). No second TIME is started here, and the
	// once-only floor is TestTimerDoubleDispatchSingleDelivery's.
	e1, _ := timerEngine(t)
	e1.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "persist", "duration": "500ms", "message": "still owed"})
	st := engineStore(e1)

	time.Sleep(700 * time.Millisecond) // deadline passes with no scheduler
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
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
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
	// A bare engine (no timers wired) refuses honestly, never no-ops.
	e, _, _, _, _ := setupEngine(t)
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "list"}); err == nil {
		t.Fatal("nil backend must refuse, not silently succeed")
	}
}

// helpers

func ctxBG() context.Context { return context.Background() }

// engineStore recovers the concrete store the test engine was built
// over, for the assertions the engine's port does not carry.
func engineStore(e *Engine) *store.Store { return e.store.(*store.Store) }

// RESIDENT DELIVERY (the promise's other half): a fired timer reaches
// the identity's context at the next turn boundary, exactly once — the
// window is "firings after the resident's last own turn".
func TestTimerFiringsReachResidentWindow(t *testing.T) {
	e, _ := timerEngine(t)
	st := engineStore(e)

	// Resident spoke at T0; timer fires at T1 > T0.
	if err := st.AddConversationTurn("resident", "I'll check the build soon."); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOutboxMessage("timer_acme_ab12", "operator", "", "check the Acme build", nil); err != nil {
		t.Fatal(err)
	}
	// An older firing (before the resident's last turn) is outside the window.
	if err := st.AddOutboxMessage("timer_old_cd34", "operator", "", "stale firing", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.AddOutboxMessage("timerXnot_a_firing", "operator", "", "unrelated outbox row", nil); err != nil {
		t.Fatal(err)
	}
	// Same-second writes must still order correctly (the RFC3339Nano text
	// compare failed exactly here; numeric ms domain fixes it).
	if err := st.BumpOutboxCreatedMs("timer_old_cd34", -60000); err != nil {
		t.Fatal(err)
	}

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

	// After the resident's NEXT turn, the window advances past it. Stated
	// rather than raced for the same reason: the firing is placed before
	// the coming turn instead of hoping the wall clock advances between
	// two adjacent writes.
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

func TestTimerTagStatusQueryEvery(t *testing.T) {
	e, _ := timerEngine(t)

	// set with tag + every
	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
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

	// one-shot with tag
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "tea2", "tag": "personal", "duration": "40m"})

	// list: both pending, relative time + status present
	list, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{"action": "list"})
	for _, want := range []string{"standup", "#ops", "tea2", "pending", "in "} {
		if !strings.Contains(list, want) {
			t.Fatalf("list must show %q: %q", want, list)
		}
	}

	// query by tag
	q, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "query", "tag": "ops"})
	if !strings.Contains(q, "standup") || strings.Contains(q, "tea2") {
		t.Fatalf("tag query must filter: %q", q)
	}
	// query by text
	q2, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "query", "query": "standup notes"})
	if !strings.Contains(q2, "standup") {
		t.Fatalf("text query must find: %q", q2)
	}

	// A fired timer stays visible as fired (the alarm-history promise).
	st := engineStore(e)
	owner := NewTimerDeliveryOwner(e)
	if r := owner.OnAlarm(ctxBG(), "tea2", "wall", time.Now().UnixMilli(), encodeTimerPayload("personal", "")); !r.Accepted {
		t.Fatal("fire must deliver")
	}
	list2, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "query", "status": "fired"})
	if !strings.Contains(list2, "tea2") {
		t.Fatalf("fired alarms must be searchable: %q", list2)
	}
	// And it is listed at the time it fired: every fired alarm used to be
	// dated the zero of the clock, because its row holds no deadline.
	// (The year is asked for, not the day: a run across midnight must not
	// fail on the date.)
	fired := time.Now().UTC()
	if strings.Contains(list2, "1970") || !(strings.Contains(list2, fired.Format("2006")+" (fired)") || strings.Contains(list2, fired.Add(-time.Hour).Format("2006")+" (fired)")) {
		t.Fatalf("a fired alarm must be listed at the time it fired, not in 1970: %q", list2)
	}

	// Recurring row: repeat_every rides the durable row.
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

// WHAT IS SET CLOSES THE LISTING. The conversation loop cuts a tool
// result over its bound (32,000 characters unless the operator sets
// another) from the front. Listed first, the alarms still owed were what
// the cut took: an identity with a long history read old firings and
// nothing it had set. The read lists the newest prompt.alarm_firings
// firings; set high enough to list all sixty, the listing still ends with
// what is set.
func TestTheAlarmsReadListsWhatIsSetAfterEveryFiring(t *testing.T) {
	e, _ := timerEngine(t)
	e.alarmFirings = func() int { return 1000 }
	if _, err := e.ExecuteAction(ctxBG(), "verb", "work", map[string]any{
		"action": "alarm.set", "id": "still-owed", "duration": "10m", "message": "call the harbor office"}); err != nil {
		t.Fatal(err)
	}
	owner := NewTimerDeliveryOwner(e)
	t.Cleanup(owner.Stop)
	// Sixty firings of one daily alarm, its message the length of a note
	// an identity leaves itself: more than the loop's default bound.
	message := strings.Repeat("review the day's commitments. ", 20)
	first := time.Now().Add(-60 * 24 * time.Hour).UnixMilli()
	for day := int64(0); day < 60; day++ {
		if r := owner.OnAlarm(ctxBG(), "daily", "wall", first+day*24*3600*1000, encodeTimerPayload("ops", message)); !r.Accepted {
			t.Fatalf("firing %d was not delivered: %v", day, r.Err)
		}
	}
	list, err := e.ExecuteAction(ctxBG(), "verb", "recall", map[string]any{"source": "alarms"})
	if err != nil {
		t.Fatal(err)
	}
	const loopBound = limits.DefaultToolResultChars
	runes := []rune(list)
	if len(runes) <= loopBound {
		t.Fatalf("the history must pass the loop's bound for the cut to show: %d characters", len(runes))
	}
	if owed := strings.Index(list, "still-owed"); owed < 0 || owed < strings.LastIndex(list, "(fired)") {
		t.Fatalf("what is set must be listed after every firing; it stands at %d of %d characters", owed, len(list))
	}
	// Of an answer over its bound the loop keeps the tail.
	kept := string(runes[len(runes)-loopBound:])
	for _, want := range []string{"still-owed", "(pending)", "call the harbor office"} {
		if !strings.Contains(kept, want) {
			t.Fatalf("the loop's cut took %q, an alarm still owed, and kept old firings", want)
		}
	}
}

// THE WAKE SEAM: acceptance commits both notice and wake, then signals the
// executor. A floor on its own no longer discharges the promise.
func TestTimerOwnerWakes(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	owner := NewTimerDeliveryOwner(e)
	t.Cleanup(owner.Stop)
	poked := false
	e.workWake = func() { poked = true }

	r := owner.OnAlarm(ctxBG(), "wake7", "wall", time.Now().UnixMilli(), encodeTimerPayload("morning", "good morning"))
	if !r.Accepted {
		t.Fatal("floor must accept")
	}
	w, err := st.ClaimWork([]string{TimerWakeWorkKind}, time.Now().UnixMilli())
	if err != nil || w == nil || !poked {
		t.Fatalf("wake not durably handed to the executor: %+v %v poked=%t", w, err, poked)
	}
	var got TimerWake
	if err := json.Unmarshal([]byte(w.Payload), &got); err != nil || got.AlarmID != "wake7" || got.Tag != "morning" || got.Message != "good morning" {
		t.Fatalf("wake payload: %+v %v", got, err)
	}
}

func TestStoppedTimerOwnerCannotSilentlyLoseAWake(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	o := NewTimerDeliveryOwner(e)
	o.Stop()
	result := o.OnAlarm(t.Context(), "stopped", "wall", 1, "still owed")
	if result.Accepted || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("shutdown acknowledged a lost wake: %+v", result)
	}
	rows, err := st.UndeliveredMessages()
	if err != nil || len(rows) != 0 {
		t.Fatalf("stopped handoff wrote: %+v %v", rows, err)
	}
	if n, err := st.PendingWorkCount(); err != nil || n != 0 {
		t.Fatalf("stopped handoff queued: %d %v", n, err)
	}
}

func TestTimerOwnerStopWaitsForWake(t *testing.T) {
	e, _ := timerEngine(t)
	owner := NewTimerDeliveryOwner(e)
	started := make(chan struct{})
	release := make(chan struct{})
	owner.OnSafeWake = func(context.Context, *SafeWakes) {
		close(started)
		<-release
	}
	owner.OnSafeAlarm(context.Background(), "owned", "wall", time.Now().UnixMilli(), "")
	owner.SafePassEnd(context.Background())
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

func TestTimerSetWorksInSafeMode(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	e.timers = NewStoreTimers(st)
	e.SetSafeMode("test safe reason")

	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "safe-timer", "duration": "10m", "message": "still owed"}); err != nil {
		t.Fatalf("the verb itself must not refuse in SAFE: %v", err)
	}
	timers, _ := e.timers.ListTimers()
	if len(timers) != 1 {
		t.Fatal("the promise landed")
	}

	st.SetFrozen(true, "test: integrity lost")
	_, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]any{
		"action": "set", "id": "later-timer", "duration": "10m", "message": "cannot be promised now"})
	var frozen *store.FrozenError
	if !errors.As(err, &frozen) {
		t.Fatalf("under the freeze a new timer is refused by the projection, got %v", err)
	}
	if timers, _ = e.timers.ListTimers(); len(timers) != 1 {
		t.Fatalf("the refused timer must not exist: %d timers", len(timers))
	}
}

func TestTimerDoubleDispatchSingleDelivery(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	owner := NewTimerDeliveryOwner(engine)
	t.Cleanup(owner.Stop)

	wakes := map[string]int{}
	drain := func() {
		t.Helper()
		for {
			w, err := st.ClaimWork([]string{TimerWakeWorkKind}, time.Now().UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			if w == nil {
				return
			}
			var firing TimerWake
			if err := json.Unmarshal([]byte(w.Payload), &firing); err != nil {
				t.Fatal(err)
			}
			wakes[firing.DeliveryID()]++
			if err := st.CompleteWork(w.ID); err != nil {
				t.Fatal(err)
			}
		}
	}

	deadline := time.Now().Add(time.Hour).UnixMilli()
	payload := encodeTimerPayload("identity", "look at yourself")

	// The same firing (alarmID + deadline) dispatched concurrently.
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
	drain()
	// Completion is not permission for a second dispatch of this firing.
	if result := owner.OnAlarm(context.Background(), "self_reflect", "wall", deadline, payload); !result.Accepted {
		t.Fatalf("duplicate after completion: %+v", result)
	}
	drain()
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

	// A NEW firing (deadline moved — repeating timer re-armed) delivers again.
	next := deadline + 15*60*1000
	owner.OnAlarm(context.Background(), "self_reflect", "wall", next, payload)

	owner.Stop()
	drain()
	first, second := fmt.Sprintf("timer_self_reflect_%d", deadline), fmt.Sprintf("timer_self_reflect_%d", next)
	if wakes[first] != 1 {
		t.Errorf("one firing dispatched twice = exactly one wake; got %d", wakes[first])
	}
	if wakes[second] != 1 {
		t.Errorf("a new firing must wake again, exactly once; got %d", wakes[second])
	}
	if len(wakes) != 2 {
		t.Errorf("wakes for firings that never happened: %v", wakes)
	}
}

func TestTimerDeliveryFailureDoesNotMarkFiringDelivered(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	owner := NewTimerDeliveryOwner(engine)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	const deadline = int64(42)
	got := owner.OnAlarm(context.Background(), "retry", "wall", deadline, "message")
	if got.Accepted {
		t.Fatal("failed outbox delivery was accepted")
	}
	if got.Err == nil {
		t.Fatal("the delivery failure's reason was dropped on the way back to TIME")
	}
}

func TestTimerDeliveryRefusalCarriesTheFreezeAsItsReason(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	owner := NewTimerDeliveryOwner(engine)
	st.SetFrozen(true, "test: integrity lost")
	got := owner.OnAlarm(context.Background(), "frozen", "wall", int64(42), encodeTimerPayload("tea", "kettle"))
	if got.Accepted {
		t.Fatal("a delivery the freeze refused was accepted")
	}
	var frozen *store.FrozenError
	if !errors.As(got.Err, &frozen) {
		t.Fatalf("the decline's reason must be the freeze, typed; got %v", got.Err)
	}
	if got.NextDeadline != nil {
		t.Fatal("a faulted decline names no replacement deadline; the row's fate is TIME's transition")
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

// SAFE firings are queued synchronously and delivered per pass: no
// goroutine and no drainer while TIME's pass is still adding, then ONE
// drainer that sees the whole pass in one batch. A later firing of the
// same alarm replaces its entry, so what waits is bounded by the alarms.
func TestSafeFiringsQueueUntilThePassEnds(t *testing.T) {
	engine, _, _, _, _ := setupEngine(t)
	owner := NewTimerDeliveryOwner(engine)
	t.Cleanup(owner.Stop)
	var starts atomic.Int32
	batches := make(chan []SafeFiring, 4)
	owner.OnSafeWake = func(_ context.Context, q *SafeWakes) {
		starts.Add(1)
		select {
		case batches <- q.Fresh():
		default:
		}
		q.Take()
		for !q.Idle() {
			q.Fresh()
			q.Take()
		}
	}
	const n = 150
	for i := 0; i < n; i++ {
		owner.OnSafeAlarm(context.Background(), fmt.Sprintf("bk_%03d", i), "wall", int64(1000+i), `{"message":"m"}`)
	}
	owner.OnSafeAlarm(context.Background(), "bk_000", "wall", 5000, `{"message":"m"}`) // the same alarm again
	time.Sleep(50 * time.Millisecond)
	if got := starts.Load(); got != 0 {
		t.Fatalf("a drainer started before the pass ended (%d)", got)
	}
	if got := owner.SafeWakes().Len(); got != n {
		t.Fatalf("one entry per alarm: %d, want %d", got, n)
	}
	owner.SafePassEnd(context.Background())
	select {
	case b := <-batches:
		if len(b) != n {
			t.Fatalf("the drainer's first announcement holds the whole pass: %d, want %d", len(b), n)
		}
		if b[0].AlarmID != "bk_000" || b[0].Deadline != 5000 || b[0].Coalesced != 1 {
			t.Fatalf("a later firing replaces the alarm's entry and counts the one it replaced: %+v", b[0])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the pass end started no drainer")
	}
	owner.Stop()
	if got := starts.Load(); got != 1 {
		t.Fatalf("exactly one drainer: %d", got)
	}
}

// THE NEWEST FIRINGS, AND ONE LINE FOR THE REST (row 9.4 of the second
// decision sheet). Every firing since birth was listed, so a daily alarm a
// year old put some 8,000 tokens of the past into every read. The read
// lists the newest prompt.alarm_firings (20 by default), what is set after
// them, and one line before them: how many came earlier, and the recall
// that lists them. That recall pages back to the first firing, firings
// alone; the words a read was narrowed by ride the line.
func TestTheAlarmsReadListsTheNewestFiringsAndSaysHowToAskForTheRest(t *testing.T) {
	e, _ := timerEngine(t)
	if _, err := e.ExecuteAction(ctxBG(), "verb", "work", map[string]any{
		"action": "alarm.set", "id": "still-owed", "duration": "10m", "message": "call the harbor office"}); err != nil {
		t.Fatal(err)
	}
	owner := NewTimerDeliveryOwner(e)
	t.Cleanup(owner.Stop)
	first := time.Now().Add(-60 * 24 * time.Hour).UnixMilli()
	for day := int64(0); day < 60; day++ {
		if r := owner.OnAlarm(ctxBG(), "daily", "wall", first+day*24*3600*1000, encodeTimerPayload("ops", fmt.Sprintf("firing number %d.", day))); !r.Accepted {
			t.Fatalf("firing %d was not delivered: %v", day, r.Err)
		}
	}
	read := func(args map[string]any) string {
		t.Helper()
		args["source"] = "alarms"
		out, err := e.ExecuteAction(ctxBG(), "verb", "recall", args)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// lists reports whether the answer lists exactly the firings from..to.
	lists := func(out string, from, to int) bool {
		for day := 0; day < 60; day++ {
			if strings.Contains(out, fmt.Sprintf("firing number %d.", day)) != (day >= from && day <= to) {
				return false
			}
		}
		return true
	}

	out := read(map[string]any{"query": ""})
	if want := "Showing the newest 20 of 60 firings; ask for the earlier ones with recall source=alarms after_seq=41."; !strings.Contains(out, "\n"+want+"\n") {
		t.Fatalf("the read does not say how many firings came earlier and how to ask for them (%q):\n%s", want, out)
	}
	if !lists(out, 40, 59) || !strings.HasSuffix(out, `"call the harbor office"`) {
		t.Fatalf("the read must list the twenty newest firings and end with what is set:\n%s", out)
	}
	if strings.Index(out, "Showing the newest") > strings.Index(out, "firing number 40.") {
		t.Fatalf("the line stands where the earlier firings would be, before the ones listed:\n%s", out)
	}

	// The line's own recall: the twenty before them, firings alone.
	out = read(map[string]any{"query": "", "after_seq": float64(41)})
	if want := "Showing the newest 20 of 40 firings; ask for the earlier ones with recall source=alarms after_seq=21."; !strings.Contains(out, want) {
		t.Fatalf("a page of earlier firings does not say how to ask for the ones before it (%q):\n%s", want, out)
	}
	if !lists(out, 20, 39) || strings.Contains(out, "still-owed") {
		t.Fatalf("after_seq=41 must list firings 21 to 40 and nothing that is set:\n%s", out)
	}
	out = read(map[string]any{"query": "", "after_seq": float64(21)})
	if !lists(out, 0, 19) || strings.Contains(out, "Showing the newest") {
		t.Fatalf("after_seq=21 must list the first twenty firings and no line, none being earlier:\n%s", out)
	}

	// The words that narrowed the read ride its line.
	if out = read(map[string]any{"query": "ops"}); !strings.Contains(out, `ask for the earlier ones with recall source=alarms query="ops" after_seq=41.`) {
		t.Fatalf("the line drops the words the read was narrowed by:\n%s", out)
	}
	// The operator's number, read at each read.
	e.alarmFirings = func() int { return 5 }
	if out = read(map[string]any{"query": ""}); !strings.Contains(out, "Showing the newest 5 of 60 firings; ask for the earlier ones with recall source=alarms after_seq=56.") || !lists(out, 55, 59) {
		t.Fatalf("with prompt.alarm_firings at 5 the read lists:\n%s", out)
	}
	// The other standing sources still read whole.
	if _, err := e.ExecuteAction(ctxBG(), "verb", "recall", map[string]any{"source": "projects", "query": "", "after_seq": float64(3)}); err == nil || !strings.Contains(err.Error(), "reads whole") {
		t.Fatalf("after_seq on a standing source other than alarms must be refused: %v", err)
	}
}
