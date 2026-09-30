package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

type OutboxRepairError struct{ Requirement string }

const HeldMessagePageSize = 50

func (e *OutboxRepairError) Error() string { return "outbox repair requires " + e.Requirement }

type HeldMessage struct {
	ID        string `json:"id"`
	Recipient string `json:"recipient"`
	Channel   string `json:"channel"`
	Reason    string `json:"reason"`
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated"`
	SHA256    string `json:"sha256"`
}

func outboxRepairDigest(m OutboxMessage) string {
	h := sha256.New()

	for _, text := range []string{m.ID, m.ToRole, m.ToIdentity, m.Content, m.DeliveredVia, m.DeliveredAt, m.LastError, m.Effect, m.RequestedChannel, m.RecipientError} {
		_ = binary.Write(h, binary.LittleEndian, uint64(len(text)))
		_, _ = io.WriteString(h, text)
	}
	_ = binary.Write(h, binary.LittleEndian, [6]uint64{uint64(m.Delivered), m.CreatedSeq, uint64(m.CreatedMs), uint64(m.Attempts), uint64(m.LastAttemptMs), boolToUint(m.Parked)})
	return hex.EncodeToString(h.Sum(nil))
}
func boolToUint(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

func (s *Store) HeldMessages(ctx context.Context, after string) ([]HeldMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.QueryContext(ctx, "SELECT "+outboxColumns+" FROM outbox WHERE to_role='peer' AND delivered=0 AND recipient_error<>'' AND id>? ORDER BY id LIMIT ?", after, HeldMessagePageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages, err := scanOutbox(rows)
	if err != nil {
		return nil, err
	}
	out := make([]HeldMessage, 0, len(messages))
	for _, m := range messages {
		preview, truncated, n := m.Content, false, 0
		for offset := range m.Content {
			if n == 2000 {
				preview = m.Content[:offset]
				truncated = true
				break
			}
			n++
		}
		out = append(out, HeldMessage{ID: m.ID, Recipient: m.ToIdentity, Channel: m.RequestedChannel, Reason: m.RecipientError, Preview: strings.Clone(preview), Truncated: truncated, SHA256: outboxRepairDigest(m)})
	}
	return out, nil
}

func (s *Store) InspectHeldMessage(ctx context.Context, id string) (HeldMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.QueryContext(ctx, "SELECT "+outboxColumns+" FROM outbox WHERE id=? AND to_role='peer' AND delivered=0 AND recipient_error<>''", id)
	if err != nil {
		return HeldMessage{}, err
	}
	defer rows.Close()
	messages, err := scanOutbox(rows)
	if err != nil {
		return HeldMessage{}, err
	}
	if len(messages) != 1 {
		return HeldMessage{}, &OutboxRepairError{Requirement: "an existing held message; refresh the view"}
	}
	m := messages[0]
	return HeldMessage{ID: m.ID, Recipient: m.ToIdentity, Channel: m.RequestedChannel, Reason: m.RecipientError, Preview: m.Content, SHA256: outboxRepairDigest(m)}, nil
}

func (s *Store) RepairHeldMessage(ctx context.Context, id, expected, recipient, channel string) (retErr error) {
	if strings.TrimSpace(recipient) == "" || channel == "" {
		return &OutboxRepairError{Requirement: "an explicit recipient and channel"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			retErr = errors.Join(retErr, err)
		}
	}()
	rows, err := tx.QueryContext(ctx, "SELECT "+outboxColumns+" FROM outbox WHERE id=?", id)
	if err != nil {
		return err
	}
	messages, err := scanOutbox(rows)
	rows.Close()
	if err != nil {
		return err
	}
	if len(messages) != 1 {
		return &OutboxRepairError{Requirement: "an existing held peer message"}
	}
	m := messages[0]
	if m.ToRole != "peer" || m.RecipientError == "" || m.Delivered != 0 || m.Parked || m.Attempts != 0 || m.Effect != "" {
		return &OutboxRepairError{Requirement: "a held message with no prior delivery attempt or uncertain effect"}
	}
	if expected == "" || outboxRepairDigest(m) != expected {
		return &OutboxRepairError{Requirement: "the unchanged message shown by the current view; refresh it"}
	}
	result, err := tx.ExecContext(ctx, "UPDATE outbox SET to_identity=?,requested_channel=?,recipient_error='' WHERE id=?", recipient, channel, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return &OutboxRepairError{Requirement: "exactly the observed message to be corrected"}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("repair commit outcome needs a fresh message read; do not assume it failed: %w", err)
	}
	return nil
}
