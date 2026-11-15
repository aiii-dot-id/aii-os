package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type MissingEventTargetError struct {
	Event    ledger.EventType
	Kind, ID string
}

func (e *MissingEventTargetError) Error() string {
	return fmt.Sprintf("%s cites unknown %s %q — signed event applies to nothing", e.Event, e.Kind, e.ID)
}

type UnknownEventTypeError struct{ Type ledger.EventType }

func (e *UnknownEventTypeError) Error() string {
	return fmt.Sprintf("unknown event type: %s", e.Type)
}

type ReplayRefusedError struct {
	Seq  uint64
	Type ledger.EventType
	Err  error
}

func (e *ReplayRefusedError) Error() string {
	return fmt.Sprintf("materialize failed at seq %d: %v", e.Seq, e.Err)
}

func (e *ReplayRefusedError) Unwrap() error { return e.Err }

func ruleRefused(err error) bool {
	var e *sqlite.Error
	if errors.As(err, &e) {
		return e.Code()&0xff == sqlite3.SQLITE_CONSTRAINT
	}
	return !errors.Is(err, sql.ErrTxDone) && !errors.Is(err, sql.ErrConnDone) && !errors.Is(err, driver.ErrBadConn) &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

type ReplayBoundaryError struct{ Reason string }

func (e *ReplayBoundaryError) Error() string { return e.Reason }

func MayPass(err error) bool {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code() & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return true
	}
	return false
}

func Fault(err error) bool {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code() & 0xff {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED, sqlite3.SQLITE_IOERR, sqlite3.SQLITE_FULL,
		sqlite3.SQLITE_CANTOPEN, sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOMEM,
		sqlite3.SQLITE_READONLY, sqlite3.SQLITE_NOTADB:
		return true
	}
	return false
}

func (s *Store) Fault(err error) bool { return Fault(err) }
