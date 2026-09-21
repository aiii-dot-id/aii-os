package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

// .
// .
// .
// .
// .
// .
// .
func ConvertDatabase(ctx context.Context, source, destination string, compressed bool) (published bool, retErr error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := compressvfs.Register(); err != nil {
		return false, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return false, fmt.Errorf("destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := checkNoDatabaseSidecars(destination); err != nil {
		return false, err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".db-convert-")
	if err != nil {
		return false, err
	}
	defer func() { retErr = errors.Join(retErr, os.RemoveAll(staging)) }()
	image := filepath.Join(staging, "image.db")
	if err := snapshotDatabase(ctx, source, image); err != nil {
		return false, err
	}
	candidate := image
	if compressed {
		candidate = filepath.Join(staging, "compressed.db")
		if err := encodeDatabaseImage(ctx, image, candidate); err != nil {
			return false, err
		}
	}
	if err := checkDatabaseImage(ctx, candidate); err != nil {
		return false, err
	}
	file, err := os.OpenFile(candidate, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	if err := file.Chmod(0600); err != nil {
		return false, errors.Join(err, file.Close())
	}
	err = errors.Join(file.Sync(), file.Close())
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return atomicfile.PublishNew(candidate, destination)
}

func snapshotDatabase(ctx context.Context, source, destination string) (retErr error) {
	db, err := sql.Open("sqlite", databaseURI(source)+"?vfs="+compressvfs.Name+"&mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, db.Close()) }()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	defer func() {
		_, err := conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		retErr = errors.Join(retErr, err)
	}()
	var schemas int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&schemas); err != nil {
		return fmt.Errorf("pin database image: %w", err)
	}
	return backupConnection(ctx, conn, databaseURI(destination))
}

func encodeDatabaseImage(ctx context.Context, source, destination string) (retErr error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, in.Close()) }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, out.Close()) }()
	return compressvfs.EncodeImage(ctx, in, info.Size(), out)
}

func checkDatabaseImage(ctx context.Context, path string) (retErr error) {
	db, err := sql.Open("sqlite", databaseURI(path)+"?vfs="+compressvfs.Name+"&mode=ro&immutable=1&_pragma=query_only(1)")
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, db.Close()) }()
	db.SetMaxOpenConns(1)
	if err := pragmaClean(ctx, db, "integrity_check", "ok"); err != nil {
		return err
	}
	return pragmaClean(ctx, db, "foreign_key_check", "")
}
