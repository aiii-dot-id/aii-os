package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func heldMessageFixture(t *testing.T) (*Store, HeldMessage) {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.AddOutboxMessage("held", "peer", "old-person", "original body", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE outbox SET recipient_error='unresolved',requested_channel='old-channel' WHERE id='held'"); err != nil {
		t.Fatal(err)
	}
	view, err := s.HeldMessages(t.Context(), "")
	if err != nil || len(view) != 1 {
		t.Fatalf("fixture: %v %v", view, err)
	}
	return s, view[0]
}
func TestHeldMessageRepairIsOneCheckedCorrection(t *testing.T) {
	s, m := heldMessageFixture(t)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- s.RepairHeldMessage(context.Background(), m.ID, m.SHA256, "quinn", "signal")
		}()
	}
	group.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else {
			var refusal *OutboxRepairError
			if !errors.As(err, &refusal) {
				t.Fatal(err)
			}
		}
	}
	if succeeded != 1 {
		t.Fatalf("correction accepted %d times", succeeded)
	}
	mail, err := s.PendingPeerDeliveries()
	if err != nil || len(mail) != 1 {
		t.Fatalf("queue: %v %v", mail, err)
	}
	got := mail[0]
	if got.ToIdentity != "quinn" || got.RequestedChannel != "signal" || got.RecipientError != "" || got.Content != "original body" || got.Attempts != 0 || got.Delivered != 0 {
		t.Fatalf("correction changed more than addressing: %+v", got)
	}
}
func TestHeldMessageRepairRefusesUnsafeOrStaleRows(t *testing.T) {
	for _, change := range []string{"content='changed'", "delivered=1", "attempts=1", "effect='unknown'", "parked=1", "to_role='operator'", "recipient_error=''"} {
		t.Run(change, func(t *testing.T) {
			s, m := heldMessageFixture(t)
			if _, err := s.db.Exec("UPDATE outbox SET " + change + " WHERE id='held'"); err != nil {
				t.Fatal(err)
			}
			if change != "content='changed'" {
				// Exercise each eligibility rule with a current fingerprint,
				// not a stale token that would mask a missing state check.
				rows, err := s.db.Query("SELECT " + outboxColumns + " FROM outbox WHERE id='held'")
				if err != nil {
					t.Fatal(err)
				}
				updated, err := scanOutbox(rows)
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				m.SHA256 = outboxRepairDigest(updated[0])
			}
			if err := s.RepairHeldMessage(t.Context(), m.ID, m.SHA256, "quinn", "signal"); err == nil {
				t.Fatal("unsafe or stale correction admitted")
			}
			var recipient string
			if err := s.db.QueryRow("SELECT to_identity FROM outbox WHERE id='held'").Scan(&recipient); err != nil || recipient != "old-person" {
				t.Fatalf("refusal mutated recipient: %q %v", recipient, err)
			}
		})
	}
	s, m := heldMessageFixture(t)
	s.SetFrozen(true, "integrity test")
	var frozen *FrozenError
	if err := s.RepairHeldMessage(t.Context(), m.ID, m.SHA256, "quinn", "signal"); !errors.As(err, &frozen) {
		t.Fatalf("SAFE correction: %v", err)
	}
}
func TestHeldMessageInspectionDoesNotAcknowledgeAndShowsFullText(t *testing.T) {
	s, _ := heldMessageFixture(t)
	body := strings.Repeat("λ", 3000)
	if _, err := s.db.Exec("UPDATE outbox SET content=? WHERE id='held'", body); err != nil {
		t.Fatal(err)
	}
	page, err := s.HeldMessages(t.Context(), "")
	if err != nil || len(page) != 1 || !page[0].Truncated || len([]rune(page[0].Preview)) != 2000 {
		t.Fatalf("preview: %v %v", page, err)
	}
	full, err := s.InspectHeldMessage(t.Context(), "held")
	if err != nil || full.Preview != body || full.Truncated || full.SHA256 != page[0].SHA256 {
		t.Fatalf("full inspection: %v %v", full, err)
	}
	var delivered int
	if err := s.db.QueryRow("SELECT delivered FROM outbox WHERE id='held'").Scan(&delivered); err != nil || delivered != 0 {
		t.Fatal("inspection acknowledged mail")
	}
	a, b := OutboxMessage{Content: string([]byte{255})}, OutboxMessage{Content: string([]byte{254})}
	if outboxRepairDigest(a) == outboxRepairDigest(b) {
		t.Fatal("different raw message bytes share a stale-view token")
	}
}
