package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func eventStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestTurnEventsFrozenStoreLeavesDeliveryPending(t *testing.T) {
	s, _ := eventStore(t)
	seedTurnEvents(t, s)
	rows, err := s.PendingTurnEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s.SetFrozen(true, "fixture SAFE")
	var frozen *FrozenError
	if err := s.RecordTurnEvents(t.Context(), withEventText(rows), "t"); !errors.As(err, &frozen) {
		t.Fatalf("SAFE write: %v", err)
	}
	if pending, err := s.PendingTurnEvents(t.Context()); err != nil || len(pending) != 3 {
		t.Fatalf("SAFE changed delivery: %d %v", len(pending), err)
	}
}

func seedTurnEvents(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.RecordInbound("in", "mail", "friend", "message"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOutboxMessage("timer_alarm_1", "operator", "", "alarm", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOutboxMessage("out", "peer", "friend", "sent", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkEffectUnknown(claimFor(t, s, "out"), "mail", "lost reply"); err != nil {
		t.Fatal(err)
	}
}

func withEventText(events []TurnEvent) []TurnEvent {
	for i := range events {
		events[i].Text = "actual framed input for " + events[i].ID()
	}
	return events
}

func TestTurnEventsAcknowledgeExactSourcesAndSurviveRestart(t *testing.T) {
	s, path := eventStore(t)
	seedTurnEvents(t, s)
	events, err := s.PendingTurnEvents(t.Context())
	if err != nil || len(events) != 3 {
		t.Fatalf("pending=%v %v", events, err)
	}
	// A later outcome and same-time arrival are NOT part of the earlier read.
	// The outcome is written as a fixture: no writer retries a row of
	// unknown effect, and the page's mark is the operator's notices' alone
	// (S-3), so it refuses a person's message.
	if _, err := s.DB().Exec(`UPDATE outbox SET delivered = 1, delivered_via = 'mail', effect = 'performed', last_error = '',
		 attempts = attempts + 1, last_attempt_ms = last_attempt_ms + 1 WHERE id = 'out'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordInbound("later", "mail", "friend", "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE inbound SET received_ms=0 WHERE id='later'`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTurnEvents(t.Context(), withEventText(events), "turn-a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pending, err := s.PendingTurnEvents(t.Context())
	if err != nil || len(pending) != 2 {
		t.Fatalf("new facts consumed: %v %v", pending, err)
	}
	if pending[0].ID() != "later" || pending[1].Outcome == nil || pending[1].Outcome.Attempts != 2 {
		t.Fatalf("wrong remaining rows: %+v", pending)
	}
	if err := s.RecordTurnEvents(t.Context(), withEventText(pending), "turn-b"); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.PendingTurnEvents(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("acknowledgments lost: %d %v", len(rows), err)
	}
	var kept int
	if err := s.DB().QueryRow(`SELECT count(*) FROM conversations WHERE json_extract(details,'$.channel')='turn_events'`).Scan(&kept); err != nil || kept != 5 {
		t.Fatalf("input history=%d %v", kept, err)
	}
	rows, err := s.PendingPeerDeliveries()
	if err != nil || len(rows) != 0 {
		t.Fatalf("acknowledgment replayed external delivery: %v %v", rows, err)
	}
}

func TestTurnEventsRecordAndAcknowledgmentAreAtomic(t *testing.T) {
	for _, failure := range []string{"record", "second input"} {
		t.Run(failure, func(t *testing.T) {
			s, _ := eventStore(t)
			seedTurnEvents(t, s)
			events, err := s.PendingTurnEvents(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			stmt := `CREATE TRIGGER fail_event_record BEFORE INSERT ON conversations BEGIN SELECT RAISE(ABORT,'fixture failure'); END`
			if failure == "second input" {
				stmt = `CREATE TRIGGER fail_event_record BEFORE INSERT ON turn_annotations WHEN new.kind='turn_event.timer' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`
			}
			if _, err := s.DB().Exec(stmt); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordTurnEvents(t.Context(), withEventText(events), "t"); err == nil {
				t.Fatal("failed write acknowledged")
			}
			pending, err := s.PendingTurnEvents(t.Context())
			if err != nil || len(pending) != 3 {
				t.Fatalf("partial acknowledgment: %d %v", len(pending), err)
			}
			var n int
			if err := s.DB().QueryRow(`SELECT count(*) FROM conversations WHERE json_extract(details,'$.channel')='turn_events'`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("partial input record: %d %v", n, err)
			}
		})
	}
}

func TestTurnEventsHistoricalSchemaUpgradesWithoutLosingDeliveryState(t *testing.T) {
	s, path := eventStore(t)
	seedTurnEvents(t, s)
	// The existing schema is unchanged; no new column or table is required.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows, err := s.PendingTurnEvents(t.Context())
	if err != nil || len(rows) != 3 {
		t.Fatalf("historical events lost: %d %v", len(rows), err)
	}
	for _, e := range rows {
		if e.Outcome != nil && (e.Outcome.Effect != "unknown" || !e.Outcome.Parked) {
			t.Fatalf("upgrade made uncertain send retryable: %+v", e.Outcome)
		}
	}
}

func TestTurnEventsPreviousDeclarationCanStillOpenPopulatedInputs(t *testing.T) {
	s, _ := eventStore(t)
	seedTurnEvents(t, s)
	rows, err := s.PendingTurnEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTurnEvents(t.Context(), withEventText(rows), "t"); err != nil {
		t.Fatal(err)
	}
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reconcileSchema(string(raw)); err != nil {
		t.Fatalf("previous declaration cannot open populated input metadata: %v", err)
	}
	if _, err := s.reconcileSchema(string(raw)); err != nil {
		t.Fatalf("forward reconciliation failed: %v", err)
	}
	if pending, err := s.PendingTurnEvents(t.Context()); err != nil || len(pending) != 0 {
		t.Fatalf("schema round trip changed delivery: %d %v", len(pending), err)
	}
	// The existing strict interaction decoder can read every new occurrence;
	// unknown details fields would fail here, even with an unchanged schema.
	history, err := s.DB().Query("SELECT " + interactionSelect + " FROM conversations c")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	for history.Next() {
		if _, err := scanInteraction(history); err != nil {
			t.Fatalf("reader cannot decode new input: %v", err)
		}
	}
	if err := history.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestTurnEventsBoundTheBacklogWithoutDiscardingRemainder(t *testing.T) {
	s, _ := eventStore(t)
	for i := 0; i < 150; i++ {
		if _, err := s.RecordInbound(fmt.Sprintf("in-%03d", i), "mail", "friend", "hello"); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for len(seen) < 150 {
		rows, err := s.PendingTurnEvents(t.Context())
		if err != nil || len(rows) > interaction.MaxPageRows+1 || len(rows) == 0 {
			t.Fatalf("unbounded/missing page: %d %v", len(rows), err)
		}
		if len(rows) > interaction.MaxPageRows {
			rows = rows[:interaction.MaxPageRows]
		}
		for _, e := range rows {
			if seen[e.ID()] {
				t.Fatal("acknowledged row reappeared")
			}
			seen[e.ID()] = true
		}
		if err := s.RecordTurnEvents(t.Context(), withEventText(rows), "t"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTurnEventsDoNotAcknowledgeAnUnrecordedParticipant(t *testing.T) {
	s, _ := eventStore(t)
	seedTurnEvents(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	details := interaction.Details{}
	if _, err := s.RecordConversation(ctx, "participant", "actual arrival", "t", details); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	if rows, err := s.PendingTurnEvents(t.Context()); err != nil || len(rows) != 3 {
		t.Fatalf("failed input consumed arrival: %d %v", len(rows), err)
	}
	ref, err := s.RecordConversation(context.Background(), "participant", "actual arrival", "t", details)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AnnotateTurn(ref.Sequence, "turn_event.arrival", "in", `{}`); err != nil {
		t.Fatal(err)
	}
	rows, err := s.PendingTurnEvents(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatalf("arrival repeated: %d %v", len(rows), err)
	}
}

// A queued peer message no adapter was asked to carry is an observation at
// attempt 0 — shown once, then quiet until it is really tried, when the
// higher attempt makes it new. A row delivered before attempts were counted
// has nothing to say, and operator mail is not a delivery observation.
func TestTurnEventsNeverAttemptedSendIsObservedUntilTried(t *testing.T) {
	s, _ := eventStore(t)
	if err := s.AddOutboxMessage("waiting", "peer", "friend", "not carried yet", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOutboxMessage("historical", "peer", "friend", "delivered long ago", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOutboxMessage("to-operator", "operator", "", "on the dashboard", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE outbox SET delivered=1 WHERE id='historical'`); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingTurnEvents(t.Context())
	if err != nil || len(pending) != 1 || pending[0].Outcome == nil || pending[0].ID() != "waiting" || pending[0].Outcome.Attempts != 0 {
		t.Fatalf("never-attempted send not pending alone: %+v %v", pending, err)
	}
	if err := s.RecordTurnEvents(t.Context(), withEventText(pending), "turn-a"); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.PendingTurnEvents(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("an acknowledged waiting send reappeared unchanged: %d %v", len(rows), err)
	}
	if _, _, err := s.RecordDeliveryAttempt(claimFor(t, s, "waiting"), "fixture refusal", false, 8); err != nil {
		t.Fatal(err)
	}
	rows, err := s.PendingTurnEvents(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID() != "waiting" || rows[0].Outcome.Attempts != 1 {
		t.Fatalf("the first real attempt is not a new observation: %+v %v", rows, err)
	}
}
