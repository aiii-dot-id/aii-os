package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// pageStore is a store with arrivals written at known instants.
func pageStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func arriveAt(t *testing.T, s *Store, id, address, body string, ms int64) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO inbound (id, channel, address, body, received_ms) VALUES (?, 'mail', ?, ?, ?)`, id, address, body, ms); err != nil {
		t.Fatal(err)
	}
}

// recordInput records one input under a turn, and names the arrival it
// carried the way the app's recorders do.
func recordInput(t *testing.T, s *Store, turn string, role interaction.Role, kind interaction.Kind, arrival string) {
	t.Helper()
	ref, err := s.RecordInteraction(context.Background(), InteractionInput{ID: "in_" + turn + "_" + string(role) + "_" + arrival, Kind: kind, Role: role, Content: "words", TurnID: turn})
	if err != nil {
		t.Fatal(err)
	}
	if arrival != "" {
		if err := s.AnnotateTurn(ref.Sequence, "turn_event.arrival", arrival, `{}`); err != nil {
			t.Fatal(err)
		}
	}
}

// WHETHER AN ARRIVAL WOKE THE IDENTITY IS READ FROM THE RECORD. A turn
// began with it when the input naming it is the first recorded under its
// turn; a steered arrival joins a turn someone else began, and one
// carried in a later turn's events was read without waking anyone.
func TestRecentArrivalsSayWhetherATurnBeganWithThem(t *testing.T) {
	s := pageStore(t)
	arriveAt(t, s, "woke", "robin@example.test", "wake up", 1000)
	arriveAt(t, s, "steered", "robin@example.test", "and another thing", 2000)
	arriveAt(t, s, "later", "contact-b@example.test", "no hurry", 3000)
	arriveAt(t, s, "unread", "stranger@example.test", strings.Repeat("é", PreviewChars+5), 4000)
	arriveAt(t, s, "historical", "contact-b@example.test", "read before", 500)
	if _, err := s.DB().Exec(`UPDATE inbound SET previously_seen = 1 WHERE id = 'historical'`); err != nil {
		t.Fatal(err)
	}
	recordInput(t, s, "turn_woke", interaction.Participant, interaction.Message, "woke")
	recordInput(t, s, "turn_woke", interaction.Resident, interaction.Message, "")
	recordInput(t, s, "turn_op", interaction.Operator, interaction.Message, "")
	recordInput(t, s, "turn_op", interaction.Participant, interaction.Message, "steered")
	recordInput(t, s, "turn_op", interaction.System, interaction.Notice, "later")

	got, total, err := s.RecentArrivals(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(got) != 4 {
		t.Fatalf("total %d, listed %d; want 5 in all and the newest 4", total, len(got))
	}
	want := []struct {
		id         string
		read, woke bool
	}{{"unread", false, false}, {"later", true, false}, {"steered", true, false}, {"woke", true, true}}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Read != w.read || got[i].Woke != w.woke {
			t.Errorf("row %d = %s read=%v woke=%v; want %s read=%v woke=%v", i, got[i].ID, got[i].Read, got[i].Woke, w.id, w.read, w.woke)
		}
	}
	if n := len([]rune(got[0].Body)); n != PreviewChars || got[0].BodyChars != PreviewChars+5 {
		t.Fatalf("a long body came back as %d of %d characters; want the first %d and its whole length", n, got[0].BodyChars, PreviewChars)
	}
	all, _, err := s.RecentArrivals(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if last := all[len(all)-1]; last.ID != "historical" || !last.Read || last.Woke {
		t.Fatalf("an arrival an upgrade marked read: %+v", last)
	}
}

// What a person wrote since a send is counted from an instant inclusive:
// ArrivalsFrom hands over every arrival at or after it, oldest first.
func TestArrivalsFromIsInclusiveAndOldestFirst(t *testing.T) {
	s := pageStore(t)
	arriveAt(t, s, "before", "robin@example.test", "earlier", 1999)
	arriveAt(t, s, "at", "robin@example.test", "at the instant", 2000)
	arriveAt(t, s, "after", "robin@example.test", "a long answer", 2001)
	var ids []string
	if err := s.ArrivalsFrom(context.Background(), 2000, 4, func(m Inbound, chars int) {
		ids = append(ids, m.ID+":"+m.Body)
		if m.ID == "after" && chars != len("a long answer") {
			t.Errorf("whole length %d", chars)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "at:at t,after:a lo" {
		t.Fatalf("arrivals from 2000 = %v", ids)
	}
}

// Every message not delivered is listed, oldest first, beside the newest
// delivered; the totals say what a page leaves out, and a row carries its
// dispatch claim.
func TestPeerSendsListOpenRowsAndTheNewestDelivered(t *testing.T) {
	s := pageStore(t)
	for i, id := range []string{"open1", "open2", "del1", "del2", "del3"} {
		if err := s.AddOutboxMessage(id, "peer", "robin", "body "+id, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`UPDATE outbox SET created_ms = ? WHERE id = ?`, 100*(i+1), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddOutboxMessage("notice", "operator", "", "for the dashboard", nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"del1", "del2", "del3"} {
		if err := s.MarkDispatchDelivered(claimFor(t, s, id), "org.example.mail"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`UPDATE outbox SET last_attempt_ms = CASE id WHEN 'del1' THEN 10 WHEN 'del2' THEN 30 ELSE 20 END WHERE delivered = 1`); err != nil {
		t.Fatal(err)
	}
	p, err := s.PeerSends(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(rows []OutboxMessage) (out []string) {
		for _, m := range rows {
			out = append(out, m.ID)
		}
		return out
	}
	if got := strings.Join(ids(p.Open), ","); got != "open1,open2" || p.OpenTotal != 2 {
		t.Fatalf("open = %s of %d", got, p.OpenTotal)
	}
	if got := strings.Join(ids(p.Delivered), ","); got != "del2,del3" || p.DeliveredTotal != 3 {
		t.Fatalf("delivered = %s of %d; want the newest two of three", got, p.DeliveredTotal)
	}
	if p.Delivered[0].DispatchedMs == 0 {
		t.Fatal("a delivered row lost its dispatch claim")
	}
}
