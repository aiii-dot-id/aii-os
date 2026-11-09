package app

import (
	"context"
	"testing"
	"time"
)

func TestSpokenWordsWaitForRoomInAFullQueue(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "full-queue", true)
	identifyTheOperator(t, a, h.id, 3)
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to queue behind")
	}
	a.steerFlush = func([]steerEntry) {} // what the turn leaves at its end is not under test
	defer a.EndTurn()
	for i := 0; i < maxPendingSteers; i++ {
		if _, err := a.Steer("typed words"); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- a.observeOperatorVoice(context.Background(), "and one more thing", heardUtterance{SessionID: h.id, Sequence: 3, Text: "and one more thing", Answer: true, Operator: true})
	}()
	select {
	case err := <-done:
		t.Fatalf("the words did not wait for room: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if said := steeredWords(a.DrainSteering()); len(said) != maxPendingSteers {
		t.Fatalf("the tool boundary took %d of the queued messages, want %d", len(said), maxPendingSteers)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the words were not placed once there was room: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the words still wait after the queue was emptied")
	}
	a.turnMu.Lock()
	queued := append([]steerEntry(nil), a.steers...)
	a.turnMu.Unlock()
	if len(queued) != 1 || queued[0].content != voiceMarker+"and one more thing" || queued[0].role != roleOperator {
		t.Fatalf("the spoken words are not in the queue as the operator's: %+v", queued)
	}
}
