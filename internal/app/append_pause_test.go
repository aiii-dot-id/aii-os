package app

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// landing is one append through the door: when it was asked, when it
// returned, and what it returned.
type landing struct {
	evt           *ledger.Event
	err           error
	asked, landed time.Time
}

func appendThroughDoor(a *App, id string) landing {
	l := landing{asked: time.Now()}
	l.evt, l.err = a.door.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id": id, "content": "observed " + id, "category": "observation", "provenance": "self",
	}, "")
	l.landed = time.Now()
	return l
}

func (l landing) waited() time.Duration { return l.landed.Sub(l.asked) }

// widenPauseBound separates deliberate pause delays from setup and scheduling
// overhead so the test measures whether the operation actually waited.
func widenPauseBound(t *testing.T, d time.Duration) {
	t.Helper()
	was := appendPauseBound
	appendPauseBound = d
	t.Cleanup(func() { appendPauseBound = was })
}

func receive(t *testing.T, ch <-chan landing, within time.Duration) landing {
	t.Helper()
	select {
	case l := <-ch:
		return l
	case <-time.After(within):
		t.Fatalf("the append did not return within %v", within)
		return landing{}
	}
}

// An append arriving while the witness answers waits, and lands after the
// head, which is on time and seals.
func TestAnAppendDuringTheBookmarkWaitsAndLandsAfterTheHeadOnTime(t *testing.T) {
	widenPauseBound(t, 30*time.Second)
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	landed := make(chan landing, 1)
	stillWaiting := make(chan bool, 1)
	f.witness.Slowly(func() {
		go func() { landed <- appendThroughDoor(app, "exp_during_the_bookmark") }()
		time.Sleep(300 * time.Millisecond) // the append has had every chance to land
		stillWaiting <- len(landed) == 0
	})
	bookmarked := app.ledger.LastSeq()
	began := time.Now()
	err := app.anchorer.CheckAndAnchor()
	pass := time.Since(began)
	f.witness.Slowly(nil)
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	l := receive(t, landed, 10*time.Second)
	if l.err != nil {
		t.Fatal(l.err)
	}
	if !<-stillWaiting {
		t.Fatal("THE APPEND LANDED WHILE THE WITNESS WAS ANSWERING")
	}
	head := bookmarked + 1
	if sealed := app.ledger.SealedSeq(); sealed != head || l.evt.Seq != head+1 {
		t.Fatalf("the head should be on time at %d and sealed, the append after it: sealed through %d, the append at %d", head, sealed, l.evt.Seq)
	}
	// The head passed its own pause: the pass did not wait out the bound.
	if pass >= appendPauseBound/2 {
		t.Fatalf("the anchor pass took %v, the bound is %v: the head waited on its own pause", pass, appendPauseBound)
	}
}

// A witness slower than the bound: the append goes through at the bound,
// and the head lands after it, late, sealing nothing.
func TestAWitnessSlowerThanTheBoundLetsTheAppendThroughAndTheHeadLandsLate(t *testing.T) {
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	through := make(chan landing, 1)
	f.witness.Slowly(func() {
		landed := make(chan landing, 1)
		go func() { landed <- appendThroughDoor(app, "exp_past_the_bound") }()
		// The witness answers only once the append is through.
		select {
		case l := <-landed:
			through <- l
		case <-time.After(appendPauseBound + 10*time.Second):
		}
	})
	bookmarked, sealedBefore := app.ledger.LastSeq(), app.ledger.SealedSeq()
	err := app.anchorer.CheckAndAnchor()
	f.witness.Slowly(nil)
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	l := receive(t, through, time.Second)
	if l.err != nil {
		t.Fatal(l.err)
	}
	if l.waited() < appendPauseBound/2 || l.waited() > 2*appendPauseBound {
		t.Fatalf("the append waited %v; the bound is %v", l.waited(), appendPauseBound)
	}
	if l.evt.Seq != bookmarked+1 || app.ledger.LastSeq() != bookmarked+2 || app.ledger.SealedSeq() != sealedBefore {
		t.Fatalf("want the append at %d and the late head after it, nothing sealed: append %d, last %d, sealed %d (before %d)",
			bookmarked+1, l.evt.Seq, app.ledger.LastSeq(), app.ledger.SealedSeq(), sealedBefore)
	}
}

func TestAnAppendNeverWaitsLongerThanTheBound(t *testing.T) {
	widenPauseBound(t, 8*time.Second)
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	release := app.door.PauseAppends()
	defer release()
	under := make(chan landing, 1)
	go func() { under <- appendThroughDoor(app, "exp_under_an_unreleased_pause") }()
	l := receive(t, under, 3*appendPauseBound)
	if l.err != nil {
		t.Fatal(l.err)
	}
	if l.waited() < appendPauseBound-200*time.Millisecond || l.waited() > 2*appendPauseBound {
		t.Fatalf("an append under a pause nobody released waited %v; the bound is %v", l.waited(), appendPauseBound)
	}
	// The bound passed: the pause holds nothing more, released or not.
	// The door says so itself, and the next append is not held for
	// another bound. Its wait is an append's own cost, which under -race
	// on a loaded host has measured 647 ms, so the time check asks only
	// that no second pause was served (a re-armed pause costs a whole
	// bound); the state check carries the claim.
	app.door.mu.Lock()
	held := app.door.pauseEnd != nil && time.Now().Before(app.door.pauseUntil)
	app.door.mu.Unlock()
	if held {
		t.Fatal("the pause is still in force after its bound")
	}
	if again := appendThroughDoor(app, "exp_after_the_bound"); again.err != nil || again.waited() > appendPauseBound*3/4 {
		t.Fatalf("an append after the bound waited %v (%v)", again.waited(), again.err)
	}
}

func TestShutdownDuringAPauseReturnsWithinTheBound(t *testing.T) {
	widenPauseBound(t, 5*time.Second)
	// The baseline: an identical app, no pause, shut down on this host now.
	baseline := func() time.Duration {
		app := New(newSealedFixture(t).cfg)
		if err := startLiveForTest(app); err != nil {
			t.Fatal(err)
		}
		began := time.Now()
		stopApp(t, app)
		return time.Since(began)
	}()
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	asked, answer := make(chan struct{}), make(chan struct{})
	f.witness.Slowly(func() {
		close(asked)
		select {
		case <-answer:
		case <-time.After(2 * teardownBound): // silent for as long as Shutdown is waited for
		}
	})
	anchored := make(chan error, 1)
	go func() { anchored <- app.anchorer.CheckAndAnchor() }()
	defer func() {
		close(answer)
		<-anchored // the pass ends once the witness answers; its mint finds the record closed
	}()
	<-asked
	waiting := make(chan landing, 1)
	if !app.runBackground(func() { waiting <- appendThroughDoor(app, "exp_while_shutting_down") }) {
		t.Fatal("fixture: background work refused")
	}
	time.Sleep(100 * time.Millisecond)

	began := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), teardownBound)
	defer cancel()
	err := app.Shutdown(ctx)
	took := time.Since(began)
	limit := 2*appendPauseBound + max(3*time.Second, 3*baseline)
	t.Logf("shutdown took %v with an append waiting on a pause (bound %v); without one it took %v, so the limit is %v", took, appendPauseBound, baseline, limit)
	if err != nil {
		t.Fatalf("SHUTDOWN DID NOT RETURN while the witness was silent: after %v: %v", took, err)
	}
	if took > limit {
		t.Fatalf("SHUTDOWN WAITED %v BEHIND A PAUSE BOUNDED AT %v: the same shutdown without a pause took %v", took, appendPauseBound, baseline)
	}
	select {
	case err := <-anchored:
		anchored <- err // for the deferred join
		t.Fatalf("SHUTDOWN WAITED ON THE WITNESS: the anchor pass had ended (%v) before Shutdown returned, after %v", err, took)
	default:
	}
	if l := receive(t, waiting, time.Second); l.waited() > 2*appendPauseBound {
		t.Fatalf("the waiting append waited %v; the bound is %v", l.waited(), appendPauseBound)
	}
}

func TestSafeEntryDuringAPauseDoesNotWaitOnIt(t *testing.T) {
	widenPauseBound(t, 8*time.Second)
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	release := app.door.PauseAppends()
	defer release()
	waiting := make(chan landing, 1)
	go func() { waiting <- appendThroughDoor(app, "exp_when_safe_begins") }()
	time.Sleep(100 * time.Millisecond)
	began := time.Now()
	app.enterSafe("test: the record is frozen during a pause")
	if took := time.Since(began); took > appendPauseBound/2 {
		t.Fatalf("SAFE ENTRY WAITED %v BEHIND A PAUSE", took)
	}
	l := receive(t, waiting, 2*appendPauseBound)
	if l.err == nil || l.waited() > 2*appendPauseBound {
		t.Fatalf("the append waiting when SAFE began should be refused once the pause ends: waited %v, err %v", l.waited(), l.err)
	}
}
