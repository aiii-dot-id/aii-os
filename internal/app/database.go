package app

import (
	"context"
	"fmt"
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
	state := dashboard.DatabaseState{Preferred: cfg.Identity.DBFormat,
		OptimizeLevels: cfg.Identity.DBOptimizeLevels, LearnDictionary: cfg.Identity.DBLearnDictionary}
	path := cfg.Identity.DBPath
	if view := a.databaseView.Load(); view != nil && view.store != nil {
		if view.path != "" {
			path = view.path
		}
		ctx := a.lifetime()
		format, err := view.store.DatabaseFormat(ctx)
		state.Active = format
		state.Notice = view.notice
		if err != nil {
			state.Notice = err.Error()
		} else {
			_, safe := a.SafeMode()
			state.CanExport = !safe
			var dictionaryBytes int
			dictionaryBytes, err = view.store.CompressionDictionaryBytes(ctx)
			if err != nil {
				state.Notice = err.Error()
				state.CanExport = false
			} else {
				state.DictionaryBytes = &dictionaryBytes
			}
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
