package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

type bootChoice int

const (
	bootLive bootChoice = iota
	bootFirstboot
)

func (a *App) chooseBoot() (bootChoice, error) {
	if fileExists(a.cfg.Identity.LedgerPath) {
		return bootLive, nil
	}

	if continuity.JournalledRestore(continuitySettingsOf(a.configSnapshot())) {
		return bootLive, nil
	}
	for _, path := range []string{a.cfg.Identity.LedgerPath, filepath.Join("data", "ledger.jsonl")} {
		if artifact, err := ledger.RecordArtifact(path); err != nil {
			return 0, fmt.Errorf("inspect identity record before FIRSTBOOT: %w", err)
		} else if artifact != "" {

			if path == a.cfg.Identity.LedgerPath {
				return bootLive, nil
			}

			return 0, a.choiceHere(fmt.Errorf("refusing FIRSTBOOT: %w: record survives at %s, and the configuration names %s; this start does not choose, and is not a new birth", ledger.ErrRecordExists, artifact, a.cfg.Identity.LedgerPath))
		}
	}

	if continuity.RestoringOnNewMachine(continuitySettingsOf(a.configSnapshot())) {
		if database, err := present(a.cfg.Identity.DBPath); err == nil && !database {
			return bootFirstboot, nil
		}
	}
	ev, err := a.identityEvidence()
	if err != nil {
		return 0, fmt.Errorf("refusing FIRSTBOOT: %w — whether an identity already lives here cannot be told, so none is born over it", err)
	}

	if ev != "" && a.continuity.HoldsRestoreSource(continuitySettingsOf(a.configSnapshot())) {
		return bootLive, nil
	}
	if ev != "" {

		return 0, a.choiceHere(fmt.Errorf("refusing FIRSTBOOT: no ledger at %s but this container holds identity evidence (%s) — an existing identity failed to load, and there is no record here to start it from; not a new birth", a.cfg.Identity.LedgerPath, ev))
	}
	return bootFirstboot, nil
}

func (a *App) identityEvidence() (string, error) {
	cfg := a.configSnapshot()
	var ev []string
	var unknown error
	seen := map[string]bool{}
	note := func(kind, p string) {
		if p == "" || seen[p] {
			return
		}
		found, err := present(p)
		if err != nil {
			unknown = errors.Join(unknown, fmt.Errorf("could not inspect the %s at %s: %w", kind, p, err))
			return
		}
		if found {
			seen[p] = true
			ev = append(ev, kind+" "+p)
		}
	}
	note("signing key", cfg.Identity.KeyPath)
	note("projection db", cfg.Identity.DBPath)
	note("ledger", filepath.Join("data", "ledger.jsonl"))
	note("signing key", filepath.Join("data", "identity.sec"))
	note("projection db", filepath.Join("data", "aii.db"))
	for _, dir := range []string{a.backupsDir(cfg), filepath.Join("data", continuity.BackupsDirName)} {
		backup, err := aBackupFile(dir)
		if err != nil {
			unknown = errors.Join(unknown, err)
			continue
		}
		note("ledger backup", backup)
	}
	for _, dir := range []string{filepath.Dir(cfg.Identity.LedgerPath), "data"} {
		set, err := continuity.SetAsideIn(dir)
		if err != nil {
			unknown = errors.Join(unknown, err)
			continue
		}
		note("set kept aside", set)
	}
	return strings.Join(ev, ", "), unknown
}

func (a *App) backupsDir(cfg Config) string {
	return a.continuity.BackupsDir(continuitySettingsOf(cfg))
}

func present(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func aBackupFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("could not list the backups in %s: %w", dir, err)
	}
	for _, e := range entries {

		if continuity.IsBackupEvidence(e.Name()) {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", nil
}

func (a *App) choiceHere(refusal error) error {
	cfg := a.configSnapshot()
	home, err := filepath.Abs(".")
	if err != nil {
		return errors.Join(refusal, err)
	}
	c := IdentityChoice{Home: home, Why: refusal.Error()}
	for _, f := range []struct {
		key, was string
		here     func(string) bool
	}{
		{"ledger_path", cfg.Identity.LedgerPath, func(p string) bool { found, err := ledger.RecordArtifact(p); return err == nil && found != "" }},
		{"db_path", cfg.Identity.DBPath, fileExists},
		{"key_path", cfg.Identity.KeyPath, fileExists},
	} {
		files := IdentityFiles{Key: f.key, Was: f.was}
		seen := map[string]bool{}
		for _, p := range []string{f.was, filepath.Join("data", filepath.Base(f.was))} {
			abs, err := filepath.Abs(p)
			if err != nil || seen[abs] || !f.here(p) {
				continue
			}
			seen[abs] = true
			files.Found = append(files.Found, abs)
		}
		c.Files = append(c.Files, files)
	}
	return &IdentityChoiceRefusal{Choice: c, err: refusal}
}
