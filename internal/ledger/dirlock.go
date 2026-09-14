package ledger

import (
	"fmt"
	"os"
	"path/filepath"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
const LedgerDirLockName = ".ledger.lock"

// .
// .
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
