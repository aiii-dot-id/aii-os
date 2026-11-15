package updates

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

func installBinary(tgt target, archiveBytes []byte, dataDir, owner, version string) (already bool, err error) {

	newBinary, beside, err := extractBinary(archiveBytes)
	if err != nil {
		return false, fmt.Errorf("extract binary from archive: %w", err)
	}
	return tgt.replace(sha256hex(newBinary), func(string) error {
		return swapWithBeside(tgt, newBinary, beside, dataDir, owner, version)
	})
}

func swapWithBeside(tgt target, newBinary []byte, beside []installedFile, dataDir, owner, version string) error {
	if err := swapBinary(tgt, newBinary, beside, dataDir, owner, version); err != nil {
		return fmt.Errorf("binary swap: %w", err)
	}
	return nil
}

func installBeside(path string, data []byte) error {
	mode, aside := os.FileMode(0o755), true
	if slices.Contains(ShippedDocuments(), filepath.Base(path)) {
		mode, aside = 0o644, false
	} else if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		aside = false
	}
	tmp, err := stageFileDurably(path, data, mode)
	if err != nil {
		return err
	}
	published, err := publishBeside(aside, tmp, path)
	switch {
	case err == nil:
		return nil
	case published:
		return &besideDisturbed{err: err}
	}
	os.Remove(tmp)
	return err
}

type besideDisturbed struct{ err error }

func (e *besideDisturbed) Error() string { return "published but not durable: " + e.err.Error() }

func (e *besideDisturbed) Unwrap() error { return e.err }

func undoThrough(i int, err error) int {
	var disturbed *besideDisturbed
	if errors.As(err, &disturbed) {
		return i + 1
	}
	return i
}
