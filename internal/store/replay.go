package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

type EventSource func(yield func(*ledger.Event) error) error

func EventSlice(events []ledger.Event) EventSource {
	return func(yield func(*ledger.Event) error) error {
		for i := range events {
			if err := yield(&events[i]); err != nil {
				return err
			}
		}
		return nil
	}
}

func (s *Store) ReplayAll(source EventSource) (retErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.w().Begin()
	if err != nil {
		return fmt.Errorf("begin replay transaction: %w", err)
	}
	s.txh = tx
	committed := false
	defer func() {
		s.txh = nil
		if !committed {
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				retErr = errors.Join(retErr, fmt.Errorf("rollback projection rebuild: %w", err))
			} else if err == nil && retErr != nil {
				retErr = fmt.Errorf("%w (rebuild rolled back — the prior projection stands)", retErr)
			}
		}
	}()

	if err := s.replayInTransaction(source, nil); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("publish rebuilt projection: %w", err)
	}
	committed = true
	return nil
}

func (s *Store) replayInTransaction(source EventSource, expected *MirrorHead) error {

	if _, err := s.h().Exec("PRAGMA defer_foreign_keys = ON"); err != nil {
		return fmt.Errorf("defer foreign keys for replay: %w", err)
	}

	for _, table := range DerivedTables() {
		if _, err := s.h().Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}

	var last MirrorHead
	if source != nil {
		if err := source(func(evt *ledger.Event) error {
			if expected != nil && evt.Seq != last.Seq+1 {
				return fmt.Errorf("replay sequence %d does not follow %d", evt.Seq, last.Seq)
			}
			last = MirrorHead{Seq: evt.Seq, Hash: evt.EntryHash()}
			if err := s.materializeLocked(evt, true); err != nil {
				return fmt.Errorf("materialize failed at seq %d: %w", evt.Seq, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}

	if expected != nil && last != *expected {
		return fmt.Errorf("replay terminal boundary differs from verified record")
	}
	if expected != nil {
		actual, err := readMirrorHead(s.schemaContext, s.txh)
		if err != nil {
			return err
		}
		var rows uint64
		if err := s.h().QueryRow("SELECT COUNT(*) FROM ledger").Scan(&rows); err != nil {
			return err
		}
		if actual != *expected || rows != expected.Seq {
			return fmt.Errorf("materialized mirror differs from verified record")
		}
	}
	return nil
}

func (s *Store) ReplayFromFile(ledgerPath string) error {
	return s.ReplayAll(func(yield func(*ledger.Event) error) error {
		if err := ledger.Stream(ledgerPath, yield); err != nil {
			return fmt.Errorf("read ledger: %w", err)
		}
		return nil
	})
}
