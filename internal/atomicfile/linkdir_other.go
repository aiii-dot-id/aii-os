//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
)

func LinkDir(target, alias string) error {
	rel, err := filepath.Rel(filepath.Dir(alias), target)
	if err != nil {
		return err
	}
	return os.Symlink(rel, alias)
}
