package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/google/uuid"
)

type TurnEvent struct {
	Arrival *Inbound
	Timer   *OutboxMessage
	Outcome *OutboxMessage
	Text    string
}

func (e TurnEvent) ID() string {
	if e.Arrival != nil {
		return e.Arrival.ID
	}
	if e.Timer != nil {
		return e.Timer.ID
	}
	return e.Outcome.ID
}
func (e TurnEvent) at() int64 {
	if e.Arrival != nil {
		return e.Arrival.ReceivedMs
	}
	if e.Timer != nil {
		return e.Timer.CreatedMs
	}
	if e.Outcome.LastAttemptMs == 0 {
		return e.Outcome.CreatedMs
	}
	return e.Outcome.LastAttemptMs
}

type inputEventSource struct {
	Kind, ID string
	Attempt  int
}

func (e TurnEvent) source() (*inputEventSource, error) {
	switch {
	case e.Arrival != nil && e.Timer == nil && e.Outcome == nil:
		return &inputEventSource{Kind: "arrival", ID: e.Arrival.ID}, nil
	case e.Timer != nil && e.Arrival == nil && e.Outcome == nil:
		return &inputEventSource{Kind: "timer", ID: e.Timer.ID}, nil
	case e.Outcome != nil && e.Arrival == nil && e.Timer == nil:
		return &inputEventSource{Kind: "delivery", ID: e.Outcome.ID, Attempt: e.Outcome.Attempts}, nil
	default:
		return nil, interaction.Invalid("one source per turn input required")
	}
}

func (s *Store) PendingTurnEvents(ctx context.Context) (_ []TurnEvent, retErr error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tx, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, tx.Close()) }()
	rows, err := tx.QueryContext(ctx, `SELECT id,channel,address,body,received_ms FROM inbound i
	 WHERE COALESCE(previously_seen,0)=0 AND NOT EXISTS (`+arrivalTaken+`)
	 ORDER BY received_ms,id LIMIT ?`, interaction.MaxPageRows+1)
	if err != nil {
		return nil, err
	}
	var events []TurnEvent
	for rows.Next() {
		m := new(Inbound)
		if err := rows.Scan(&m.ID, &m.Channel, &m.Address, &m.Body, &m.ReceivedMs); err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, TurnEvent{Arrival: m})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, timer := range []bool{true, false} {

		where := `to_role='peer' AND NOT ` + inFlight + ` AND (last_attempt_ms>0 OR (delivered=0 AND parked=0)) AND NOT EXISTS
		 (SELECT 1 FROM turn_annotations a JOIN conversations c ON c.turn_seq=a.turn_seq WHERE a.kind='turn_event.delivery'
		 AND a.key=outbox.id AND COALESCE(json_extract(a.payload,'$.attempt'),0)>=outbox.attempts)`
		order := "CASE WHEN last_attempt_ms>0 THEN last_attempt_ms ELSE created_ms END,id"
		if timer {
			where = `substr(id,1,6)='timer_' AND NOT EXISTS
		 (SELECT 1 FROM turn_annotations a JOIN conversations c ON c.turn_seq=a.turn_seq WHERE a.kind='turn_event.timer' AND a.key=outbox.id)`
			order = "created_ms,id"
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+outboxColumns+" FROM outbox WHERE "+where+" ORDER BY "+order+" LIMIT ?", interaction.MaxPageRows+1)
		if err != nil {
			return nil, err
		}
		messages, err := scanOutbox(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		for i := range messages {
			if timer {
				events = append(events, TurnEvent{Timer: &messages[i]})
			} else {
				events = append(events, TurnEvent{Outcome: &messages[i]})
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].at() != events[j].at() {
			return events[i].at() < events[j].at()
		}
		return events[i].ID() < events[j].ID()
	})
	return events, nil
}

func (s *Store) RecordTurnEvents(ctx context.Context, events []TurnEvent, turn string) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) > interaction.MaxPageRows {
		return interaction.Invalid("bounded turn input required")
	}
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	bytes := 0
	for _, e := range events {
		ref, err := e.source()
		if err != nil {
			return err
		}
		if ref.ID == "" || ref.Attempt < 0 {
			return interaction.Invalid("source ID and observed attempt required")
		}
		bytes += len(e.Text)
		if e.Text == "" || bytes > interaction.MaxPageBytes {
			return interaction.Invalid("bounded recoverable event text required")
		}
		var found int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM turn_annotations a JOIN conversations c ON c.turn_seq=a.turn_seq WHERE a.kind=? AND a.key=? AND COALESCE(json_extract(a.payload,'$.attempt'),0)>=? LIMIT 1`, "turn_event."+ref.Kind, ref.ID, ref.Attempt).Scan(&found)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		input, _, err := s.appendInteractionTx(ctx, tx, interaction.Input{ID: "events_" + uuid.NewString(), Kind: interaction.Notice, Role: interaction.System, Content: e.Text, TurnID: turn, Details: interaction.Details{Channel: "turn_events"}})
		if err != nil {
			return err
		}
		if err := s.annotateTurnTx(ctx, tx, input.Sequence, "turn_event."+ref.Kind, ref.ID, fmt.Sprintf(`{"attempt":%d}`, ref.Attempt)); err != nil {
			return err
		}
	}
	err = tx.Commit()
	changed = true
	return err
}

func (s *Store) InboundInputRecorded(ctx context.Context, id string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM turn_annotations a JOIN conversations c ON c.turn_seq=a.turn_seq WHERE a.kind='turn_event.arrival' AND a.key=? LIMIT 1`, id).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
