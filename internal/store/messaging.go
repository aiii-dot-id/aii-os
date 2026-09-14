package store

import (
	"fmt"
	"time"
)

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
// .

// .
type Inbound struct {
	ID         string
	Channel    string
	Address    string
	Body       string
	ReceivedMs int64
}

// .
// .
// .
// .
// .
// .
func (s *Store) RecordInbound(id, channel, address, body string) (bool, error) {
	if id == "" || channel == "" || address == "" {
		return false, fmt.Errorf("an arrival needs an id, a channel and a sender")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(
		`INSERT INTO inbound (id, channel, address, body, received_ms)
		 VALUES (?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id, channel, address, body, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
func (s *Store) InboundSince(sinceMs int64) ([]Inbound, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, channel, address, body, received_ms FROM inbound
		  WHERE received_ms > ? ORDER BY received_ms ASC, id ASC`, sinceMs)
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
