package atomicfile

import (
	"fmt"
	"os"
)

type earlier struct{ f *os.File }

func (e *earlier) release() {
	if e != nil {
		e.f.Close()
	}
}

func (e *earlier) empty(path string) error {
	if e == nil {
		return nil
	}
	defer e.f.Close()
	names, err := namesOf(e.f)
	if err != nil {
		return fmt.Errorf("the file %s replaced could not be asked whether it has another name, and may hold its earlier contents there: %w", path, err)
	}
	if names == 0 {
		return nil
	}
	if now, err := os.Lstat(path); err == nil {
		if st, serr := e.f.Stat(); serr == nil && os.SameFile(now, st) {
			return nil
		}
	}
	if err := emptyFile(e.f); err != nil {
		return fmt.Errorf("the file %s replaced has another name, and emptying it failed, so the earlier contents may be readable there: %w", path, err)
	}
	return nil
}
