package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"
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
	if !arrivalIDForm.MatchString(id) {
		return false, &ArrivalIDRefusal{ID: id}
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

const MaxArrivalIDChars = 192

var arrivalIDForm = regexp.MustCompile(`^[A-Za-z0-9._:@+=/-]{1,192}$`)

type ArrivalIDRefusal struct{ ID string }

func (e *ArrivalIDRefusal) Error() string {
	return fmt.Sprintf("inbound.id_form: an arrival's id is at most %d characters of letters, digits and . _ : @ + = / -; this one (%d bytes) is not", MaxArrivalIDChars, len(e.ID))
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
		if err := scanInbound(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanInbound(row rowScanner, m *Inbound) error {
	return row.Scan(&m.ID, &m.Channel, &m.Address, &m.Body, &m.ReceivedMs)
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

func (s *Store) InboundByID(id string) (m Inbound, ok bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	err = scanInbound(s.db.QueryRow(`SELECT id, channel, address, body, received_ms FROM inbound WHERE id = ?`, id), &m)
	if errors.Is(err, sql.ErrNoRows) {
		return Inbound{}, false, nil
	}
	return m, err == nil, err
}

type ListedArrival struct {
	Seq uint64
	Inbound
}

func (s *Store) ArrivalsBefore(beforeSeq uint64, limit, previewChars int) ([]ListedArrival, error) {
	if previewChars < 0 || previewChars > int(^uint(0)>>1)/utf8.UTFMax {
		return nil, fmt.Errorf("arrival preview character bound is out of range")
	}
	if beforeSeq == 0 {
		beforeSeq = 1 << 62
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT rowid, id, channel, address, substr(CAST(body AS BLOB), 1, ?), received_ms FROM inbound
		 WHERE rowid < ? ORDER BY rowid DESC LIMIT ?`, previewChars*utf8.UTFMax, int64(beforeSeq), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ListedArrival
	for rows.Next() {
		var a ListedArrival
		if err := rows.Scan(&a.Seq, &a.ID, &a.Channel, &a.Address, &a.Body, &a.ReceivedMs); err != nil {
			return nil, err
		}
		if runes := []rune(a.Body); len(runes) > previewChars {
			a.Body = string(runes[:previewChars])
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
