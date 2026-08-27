package identity

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The timer verb per docs/VERB_TIMER.md: requested (never gated), owed
// (durable + delivered), simple (when|duration + message).

func timerEngine(t *testing.T) (*Engine, func()) {
	e, st, lg, kp, _ := setupEngine(t)
	e.SetTimers(NewStoreTimers(st))
	// a real TIME with the delivery owner registered, not started —
	// tests drive dispatch directly or via a short-lived scheduler.
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
	// Distinctive ids: a bare "a" assertion false-positives on weekday names
	// ("Sat") inside the formatted timestamp. Assert on full identity.
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
	res, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "n", "duration": "90"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "n") {
		t.Fatalf("bare-seconds duration must parse: %q", res)
	}
	// both when and duration = error
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "z", "when": "2026-08-18T07:00:00Z", "duration": "5m"}); err == nil {
		t.Fatal("when+duration must refuse")
	}
	// bad RFC3339
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "z", "when": "tomorrow"}); err == nil {
		t.Fatal("bad when must refuse with guidance")
	}
	// unknown action
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "explode"}); err == nil {
		t.Fatal("unknown action must refuse")
	}
}

func TestTimerSurvivesRestart(t *testing.T) {
	// set on engine 1; a fresh TIME over the SAME store fires it late
	// but exactly once, message intact (the restart promise).
	e1, _ := timerEngine(t)
	e1.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
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
	// A bare engine (no timers wired) refuses honestly, never no-ops.
	e, _, _, _, _ := setupEngine(t)
	if _, err := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "list"}); err == nil {
		t.Fatal("nil backend must refuse, not silently succeed")
	}
}

// helpers

func ctxBG() context.Context { return context.Background() }

func engineStore(e *Engine) *store.Store { return e.store }

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
	if err := st.AddOutboxMessage("timer_helsing_ab12", "operator", "", "check the Helsing build", nil); err != nil {
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

	// EVERY timestamp this test depends on is set here, not inherited
	// from how fast the machine happens to be.
	//
	// The window is "firings after the resident's last turn", compared in
	// whole milliseconds: LastTurnAtMs parses conversations.created_at
	// (RFC3339Nano) and returns UnixMilli, while outbox rows carry
	// created_ms. On Apple Silicon the turn and the firing above land in
	// the SAME millisecond often enough to fail this roughly one run in
	// five, because created_ms > sinceMs then excludes the firing. On the
	// Linux host the writes are slow enough to separate and it passes.
	//
	// That is a real weakness in the ordering domain and NOT what this
	// test is for: milliseconds cannot totally order two writes, so a
	// firing minted in the same millisecond as the resident's last turn
	// is dropped, and `>=` only trades the dropped firing for a repeated
	// one at the next boundary. Fixing it means giving the two tables a
	// shared monotonic sequence — conversations already has turn_seq,
	// outbox has nothing comparable — which is an ordering-domain change
	// and needs a ruling, not a patch. Recorded here so a green run is
	// not read as proof that same-millisecond delivery is sound.
	//
	// What THIS test owns is the window semantics, so it states the
	// ordering it means instead of racing for it.
	if err := st.BumpOutboxCreatedMs("timer_helsing_ab12", +1); err != nil {
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
	if len(firings) != 1 || !strings.Contains(firings[0].Content, "Helsing") {
		t.Fatalf("window must contain exactly the post-turn firing, got %+v", firings)
	}

	// After the resident's NEXT turn, the window advances past it. Stated
	// rather than raced for the same reason: the firing is placed before
	// the coming turn instead of hoping the wall clock advances between
	// two adjacent writes.
	if err := st.BumpOutboxCreatedMs("timer_helsing_ab12", -2); err != nil {
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

// Tags, status detail, recurrence, and query — the alarm-review adds
// (James, 2026-08-17): set returns tag+id+cadence; list shows relative
// time + full timestamp + status; fired alarms stay searchable; every=
// arms a recurring row.
func TestTimerTagStatusQueryEvery(t *testing.T) {
	e, _ := timerEngine(t)

	// set with tag + every
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

	// one-shot with tag
	e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "set", "id": "tea2", "tag": "personal", "duration": "40m"})

	// list: both pending, relative time + status present
	list, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{"action": "list"})
	for _, want := range []string{"standup", "#ops", "tea2", "pending", "in "} {
		if !strings.Contains(list, want) {
			t.Fatalf("list must show %q: %q", want, list)
		}
	}

	// query by tag
	q, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "query", "tag": "ops"})
	if !strings.Contains(q, "standup") || strings.Contains(q, "tea2") {
		t.Fatalf("tag query must filter: %q", q)
	}
	// query by text
	q2, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
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
	list2, _ := e.ExecuteAction(ctxBG(), "verb", "timer", map[string]interface{}{
		"action": "query", "status": "fired"})
	if !strings.Contains(list2, "tea2") {
		t.Fatalf("fired alarms must be searchable: %q", list2)
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

// THE WAKE SEAM (the promise's final half): after the durable floor
// lands, the owner calls OnWake with the alarm's identity — the resident
// wakes, not just the note.
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

// SAFE-mode timers: SET still works (a promise can be made in distress —
// the alarm row is store-side, not ledger), the wake is honest (finding
// 45's shape), and note/commit refuse. The verb itself must not refuse —
// refusing to set an alarm in SAFE would be the substrate managing the
// resident's promises for it.
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

// Aeon incident 2026-08-17: the same firing dispatched twice (queue dedup
// dies with the work item's terminal state while the alarm row outlives
// it) — the identity woke twice and spoke to itself in duplicate turns.
// One firing = one floor + one wake, ever.
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

	// A NEW firing (deadline moved — repeating timer re-armed) delivers again.
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
