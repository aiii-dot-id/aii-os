package store

import (
	"database/sql"
	"strings"
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
func (s *Store) OnOutboxWrite(fn func()) {
	s.mu.Lock()
	s.outboxListeners = append(s.outboxListeners, fn)
	s.mu.Unlock()
}

func (s *Store) notifyOutbox() {
	s.mu.RLock()
	listeners := make([]func(), len(s.outboxListeners))
	copy(listeners, s.outboxListeners)
	s.mu.RUnlock()
	for _, fn := range listeners {
		fn()
	}
}

// .

// .
type OutboxMessage struct {
	ID           string
	ToRole       string
	ToIdentity   string
	Content      string
	Delivered    int
	DeliveredVia string
	CreatedSeq   uint64
	CreatedMs    int64
	DeliveredAt  string
	// .
	// .
	// .
	Attempts      int
	LastError     string
	LastAttemptMs int64
	Parked        bool
	Effect        string
}

// .
// .
// .
// .
// .
func (s *Store) UndeliveredFor(role string) ([]OutboxMessage, error) {
	all, err := s.UndeliveredMessages()
	if err != nil {
		return nil, err
	}
	var out []OutboxMessage
	for _, m := range all {
		if m.ToRole == role {
			out = append(out, m)
		}
	}
	return out, nil
}

const outboxColumns = `id, to_role, to_identity, content, delivered, delivered_via, created_seq, created_ms, delivered_at,
	 attempts, last_error, last_attempt_ms, parked, effect`

// .
func scanOutbox(rows *sql.Rows) ([]OutboxMessage, error) {
	var messages []OutboxMessage
	for rows.Next() {
		var m OutboxMessage
		var toIdentity, deliveredVia, deliveredAt sql.NullString
		var createdSeq sql.NullInt64
		var parked int
		if err := rows.Scan(&m.ID, &m.ToRole, &toIdentity, &m.Content, &m.Delivered, &deliveredVia, &createdSeq, &m.CreatedMs, &deliveredAt,
			&m.Attempts, &m.LastError, &m.LastAttemptMs, &parked, &m.Effect); err != nil {
			return nil, err
		}
		m.ToIdentity = toIdentity.String
		m.DeliveredVia = deliveredVia.String
		m.DeliveredAt = deliveredAt.String
		m.Parked = parked != 0
		if createdSeq.Valid {
			m.CreatedSeq = uint64(createdSeq.Int64)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// .
// .
// .
// .
func (s *Store) UndeliveredMessages() ([]OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT ` + outboxColumns + ` FROM outbox WHERE delivered = 0 ORDER BY created_ms ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutbox(rows)
}

// .
// .
func (s *Store) PendingPeerDeliveries() ([]OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT ` + outboxColumns + ` FROM outbox
		 WHERE delivered = 0 AND parked = 0 AND to_role = 'peer' ORDER BY created_ms ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutbox(rows)
}

// .
// .
// .
// .
// .
func (s *Store) OutboxOutcomesSince(sinceMs int64) ([]OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT `+outboxColumns+` FROM outbox
		 WHERE to_role = 'peer' AND last_attempt_ms > ? ORDER BY last_attempt_ms ASC, id ASC`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutbox(rows)
}

// .
// .
// .
// .
// .
func (s *Store) RecordDeliveryAttempt(messageID, answer string, permanent bool, parkAfter int) (attempts int, parked bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	perm := 0
	if permanent {
		perm = 1
	}
	if _, err = s.db.Exec(`UPDATE outbox SET attempts = attempts + 1, last_error = ?, last_attempt_ms = ?,
		 parked = CASE WHEN ? = 1 OR attempts + 1 >= ? THEN 1 ELSE parked END WHERE id = ?`,
		answer, time.Now().UTC().UnixMilli(), perm, parkAfter, messageID); err != nil {
		return 0, false, err
	}
	var p int
	if err = s.db.QueryRow(`SELECT attempts, parked FROM outbox WHERE id = ?`, messageID).Scan(&attempts, &p); err != nil {
		return 0, false, err
	}
	return attempts, p != 0, nil
}

// .
// .
// .
// .
func (s *Store) MarkEffectUnknown(messageID, via, answer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE outbox SET attempts = attempts + 1, last_error = ?, last_attempt_ms = ?,
		 parked = 1, effect = 'unknown', delivered_via = ? WHERE id = ?`,
		answer, time.Now().UTC().UnixMilli(), via, messageID)
	return err
}

// .
// .
func (s *Store) MarkDelivered(messageID, via string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	_, err := s.db.Exec(
		`UPDATE outbox SET delivered = 1, delivered_via = ?, delivered_at = ?, attempts = attempts + 1,
		 last_attempt_ms = ?, last_error = '', effect = 'performed' WHERE id = ?`,
		via, now.Format(time.RFC3339Nano), now.UnixMilli(), messageID,
	)
	return err
}

// .
func (s *Store) AddOutboxMessage(id, toRole, toIdentity, content string, createdSeq *uint64) error {
	_, err := s.addOutboxMessage(id, toRole, toIdentity, content, createdSeq, false)
	return err
}

// .
// .
func (s *Store) AddOutboxMessageOnce(id, toRole, toIdentity, content string, createdSeq *uint64) (bool, error) {
	return s.addOutboxMessage(id, toRole, toIdentity, content, createdSeq, true)
}

// .
func (s *Store) SetOperatorASCII(on bool) { s.operatorASCII.Store(on) }

// .
// .
// .
// .
// .
var operatorFold = strings.NewReplacer(
	"\u2014", "-", "\u2013", "-", "\u2212", "-", "\u00b7", "-",
	"\u201c", "\"", "\u201d", "\"", "\u2018", "'", "\u2019", "'",
	"\u2026", "...", "\u2192", "->", "\u2190", "<-", "\u2194", "<->",
	"\u00a7", "S.", "\u00d7", "x", "\u2022", "*", "\u2264", "<=", "\u2265", ">=",
)

// .
func foldOperatorASCII(s string) string { return operatorFold.Replace(s) }

func (s *Store) addOutboxMessage(id, toRole, toIdentity, content string, createdSeq *uint64, once bool) (bool, error) {
	if toRole == "operator" && s.operatorASCII.Load() {
		content = foldOperatorASCII(content)
	}
	// .
	// .
	var seqVal interface{}
	if createdSeq != nil {
		seqVal = *createdSeq
	}

	insert := `INSERT INTO outbox (id, to_role, to_identity, content, delivered, created_seq, created_ms)
		 VALUES (?, ?, ?, ?, 0, ?, ?)`
	if once {
		insert = `INSERT INTO outbox (id, to_role, to_identity, content, delivered, created_seq, created_ms)
		 VALUES (?, ?, ?, ?, 0, ?, ?) ON CONFLICT(id) DO NOTHING`
	}
	s.mu.Lock()
	result, err := s.db.Exec(insert,
		id, toRole, toIdentity, content, seqVal, time.Now().UTC().UnixMilli(),
	)
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	// .
	// .
	// .
	if rows > 0 {
		s.notifyOutbox()
	}
	return rows > 0, nil
}

// .
// .
// .
// .
// .
// .
// .
func (s *Store) TimerFiringsSince(sinceMs int64) ([]OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(
		`SELECT id, to_role, to_identity, content, created_ms FROM outbox
		 WHERE substr(id, 1, 6) = 'timer_' AND created_ms > ? ORDER BY created_ms ASC`,
		sinceMs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxMessage
	for rows.Next() {
		var m OutboxMessage
		var toIdentity sql.NullString
		if err := rows.Scan(&m.ID, &m.ToRole, &toIdentity, &m.Content, &m.CreatedMs); err != nil {
			return nil, err
		}
		m.ToIdentity = toIdentity.String
		out = append(out, m)
	}
	return out, rows.Err()
}

// .
// .
func (s *Store) BumpOutboxCreatedMs(id string, deltaMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE outbox SET created_ms = created_ms + ? WHERE id = ?`, deltaMs, id)
	return err
}

// .
// .
// .
func (s *Store) LastTurnAtMs(role string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ts string
	err := s.db.QueryRow(
		`SELECT created_at FROM conversations WHERE role = ? ORDER BY turn_seq DESC LIMIT 1`, role,
	).Scan(&ts)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if t, perr := time.Parse(time.RFC3339Nano, ts); perr == nil {
		return t.UnixMilli(), nil
	}
	return 0, nil
}
