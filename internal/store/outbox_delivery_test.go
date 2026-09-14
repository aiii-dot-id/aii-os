package store

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
func TestOutboxDeliveryRecord(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"msg_a", "msg_b", "msg_c"} {
		if err := s.AddOutboxMessage(id, "peer", "james", "hello from "+id, nil); err != nil {
			t.Fatal(err)
		}
	}
	// .
	if err := s.BumpOutboxCreatedMs("msg_a", 5000); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingPeerDeliveries()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 || pending[0].ID != "msg_b" || pending[2].ID != "msg_a" {
		t.Fatalf("pending peer mail leaves oldest first by created_ms: %+v", ids(pending))
	}

	// .
	n, parked, err := s.RecordDeliveryAttempt("msg_b", "smtp: refused", false, 2)
	if err != nil || n != 1 || parked {
		t.Fatalf("first refusal: attempts=%d parked=%v err=%v", n, parked, err)
	}
	n, parked, err = s.RecordDeliveryAttempt("msg_b", "smtp: refused again", false, 2)
	if err != nil || n != 2 || !parked {
		t.Fatalf("second refusal parks at the ceiling: attempts=%d parked=%v err=%v", n, parked, err)
	}
	// .
	if n, parked, err = s.RecordDeliveryAttempt("msg_c", "address invalid", true, 8); err != nil || n != 1 || !parked {
		t.Fatalf("a permanent refusal parks on the first attempt: attempts=%d parked=%v err=%v", n, parked, err)
	}
	// .
	if err := s.MarkEffectUnknown("msg_a", "org.example.telegram", "response lost"); err != nil {
		t.Fatal(err)
	}
	if pending, _ = s.PendingPeerDeliveries(); len(pending) != 0 {
		t.Fatalf("parked rows are asked no more: %+v", ids(pending))
	}
	// .
	all, _ := s.UndeliveredMessages()
	if len(all) != 3 || all[0].ID != "msg_b" {
		t.Fatalf("undelivered keeps parked rows and time order: %+v", ids(all))
	}
	// .
	out, err := s.OutboxOutcomesSince(0)
	if err != nil || len(out) != 3 {
		t.Fatalf("three attempted rows: %d %v", len(out), err)
	}
	byID := map[string]OutboxMessage{}
	for _, m := range out {
		byID[m.ID] = m
	}
	if b := byID["msg_b"]; !b.Parked || b.Attempts != 2 || b.LastError != "smtp: refused again" || b.Effect != "" {
		t.Fatalf("msg_b: %+v", b)
	}
	if a := byID["msg_a"]; !a.Parked || a.Effect != "unknown" || a.DeliveredVia != "org.example.telegram" || a.Delivered != 0 {
		t.Fatalf("msg_a: %+v", a)
	}
	// .
	if err := s.AddOutboxMessage("msg_d", "peer", "james", "one more", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDelivered("msg_d", "org.example.telegram"); err != nil {
		t.Fatal(err)
	}
	out, _ = s.OutboxOutcomesSince(0)
	var d OutboxMessage
	for _, m := range out {
		if m.ID == "msg_d" {
			d = m
		}
	}
	if d.Effect != "performed" || d.Attempts != 1 || d.Delivered != 1 || d.DeliveredVia != "org.example.telegram" {
		t.Fatalf("msg_d: %+v", d)
	}
	// .
	last := d.LastAttemptMs
	if out, _ = s.OutboxOutcomesSince(last); len(out) != 0 {
		t.Fatalf("outcomes are reported once, on the clock: %v", ids(out))
	}
}

func ids(ms []OutboxMessage) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}
