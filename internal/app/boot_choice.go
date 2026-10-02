package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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

	if journalledRestore(a.configSnapshot()) {
		return bootLive, nil
	}

	if restoringOnNewMachine(a.configSnapshot()) {
		return bootFirstboot, nil
	}
	for _, path := range []string{a.cfg.Identity.LedgerPath, filepath.Join("data", "ledger.jsonl")} {
		if artifact, err := ledger.RecordArtifact(path); err != nil {
			return 0, fmt.Errorf("inspect identity record before FIRSTBOOT: %w", err)
		} else if artifact != "" {

			if path == a.cfg.Identity.LedgerPath && fileExists(a.cfg.Identity.KeyPath) {
				return bootLive, nil
			}
			return 0, fmt.Errorf("refusing FIRSTBOOT: %w: record survives at %s — recovery required, not a new birth", ledger.ErrRecordExists, artifact)
		}
	}
	ev, err := a.identityEvidence()
	if err != nil {
		return 0, fmt.Errorf("refusing FIRSTBOOT: %w — whether an identity already lives here cannot be told, so none is born over it", err)
	}
	if ev != "" {
		return 0, fmt.Errorf("refusing FIRSTBOOT: no ledger at %s but this container holds identity evidence (%s) — an existing identity failed to load; recovery required, not a new birth", a.cfg.Identity.LedgerPath, ev)
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
	for _, dir := range []string{a.backupsDir(cfg), filepath.Join("data", backupsDirName)} {
		backup, err := aBackupFile(dir)
		if err != nil {
			unknown = errors.Join(unknown, err)
			continue
		}
		note("ledger backup", backup)
	}
	return strings.Join(ev, ", "), unknown
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

		if backupEvidenceRe.MatchString(e.Name()) || strings.HasPrefix(e.Name(), snapshotWorkPrefix) {
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", nil
}
