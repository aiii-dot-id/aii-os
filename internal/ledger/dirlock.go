package ledger

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
)

const LedgerDirLockName = ".ledger.lock"

func lockLedgerDir(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("ledger directory: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, LedgerDirLockName), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot open the ledger directory lock: %w", err)
	}
	if err := lockLedgerFile(f); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return nil, fmt.Errorf("cannot lock the ledger directory %q: %w (close after refusal: %v)", dir, err, closeErr)
		}
		return nil, fmt.Errorf("cannot lock the ledger directory %q: %w", dir, err)
	}
	return f, nil
}

func lockLedgerFile(file *os.File) error {
	err := filelock.Lock(file)
	if errors.Is(err, filelock.ErrHeld) {
		return fmt.Errorf("%w: %w", ErrLedgerInUse, err)
	}
	return err
}
