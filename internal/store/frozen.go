package store

import (
	"context"
	"database/sql"
	"sync/atomic"
	"time"
)

type frozenState struct {
	on     atomic.Bool
	reason atomic.Value
}

type FrozenError struct{ Reason string }

func (e *FrozenError) Error() string {
	return "the projection is frozen: " + e.Reason + " (SAFE: it is queryable, not writable)"
}

func (s *Store) SetFrozen(on bool, reason string) {
	if on {
		s.frozen.reason.Store(reason)
	}
	s.frozen.on.Store(on)
}

func (s *Store) Frozen() (string, bool) {
	if !s.frozen.on.Load() {
		return "", false
	}
	r, _ := s.frozen.reason.Load().(string)
	return r, true
}

func (s *Store) frozenErr() error {
	if r, on := s.Frozen(); on {
		return &FrozenError{Reason: r}
	}
	return nil
}

type guarded struct {
	raw *sql.DB
	s   *Store
}

func (g guarded) Exec(query string, args ...interface{}) (sql.Result, error) {
	if err := g.s.frozenErr(); err != nil {
		return nil, err
	}
	return g.raw.Exec(query, args...)
}

func (g guarded) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return g.raw.Query(query, args...)
}

func (g guarded) QueryRow(query string, args ...interface{}) *sql.Row {
	return g.raw.QueryRow(query, args...)
}

func (g guarded) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if err := g.s.frozenErr(); err != nil {
		return nil, err
	}
	return g.raw.ExecContext(ctx, query, args...)
}

func (g guarded) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return g.raw.QueryContext(ctx, query, args...)
}

func (g guarded) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return g.raw.QueryRowContext(ctx, query, args...)
}

func (g guarded) Begin() (*sql.Tx, error) {
	return g.BeginTx(context.Background(), nil)
}

func (g guarded) BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	if err := g.s.frozenErr(); err != nil {
		return nil, err
	}
	return g.raw.BeginTx(ctx, options)
}

func (s *Store) w() guarded { return guarded{raw: s.db, s: s} }

func (s *Store) postureMeta(key, value string) error {

	_, err := s.db.Exec(`INSERT INTO runtime_meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
