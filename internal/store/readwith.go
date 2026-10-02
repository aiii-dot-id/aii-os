package store

import (
	"context"
	"database/sql"
	"errors"
)

type Reader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) ReadWith(ctx context.Context, fn func(Reader) error) error {
	snap, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return err
	}
	err = fn(snap)
	return errors.Join(err, snap.Close())
}
