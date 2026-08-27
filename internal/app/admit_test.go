package app

import (
	"sync"
	"testing"
)

// Deciding "steer or start a turn" and CLAIMING the turn have to be one
// step. Asking Steer and then starting one leaves a window: the
// dashboard read loop returns to ReadMessage the instant it launches the
// turn goroutine, so the next message asks a gate the first turn has not
// taken yet, is told "no turn", and becomes a SECOND TURN queued behind
// the first — the opposite of what steering is for.

func TestASecondMessageSteersInsteadOfRacingForTheTurn(t *testing.T) {
	a := newSteerApp(t)

	// First message: no turn running, so the gate is now HELD by us —
	// exactly as the dashboard's turn goroutine would hold it.
	steered, err := a.AdmitOperator("read the ledger")
	if err != nil {
		t.Fatal(err)
	}
	if steered {
		t.Fatal("the first message steered into a turn that does not exist")
	}
	if !a.TurnActive() {
		t.Fatal("admission returned 'start a turn' without taking the gate — " +
			"this is the window: the next message will be told 'no turn' too")
	}

	// Second message, arriving before the turn body has done anything.
	steered2, err := a.AdmitOperator("actually, check the outbox first")
	if err != nil {
		t.Fatal(err)
	}
	if !steered2 {
		t.Fatal("the second message opened its own turn instead of joining the first")
	}
	pending := a.PendingSteers()
	if len(pending) != 1 || pending[0] != "actually, check the outbox first" {
		t.Fatalf("the second message did not reach the running turn: %v", pending)
	}
	a.releaseTurn()
}

// Many messages at once: exactly one may take the gate, the rest steer.
// Whichever wins is fine; two winners is not.
func TestExactlyOneConcurrentMessageTakesTheTurn(t *testing.T) {
	a := newSteerApp(t)

	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	starts := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			steered, err := a.AdmitOperator("say something")
			if err != nil {
				return // queue full is a legitimate refusal
			}
			if !steered {
				mu.Lock()
				starts++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if starts != 1 {
		t.Fatalf("%d messages were each told to start a turn — one identity, one voice", starts)
	}
	a.releaseTurn()
}

// Releasing must hand the gate back, or the identity is deaf after one
// turn.
func TestTheGateIsClaimableAgainAfterTheTurnEnds(t *testing.T) {
	a := newSteerApp(t)
	if steered, _ := a.AdmitOperator("first"); steered {
		t.Fatal("steered with no turn running")
	}
	a.releaseTurn()
	if a.TurnActive() {
		t.Fatal("the gate was not returned")
	}
	if steered, _ := a.AdmitOperator("second"); steered {
		t.Fatal("the next message steered into a turn that had already ended")
	}
	a.releaseTurn()
}
