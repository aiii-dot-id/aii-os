package continuity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type databaseDownload struct {
	*os.File
	directory string
}

func (d *databaseDownload) Close() error {
	err := errors.Join(d.File.Close(), os.RemoveAll(d.directory))
	if err != nil {
		logsink.Warn("store.error", "database export cleanup: %v", err)
	}
	return err
}

func (r *Runtime) ExportDatabase(ctx context.Context) (io.ReadCloser, int64, error) {
	if r.w.Safe() {
		return nil, 0, fmt.Errorf("database export makes a file and is unavailable in SAFE")
	}
	if !r.maint.mu.TryLock() {
		return nil, 0, fmt.Errorf("maintenance is already running")
	}
	defer r.maint.mu.Unlock()
	opened := r.w.DatabasePath()
	if opened == "" {
		return nil, 0, fmt.Errorf("no active database to export")
	}
	dir, err := os.MkdirTemp(filepath.Dir(opened), ".db-convert-export-")
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(dir, "export.db")
	if published, err := store.ConvertDatabase(ctx, opened, path, false); err != nil || !published {
		if err == nil {
			err = fmt.Errorf("database export was not published")
		}
		return nil, 0, errors.Join(err, os.RemoveAll(dir))
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, errors.Join(err, os.RemoveAll(dir))
	}
	info, err := file.Stat()
	if err != nil {
		return nil, 0, errors.Join(err, file.Close(), os.RemoveAll(dir))
	}
	return &databaseDownload{File: file, directory: dir}, info.Size(), nil
}
