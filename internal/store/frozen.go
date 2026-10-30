package store

import (
	"github.com/aiii-dot-id/aii-os/internal/hold"

	"context"
	"database/sql"
	"sync/atomic"
)

type frozenState struct {
	on      atomic.Bool
	reason  atomic.Value
	waiting atomic.Bool
}

type FrozenError struct {
	Reason  string
	Waiting bool
}

func (e *FrozenError) Error() string {
	if e.Waiting {
		return "the projection is frozen: " + e.Reason + " (DEGRADED: it is queryable, and not writable " + hold.While + ")"
	}
	return "the projection is frozen: " + e.Reason + " (SAFE: it is queryable, not writable)"
}

func (s *Store) SetFrozen(on bool, reason string) { s.SetHold(on, hold.Safe(reason)) }

func (s *Store) SetHold(on bool, h hold.Hold) {
	if on {
		s.frozen.reason.Store(h.Reason)
		s.frozen.waiting.Store(h.Waiting)
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
		return &FrozenError{Reason: r, Waiting: s.frozen.waiting.Load()}
	}
	return nil
}

type guarded struct {
	raw *sql.DB
	s   *Store
}

func (g guarded) Exec(query string, args ...any) (sql.Result, error) {
	if err := g.s.frozenErr(); err != nil {
		return nil, err
	}
	return g.raw.Exec(query, args...)
}

func (g guarded) Query(query string, args ...any) (*sql.Rows, error) {
	return g.raw.Query(query, args...)
}

func (g guarded) QueryRow(query string, args ...any) *sql.Row {
	return g.raw.QueryRow(query, args...)
}

func (g guarded) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if err := g.s.frozenErr(); err != nil {
		return nil, err
	}
	return g.raw.ExecContext(ctx, query, args...)
}

func (g guarded) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return g.raw.QueryContext(ctx, query, args...)
}

func (g guarded) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
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

	return setRuntimeMeta(s.db, key, value)
}
