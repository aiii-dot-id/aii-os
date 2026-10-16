package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

func docSeedKey(normalize func([]byte) []byte, b []byte) string {
	if normalize != nil {
		b = normalize(b)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

const sidecarSuffix = ".new"

func publishDoc(path string, data []byte, label string) bool {
	published, err := atomicfile.WriteReplace(path, data, 0o644)
	if err != nil {
		if !published {
			logsink.Warn("seed.error", "%s: publish %s: %v", label, path, err)
			return false
		}

		logsink.Warn("seed.error", "%s: published %s; directory sync failed (durability not proven): %v", label, path, err)
	}
	return true
}

func seedDoc(path string, want []byte, normalize func([]byte) []byte, shipped []string, record, label string) {
	sidecar := path + sidecarSuffix
	wantKey := docSeedKey(normalize, want)
	recorded := readSeedRecord(record, label)
	writing := []string{wantKey}
	cur, err := os.ReadFile(path)
	switch {
	case err == nil:
		if bytes.Equal(cur, want) {
			retireSidecar(sidecar, label)

			noteSeedRecord(record, recorded, writing, label)
			return
		}
		key := docSeedKey(normalize, cur)
		if !slices.Contains(recorded, key) && !slices.Contains(shipped, key) {

			if prev, err := os.ReadFile(sidecar); err == nil && bytes.Equal(prev, want) {
				return
			}
			if publishDoc(sidecar, want, label) {
				logsink.Info("seed.decision", "%s: %s is the identity's own — left the current platform version beside it at %s", label, path, sidecar)
			}
			return
		}
		if !slices.Contains(shipped, key) {
			logsink.Info("seed.decision", "%s: %s holds what the platform last wrote there (%s names it), a version no shipped key names — upgrading it", label, path, record)
		}
		if key != wantKey {

			writing = []string{key, wantKey}
		}
	case !os.IsNotExist(err):
		logsink.Warn("seed.error", "%s: unreadable, not seeding: %v", label, err)
		return
	}
	recorded = noteSeedRecord(record, recorded, writing, label)
	if publishDoc(path, want, label) {
		logsink.Info("seed.end", "%s: seeded %d bytes into %s", label, len(want), path)
		noteSeedRecord(record, recorded, []string{wantKey}, label)
		retireSidecar(sidecar, label)
	}
}

const seedRecordDirName = "seeded-docs"

const seedRecordSuffix = ".seedkey"

const overlayRecordName = uiOverlayDirName + "-README.md"

func (a *App) seedRecordPath(name string) string {
	ledger := a.configSnapshot().Identity.LedgerPath
	if ledger == "" {
		return ""
	}
	return filepath.Join(identityDataDir(ledger), seedRecordDirName, name+seedRecordSuffix)
}

func readSeedRecord(path, label string) []string {
	if path == "" {
		return nil
	}
	b, err := atomicfile.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logsink.Warn("seed.error", "%s: the record of what the platform wrote, %s, is unreadable; the shipped keys alone decide: %v", label, path, err)
		}
		return nil
	}
	var keys []string
	for _, line := range strings.Split(string(b), "\n") {
		if len(line) == 2*sha256.Size && strings.Trim(line, "0123456789abcdef") == "" {
			keys = append(keys, line)
		}
	}
	return keys
}

func noteSeedRecord(path string, had, keys []string, label string) []string {
	if path == "" || slices.Equal(had, keys) {
		return had
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		logsink.Warn("seed.error", "%s: the record of what the platform wrote cannot be kept at %s; the shipped keys alone decide: %v", label, path, err)
		return had
	}
	published, err := atomicfile.WriteReplace(path, []byte(strings.Join(keys, "\n")+"\n"), 0o600)
	if err != nil {
		if !published {
			logsink.Warn("seed.error", "%s: the record of what the platform wrote was not written at %s; the shipped keys alone decide: %v", label, path, err)
			return had
		}
		logsink.Warn("seed.error", "%s: wrote the record %s; directory sync failed (durability not proven): %v", label, path, err)
	}
	return keys
}

func (a *App) seededDocPath(name string) string {
	home := identityHomeFromConfig(a.cfg.SourcePath)
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	return filepath.Join(home, name)
}

func (a *App) bringSeededDocHome(path string, normalize func([]byte) []byte, shipped []string, label string) {
	stray := filepath.Join(filepath.Dir(a.cfg.SourcePath), filepath.Base(path))
	if abs, err := filepath.Abs(stray); err == nil {
		stray = abs
	}
	if stray == path {
		return
	}
	cur, err := os.ReadFile(stray)
	if err != nil {
		if !os.IsNotExist(err) {
			logsink.Warn("seed.error", "%s: %s is unreadable and stays where it is: %v", label, stray, err)
		}
		return
	}
	if slices.Contains(shipped, docSeedKey(normalize, cur)) {
		if err := errors.Join(os.Remove(stray), removeIfPresent(stray+sidecarSuffix)); err != nil {
			logsink.Warn("seed.error", "%s: the platform's copy at %s could not be removed: %v", label, stray, err)
			return
		}
		logsink.Info("seed.decision", "%s: removed the platform's copy at %s — the doc lives at %s", label, stray, path)
		return
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		logsink.Warn("seed.decision", "%s: the identity's own doc is at %s and another of theirs at %s — both left as they are", label, path, stray)
		return
	}
	if err := os.Rename(stray, path); err != nil {
		logsink.Warn("seed.error", "%s: the identity's own doc at %s could not move to %s: %v", label, stray, path, err)
		return
	}
	logsink.Info("seed.decision", "%s: the identity's own doc moved from %s to %s", label, stray, path)
	if err := removeIfPresent(stray + sidecarSuffix); err != nil {
		logsink.Warn("seed.error", "%s: %s could not be removed: %v", label, stray+sidecarSuffix, err)
	}
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func retireSidecar(sidecar, label string) {
	if err := os.Remove(sidecar); err == nil {
		logsink.Info("seed.decision", "%s: retired %s — the deployed doc is current", label, sidecar)
	}
}
