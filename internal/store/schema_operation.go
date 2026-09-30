package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

type SchemaError struct {
	Phase string
	Cause error
}

func (e *SchemaError) Error() string { return "schema " + e.Phase + ": " + e.Cause.Error() }
func (e *SchemaError) Unwrap() error { return e.Cause }

type SchemaOutcome string

const (
	SchemaUnchanged SchemaOutcome = "unchanged"
	SchemaCommitted SchemaOutcome = "committed"
	SchemaUnknown   SchemaOutcome = "readback required"
)

type SchemaFailure struct {
	Outcome SchemaOutcome
	Cause   error
}

func (e *SchemaFailure) Error() string {
	return "schema outcome " + string(e.Outcome) + ": " + e.Cause.Error()
}
func (e *SchemaFailure) Unwrap() error { return e.Cause }

type ReplayRequiredError struct{ Table string }

func (e *ReplayRequiredError) Error() string {
	return "schema repair of " + e.Table + " requires verified record replay"
}

type contextQueries interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type boundStatements struct {
	contextQueries
	ctx context.Context
}

func (b boundStatements) Exec(q string, args ...any) (sql.Result, error) {
	return b.contextQueries.ExecContext(b.ctx, q, args...)
}
func (b boundStatements) Query(q string, args ...any) (*sql.Rows, error) {
	return b.contextQueries.QueryContext(b.ctx, q, args...)
}
func (b boundStatements) QueryRow(q string, args ...any) *sql.Row {
	return b.contextQueries.QueryRowContext(b.ctx, q, args...)
}
func (b boundStatements) ExecContext(_ context.Context, q string, args ...any) (sql.Result, error) {
	return b.contextQueries.ExecContext(b.ctx, q, args...)
}
func (b boundStatements) QueryContext(_ context.Context, q string, args ...any) (*sql.Rows, error) {
	return b.contextQueries.QueryContext(b.ctx, q, args...)
}
func (b boundStatements) QueryRowContext(_ context.Context, q string, args ...any) *sql.Row {
	return b.contextQueries.QueryRowContext(b.ctx, q, args...)
}

func (s *Store) withSchema(ctx context.Context, target string, apply func() error) (retErr error) {
	outcome := SchemaUnchanged
	defer func() {
		if retErr != nil {
			retErr = &SchemaFailure{Outcome: outcome, Cause: retErr}
		}
	}()
	if err := s.frozenErr(); err != nil {
		return err
	}
	if s.txh != nil {
		return fmt.Errorf("schema operation cannot nest a transaction")
	}
	ref, err := referenceSchema(target)
	if err != nil {
		return &SchemaError{Phase: "target", Cause: err}
	}
	if err := validateSchemaAnnotations(target, ref); err != nil {
		return &SchemaError{Phase: "target", Cause: err}
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	discarded := false
	discard := func() { discarded = true; _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	var cookie, after, fk, legacy int
	if err := conn.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&cookie); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, `SELECT name,COALESCE(sql,'') FROM sqlite_schema WHERE type='table'`)
	if err != nil {
		return err
	}
	live := map[string]string{}
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			rows.Close()
			return err
		}
		live[sqliteName(name)] = statement
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	views := map[string]bool{}
	for name := range ref.Objects["view"] {
		views[name] = true
	}
	viewRows, err := conn.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='view'")
	if err != nil {
		return err
	}
	var sourceViews []string
	for viewRows.Next() {
		var name string
		if e := viewRows.Scan(&name); e != nil {
			viewRows.Close()
			return e
		}
		sourceViews = append(sourceViews, name)
	}
	err = viewRows.Err()
	viewRows.Close()
	if err != nil {
		return err
	}
	for _, name := range sourceViews {
		if probe, e := conn.QueryContext(ctx, "SELECT * FROM "+quoteIdentifier(name)+" LIMIT 0"); e == nil {
			probe.Close()
			views[name] = true
		}
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&after); err != nil {
		return err
	}
	if cookie != after {
		return &SchemaError{"preflight", errors.New("source catalog changed during inspection")}
	}
	replace := false
	for name, statement := range ref.Objects["table"] {
		if ref.Tables[name].Kind == "table" && live[name] != "" && canonDDL(live[name]) != canonDDL(statement) {
			replace = true
		}
	}
	for _, name := range declaredRetiredTables(target) {
		if live[name] != "" {
			replace = true
		}
	}
	for _, name := range declaredDerivedPredecessors(target) {
		if live[name] != "" {
			replace = true
		}
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return err
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA legacy_alter_table").Scan(&legacy); err != nil {
		return err
	}
	if replace {
		defer func() {
			if discarded {
				return
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_, e1 := conn.ExecContext(cleanup, fmt.Sprintf("PRAGMA legacy_alter_table=%d", legacy))
			_, e2 := conn.ExecContext(cleanup, fmt.Sprintf("PRAGMA foreign_keys=%d", fk))
			var restoredFK, restoredLegacy int
			e3 := conn.QueryRowContext(cleanup, "PRAGMA foreign_keys").Scan(&restoredFK)
			e4 := conn.QueryRowContext(cleanup, "PRAGMA legacy_alter_table").Scan(&restoredLegacy)
			if e3 == nil && e4 == nil && (restoredFK != fk || restoredLegacy != legacy) {
				e3 = errors.New("connection settings did not restore")
			}
			if err := errors.Join(e1, e2, e3, e4); err != nil {
				discard()
				retErr = errors.Join(retErr, &SchemaError{"connection cleanup", err})
			}
		}()
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "PRAGMA legacy_alter_table=ON"); err != nil {
			return err
		}
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	s.txh, s.schemaContext = tx, ctx
	defer func() {
		s.txh, s.schemaContext = nil, nil
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			outcome = SchemaUnknown
			discard()
			retErr = errors.Join(retErr, &SchemaError{"rollback", err})
		}
	}()
	if err := tx.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&after); err != nil {
		return err
	}
	if cookie != after {
		return &SchemaError{"preflight", errors.New("source catalog changed before apply")}
	}
	if !replace {
		if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); err != nil {
			return err
		}
	}
	if err := apply(); err != nil {
		return &SchemaError{"apply", err}
	}
	for _, name := range sortedKeys(views) {
		probe, err := tx.QueryContext(ctx, "SELECT * FROM "+quoteIdentifier(name)+" LIMIT 0")
		if err != nil {
			return &SchemaError{"verify view " + name, err}
		}
		if err := probe.Close(); err != nil {
			return err
		}
	}
	if replace {
		rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		bad := rows.Next()
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if bad {
			return &SchemaError{"verify", errors.New("foreign key check failed")}
		}
	}
	outcome = SchemaUnknown
	if err := tx.Commit(); err != nil {
		discard()
		return &SchemaError{"commit", err}
	}
	outcome = SchemaCommitted
	return nil
}
