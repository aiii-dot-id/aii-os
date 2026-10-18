package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

type emptiedError struct {
	err   error
	wrote int
	of    int
}

func (e *emptiedError) Error() string {
	return fmt.Sprintf("%v — the file is left holding %d of the %d bytes of this write, and nothing of what it held before",
		e.err, e.wrote, e.of)
}

func (e *emptiedError) Unwrap() error { return e.err }

func truncateWriteClose(f *os.File, data []byte) error {
	if err := f.Truncate(0); err != nil {
		f.Close()
		return err
	}
	n, err := f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return &emptiedError{err: err, wrote: n, of: len(data)}
	}
	return nil
}

func writeFileNoFollowSame(path string, data []byte, perm os.FileMode, measured os.FileInfo, held func(string, os.FileInfo, *os.File) error) (same bool, made os.FileInfo, err error) {
	there := measured != nil
	for range 3 {
		if there {
			f, st, err := openHeld(path, perm, false, held)
			if err == nil {
				return measured != nil && os.SameFile(measured, st), nil, truncateWriteClose(f, data)
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return false, nil, err
			}
		}
		f, st, err := openHeld(path, perm, true, held)
		if err == nil {
			return measured == nil, st, truncateWriteClose(f, data)
		}
		if !errors.Is(err, fs.ErrExist) {

			return false, st, err
		}
		there = true
	}
	return false, nil, fmt.Errorf("%s was made and removed by something else each time this write tried it; nothing was written", path)
}

func openForCopy(path string, perm os.FileMode, fresh bool, held func(string, os.FileInfo, *os.File) error) (*os.File, os.FileInfo, error) {
	if !fresh {
		f, _, err := openHeld(path, perm, false, held)
		if err == nil {
			if err := f.Truncate(0); err != nil {
				f.Close()
				return nil, nil, err
			}
			return f, nil, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, nil, err
		}
	}
	f, st, err := openHeld(path, perm, true, held)
	if errors.Is(err, fs.ErrExist) {
		return nil, nil, fmt.Errorf("%s appeared while it was being copied to: nothing was copied, and what is there was not touched", path)
	}
	if err != nil {
		return nil, st, err
	}
	return f, st, nil
}

func openHeld(path string, perm os.FileMode, create bool, held func(string, os.FileInfo, *os.File) error) (*os.File, os.FileInfo, error) {
	f, st, err := openToWrite(path, perm, create)
	if err != nil || held == nil {
		return f, st, err
	}
	kept := held(path, st, f)
	if kept == nil {
		return f, st, nil
	}
	f.Close()
	if !create {
		return nil, nil, kept
	}
	return nil, st, kept
}
