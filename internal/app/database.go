package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type databaseView struct {
	store  *store.Store
	path   string
	notice string
}

func (a *App) activateDatabaseFormat(ctx context.Context, cfg Config, current *store.Store) (*store.Store, error) {
	if _, safe := a.SafeMode(); safe {
		return current, nil
	}
	path, err := filepath.Abs(cfg.Identity.DBPath)
	if err != nil {
		return current, fmt.Errorf("resolve opened database path: %w", err)
	}
	result, err := current.ActivateFormatAtStartup(ctx, cfg.Identity.DBPath, cfg.Identity.DBFormat)
	if result.Opened == nil {
		a.store = nil
		return nil, fmt.Errorf("database format activation could not establish a usable store; recovery at %s: %w", result.Recovery, err)
	}
	view := &databaseView{store: result.Opened, path: path}
	if err != nil {
		view.notice = err.Error()
		logsink.Warn("store.error", "database format preference not fully applied: %v", err)
	}
	a.databaseView.Store(view)
	return result.Opened, nil
}

func (a *App) databaseState() dashboard.DatabaseState {
	cfg := a.configSnapshot()
	state := dashboard.DatabaseState{Preferred: cfg.Identity.DBFormat}
	path := cfg.Identity.DBPath
	if view := a.databaseView.Load(); view != nil && view.store != nil {
		if view.path != "" {
			path = view.path
		}
		format, err := view.store.DatabaseFormat(context.Background())
		state.Active = format
		state.Notice = view.notice
		if err != nil {
			state.Notice = err.Error()
		} else {
			_, safe := a.SafeMode()
			state.CanExport = !safe
		}
	}
	paths, err := store.FormatRecoveryDirectories(path)
	state.Recovery = paths
	if err != nil {
		if state.Notice != "" {
			state.Notice += "; "
		}
		state.Notice += err.Error()
	}
	return state
}

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

func (a *App) exportDatabase(ctx context.Context) (io.ReadCloser, int64, error) {
	if _, safe := a.SafeMode(); safe {
		return nil, 0, fmt.Errorf("database export makes a file and is unavailable in SAFE")
	}
	if !a.maintMu.TryLock() {
		return nil, 0, fmt.Errorf("maintenance is already running")
	}
	defer a.maintMu.Unlock()
	view := a.databaseView.Load()
	if view == nil || view.store == nil || view.path == "" {
		return nil, 0, fmt.Errorf("no active database to export")
	}
	dir, err := os.MkdirTemp(filepath.Dir(view.path), ".db-convert-export-")
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(dir, "export.db")
	if published, err := store.ConvertDatabase(ctx, view.path, path, false); err != nil || !published {
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
