package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// bootChoice is the runtime's first decision: whether this container
// holds an identity to resume or a blank world to birth into.
type bootChoice int

const (
	bootLive bootChoice = iota
	bootFirstboot
)

// chooseBoot resolves FIRSTBOOT vs LIVE — with one refusal between
// them. FIRSTBOOT mints an identity, so it must never run over an
// existing one: a signing key or a projection db without a readable
// ledger is not a first boot, it is an identity whose record failed to
// resolve (a moved mobile container, a lost file). Booting a new
// identity there forks the resident (Sev 2026-08-26, P0). Recovery is
// an operator act; the runtime's whole duty here is to refuse.
func (a *App) chooseBoot() (bootChoice, error) {
	if fileExists(a.cfg.Identity.LedgerPath) {
		return bootLive, nil
	}
	if ev := a.identityEvidence(); ev != "" {
		return 0, fmt.Errorf("refusing FIRSTBOOT: no ledger at %s but this container holds identity evidence (%s) — an existing identity failed to load; recovery required, not a new birth", a.cfg.Identity.LedgerPath, ev)
	}
	return bootFirstboot, nil
}

// identityEvidence lists files that prove an identity already lives
// here even when the ledger does not resolve. Empty means a genuinely
// blank container — the only ground FIRSTBOOT may build on.
//
// The configured paths are checked first; the STANDARD layout is
// evidence in its own right after them — a config that lost its
// identity paths (quarantined on mobile, hand-damaged on desktop) must
// not let FIRSTBOOT build over what actually lives in this world dir
// (D02 residual, Sev 2026-08-26). Relative, like the defaults: the
// one-directory law makes the working directory the world.
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

// aBackupFile returns one ledger copy from dir, or "" when it holds none.
//
// The maintenance pass keeps up to eight FULL ledger copies here — a
// layer that landed AFTER the evidence rule was written and was never
// re-audited against it. So an operator who deletes the three obvious
// files, or a restore that stops halfway, leaves the previous resident's
// ENTIRE record in the container while every other probe reports a blank
// world, and FIRSTBOOT mints a second identity over a recoverable one
// (Beta journey #3). WHICH copy is newest is maintenance's question and
// maintenance's rule (newestBackupSeq, which reads the seq as a number);
// the refusal only has to prove a record survives and say where it is.
func aBackupFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if backupSeqRe.MatchString(e.Name()) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}
