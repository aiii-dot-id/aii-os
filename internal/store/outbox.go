package store

import (
	"database/sql"
	"time"
)

// --- Outbox push hooks ---
//
// Outbox writes are EVENTS for the operator surface: a wake speech or a
// send-verb message must reach a CONNECTED dashboard within milliseconds,
// not at the next connect (Aeon's first wake, 2026-08-17). The store
// signals registered listeners after a successful write; the app wires
// the dashboard's pump to it. Listeners must not call back into the
// store synchronously (the signal fires outside the store lock, but the
// contract stays: signal, don't shop).

// OnOutboxWrite registers a listener fired after every successful
// AddOutboxMessage. The listener receives no payload — the reader pulls
// UndeliveredMessages once, coalescing bursts.
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
		fn() // listener contract: non-blocking signal
	}
}

// --- Outbox ---

// OutboxMessage is an undelivered message from the identity.
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
}

// UndeliveredFor returns undelivered messages addressed to one role.
//
// Asking "what is undelivered" and acting as if it were "what is MINE" is
// what put peer-addressed mail on the operator's screen and marked it
// delivered. One query, and the caller says whose.
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

// UndeliveredMessages returns all undelivered outbox messages, oldest first.
func (s *Store) UndeliveredMessages() ([]OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT id, to_role, to_identity, content, delivered, delivered_via, created_seq, created_ms, delivered_at
		 FROM outbox WHERE delivered = 0 ORDER BY created_seq ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []OutboxMessage
	for rows.Next() {
		var m OutboxMessage
		var toIdentity, deliveredVia, deliveredAt sql.NullString
		var createdSeq sql.NullInt64 // nullable: send is outbox-only, no producing event
		if err := rows.Scan(&m.ID, &m.ToRole, &toIdentity, &m.Content, &m.Delivered, &deliveredVia, &createdSeq, &m.CreatedMs, &deliveredAt); err != nil {
			return nil, err
		}
		m.ToIdentity = toIdentity.String
		m.DeliveredVia = deliveredVia.String
		m.DeliveredAt = deliveredAt.String
		if createdSeq.Valid {
			m.CreatedSeq = uint64(createdSeq.Int64)
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// MarkDelivered marks an outbox message as delivered.
func (s *Store) MarkDelivered(messageID, via string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`UPDATE outbox SET delivered = 1, delivered_via = ?, delivered_at = ? WHERE id = ?`,
		via, time.Now().UTC().Format(time.RFC3339Nano), messageID,
	)
	return err
}

// AddOutboxMessage adds a message to the outbox.
func (s *Store) AddOutboxMessage(id, toRole, toIdentity, content string, createdSeq *uint64) error {
	_, err := s.addOutboxMessage(id, toRole, toIdentity, content, createdSeq, false)
	return err
}

// AddOutboxMessageOnce inserts one idempotent runtime delivery. The caller
// supplies a stable id; added is false when that exact delivery already exists.
func (s *Store) AddOutboxMessageOnce(id, toRole, toIdentity, content string, createdSeq *uint64) (bool, error) {
	return s.addOutboxMessage(id, toRole, toIdentity, content, createdSeq, true)
}

func (s *Store) addOutboxMessage(id, toRole, toIdentity, content string, createdSeq *uint64, once bool) (bool, error) {
	// created_seq is nullable and always null today: send is outbox-only,
	// never mints a ledger event. Pointer (not uint64) so nil = NULL.
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

	// Notify OUTSIDE the store lock — notifyOutbox takes the read lock
	// (the self-deadlock that hung the identity tests for a night class:
	// notify-under-Lock = RLock-under-Lock on the same RWMutex).
	if rows > 0 {
		s.notifyOutbox()
	}
	return rows > 0, nil
}

// TimerFiringsSince returns timer-fired outbox rows created after the
// given unix-ms instant, oldest first — the resident-delivery window:
// firings the identity has not yet seen (they age out once the
// resident's own turn advances past them). Timer rows are the delivery
// owner's writes (id prefix "timer_"), to the operator. Numeric domain
// by design: RFC3339Nano text compares unreliably (trailing-zero
// trimming breaks lexicographic order — found by test 2026-08-17).
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

// BumpOutboxCreatedMs shifts a row's created_ms (test fixture:
// simulate an older firing without sleeping).
func (s *Store) BumpOutboxCreatedMs(id string, deltaMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`UPDATE outbox SET created_ms = created_ms + ? WHERE id = ?`, deltaMs, id)
	return err
}

// LastTurnAtMs returns the unix-ms instant of the role's latest turn
// (0 when none) — the resident-delivery window anchor. The stored text
// is RFC3339(Nano); parsed here so comparisons stay numeric.
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
	return 0, nil // unparsable legacy text: window from the beginning — honest include
}
