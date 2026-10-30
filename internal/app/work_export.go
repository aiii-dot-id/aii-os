package app

import (
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/workrecord"
)

func (a *App) exportWork(ctx context.Context, since time.Time, session, project string, w io.Writer) error {
	st, err := recordSource{a}.store()
	if err != nil {
		return err
	}
	var projects workrecord.Projects
	if a.projects != nil {
		projects = a.projects
	}
	_, err = workrecord.Write(ctx, w, st, projects, workrecord.Filter{Since: since, Session: session, Project: project}, time.Now())
	return err
}

func WorkRecordPlaces(cfg *Config, home string) (db, recordLock, projects string) {
	in := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(home, p)
	}
	return in(cfg.Identity.DBPath), filepath.Join(filepath.Dir(in(cfg.Identity.LedgerPath)), ledger.LedgerDirLockName), in(projectsRoot(*cfg))
}

func projectsRoot(cfg Config) string {
	if cfg.Projects.Root != "" {
		return cfg.Projects.Root
	}
	return filepath.Join(cfg.Tools.CWD, "projects")
}
