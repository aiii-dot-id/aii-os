package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const diskMargin int64 = 2 << 30

func freeDisk(dir string) (int64, error) {
	p := filepath.Clean(dir)
	for {
		_, err := os.Stat(p)
		if err == nil {
			return freeDiskAt(p)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return 0, err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return 0, fmt.Errorf("no part of %s exists", dir)
		}
		p = parent
	}
}
