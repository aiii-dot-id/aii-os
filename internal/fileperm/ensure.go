package fileperm

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

var ErrNotOwnerOnly = errors.New("the directory is open to other accounts")

func EnsureOwnerOnlyDir(dir string) error {
	st, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := MkdirOwnerOnly(dir); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		st, err = os.Stat(dir)
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	ok, err := IsOwnerOnlyDir(dir)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dir, err)
	}
	if !ok {
		return fmt.Errorf("%w: %s", ErrNotOwnerOnly, dir)
	}
	return nil
}
