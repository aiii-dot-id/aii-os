package updates

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

type recovery struct {
	dataDir        string
	backup, record string
	tgt            target
	legacy         bool
}

func recoveryOf(dataDir, exePath string) (r recovery, ok bool, err error) {
	tgt, terr := targetOf(exePath)
	if legacyStands(dataDir) {

		return recovery{dataDir: dataDir, backup: filepath.Join(dataDir, previousFile), record: filepath.Join(dataDir, pendingFile), tgt: tgt, legacy: true}, true, nil
	}
	if terr != nil {
		return recovery{}, false, terr
	}
	mine, _, err := claimOf(dataDir, tgt)
	if !mine {
		return recovery{}, false, err
	}
	return recovery{dataDir: dataDir, backup: tgt.backup, record: tgt.record, tgt: tgt}, true, nil
}

func legacyStands(dataDir string) bool {
	prev := filepath.Join(dataDir, previousFile)
	_, err := os.Stat(prev)
	return err == nil || statRefusal(prev, err) != nil
}

func claimOf(dataDir string, tgt target) (mine bool, other string, err error) {
	p, present, err := readClaim(tgt.record)
	if !present {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("the update record %s cannot be read (%v) — no identity can be shown to own it, so no boot rolls it back or retires it, and it refuses every update of %s until it is removed by hand", tgt.record, err, tgt.path)
	}
	me, err := ownerOf(dataDir)
	if err != nil {
		return false, "", fmt.Errorf("cannot name this identity's data directory (%v) — the update record %s is left untouched", err, tgt.record)
	}
	if p.Owner != me {
		return false, p.Owner, nil
	}
	return true, "", nil
}

func checkRollbackAt(dataDir, exePath string) string {

	_ = os.Remove(exePath + ".old")

	platform, _ := hostTarget()
	for i, p := range ShippedPrograms(platform) {
		if i > 0 {
			_ = os.Remove(filepath.Join(filepath.Dir(exePath), p.Name) + ".old")
		}
	}

	r, ok, err := recoveryOf(dataDir, exePath)
	if ok {
		return settleLocked(dataDir, exePath, r.tgt, false)
	}
	if err != nil {
		logsink.Warn("updates.refusal", "rollback check: %v", err)
	}

	if !legacyStands(dataDir) {
		os.Remove(filepath.Join(dataDir, pendingFile))
	}
	return ""
}

func settleLocked(dataDir, exePath string, tgt target, uncounted bool) string {
	if tgt.path != "" {
		release, err := lockTarget(tgt)
		if errors.Is(err, ErrAlreadyApplying) {
			logsink.Info("updates.decision", "rollback check deferred — %v; this boot leaves the update state as it is, and the next boot settles it", err)
			return ""
		}
		if err != nil {
			logsink.Warn("updates.refusal", "rollback check deferred — %v; the update state is left as it is, and the next boot tries again", err)
			return ""
		}
		defer release()
	}
	r, ok, err := recoveryOf(dataDir, exePath)
	switch {
	case err != nil:
		logsink.Warn("updates.refusal", "rollback check: %v", err)
		return ""
	case !ok:
		return ""
	case r.tgt != tgt:
		logsink.Warn("updates.refusal", "rollback check deferred — the running program resolved to %s, then to %s; the update state is left as it is, and the next boot tries again", tgt.path, r.tgt.path)
		return ""
	}
	return r.settle(uncounted)
}

func RollBackUnsettled(dataDir string) string {
	exePath, err := os.Executable()
	if err != nil {
		logsink.Warn("updates.refusal", "rollback skipped — cannot locate running binary: %v", err)
		return ""
	}
	return rollBackUnsettledAt(dataDir, exePath)
}

func rollBackUnsettledAt(dataDir, exePath string) string {
	r, ok, err := recoveryOf(dataDir, exePath)
	if err != nil {
		logsink.Warn("updates.refusal", "rollback: %v", err)
		return ""
	}
	if !ok {
		return ""
	}
	return settleLocked(dataDir, exePath, r.tgt, true)
}

func (r recovery) settle(uncounted bool) string {
	markerPath := filepath.Join(r.dataDir, markerFile)

	if _, err := os.Stat(r.backup); err != nil {
		if refusal := statRefusal(r.backup, err); refusal != nil {

			logsink.Warn("updates.refusal", "rollback check cannot stat %s: %v — leaving update state untouched", r.backup, refusal)
			return ""
		}
		os.Remove(r.record)
		return ""
	}
	if _, err := os.Stat(markerPath); err == nil {

		pend, _, _ := readRecord(r.record)
		if !r.legacy && keptForTheOperator(r.tgt, pend) {
			r.keepForTheOperator()
			return ""
		}
		r.retire(pend)
		return ""
	} else if refusal := statRefusal(markerPath, err); refusal != nil {

		logsink.Warn("updates.refusal", "rollback check cannot stat %s: %v — leaving update state untouched", markerPath, refusal)
		return ""
	}

	pend, present, err := readRecord(r.record)
	if !present || err != nil {
		if !r.legacy {

			logsink.Warn("updates.refusal", "the update record %s became unreadable (%v) — leaving update state untouched", r.record, err)
			return ""
		}

		if uncounted {
			logsink.Warn("updates.refusal", "ROLLBACK DEFERRED \u2014 the update state beside %s is an earlier build's and has no record to restore by; the first start that reaches its boot adopts it", r.backup)
			return ""
		}
		prevBytes, err := atomicfile.ReadFile(r.backup)
		if err != nil {
			logsink.Warn("updates.decision", "unreadable backup with no tombstone — retiring it: %v", err)
			os.Remove(r.backup)
			return ""
		}
		if err := writeRecord(r.record, updatePending{
			Attempts:     1,
			BackupSHA256: sha256hex(prevBytes),
		}); err != nil {
			logsink.Warn("updates.error", "could not adopt legacy update state: %v", err)
		}
		return ""
	}

	if pend.Attempts == 0 && !uncounted {

		pend.Attempts = 1
		if err := writeRecord(r.record, pend); err != nil {
			logsink.Warn("updates.error", "could not record first boot attempt: %v", err)
		}
		return ""
	}

	return r.rollback(pend)
}

func (r recovery) rollback(pend updatePending) string {
	exePath := r.tgt.exe
	if exePath == "" {
		logsink.Warn("updates.refusal", "ROLLBACK DEFERRED \u2014 the running program could not be resolved; the backup %s and its record are kept, and the next boot tries again", r.backup)
		return ""
	}

	if ok, why := canStageBesideAt(r.tgt.path); !ok {
		logsink.Warn("updates.refusal", "ROLLBACK DEFERRED — %s; the previous binary is kept at %s (restore it with your package manager, or from data/backups/)", why, r.backup)
		return ""
	}
	if r.tgt.bundle {
		if r.legacy {

			logsink.Warn("updates.refusal", "ROLLBACK DEFERRED — the previous program %s, kept in the data directory by an earlier build, is a bare binary and %s is a sealed application bundle that only a whole bundle replaces; it is kept, and the next healthy boot retires it", r.backup, r.tgt.path)
			return ""
		}
		return r.exchangeBack(pend)
	}

	prevBytes, err := atomicfile.ReadFile(r.backup)
	if err != nil {
		logsink.Error("updates.error", "ROLLBACK FAILED — cannot read backup binary: %v", err)
		return ""
	}

	if pend.BackupSHA256 != "" && sha256hex(prevBytes) != pend.BackupSHA256 {
		logsink.Error("updates.refusal", "ROLLBACK REFUSED — backup hashes to %.12s, swap recorded %.12s; the backup is not a backup, and the current binary stays",
			sha256hex(prevBytes), pend.BackupSHA256)
		return ""
	}

	if pend.NewSHA256 != "" {
		if cur, err := os.ReadFile(exePath); err == nil && sha256hex(cur) != pend.NewSHA256 {
			cut, err := r.settleCutOff(pend)
			if err != nil {
				logsink.Error("updates.error", "ROLLBACK FAILED — %v; the backups and the record are kept, and the next boot tries again", err)
				return ""
			}
			if cut {
				logsink.Info("updates.decision", "rollback: the update was cut off before its binary was published — what it had written beside it is put back; retiring update state")
			} else {
				logsink.Info("updates.decision", "rollback skipped — the binary changed since the update (operator repair); retiring update state")
			}
			r.retire(pend)
			return ""
		}
	}

	if err := r.restoreBeside(pend); err != nil {
		logsink.Error("updates.error", "ROLLBACK FAILED — %v; the backups and the record are kept, and the next boot tries again", err)
		return ""
	}

	tmpRestore, err := stageFileDurably(exePath, prevBytes, 0o755)
	if err != nil {
		logsink.Error("updates.error", "ROLLBACK FAILED — cannot stage previous binary: %v", err)
		return ""
	}
	restored, rerr := atomicfile.ReplaceExecutable(tmpRestore, exePath)
	if rerr != nil && !restored {
		os.Remove(tmpRestore)
		logsink.Error("updates.error", "ROLLBACK FAILED — cannot restore previous binary: %v", rerr)
		return ""
	}
	if rerr != nil {

		logsink.Error("updates.error", "ROLLBACK restored the previous binary but could not make the directory entry durable — keeping the backup: %v", rerr)
		return r.backup
	}

	r.noteFailed(pend)
	r.retire(pend)
	logsink.Warn("updates.end", "ROLLBACK — restored previous binary (update failed to boot)")
	return r.backup
}

func (r recovery) noteFailed(pend updatePending) bool {
	if r.tgt.failed == "" || pend.Version == "" {
		return false
	}
	raw, err := json.Marshal(failedUpdate{Version: pend.Version, NewSHA256: pend.NewSHA256, At: time.Now().UTC()})
	if err == nil {
		err = writeFileAtomic(r.tgt.failed, raw, 0o644)
	}
	if err != nil {
		logsink.Warn("updates.error", "the rollback could not record which release it undid (%v) — the restored program may install %s again", err, pend.Version)
		return false
	}
	return true
}

func keptForTheOperator(tgt target, pend updatePending) bool {
	return tgt.bundle && pend.Version == ""
}

func (r recovery) keepForTheOperator() {
	if err := withdraw(r.record); err != nil {
		logsink.Warn("updates.error", "the update of %s was installed by an earlier release; its record could not be retired: %v", r.tgt.path, err)
		return
	}
	logsink.Info("updates.decision", "the update of %s was installed by an earlier release, which kept no count of its boots: it is not rolled back, and the app it replaced is kept at %s until the next update replaces it", r.tgt.path, r.backup)
}

func PreviousKept() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return previousKeptAt(exe)
}

func previousKeptAt(exePath string) string {
	tgt, err := targetOf(exePath)
	if err != nil || !tgt.bundle {
		return ""
	}
	if _, err := os.Stat(tgt.record); !errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if fi, err := os.Stat(tgt.backup); err != nil || !fi.IsDir() {
		return ""
	}
	return tgt.backup
}

func (r recovery) retire(pend updatePending) {
	if r.legacy {
		os.Remove(r.backup)
	} else {
		for _, path := range besideBackups(r.tgt, pend) {
			os.Remove(path)
		}
		_ = withdrawBackup(r.tgt)
	}
	os.Remove(r.record)
}

func (r recovery) settleCutOff(pend updatePending) (cut bool, err error) {
	if r.legacy || r.tgt.bundle || r.tgt.exe == "" || len(pend.Beside) == 0 || pend.BackupSHA256 == "" {
		return false, nil
	}
	if have, herr := imageHash(r.tgt.exe); herr != nil || have != pend.BackupSHA256 {
		return false, nil
	}
	return true, r.restoreBeside(pend)
}

func (r recovery) restoreBeside(pend updatePending) error {
	for _, b := range pend.Beside {
		if b.Name == "" || b.Name != filepath.Base(b.Name) {
			logsink.Warn("updates.refusal", "rollback: the update record names %q beside the binary, which is not a file name — left alone", b.Name)
			continue
		}
		path := filepath.Join(filepath.Dir(r.tgt.exe), b.Name)
		cur, err := os.ReadFile(path)

		absent := errors.Is(err, os.ErrNotExist) && b.BackupSHA256 != ""
		if !absent && (err != nil || sha256hex(cur) != b.NewSHA256) {
			logsink.Info("updates.decision", "rollback: %s is not the file the update installed (changed or removed since) — left as it is", b.Name)
			continue
		}
		if b.BackupSHA256 == "" {
			if err := os.Remove(path); err != nil {
				logsink.Warn("updates.error", "rollback: %s, which the update added, could not be removed: %v", b.Name, err)
			}
			continue
		}
		prev, err := atomicfile.ReadFile(besideBackup(r.tgt, b.Name))
		if err != nil || sha256hex(prev) != b.BackupSHA256 {
			logsink.Error("updates.refusal", "rollback: what was kept of %s is not what the swap recorded (%v) — the update's %s stays", b.Name, err, b.Name)
			continue
		}
		if err := installBeside(path, prev); err != nil {
			return fmt.Errorf("cannot restore %s beside the binary: %w", b.Name, err)
		}
	}
	return nil
}
