package store

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// THE SESSION A TURN DELIVERED IS THE ONE ITS DELIVERY NOTICE NAMES. A
// lesson stored after `work deliver` is linked to the session the same
// turn delivered. That is read from the
// delivery notice the turn wrote, not from the calls that named the
// session: a session a turn worked on and a later turn delivered was the
// later turn's. Two deliveries in one turn give
// the later one; a turn that delivered nothing gives none.
func TestTheSessionATurnDeliveredIsTheOneItsDeliveryNoticeNames(t *testing.T) {
	s := testStore(t)
	at := func(turn string) context.Context { return interaction.WithTurn(context.Background(), turn) }
	if err := s.StartWorkSession(at("turn_work"), "ws_done", "deploy the preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolStart("turn_work", 1, "main", "m", "", "work", "{}"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionDeliveredInTurn("turn_work"); err != nil || got != "" {
		t.Fatalf("an active session is not a delivered one: %q, %v", got, err)
	}
	if err := s.DeliverWorkSession(at("turn_deliver"), "ws_done", "served: preview up", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionDeliveredInTurn("turn_work"); err != nil || got != "" {
		t.Fatalf("the turn that worked on it did not deliver it: %q, %v", got, err)
	}
	if got, err := s.SessionDeliveredInTurn("turn_deliver"); err != nil || got != "ws_done" {
		t.Fatalf("the turn that delivered it: %q, %v", got, err)
	}
	for _, id := range []string{"ws_first", "ws_second"} {
		if err := s.StartWorkSession(at("turn_two"), id, "one of two"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeliverWorkSession(at("turn_two"), id, "served: done", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.SessionDeliveredInTurn("turn_two"); err != nil || got != "ws_second" {
		t.Fatalf("two deliveries in one turn give the later: %q, %v", got, err)
	}
	if got, err := s.SessionDeliveredInTurn("turn_other"); err != nil || got != "" {
		t.Fatalf("another turn delivered nothing: %q, %v", got, err)
	}
}
