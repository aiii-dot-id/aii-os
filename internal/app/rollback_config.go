package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/updates"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

const (
	configKeptUnder    = ".before-"
	configBeforeUpdate = ".before-update"
	configRolledBack   = ".rolled-back"
	configRefusedOnce  = ".refused-once"
)

type rollbackFile struct{ path, what string }

func rollbackFiles(configPath string) []rollbackFile {
	return []rollbackFile{{configPath, "the configuration"}, {providerFilePath(configPath), ProvidersFileName}}
}

func keepConfigUnderUpdate(path, running string, unsettled error) {
	if path == "" || !version.Valid(running) || !errors.Is(unsettled, updates.ErrBootRecoveryPending) {
		return
	}
	var anothers *updates.AnothersUpdateError
	if errors.As(unsettled, &anothers) {
		logsink.Info("updates.decision", "%v: this identity keeps its configuration as the previous version left it and holds its plugin data upgrades until that update has", anothers)
	}
	for _, f := range rollbackFiles(path) {
		keepFileUnder(f.path, f.what, running)
	}
}

func keepFileUnder(path, what, running string) {
	copyPath := path + configKeptUnder + running
	if info, err := os.Stat(copyPath); err == nil && info.Size() > 0 || err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
	data, err := atomicfile.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		logsink.Warn("updates.refusal", "%s could not be read to keep a copy of it while an update is not settled: %v", what, err)
		return
	}
	if _, err := writeFileAtomic(copyPath, data); err != nil {
		logsink.Warn("updates.refusal", "a copy of %s could not be kept while an update is not settled: %v", what, err)
		return
	}
	logsink.Info("updates.decision", "kept %s as the previous version left it (%s): if the program goes back, it is put back", what, copyPath)
}

func keptUnderVersions(path string) []string {
	entries, _ := os.ReadDir(filepath.Dir(path))
	prefix := filepath.Base(path) + configKeptUnder
	var versions []string
	for _, e := range entries {
		if v, ok := strings.CutPrefix(e.Name(), prefix); ok && !e.IsDir() && version.Valid(v) {
			versions = append(versions, v)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return version.Compare(versions[i], versions[j]) < 0 })
	return versions
}

func keptUnderAny(configPath string) []string {
	var versions []string
	for _, f := range rollbackFiles(configPath) {
		versions = append(versions, keptUnderVersions(f.path)...)
	}
	return versions
}

func keptSince(kept []string, running string, atRollback bool) []string {
	var out []string
	for _, v := range kept {
		if c := version.Compare(v, running); c > 0 || c == 0 && atRollback {
			out = append(out, v)
		}
	}
	return out
}

func putBackConfigKept(configPath, running string, atRollback bool) error {
	if configPath == "" {
		return nil
	}
	var errs []error
	for _, f := range rollbackFiles(configPath) {
		errs = append(errs, putBackFileKept(f.path, f.what, running, atRollback))
	}
	return errors.Join(errs...)
}

func putBackFileKept(path, what, running string, atRollback bool) error {
	var from string
	for _, v := range keptSince(keptUnderVersions(path), running, atRollback) {
		if info, err := os.Stat(path + configKeptUnder + v); err == nil && info.Size() > 0 {
			from = v
			break
		}
	}
	if from == "" {
		if atRollback {
			logsink.Warn("updates.decision", "no copy of %s from before the update stands, so the restored version reads the file as the newer one left it; if it refuses a key, remove that key from %s", what, path)
		}
		return nil
	}
	copyPath := path + configKeptUnder + from
	kept, err := atomicfile.ReadFile(copyPath)
	if err != nil {
		return fmt.Errorf("read %s kept before version %s: %w", what, from, err)
	}
	if now, err := atomicfile.ReadFile(path); err == nil && !bytes.Equal(now, kept) {
		if _, err := writeFileAtomic(path+configRolledBack, now); err != nil {
			return fmt.Errorf("set %s as version %s left it aside: %w", what, from, err)
		}
		logsink.Warn("updates.decision", "the program went back from version %s, so %s as that version left it is set aside as %s; what was changed under it is there and not in use", from, what, path+configRolledBack)
	}

	published, err := atomicfile.Replace(copyPath, path)
	if !published {
		return fmt.Errorf("put back %s kept before version %s: %w", what, from, err)
	}
	if err != nil {
		logsink.Warn("updates.error", "%s kept before version %s is back at %s, and then: %v", what, from, path, err)
	}
	logsink.Warn("updates.end", "%s is as the version before %s left it", what, from)
	for _, v := range keptSince(keptUnderVersions(path), from, false) {
		if err := atomicfile.Remove(path + configKeptUnder + v); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logsink.Warn("updates.refusal", "the copy of %s kept under version %s could not be removed: %v", what, v, err)
		}
	}
	return nil
}

func PutBackConfigIfProgramWentBack(configPath string) {
	if configPath == "" || !version.Valid(Current()) {
		return
	}
	if sr := hostcap.Can(hostcap.SelfReplace); !sr.Available {
		return
	}
	if len(keptSince(keptUnderAny(configPath), Current(), false)) == 0 {
		return
	}
	putBackUnderClaim(configPath, Current(), (&App{}).claimIdentity)
}

func putBackUnderClaim(configPath, running string, claim func(dir string) (func(), error)) {

	if dir := recordDirOfRefusedConfig(configPath); dirStands(dir) {
		release, err := claim(dir)
		if err != nil {
			return
		}
		defer release()
	}
	if err := putBackConfigKept(configPath, running, false); err != nil {
		logsink.Warn("updates.refusal", "the program went back, and the files it reads could not all be put back as it left them: %v", err)
	}
}

func dirStands(dir string) bool {
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

func dropConfigKeptUnderSettled(path, running string, unsettled error) {
	if path == "" || unsettled != nil || !version.Valid(running) {
		return
	}
	for _, f := range rollbackFiles(path) {
		var copies []string
		for _, v := range keptUnderVersions(f.path) {
			if version.Compare(v, running) <= 0 {
				copies = append(copies, f.path+configKeptUnder+v)
			}
		}
		for _, c := range append(copies, f.path+configBeforeUpdate) {
			if err := atomicfile.Remove(c); err != nil && !errors.Is(err, fs.ErrNotExist) {
				logsink.Warn("updates.refusal", "the copy of %s at %s could not be removed: %v", f.what, c, err)
			}
		}
	}
}

func (a *App) dropSettledConfigCopies(configPath string) {
	if configPath == "" || len(keptUnderAny(configPath)) == 0 && !anyStands(configPath, configBeforeUpdate) {
		return
	}
	dir := filepath.Dir(a.activeIdentity(a.configSnapshot().Identity).LedgerPath)
	dropConfigKeptUnderSettled(configPath, Current(), updates.UnsettledBesideProgram(dir))
}

func anyStands(configPath, suffix string) bool {
	for _, f := range rollbackFiles(configPath) {
		if _, err := os.Lstat(f.path + suffix); err == nil {
			return true
		}
	}
	return false
}

func RollBackOverRefusedConfig(configPath string) {
	if sr := hostcap.Can(hostcap.SelfReplace); !sr.Available {
		return
	}
	putBack := func() error { return putBackConfigKept(configPath, Current(), true) }
	rolled, said, err := refusedConfigRollback(configPath, (&App{}).claimIdentity, updates.RequireSettledBootUpdate, updates.RollbackArmed, updates.RollBackUnsettled, putBack, reexecSelf)
	if err != nil {
		logsink.Error("updates.error", "%v", err)
		return
	}
	if said != "" {
		logsink.Warn("updates.decision", "%s", said)
	}
	if rolled {
		os.Exit(0)
	}
}

func ForgetRefusedStart(configPath string) {
	if configPath == "" {
		return
	}
	if err := os.Remove(configPath + configRefusedOnce); err != nil && !errors.Is(err, fs.ErrNotExist) {
		logsink.Warn("updates.refusal", "the note of an earlier refused start could not be removed (%v): while it stands, one refused start of an update that is not settled restores the previous program. Remove %s", err, configPath+configRefusedOnce)
	}
}

const (
	refusedStartNoted = "this start refused its configuration while an update of this identity is not settled, and that is noted: a second refused start restores the previous program. Correct the key the refusal names and the update stands"
	refusedStartArmed = "this start refused its configuration while an update of this identity is not settled, and that is noted. A boot of that update is already counted without a healthy start, so the next start restores the previous program, or says why it cannot, whether or not the key the refusal names is corrected"
	refusedTwiceArmed = "this is the second start in a row that refused its configuration while an update of this identity is not settled, and the previous program was not restored: the updater's lines say why. A boot of the update is already counted without a healthy start, so the next start restores the previous program, or says why it cannot, whether or not the key the refusal names is corrected"
	refusedTwiceIdle  = "this is the second start in a row that refused its configuration while an update of this identity is not settled, and the previous program was not restored: the updater's lines say why. No boot is counted against the update, and the refusal stays noted: the next refused start tries again. Correct the key the refusal names and the update stands"
)

func refusedConfigRollback(configPath string, claim func(dir string) (func(), error), unsettled func(dataDir string) error,
	armed func(dataDir string) bool, restore func(dataDir string) string, putBack, reexec func() error) (rolled bool, said string, err error) {
	dir := recordDirOfRefusedConfig(configPath)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return false, "", nil
	}
	release, err := claim(dir)
	if err != nil {
		return false, "", nil
	}
	if !errors.Is(unsettled(dir), updates.ErrBootRecoveryPending) {
		release()
		return false, "", nil
	}
	note := configPath + configRefusedOnce
	if _, serr := os.Stat(note); serr != nil {

		werr := os.WriteFile(note, nil, 0o600)
		was := armed(dir)
		release()
		if werr != nil {
			return false, "", fmt.Errorf("this start refused its configuration while an update of this identity is not settled, and that could not be noted beside the file (%v): no later refused start restores the previous program over it. Correct the key the refusal names", werr)
		}
		if was {
			return false, refusedStartArmed, nil
		}
		return false, refusedStartNoted, nil
	}

	logsink.Warn("updates.decision", "this is the second start in a row that refused its configuration while an update of this identity is not settled: the previous program is being restored")
	restored := restore(dir)
	if restored == "" {

		now := armed(dir)
		release()
		if now {
			return false, refusedTwiceArmed, nil
		}
		return false, refusedTwiceIdle, nil
	}
	_ = os.Remove(note)
	if err := afterRollback(restored, putBack, release, reexec); err != nil {
		return false, "", err
	}
	return true, "", nil
}

func recordDirOfRefusedConfig(configPath string) string {
	var named struct {
		Identity struct {
			LedgerPath string `json:"ledger_path"`
		} `json:"identity"`
	}
	if raw, err := atomicfile.ReadFile(configPath); err == nil {
		_ = json.Unmarshal(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")), &named)
	}
	if named.Identity.LedgerPath == "" {
		return filepath.Dir(defaultConfig().Identity.LedgerPath)
	}
	return filepath.Dir(named.Identity.LedgerPath)
}
