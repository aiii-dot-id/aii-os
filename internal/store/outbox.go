package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

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

	RequestedChannel string
	RecipientError   string

	Attempts      int
	LastError     string
	LastAttemptMs int64
	Parked        bool
	Effect        string

	DispatchedMs int64
}

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
	 attempts, last_error, last_attempt_ms, parked, effect, requested_channel, recipient_error, COALESCE(dispatched_ms, 0)`

func scanOutbox(rows *sql.Rows) ([]OutboxMessage, error) {
	var messages []OutboxMessage
	for rows.Next() {
		var m OutboxMessage
		var toIdentity, deliveredVia, deliveredAt sql.NullString
		var createdSeq sql.NullInt64
		var parked int
		if err := rows.Scan(&m.ID, &m.ToRole, &toIdentity, &m.Content, &m.Delivered, &deliveredVia, &createdSeq, &m.CreatedMs, &deliveredAt,
			&m.Attempts, &m.LastError, &m.LastAttemptMs, &parked, &m.Effect, &m.RequestedChannel, &m.RecipientError, &m.DispatchedMs); err != nil {
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

const walkable = `delivered = 0 AND parked = 0 AND NOT ` + inFlight

func (s *Store) PendingPeerDeliveries() ([]OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT ` + outboxColumns + ` FROM outbox
		 WHERE to_role = 'peer' AND ` + walkable + ` ORDER BY created_ms ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutbox(rows)
}

func (s *Store) PendingNotices() ([]OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var after int64
	switch raw, err := s.getRuntimeMeta(RestoreKey); {
	case err != nil:
		return nil, err
	case raw != "":
		var r Restore
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, fmt.Errorf("the recorded restore does not read, so no notice leaves the dashboard: %w", err)
		}
		after = r.broughtBackUntil()
	}
	rows, err := s.db.Query(`SELECT `+outboxColumns+` FROM outbox
		 WHERE to_role = 'operator' AND `+walkable+` AND created_ms > ? ORDER BY created_ms ASC, id ASC`, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutbox(rows)
}

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

type PeerSendPage struct {
	Open, Delivered           []OutboxMessage
	OpenTotal, DeliveredTotal int
}

func (s *Store) PeerSends(ctx context.Context, limit int) (PeerSendPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var p PeerSendPage
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(delivered = 0), 0), COALESCE(SUM(delivered <> 0), 0)
		 FROM outbox WHERE to_role = 'peer'`).Scan(&p.OpenTotal, &p.DeliveredTotal); err != nil {
		return p, err
	}
	for _, part := range []struct {
		rows  *[]OutboxMessage
		where string
	}{
		{&p.Open, `delivered = 0 ORDER BY created_ms ASC, id ASC`},
		{&p.Delivered, `delivered <> 0 ORDER BY last_attempt_ms DESC, created_ms DESC, id DESC`},
	} {
		rows, err := s.db.QueryContext(ctx, `SELECT `+outboxColumns+` FROM outbox WHERE to_role = 'peer' AND `+part.where+` LIMIT ?`, limit)
		if err != nil {
			return p, err
		}
		*part.rows, err = scanOutbox(rows)
		rows.Close()
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

type NoticePage struct {
	Rows  []OutboxMessage
	Total int
}

func (s *Store) Notices(ctx context.Context, limit int) (NoticePage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var p NoticePage
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM outbox WHERE to_role = 'operator'`).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+outboxColumns+` FROM outbox WHERE to_role = 'operator'
		 ORDER BY created_ms DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	p.Rows, err = scanOutbox(rows)
	return p, err
}

const inFlight = `(COALESCE(dispatched_ms, 0) > last_attempt_ms)`

var ErrClaimRefused = errors.New("the message is in flight or no longer waiting to be sent")

var ErrClaimSuperseded = errors.New("the claim was settled before its result arrived; the late result changes nothing")

type Dispatch struct {
	MessageID string
	At        int64
}

func (s *Store) ClaimDispatch(messageID string) (Dispatch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var last int64
	err := s.db.QueryRow(`SELECT last_attempt_ms FROM outbox WHERE id = ?`, messageID).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return Dispatch{}, ErrClaimRefused
	}
	if err != nil {
		return Dispatch{}, err
	}
	at := max(time.Now().UTC().UnixMilli(), last+1)
	res, err := s.w().Exec(`UPDATE outbox SET dispatched_ms = ?
		 WHERE id = ? AND `+walkable+` AND last_attempt_ms = ?`, at, messageID, last)
	if err != nil {
		return Dispatch{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Dispatch{}, err
	} else if n != 1 {
		return Dispatch{}, ErrClaimRefused
	}
	return Dispatch{MessageID: messageID, At: at}, nil
}

func (s *Store) settleDispatch(d Dispatch, set string, args ...interface{}) error {
	args = append(args, time.Now().UTC().UnixMilli(), d.MessageID, d.At)
	res, err := s.w().Exec(`UPDATE outbox SET `+set+`, attempts = attempts + 1, last_attempt_ms = max(?, dispatched_ms)
		 WHERE id = ? AND dispatched_ms = ? AND `+inFlight, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrClaimSuperseded
	}
	return nil
}

func (s *Store) RecordDeliveryAttempt(d Dispatch, answer string, permanent bool, parkAfter int) (attempts int, parked bool, err error) {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()

	var p int
	if err = s.db.QueryRow(`SELECT attempts, parked FROM outbox WHERE id = ?`, d.MessageID).Scan(&attempts, &p); err != nil {
		return 0, false, err
	}
	attempts++
	parked = p != 0 || permanent || attempts >= parkAfter
	park := 0
	if parked {
		park = 1
	}
	if err = s.settleDispatch(d, `last_error = ?, parked = ?`, answer, park); err != nil {
		return 0, false, err
	}
	changed = true
	return attempts, parked, nil
}

func (s *Store) MarkEffectUnknown(d Dispatch, via, answer string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.settleDispatch(d, `last_error = ?, parked = 1, effect = 'unknown', delivered_via = ?`, answer, via)
	changed = err == nil
	return err
}

func (s *Store) MarkDispatchDelivered(d Dispatch, via string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.settleDispatch(d, `delivered = 1, delivered_via = ?, delivered_at = ?, last_error = '', effect = 'performed'`,
		via, time.Now().UTC().Format(time.RFC3339Nano))
	changed = err == nil
	return err
}

func (s *Store) SettleInterruptedDispatches(answer string) (int, error) {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.w().Exec(`UPDATE outbox SET attempts = attempts + 1, last_error = ?, last_attempt_ms = max(?, dispatched_ms),
		 parked = 1, effect = 'unknown' WHERE delivered = 0 AND `+inFlight,
		answer, time.Now().UTC().UnixMilli())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	changed = err == nil && n > 0
	return int(n), err
}

const RestoreKey = "continuity.restored"

type Restore struct {
	At       time.Time `json:"at"`
	Source   string    `json:"source"`
	SourceAt time.Time `json:"source_at,omitzero"`
}

func (r Restore) BroughtBack(createdMs int64) bool { return createdMs <= r.broughtBackUntil() }

func (r Restore) broughtBackUntil() int64 {
	return max(r.At.UnixMilli(), r.SourceAt.UnixMilli())
}

func (s *Store) SettleRestore(r Restore, answer string) (int, error) {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
	tx, err := s.w().Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var was string
	switch err := tx.QueryRow(`SELECT value FROM runtime_meta WHERE key = ?`, RestoreKey).Scan(&was); {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return 0, err
	default:

		var prev Restore
		if json.Unmarshal([]byte(was), &prev) == nil && prev.At.Equal(r.At) {
			return 0, nil
		}
	}
	now := time.Now().UTC()
	res, err := tx.Exec(`UPDATE outbox SET attempts = attempts + 1, last_error = ?, last_attempt_ms = max(?, COALESCE(dispatched_ms, 0)),
		 parked = 1, effect = 'unknown' WHERE to_role = 'peer' AND delivered = 0 AND parked = 0`, answer, now.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO runtime_meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		RestoreKey, string(raw), now.Format(time.RFC3339Nano)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	changed = n > 0
	return int(n), nil
}

func (s *Store) LastRestore() (Restore, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, err := s.getRuntimeMeta(RestoreKey)
	if err != nil || raw == "" {
		return Restore{}, false, err
	}
	var r Restore
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return Restore{}, false, fmt.Errorf("the recorded restore does not read: %w", err)
	}
	return r, true, nil
}

const pageMayCarry = `to_role = 'operator' AND delivered = 0 AND effect <> 'unknown' AND NOT ` + inFlight

var ErrCarriedOffTheDashboard = errors.New("the notice is the route off the dashboard's: in flight, delivered by an adapter, or of unknown effect — the page's mark changes nothing")

func (s *Store) PageNotices() ([]OutboxMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT ` + outboxColumns + ` FROM outbox WHERE ` + pageMayCarry + ` ORDER BY created_ms ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOutbox(rows)
}

func (s *Store) MarkDelivered(messageID, via string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	res, err := s.w().Exec(
		`UPDATE outbox SET delivered = 1, delivered_via = ?, delivered_at = ?, attempts = attempts + 1,
		 last_attempt_ms = max(?, last_attempt_ms), last_error = '', effect = 'performed' WHERE id = ? AND `+pageMayCarry,
		via, now.Format(time.RFC3339Nano), now.UnixMilli(), messageID,
	)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 1 {
		changed = true
		return nil
	}
	var took string
	switch err := s.db.QueryRow(`SELECT COALESCE(delivered_via, '') FROM outbox WHERE id = ? AND to_role = 'operator' AND delivered = 1`, messageID).Scan(&took); {
	case err == nil && took == via:
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	return ErrCarriedOffTheDashboard
}

func (s *Store) OutboxContent(id string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var content string
	err := s.db.QueryRow(`SELECT content FROM outbox WHERE id=?`, id).Scan(&content)
	return content, err
}

func (s *Store) AddOutboxMessage(id, toRole, toIdentity, content string, createdSeq *uint64) error {
	_, err := s.addOutboxMessage(id, toRole, toIdentity, content, createdSeq, false)
	return err
}

func (s *Store) AddOutboxMessageOnce(id, toRole, toIdentity, content string, createdSeq *uint64) (bool, error) {
	return s.addOutboxMessage(id, toRole, toIdentity, content, createdSeq, true)
}

func (s *Store) SetOperatorASCII(on bool) { s.operatorASCII.Store(on) }

var operatorFold = strings.NewReplacer(
	"\u2014", "-", "\u2013", "-", "\u2212", "-", "\u00b7", "-",
	"\u201c", "\"", "\u201d", "\"", "\u2018", "'", "\u2019", "'",
	"\u2026", "...", "\u2192", "->", "\u2190", "<-", "\u2194", "<->",
	"\u00a7", "S.", "\u00d7", "x", "\u2022", "*", "\u2264", "<=", "\u2265", ">=",
)

func foldOperatorASCII(s string) string { return operatorFold.Replace(s) }

func (s *Store) addOutboxMessage(id, toRole, toIdentity, content string, createdSeq *uint64, once bool) (bool, error) {
	return s.insertOutboxInteraction(id, toRole, toIdentity, content, createdSeq, once, nil, false)
}

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

func (s *Store) BumpOutboxCreatedMs(id string, deltaMs int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w().Exec(`UPDATE outbox SET created_ms = created_ms + ? WHERE id = ?`, deltaMs, id)
	return err
}

func (s *Store) LastTurnAtMs(role string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ts string
	err := s.db.QueryRow(
		`SELECT created_at FROM conversations WHERE role = ? AND `+s.dialogueSelection()+` ORDER BY turn_seq DESC LIMIT 1`, role,
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
