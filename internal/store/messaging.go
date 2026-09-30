package store

import (
	"context"
	"fmt"
	"time"
)

type Inbound struct {
	ID         string
	Channel    string
	Address    string
	Body       string
	ReceivedMs int64
}

func (s *Store) RecordInbound(id, channel, address, body string) (bool, error) {
	if id == "" || channel == "" || address == "" {
		return false, fmt.Errorf("an arrival needs an id, a channel and a sender")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.w().Exec(
		`INSERT INTO inbound (id, channel, address, body, received_ms)
		 VALUES (?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id, channel, address, body, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) InboundSince(sinceMs int64) ([]Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, channel, address, body, received_ms FROM inbound
		  WHERE received_ms > ? AND previously_seen = 0 ORDER BY received_ms ASC, id ASC`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Inbound
	for rows.Next() {
		var m Inbound
		if err := rows.Scan(&m.ID, &m.Channel, &m.Address, &m.Body, &m.ReceivedMs); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const arrivalTaken = `SELECT 1 FROM turn_annotations a JOIN conversations c ON c.turn_seq=a.turn_seq
	 WHERE a.kind='turn_event.arrival' AND a.key=i.id`

const PreviewChars = 4096

type Arrival struct {
	Inbound
	BodyChars int
	Read      bool
	Woke      bool
}

func (s *Store) RecentArrivals(ctx context.Context, limit int) ([]Arrival, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbound`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT i.id, i.channel, i.address, substr(i.body, 1, ?), length(i.body), i.received_ms,
		 COALESCE(i.previously_seen, 0) <> 0 OR EXISTS (`+arrivalTaken+`),
		 EXISTS (`+arrivalTaken+` AND c.role = 'participant' AND c.turn_id <> ''
		   AND c.turn_seq = (SELECT MIN(f.turn_seq) FROM conversations f WHERE f.turn_id = c.turn_id))
		 FROM inbound i ORDER BY i.received_ms DESC, i.id DESC LIMIT ?`, PreviewChars, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Arrival
	for rows.Next() {
		var m Arrival
		if err := rows.Scan(&m.ID, &m.Channel, &m.Address, &m.Body, &m.BodyChars, &m.ReceivedMs, &m.Read, &m.Woke); err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (s *Store) ArrivalsFrom(ctx context.Context, sinceMs int64, previewChars int, fn func(m Inbound, bodyChars int)) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.QueryContext(ctx, `SELECT id, channel, address, substr(body, 1, ?), length(body), received_ms FROM inbound
		 WHERE received_ms >= ? ORDER BY received_ms ASC, id ASC`, previewChars, sinceMs)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m Inbound
		var chars int
		if err := rows.Scan(&m.ID, &m.Channel, &m.Address, &m.Body, &chars, &m.ReceivedMs); err != nil {
			return err
		}
		fn(m, chars)
	}
	return rows.Err()
}
