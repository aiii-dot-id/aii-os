package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

// .
// .
// .
// .
type FormatActivation struct {
	Opened    *Store
	Published bool
	Recovery  string
}

func ValidDatabaseFormat(format string) bool {
	return format == "" || format == compressvfs.FormatSQLite || format == compressvfs.FormatZstd
}

// .
// .
// .
func (s *Store) ActivateFormatAtStartup(ctx context.Context, path, target string) (out FormatActivation, retErr error) {
	return s.activateFormat(ctx, path, target, func(from, to string) (bool, error) {
		return atomicfile.Replace(from, to)
	})
}

// .
// .
func (s *Store) activateFormat(ctx context.Context, path, target string, publish func(string, string) (bool, error)) (out FormatActivation, retErr error) {
	out.Opened = s
	if !ValidDatabaseFormat(target) {
		return out, fmt.Errorf("unsupported database format %q", target)
	}
	if target == "" {
		return out, nil
	}
	current, err := s.DatabaseFormat(ctx)
	if err != nil || current == target {
		return out, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return out, fmt.Errorf("format change requires a writable startup store")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return out, err
	}
	if !info.Mode().IsRegular() {
		return out, fmt.Errorf("format change requires a regular database path")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return out, err
	}
	s.db.SetMaxIdleConns(0)
	defer s.db.SetMaxIdleConns(storeIdleConnections)
	defer func() {
		if conn != nil {
			retErr = errors.Join(retErr, conn.Close())
			if out.Opened == nil {
				retErr = errors.Join(retErr, s.db.Close())
			}
		}
	}()
	if s.db.Stats().InUse != 1 {
		return out, fmt.Errorf("database still has borrowed connections: %w", syscall.EBUSY)
	}
	var seq int
	var name, openedPath string
	if err := conn.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &openedPath); err != nil {
		return out, err
	}
	openedInfo, err := os.Stat(openedPath)
	if err != nil || !os.SameFile(info, openedInfo) {
		return out, fmt.Errorf("format target is not the opened database")
	}
	work, err := os.MkdirTemp(filepath.Dir(path), formatWorkPrefix(path))
	if err != nil {
		return out, err
	}
	original, candidate := filepath.Join(work, "original.db"), filepath.Join(work, "candidate.db")
	out.Recovery = original
	defer func() {
		if out.Opened != nil {
			if err := os.RemoveAll(work); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("remove conversion working material %s: %w", work, err))
			} else {
				out.Recovery = ""
			}
		}
	}()
	head, facts, err := prepareFormatImage(ctx, conn, original, candidate, target)
	if err != nil {
		// .
		if restoreErr := restoreStoreMode(context.WithoutCancel(ctx), conn); restoreErr != nil {
			out.Opened = nil
			return out, errors.Join(err, restoreErr)
		}
		return out, err
	}
	// .
	// .
	out.Opened = nil
	closeErr := conn.Close()
	conn = nil
	closeErr = errors.Join(closeErr, s.db.Close())
	if closeErr != nil {
		return out, closeErr
	}
	reopenOriginal := func(cause error) (FormatActivation, error) {
		old, err := New(path)
		out.Opened = old
		return out, errors.Join(cause, err)
	}
	if err := checkNoDatabaseSidecars(path); err != nil {
		return reopenOriginal(err)
	}
	if err := ctx.Err(); err != nil {
		return reopenOriginal(err)
	}
	out.Published, err = publish(candidate, path)
	if err != nil {
		if !out.Published {
			return reopenOriginal(err)
		}
		return out, fmt.Errorf("database replacement durability uncertain; recovery retained at %s: %w", original, err)
	}
	opened, err := New(path)
	if err != nil {
		return out, err
	}
	actual, err := opened.DatabaseFormat(ctx)
	if err == nil && actual != target {
		err = fmt.Errorf("active database format %q differs from %q", actual, target)
	}
	if err == nil {
		err = opened.QuickCheck()
	}
	if err == nil {
		err = opened.ForeignKeyCheck()
	}
	if err == nil {
		var got MirrorHead
		got, err = readMirrorHead(ctx, opened.db)
		if err == nil && got != head {
			err = fmt.Errorf("activated database mirror differs from prepared source")
		}
	}
	if err == nil {
		var got RuntimeFacts
		got, err = readRuntimeFacts(ctx, opened.db)
		if err == nil {
			if difference := facts.Differs(got); difference != "" {
				err = fmt.Errorf("activated database runtime differs: %s", difference)
			}
		}
	}
	if err != nil {
		// .
		return out, errors.Join(err, opened.db.Close())
	}
	out.Opened = opened
	return out, nil
}

func prepareFormatImage(ctx context.Context, conn *sql.Conn, original, candidate, target string) (head MirrorHead, facts RuntimeFacts, retErr error) {
	var busy, logPages, checkpointed int
	if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logPages, &checkpointed); err != nil {
		return head, facts, err
	}
	if busy != 0 {
		return head, facts, fmt.Errorf("database checkpoint has active readers: %w", syscall.EBUSY)
	}
	var mode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil {
		return head, facts, err
	}
	if mode != "delete" {
		return head, facts, fmt.Errorf("database did not leave WAL mode: %w", syscall.EBUSY)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA locking_mode=EXCLUSIVE; BEGIN EXCLUSIVE; COMMIT"); err != nil {
		return head, facts, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return head, facts, err
	}
	defer func() {
		_, err := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		retErr = errors.Join(retErr, err)
	}()
	head, retErr = readMirrorHead(ctx, conn)
	if retErr != nil {
		return
	}
	facts, retErr = readRuntimeFacts(ctx, conn)
	if retErr != nil {
		return
	}
	if err := backupConnection(ctx, conn, databaseURI(original)); err != nil {
		return head, facts, err
	}
	if err := checkDatabaseImage(ctx, original); err != nil {
		return head, facts, err
	}
	// .
	f, err := os.OpenFile(original, os.O_RDWR, 0)
	if err != nil {
		return head, facts, err
	}
	err = errors.Join(f.Chmod(0600), f.Sync(), f.Close())
	if err != nil {
		return head, facts, err
	}
	if err := atomicfile.SyncDir(filepath.Dir(original)); err != nil {
		return head, facts, err
	}
	if err := atomicfile.SyncDir(filepath.Dir(filepath.Dir(original))); err != nil {
		return head, facts, err
	}
	if published, err := ConvertDatabase(ctx, original, candidate, target == compressvfs.FormatZstd); err != nil || !published {
		return head, facts, errors.Join(err, fmt.Errorf("candidate was not prepared durably"))
	}
	return
}

func restoreStoreMode(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "PRAGMA locking_mode=NORMAL; BEGIN; COMMIT"); err != nil {
		return err
	}
	var mode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return fmt.Errorf("original database did not return to WAL mode")
	}
	return nil
}

func formatWorkPrefix(path string) string { return "." + filepath.Base(path) + ".format-" }

// .
// .
func FormatRecoveryDirectories(path string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var found []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), formatWorkPrefix(path)) {
			found = append(found, filepath.Join(filepath.Dir(path), entry.Name()))
		}
	}
	return found, nil
}
