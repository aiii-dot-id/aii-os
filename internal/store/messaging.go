package store

import (
	"fmt"
	"time"
)

// messaging.go — what arrived.
//
// ONE table and two methods. The address book left entirely: it is the
// operator's config, because knowing someone's number is a choice they
// make and not state the runtime discovers (see app.Contact). A store
// table for it had six methods and no writer.
//
// What remains is the only part that IS runtime state: arrivals, kept so
// a replayed update is not a second message.
//
// Runtime state, never identity truth. Knowing someone's address is not a
// fact about who the identity IS, and ENTITY_TYPES.md already places
// correspondence outside the chain. What an exchange MEANT can become
// identity truth, but only when the resident says so through note.

// Inbound is one message that arrived and has not been read.
type Inbound struct {
	ID         string
	Channel    string
	Address    string
	Body       string
	ReceivedMs int64
}

// RecordInbound stores an arrival, and reports whether it was NEW.
//
// Idempotent by id: a blocking read can replay an update the channel
// already gave us (Telegram does exactly that until the offset is
// acknowledged), and a replayed update is not a second message. The
// primary key enforces it rather than the adapter being trusted to.
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

// InboundSince returns everything that arrived after a moment, oldest
// first.
//
// Time, not a seen flag. The transcript already knows when the identity
// last spoke, so "what came in since then" is a QUESTION, not a piece of
// bookkeeping to maintain — the same shape TimerFiringsSince already
// uses for alarms, one file over.
//
// A seen column and a MarkInboundSeen were the first version, and they
// had the failure that bookkeeping always has: nothing called them for
// the unattended case, so arrivals from anyone not granted a wake were
// recorded unseen and read by nobody, forever, while the comment above
// them claimed "the next turn carries it".
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
