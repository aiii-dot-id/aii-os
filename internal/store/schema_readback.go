package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) verifyCommittedSchema(parent context.Context, head *MirrorHead) (retErr error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, conn.Close()) }()
	var foreign, legacy int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreign); err != nil {
		return err
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA legacy_alter_table").Scan(&legacy); err != nil {
		return err
	}
	if foreign != 1 || legacy != 0 {
		return fmt.Errorf("replacement connection has unsafe settings")
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	s.txh, s.schemaContext = tx, ctx
	defer func() {
		s.txh, s.schemaContext = nil, nil
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			retErr = errors.Join(retErr, err)
		}
	}()
	if err := s.auditSchema(); err != nil {
		return err
	}
	if err := probeMemoryFunctions(s.h()); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	bad := rows.Next()
	readErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if bad {
		return fmt.Errorf("committed database failed foreign-key verification")
	}
	if head != nil {
		got, err := readMirrorHead(ctx, tx)
		if err != nil {
			return err
		}
		var count uint64
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM ledger").Scan(&count); err != nil {
			return err
		}
		if got != *head || count != head.Seq {
			return fmt.Errorf("committed record boundary differs from the verified source")
		}
	}
	s.activeProject, err = s.getRuntimeMeta("active_project")
	return err
}
