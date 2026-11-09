package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ErrDeliveryNeverComes struct{ Why string }

func (e *ErrDeliveryNeverComes) Error() string { return e.Why }

func (s *Store) DeliveryNeverComes(ctx context.Context, sessionID, resumesOn string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deliveryNeverComes(ctx, s.db, sessionID, resumesOn)
}

type deliveryQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) deliveryNeverComes(ctx context.Context, q deliveryQuerier, sessionID, resumesOn string) error {
	child, ok := strings.CutPrefix(strings.TrimSpace(resumesOn), "delivery:")
	if !ok {
		return nil
	}
	if child == sessionID {
		return &ErrDeliveryNeverComes{fmt.Sprintf("%s is this work session; its own delivery cannot resume it", child)}
	}
	test, args, err := s.childTest()
	if err != nil {
		return err
	}
	var status string
	var harvested int64
	var isChild bool
	err = q.QueryRowContext(ctx, `SELECT status, COALESCE(harvested_ms, 0), `+test+` FROM work_sessions WHERE id = ?`, append(args, child)...).Scan(&status, &harvested, &isChild)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return &ErrDeliveryNeverComes{fmt.Sprintf("no work session %s exists", child)}
	case err != nil:
		return fmt.Errorf("read the session a delivery is awaited from: %w", err)
	case !isChild:
		return &ErrDeliveryNeverComes{fmt.Sprintf("%s is not a sub-agent's session; only a sub-agent delivers", child)}
	case status == "delivered" && harvested > 0:
		return &ErrDeliveryNeverComes{fmt.Sprintf("%s's delivery was already taken in, at %s; nothing more will come from it", child, time.UnixMilli(harvested).UTC().Format(time.RFC3339))}
	case status != "active" && status != "delivered":
		return &ErrDeliveryNeverComes{fmt.Sprintf("%s ended without delivering (%s); nothing will come from it", child, status)}
	}
	return nil
}
