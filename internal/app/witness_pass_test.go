package app

import (
	"context"
	"testing"
)

// witness_pass_test.go — one tick of the witness cadence (T66): the
// health probe reads the witness's status, and the anchor pass works from
// that reading rather than asking again.

// A pass that anchors asks the witness for its status once: the probe's
// answer is the one the cadence gate reads.
func TestAWitnessPassAsksTheWitnessForItsStatusOnce(t *testing.T) {
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	if reason, safe := app.SafeMode(); safe {
		t.Fatalf("fixture boot is SAFE: %s", reason)
	}
	asked, anchored := f.witness.StatusAsks(), app.anchorer.LastAnchoredSeq()
	app.witnessPass(context.Background(), app.anchorer)
	if app.anchorer.LastAnchoredSeq() <= anchored {
		t.Fatalf("the pass should have anchored: the anchoring point stayed at %d", anchored)
	}
	if got := f.witness.StatusAsks() - asked; got != 1 {
		t.Fatalf("one witness pass asked the witness for its status %d times, want 1", got)
	}
}

// A tick that fires once the lifetime has ended — Shutdown cancels it
// before TIME stops — asks the witness nothing: no probe, no pass.
func TestAWitnessTickAfterTheLifetimeEndedAsksNothing(t *testing.T) {
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer stopApp(t, app)
	asked, anchored := f.witness.StatusAsks(), app.anchorer.LastAnchoredSeq()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app.witnessPass(ctx, app.anchorer)
	if got := f.witness.StatusAsks() - asked; got != 0 {
		t.Fatalf("a tick after the lifetime ended asked the witness for its status %d times", got)
	}
	if app.anchorer.LastAnchoredSeq() != anchored {
		t.Fatal("a tick after the lifetime ended anchored")
	}
}
