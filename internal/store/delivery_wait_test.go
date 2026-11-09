package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A DELIVERY THAT CAN NEVER COME IS REFUSED, AND ONE THAT CAN IS KEPT
// (a review, 2026-11-08, D6): the condition named a session that did not
// exist, the session itself, or a delivery already taken in, and the work
// waited for nothing.
func TestADeliveryThatCanNeverComeIsRefusedAndOneThatCanIsKept(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if err := s.StartWorkSession(ctx, "ws_main", "the main work"); err != nil {
		t.Fatal(err)
	}
	spawn := func(id string) {
		t.Helper()
		if _, kept, err := s.EnqueueWorkWithSessionBelowLimit(&WorkItem{Kind: SubagentWorkKind, Payload: "{}", DedupKey: id, Source: "identity"}, 8, id, SubagentDescription("a goal of "+id)); err != nil || !kept {
			t.Fatalf("spawn %s: %v %v", id, kept, err)
		}
	}
	spawn("ws_working")
	spawn("ws_delivered")
	spawn("ws_taken")
	spawn("ws_lost")
	for _, id := range []string{"ws_delivered", "ws_taken"} {
		if err := s.DeliverWorkSession(ctx, id, "done", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.TakeInDelivery(context.Background(), "ws_taken", 1_762_600_000_000, "taken in"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE work_sessions SET status = 'abandoned' WHERE id = 'ws_lost'`); err != nil {
		t.Fatal(err)
	}
	if err := s.StartWorkSession(ctx, "ws_other", "not a sub-agent's"); err != nil {
		t.Fatal(err)
	}

	for on, why := range map[string]string{
		"delivery:ws_nowhere": "no work session ws_nowhere exists",
		"delivery:ws_main":    "is this work session",
		"delivery:ws_other":   "not a sub-agent's session",
		"delivery:ws_taken":   "already taken in",
		"delivery:ws_lost":    "ended without delivering (abandoned)",
	} {
		var never *ErrDeliveryNeverComes
		if err := s.DeliveryNeverComes(ctx, "ws_main", on); !errors.As(err, &never) || !strings.Contains(never.Why, why) {
			t.Errorf("%s: %v; want it refused, saying %q", on, err, why)
		}
		// Kept by no other door either: the store asks again as it keeps it.
		v := on
		if err := s.SetWorkPromise(ctx, "ws_main", nil, &v, ""); !errors.As(err, &never) {
			t.Errorf("%s was kept: %v", on, err)
		}
	}
	for _, on := range []string{"delivery:ws_working", "delivery:ws_delivered", "word", "now", ""} {
		if err := s.DeliveryNeverComes(ctx, "ws_main", on); err != nil {
			t.Errorf("%q was refused: %v", on, err)
		}
		v := on
		if err := s.SetWorkPromise(ctx, "ws_main", nil, &v, ""); err != nil {
			t.Errorf("%q could not be kept: %v", on, err)
		}
	}
}

// A DELIVERY IS TAKEN IN WITH EVERY WAIT ON IT, OR NOT AT ALL (a review,
// 2026-11-08): the mark and the waits were written apart, so a failed wait
// write left the delivery marked taken in with a wait standing on it that
// no later wake would settle. Two sessions wait; one's write is made to
// fail. Nothing is applied: the delivery is still to be taken in and both
// waits stand. With the fault gone, the next take-in settles both, with no
// model call and no restart.
func TestADeliveryIsTakenInWithEveryWaitOnItOrNotAtAll(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if _, kept, err := s.EnqueueWorkWithSessionBelowLimit(&WorkItem{Kind: SubagentWorkKind, Payload: "{}", DedupKey: "ws_b", Source: "identity"}, 8, "ws_b", SubagentDescription("find it")); err != nil || !kept {
		t.Fatalf("spawn: %v %v", kept, err)
	}
	for _, ws := range []string{"ws_one", "ws_two"} {
		if err := s.StartWorkSession(ctx, ws, "waits on b"); err != nil {
			t.Fatal(err)
		}
		on := "delivery:ws_b"
		if err := s.SetWorkPromise(ctx, ws, nil, &on, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeliverWorkSession(ctx, "ws_b", "done", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_two BEFORE UPDATE OF resumes_on ON work_sessions WHEN OLD.id = 'ws_two' BEGIN SELECT RAISE(ABORT, 'injected fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TakeInDelivery(ctx, "ws_b", 1_762_600_000_000, "taken in"); err == nil || !strings.Contains(err.Error(), "injected fault") {
		t.Fatalf("the fault was not said: %v", err)
	}
	unharvested, err := s.UnharvestedDeliveries(8)
	if err != nil || len(unharvested) != 1 {
		t.Fatalf("a failed take-in left the delivery marked taken in: %d (%v)", len(unharvested), err)
	}
	for _, ws := range []string{"ws_one", "ws_two"} {
		if w, _ := s.WorkSessionByID(ws); w.ResumesOn != "delivery:ws_b" {
			t.Fatalf("%s: a failed take-in changed its wait: %q", ws, w.ResumesOn)
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_two`); err != nil {
		t.Fatal(err)
	}
	met, err := s.TakeInDelivery(ctx, "ws_b", 1_762_600_000_001, "taken in")
	if err != nil || len(met) != 2 {
		t.Fatalf("with the fault gone, the take-in met %v (%v); want both waits", met, err)
	}
	for _, ws := range []string{"ws_one", "ws_two"} {
		if w, _ := s.WorkSessionByID(ws); w.ResumesOn != "" {
			t.Fatalf("%s still waits: %q", ws, w.ResumesOn)
		}
	}
}
