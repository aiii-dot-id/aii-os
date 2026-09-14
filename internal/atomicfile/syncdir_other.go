//go:build !windows

package atomicfile

import "os"

// .
// .
// .
// .
// .
// .
// .
func SyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
