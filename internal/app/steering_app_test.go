package app

import (
	"context"
	"strings"
	"testing"
)

// The mid-turn channel, from the operator's side.
//
// The property under test is not "the queue works" — it is that the
// operator is never left believing they were heard when they were not.
// Every refusal here is a refusal the operator can SEE.

func newSteerApp(t *testing.T) *App {
	t.Helper()
	a := New(&Config{SourcePath: t.TempDir() + "/config.json"})
	if a.turnGate == nil {
		t.Fatal("New did not create the turn gate; TurnActive would be meaningless")
	}
	return a
}

func TestTurnActiveDerivesFromTheGate(t *testing.T) {
	a := newSteerApp(t)
	if a.TurnActive() {
		t.Fatal("a fresh app reports a turn in flight")
	}
	if err := a.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !a.TurnActive() {
		t.Fatal("a turn holds the gate but TurnActive says otherwise — " +
			"this is the disagreement that showed 'present' over an identity that could not hear")
	}
	a.releaseTurn()
	if a.TurnActive() {
		t.Fatal("the turn released the gate but TurnActive still reports one")
	}
}

// With no turn to steer, Steer says so STRUCTURALLY: not delivered, not
// an error. The caller (the dashboard read loop) opens an ordinary turn
// on that answer, and it must not have to read a sentence to decide
// (AGENTS.md 5.2 — control flow never depends on error prose).
func TestSteerReportsNoTurnStructurally(t *testing.T) {
	a := newSteerApp(t)
	delivered, err := a.Steer("hello")
	if err != nil {
		t.Fatalf("no turn running is not a failure, got %v", err)
	}
	if delivered {
		t.Fatal("Steer claimed delivery with no turn to deliver into")
	}
	if said := a.DrainSteering(); len(said) != 0 {
		t.Fatalf("the words were queued anyway and would surface inside an unrelated later turn: %v", said)
	}
}

func TestSteerQueuesDuringATurnAndDrainsOnce(t *testing.T) {
	a := newSteerApp(t)
	if err := a.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.releaseTurn()

	if delivered, err := a.Steer("  that file is already fixed  "); err != nil || !delivered {
		t.Fatalf("steer during a turn: delivered=%v err=%v", delivered, err)
	}
	if delivered, err := a.Steer("and the other one too"); err != nil || !delivered {
		t.Fatalf("second steer: delivered=%v err=%v", delivered, err)
	}

	said := a.DrainSteering()
	if len(said) != 2 {
		t.Fatalf("drained %d, want both sentences", len(said))
	}
	if said[0] != "that file is already fixed" {
		t.Fatalf("the operator's words were not preserved verbatim after trim: %q", said[0])
	}
	if again := a.DrainSteering(); len(again) != 0 {
		t.Fatalf("a second drain returned %d — the operator would appear to have said it twice", len(again))
	}
}

// A silent drop is worse than no queue, because it also lies. When the
// channel is full the operator is REFUSED, and told why.
func TestSteerRefusesRatherThanDrops(t *testing.T) {
	a := newSteerApp(t)
	if err := a.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.releaseTurn()

	for i := 0; i < maxPendingSteers; i++ {
		if delivered, err := a.Steer("filler"); err != nil || !delivered {
			t.Fatalf("steer %d of %d was refused early: %v", i, maxPendingSteers, err)
		}
	}
	delivered, err := a.Steer("one too many")
	if err == nil {
		t.Fatal("the channel accepted past its bound — something was dropped in silence")
	}
	if delivered {
		t.Fatal("a refused steer reported itself delivered")
	}
	said := a.DrainSteering()
	if len(said) != maxPendingSteers {
		t.Fatalf("drained %d, want the %d that were accepted", len(said), maxPendingSteers)
	}
	for _, s := range said {
		if s == "one too many" {
			t.Fatal("the refused message was delivered anyway; the refusal was a lie")
		}
	}
}

func TestSteerRefusesAnOversizedMessage(t *testing.T) {
	a := newSteerApp(t)
	if err := a.acquireTurn(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer a.releaseTurn()

	_, err := a.Steer(strings.Repeat("x", maxSteerChars+1))
	if err == nil {
		t.Fatal("an oversized steer was accepted into a prompt the Accordion had already budgeted")
	}
	if !strings.Contains(err.Error(), "short version") {
		t.Fatalf("the refusal must offer the operator a way through, got %q", err)
	}
}

func TestCancelTurnReportsWhetherThereWasOneToStop(t *testing.T) {
	a := newSteerApp(t)
	if a.CancelTurn() {
		t.Fatal("cancelling with no turn running claimed to have stopped one")
	}

	ctx, cancel := a.beginCancellableTurn(context.Background())
	if !a.CancelTurn() {
		t.Fatal("a cancellable turn was running but CancelTurn found nothing")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("CancelTurn reported success but the turn's context is still live")
	}
	cancel() // the turn's own cleanup

	// The hazard this closes: a cancel that outlives its turn would stop
	// the next one, so an operator pressing stop on finished work would
	// kill the reply they are waiting for.
	if a.CancelTurn() {
		t.Fatal("a finished turn's cancel is still reachable — the next turn is stoppable by a stale press")
	}
}
