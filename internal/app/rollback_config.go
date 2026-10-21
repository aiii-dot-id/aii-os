package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

const (
	configBeforeUpdate = ".before-update"
	configRolledBack   = ".rolled-back"
	configRefusedOnce  = ".refused-once"
)

func keepConfigBeforeUpdate(path string, settled error) {
	if path == "" || !errors.Is(settled, updates.ErrBootRecoveryPending) {
		return
	}
	for _, f := range rollbackFiles(path) {
		keepFileBeforeUpdate(f.path, f.what)
	}
}

type rollbackFile struct{ path, what string }

func rollbackFiles(configPath string) []rollbackFile {
	return []rollbackFile{{configPath, "the configuration"}, {providerFilePath(configPath), ProvidersFileName}}
}

func keepFileBeforeUpdate(path, what string) {
	if info, err := os.Stat(path + configBeforeUpdate); err == nil && info.Size() > 0 || err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
	data, err := atomicfile.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		logsink.Warn("updates.refusal", "%s could not be read to keep a copy of it for a rollback: %v", what, err)
		return
	}
	if _, err := writeFileAtomic(path+configBeforeUpdate, data); err != nil {
		logsink.Warn("updates.refusal", "a copy of %s could not be kept for a rollback: %v", what, err)
		return
	}
	logsink.Info("updates.decision", "kept %s as the previous version left it (%s): a rollback puts it back", what, path+configBeforeUpdate)
}

func restoreConfigBeforeUpdate(path string) error {
	if path == "" {
		return nil
	}
	for _, f := range rollbackFiles(path) {
		if err := restoreFileBeforeUpdate(f.path, f.what); err != nil {
			return err
		}
	}
	return nil
}

func restoreFileBeforeUpdate(path, what string) error {
	kept, err := atomicfile.ReadFile(path + configBeforeUpdate)
	if errors.Is(err, fs.ErrNotExist) || err == nil && len(kept) == 0 {
		logsink.Warn("updates.decision", "no copy of %s from before the update stands, so the restored version reads the file as the newer one left it; if it refuses a key, remove that key from %s", what, path)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s kept from before the update: %w", what, err)
	}
	if now, err := atomicfile.ReadFile(path); err == nil && !bytes.Equal(now, kept) {
		if _, err := writeFileAtomic(path+configRolledBack, now); err != nil {
			return fmt.Errorf("set the newer version's %s aside: %w", what, err)
		}
		logsink.Warn("updates.decision", "%s as the newer version left it is set aside as %s; what was changed under it is there and not in use", what, path+configRolledBack)
	}

	published, err := atomicfile.Replace(path+configBeforeUpdate, path)
	if !published {
		return fmt.Errorf("put back %s from before the update: %w", what, err)
	}
	if err != nil {
		logsink.Warn("updates.error", "%s from before the update is back at %s, and then: %v", what, path, err)
	}
	logsink.Warn("updates.end", "%s is as the restored version left it", what)
	return nil
}

func dropConfigBeforeUpdate(path string, settled error) {
	if path == "" || settled != nil {
		return
	}

	for _, f := range rollbackFiles(path) {
		if err := atomicfile.Remove(f.path + configBeforeUpdate); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logsink.Warn("updates.refusal", "the copy of %s from before the update could not be removed: %v", f.what, err)
		}
	}
}

func RollBackOverRefusedConfig(configPath string) {
	if sr := hostcap.Can(hostcap.SelfReplace); !sr.Available {
		return
	}
	putBack := func() error { return restoreConfigBeforeUpdate(configPath) }
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
