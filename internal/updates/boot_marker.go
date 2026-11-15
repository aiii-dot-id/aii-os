package updates

import (
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

func retireBootMarker(dataDir string) (bool, error) {

	return classifyRetire(atomicfile.Replace(
		filepath.Join(dataDir, markerFile),
		filepath.Join(dataDir, retiredMarkerFile)))
}

func classifyRetire(published bool, err error) (bool, error) {
	switch {
	case err == nil:
		return true, nil
	case published:
		return true, fmt.Errorf("boot marker retired but not durably: %w", err)
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

func statRefusal(path string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		if fi, perr := os.Lstat(filepath.Dir(path)); perr == nil && !fi.IsDir() {
			return fmt.Errorf("stat %s: parent %s is not a directory", path, filepath.Dir(path))
		}
		return nil
	}
	return err
}

func rearmBootMarker(dataDir string) (back bool, err error) {

	return atomicfile.Replace(
		filepath.Join(dataDir, retiredMarkerFile),
		filepath.Join(dataDir, markerFile))
}

func RequireSettledBootUpdate(dataDir string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the running program to read its update state: %w", err)
	}
	return requireSettledAt(dataDir, exePath)
}

func requireSettledAt(dataDir, exePath string) error {
	r, ok, err := recoveryOf(dataDir, exePath)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := os.Stat(r.backup); err != nil {
		return statRefusal(r.backup, err)
	}
	marker := filepath.Join(dataDir, markerFile)
	if _, err := os.Stat(marker); err != nil {
		if refusal := statRefusal(marker, err); refusal != nil {
			return refusal
		}
		return ErrBootRecoveryPending
	}
	return nil
}

type AnothersUpdateError struct{ Owner string }

func (e *AnothersUpdateError) Error() string {
	return fmt.Sprintf("the program was updated by the identity whose data is at %s, and that update has not completed a boot", e.Owner)
}

func (e *AnothersUpdateError) Is(target error) bool { return target == ErrBootRecoveryPending }

func UnsettledBesideProgram(dataDir string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the running program to read its update state: %w", err)
	}
	return unsettledBesideAt(dataDir, exePath)
}

func unsettledBesideAt(dataDir, exePath string) error {
	if err := requireSettledAt(dataDir, exePath); err != nil {
		return err
	}
	tgt, err := targetOf(exePath)
	if err != nil || tgt.bundle {
		return nil
	}
	mine, other, _ := claimOf(dataDir, tgt)
	if mine || other == "" {
		return nil
	}
	if _, err := os.Stat(tgt.backup); err != nil {
		return statRefusal(tgt.backup, err)
	}
	return &AnothersUpdateError{Owner: other}
}

func WriteBootMarker(dataDir string) {
	exePath, err := os.Executable()
	if err != nil {
		logsink.Warn("updates.refusal", "cannot locate the running program (%v) — an update record beside it, if any, is left for a later boot", err)
		exePath = ""
	}
	writeBootMarkerAt(dataDir, exePath)
}

func writeBootMarkerAt(dataDir, exePath string) {

	if err := writeFileAtomic(filepath.Join(dataDir, markerFile), []byte("ok"), 0o644); err != nil {

		logsink.Warn("updates.error", "could not write boot marker: %v", err)
		return
	}

	legacy, _ := readPending(dataDir)
	os.Remove(filepath.Join(dataDir, pendingFile))
	os.Remove(filepath.Join(dataDir, previousFile))

	os.Remove(filepath.Join(dataDir, retiredMarkerFile))
	if exePath == "" {
		return
	}
	tgt, err := targetOf(exePath)
	if err != nil {
		logsink.Warn("updates.refusal", "%v — an update record beside it, if any, is left for a later boot", err)
		return
	}
	noteIncomplete(tgt, legacy)
	mine, other, _ := claimOf(dataDir, tgt)
	if !mine {
		if other != "" {
			logsink.Info("updates.decision", "the update of %s was applied by the identity whose data is at %s — this healthy boot leaves its backup and record alone; that identity's boot settles them", tgt.path, other)
		}
		return
	}

	pend, _, _ := readRecord(tgt.record)

	noteIncomplete(tgt, pend)
	r := recovery{dataDir: dataDir, backup: tgt.backup, record: tgt.record, tgt: tgt}
	if cut, err := r.settleCutOff(pend); err != nil {
		logsink.Warn("updates.error", "this healthy boot could not put back what a cut-off update had written beside %s: %v — its backups and record are kept, and the next healthy boot tries again", tgt.path, err)
		return
	} else if cut {
		logsink.Info("updates.decision", "an update of %s was cut off before its binary was published — what it had written beside it is put back", tgt.path)
	}
	if keptForTheOperator(tgt, pend) {
		r.keepForTheOperator()
		return
	}
	if err := withdrawSwap(tgt, pend); err != nil {
		logsink.Warn("updates.error", "this healthy boot could not retire its update of %s: %v", tgt.path, err)
	}
}

func CheckRollback(dataDir string) string {
	exePath, err := os.Executable()
	if err != nil {
		logsink.Warn("updates.refusal", "rollback check skipped — cannot locate running binary: %v", err)
		return ""
	}
	return checkRollbackAt(dataDir, exePath)
}

func RollbackArmed(dataDir string) bool {
	exePath, err := os.Executable()
	if err != nil {
		exePath = ""
	}
	return rollbackArmedAt(dataDir, exePath)
}

func rollbackArmedAt(dataDir, exePath string) bool {
	r, ok, _ := recoveryOf(dataDir, exePath)
	if !ok {
		return false
	}
	if _, err := os.Stat(r.backup); err != nil {
		return false
	}
	markerPath := filepath.Join(dataDir, markerFile)
	if _, err := os.Stat(markerPath); err == nil || statRefusal(markerPath, err) != nil {
		return false
	}
	pend, present, err := readRecord(r.record)
	return present && err == nil && pend.Attempts >= 1
}
