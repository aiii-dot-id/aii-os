package store

import "testing"

// One table: what arrived. The address book left for the operator's
// config, where a choice they make belongs — a store table for it had
// six methods and no writer, so every send refused.

// A blocking read replays what it has not seen acknowledged. The primary
// key enforces exactly-once, not the adapter's good behaviour.
func TestARecordedArrivalIsRecordedOnce(t *testing.T) {
	s := testStore(t)
	fresh, err := s.RecordInbound("in_telegram_42", "telegram", "@james", "you up?")
	if err != nil || !fresh {
		t.Fatalf("the first arrival was not new: %v %v", fresh, err)
	}
	fresh, err = s.RecordInbound("in_telegram_42", "telegram", "@james", "you up?")
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("a replayed update reported itself as a new message")
	}
	if all, _ := s.InboundSince(0); len(all) != 1 {
		t.Fatalf("a replay became %d rows", len(all))
	}
}

// "What arrived since I last spoke" is a question, not bookkeeping. A
// seen flag was the first version and nothing maintained it for the
// unattended case, so those arrivals were read by nobody, forever.
func TestArrivalsAreAnsweredByTimeNotByAFlag(t *testing.T) {
	s := testStore(t)
	if _, err := s.RecordInbound("in_old", "telegram", "@james", "before"); err != nil {
		t.Fatal(err)
	}
	older, err := s.InboundSince(0)
	if err != nil || len(older) != 1 {
		t.Fatalf("the first arrival is not visible: %+v %v", older, err)
	}
	cut := older[0].ReceivedMs

	if _, err := s.RecordInbound("in_new", "telegram", "@james", "after"); err != nil {
		t.Fatal(err)
	}
	// Two adjacent writes sometimes share a millisecond (observed a few
	// times per thousand runs; the rate swings with load), and then the row
	// meant to be after the cut sits ON it and the window comes back empty.
	// So the window is STATED, the way the alarm window states it
	// (internal/identity/timer_test.go): milliseconds cannot totally order
	// two writes, and widening the filter to >= would only trade a dropped
	// arrival for a repeated one — that is an ordering-domain change
	// needing a ruling, not a patch here. The bump must MOVE a row: Exec
	// reports no error when it matches nothing, so an id that drifted from
	// the RecordInbound above would silently restore the flake.
	res, err := s.db.Exec(`UPDATE inbound SET received_ms = received_ms + 1 WHERE id = ?`, "in_new")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("the bump moved %d rows, not the one it names — this test would be racing the clock again", n)
	}
	since, err := s.InboundSince(cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].ID != "in_new" {
		t.Fatalf("asking what came since the cut returned %+v", since)
	}
	// And nothing had to be marked for that to be true.
	if all, _ := s.InboundSince(0); len(all) != 2 {
		t.Fatalf("the record itself lost a message: %+v", all)
	}
}

func TestAnArrivalNeedsAnIdAChannelAndASender(t *testing.T) {
	s := testStore(t)
	for _, args := range [][3]string{{"", "telegram", "@x"}, {"id", "", "@x"}, {"id", "telegram", ""}} {
		if _, err := s.RecordInbound(args[0], args[1], args[2], "body"); err == nil {
			t.Fatalf("an unidentifiable arrival was accepted: %v", args)
		}
	}
}

// THE BOUNDARY IS A KNOWN, OWNED LOSS — pinned so it is a decision and
// not a surprise. InboundSince filters `received_ms > ?`, so an arrival
// landing in the SAME millisecond as the caller's cut is invisible to
// that window, and to every later one: the next cut is at least as high.
// The flake this file's first test used to suffer WAS this defect,
// sampled at random by two adjacent writes; making that test
// deterministic removed the tree's only exposure to it, so the boundary
// is asserted here directly instead.
//
// Widening to >= trades a dropped arrival for a repeated one, which is
// the same class of wrong. The honest fix is an ordering domain the
// inbound and outbox windows would share — a ruling, not a patch — and
// until it lands this test says exactly what the current contract costs.
func TestAnArrivalOnTheCutIsNotReturned(t *testing.T) {
	s := testStore(t)
	if _, err := s.RecordInbound("in_edge", "telegram", "@james", "on the boundary"); err != nil {
		t.Fatal(err)
	}
	all, err := s.InboundSince(0)
	if err != nil || len(all) != 1 {
		t.Fatalf("the arrival was not recorded: %+v %v", all, err)
	}
	onIt, err := s.InboundSince(all[0].ReceivedMs)
	if err != nil {
		t.Fatal(err)
	}
	if len(onIt) != 0 {
		t.Fatalf("the > boundary changed: asking from an arrival's own millisecond returned %+v. "+
			"If this is now >=, the repeated-delivery half of the trade needs its own test and a ruling", onIt)
	}
}
