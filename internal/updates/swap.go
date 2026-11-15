package updates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

func stageFileDurably(target string, data []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), stageStem(target)+"*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	fail := func(format string, err error) (string, error) {
		f.Close()
		os.Remove(tmp)
		return "", fmt.Errorf(format+" %s: %w", filepath.Base(target), err)
	}
	if err := f.Chmod(mode); err != nil {
		return fail("chmod staged", err)
	}
	if _, err := f.Write(data); err != nil {
		return fail("write staged", err)
	}
	if err := f.Sync(); err != nil {
		return fail("sync staged", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("close staged %s: %w", filepath.Base(target), err)
	}
	return tmp, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {

	published, err := atomicfile.WriteReplace(path, data, mode)
	if err != nil {
		if published {
			return fmt.Errorf("replace %s: published but not durable: %w", filepath.Base(path), err)
		}
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeRecord(path string, p updatePending) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw, 0o644)
}

func readRecord(path string) (p updatePending, present bool, err error) {
	raw, err := atomicfile.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && statRefusal(path, err) == nil {
		return updatePending{}, false, nil
	}
	if err != nil {
		return updatePending{}, true, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return updatePending{}, true, err
	}
	return p, true, nil
}

func readClaim(path string) (updatePending, bool, error) {
	p, present, err := readRecord(path)
	if present && err == nil && p.Owner == "" {
		err = errors.New("it names no owner")
	}
	return p, present, err
}

func canStageBeside() (bool, string) {
	exe, err := os.Executable()
	if err != nil {
		return false, "cannot locate the running binary: " + err.Error()
	}
	tgt, err := targetOf(exe)
	if err != nil {
		return false, err.Error()
	}
	return canStageBesideAt(tgt.path)
}

func canStageBesideAt(exe string) (bool, string) {
	dir := filepath.Dir(exe)
	probe, err := os.CreateTemp(dir, ".aii-swap-probe-")
	if err != nil {
		return false, exe + " cannot be replaced by this user — update it with the package manager that installed it"
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return true, ""
}

func unretiredSwap(dataDir string) bool {
	if _, err := os.Stat(filepath.Join(dataDir, previousFile)); err == nil {
		return true
	}
	_, ok := readPending(dataDir)
	return ok
}

func readPending(dataDir string) (updatePending, bool) {
	p, present, err := readRecord(filepath.Join(dataDir, pendingFile))
	return p, present && err == nil
}

func swapBinary(tgt target, newBinary []byte, beside []installedFile, dataDir, owner, version string) error {
	currentBytes, err := os.ReadFile(tgt.path)
	if err != nil {
		return fmt.Errorf("read current binary for backup: %w", err)
	}

	dir := filepath.Dir(tgt.path)
	stood := make([]standing, len(beside))
	pend := updatePending{
		Attempts:     0,
		BackupSHA256: sha256hex(currentBytes),
		NewSHA256:    sha256hex(newBinary),
		Version:      version,
		Owner:        owner,
	}
	for i, f := range beside {
		entry := besideSwap{Name: f.name, NewSHA256: sha256hex(f.data)}
		old, err := os.ReadFile(filepath.Join(dir, f.name))
		switch {
		case err == nil:
			stood[i] = standing{there: true, data: old}
			entry.BackupSHA256 = sha256hex(old)
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("read %s for backup: %w", f.name, err)
		}
		pend.Beside = append(pend.Beside, entry)
	}

	if err := writeRecord(tgt.record, pend); err != nil {

		return errors.Join(fmt.Errorf("write update record: %w", err), withdraw(tgt.record))
	}
	replaceStep("recorded")

	if err := writeFileAtomic(tgt.backup, currentBytes, 0o755); err != nil {

		return errors.Join(fmt.Errorf("write backup binary: %w", err), withdraw(tgt.record))
	}

	for i, f := range beside {
		if !stood[i].there {
			continue
		}
		if err := writeFileAtomic(besideBackup(tgt, f.name), stood[i].data, 0o644); err != nil {

			return errors.Join(fmt.Errorf("write backup of %s: %w", f.name, err), withdrawSwap(tgt, pend))
		}
	}
	replaceStep("backed-up")

	markerRetired, retireErr := retireBootMarker(dataDir)

	rearmMarker := func() (bool, error) {
		if !markerRetired {
			return true, nil
		}
		return rearmBootMarker(dataDir)
	}

	retireSwapState := func(primary error, what string) error {
		if back, rerr := rearmMarker(); rerr != nil {
			state := "NOT re-armed"
			if back {
				state = "re-armed but not durably"
			}
			return errors.Join(
				fmt.Errorf("%s: %w", what, primary),
				fmt.Errorf("boot marker %s — backup and record kept so the next boot can still recover: %w", state, rerr))
		}
		return errors.Join(fmt.Errorf("%s: %w", what, primary), withdrawSwap(tgt, pend))
	}

	if retireErr != nil {
		return retireSwapState(retireErr, "retire boot marker")
	}
	replaceStep("marker-retired")

	tmpPath, err := stageFileDurably(tgt.path, newBinary, 0o755)
	if err != nil {

		return retireSwapState(err, "write new binary")
	}

	putBack := func(n int) error {
		var errs []error
		for i := n - 1; i >= 0; i-- {
			path := filepath.Join(dir, beside[i].name)
			if stood[i].there {
				errs = append(errs, installBeside(path, stood[i].data))
			} else {
				errs = append(errs, withdraw(path))
			}
		}
		return errors.Join(errs...)
	}
	abandon := func(n int, primary error, what string) error {
		os.Remove(tmpPath)
		if perr := putBack(n); perr != nil {
			return errors.Join(
				fmt.Errorf("%s: %w", what, primary),
				fmt.Errorf("what the update had written beside %s could not all be put back — backups and record kept so the next boot can: %w", filepath.Base(tgt.path), perr))
		}
		return retireSwapState(primary, what)
	}
	for i, f := range beside {
		if err := installBeside(filepath.Join(dir, f.name), f.data); err != nil {

			return abandon(undoThrough(i, err), err, "install "+f.name+" ("+filepath.Base(tgt.path)+" not swapped)")
		}
	}
	replaceStep("beside-installed")

	published, err := atomicfile.ReplaceExecutable(tmpPath, tgt.path)
	if err != nil {
		if !published {

			return abandon(len(beside), err, "rename new binary into place")
		}

		return fmt.Errorf("new binary published but not durable — backup and record kept so the next boot can still roll back: %w", err)
	}
	replaceStep("published")
	return nil
}

func withdraw(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("could not withdraw %s: %w", path, err)
	}
	return nil
}

func extractBinary(archiveBytes []byte) (binary []byte, beside []installedFile, err error) {
	platform, _ := hostTarget()
	if platform == "windows" {
		return extractFromZip(platform, archiveBytes)
	}
	return extractFromTarGz(platform, archiveBytes)
}

func StageRefusal() string {
	if ok, why := canStageBeside(); !ok {
		return why
	}
	return ""
}
