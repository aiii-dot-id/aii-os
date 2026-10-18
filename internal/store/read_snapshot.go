package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

type readSnapshot struct {
	conn     *sql.Conn
	tx       *sql.Tx
	previous int
}

func (s *Store) beginReadSnapshot(ctx context.Context) (*readSnapshot, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	r := &readSnapshot{conn: conn}
	if err = conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&r.previous); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return nil, errors.Join(err, r.Close())
	}
	r.tx, err = conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}
	return r, nil
}
func (r *readSnapshot) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return r.tx.QueryContext(ctx, q, args...)
}
func (r *readSnapshot) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return r.tx.QueryRowContext(ctx, q, args...)
}
func (r *readSnapshot) Close() error {
	var rollback error
	if r.tx != nil {
		rollback = r.tx.Rollback()
		if errors.Is(rollback, sql.ErrTxDone) {
			rollback = nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, restore := r.conn.ExecContext(ctx, fmt.Sprintf("PRAGMA query_only=%d", r.previous))
	var current int
	verify := r.conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&current)
	if verify == nil && current != r.previous {
		verify = fmt.Errorf("read connection posture did not restore")
	}
	err := errors.Join(rollback, restore, verify)
	if err != nil {
		_ = r.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return errors.Join(err, r.conn.Close())
}
