package store

import "testing"

// .
// .
// .

// .
// .
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

// .
// .
// .
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
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
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
	// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
