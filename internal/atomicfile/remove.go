package atomicfile

import (
	"fmt"
	"os"
)

func Remove(path string) error {
	f, err := openToRemove(path)
	if err != nil {
		return fmt.Errorf("%s was not removed: %w", path, err)
	}
	if f == nil {
		return os.Remove(path)
	}
	defer f.Close()
	if names, err := namesOf(f); err != nil || names > 1 {
		if err == nil {
			err = emptyFile(f)
		}
		if err != nil {
			return fmt.Errorf("%s has another name, and emptying it failed, so it was not removed: %w", path, err)
		}
	}
	if removeWindow != nil {
		removeWindow(path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if names, err := namesOf(f); err != nil || names > 0 {
		if err == nil {
			err = emptyFile(f)
		}
		if err != nil {
			return fmt.Errorf("%s was removed, and a name made for it meanwhile may still hold it: %w", path, err)
		}
	}
	return nil
}

var removeWindow func(path string)

func emptyFile(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	return f.Sync()
}
