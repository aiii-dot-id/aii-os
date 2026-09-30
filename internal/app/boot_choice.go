package app

import (
	"fmt"
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
	if ev := a.identityEvidence(); ev != "" {
		return 0, fmt.Errorf("refusing FIRSTBOOT: no ledger at %s but this container holds identity evidence (%s) — an existing identity failed to load; recovery required, not a new birth", a.cfg.Identity.LedgerPath, ev)
	}
	return bootFirstboot, nil
}

func (a *App) identityEvidence() string {
	cfg := a.configSnapshot()
	var ev []string
	seen := map[string]bool{}
	note := func(kind, p string) {
		if p != "" && !seen[p] && fileExists(p) {
			seen[p] = true
			ev = append(ev, kind+" "+p)
		}
	}
	note("signing key", cfg.Identity.KeyPath)
	note("projection db", cfg.Identity.DBPath)
	note("ledger", filepath.Join("data", "ledger.jsonl"))
	note("signing key", filepath.Join("data", "identity.sec"))
	note("projection db", filepath.Join("data", "aii.db"))
	note("ledger backup", aBackupFile(a.backupsDir(cfg)))
	note("ledger backup", aBackupFile(filepath.Join("data", backupsDirName)))
	return strings.Join(ev, ", ")
}

func aBackupFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {

		if backupEvidenceRe.MatchString(e.Name()) || strings.HasPrefix(e.Name(), snapshotWorkPrefix) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}
