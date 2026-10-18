//go:build (linux && !android) || (darwin && !ios) || windows

package plugstore

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
)

var ErrPinned = errors.New("an activation runs from this entry")

var ErrBusy = errors.New("a file of this entry is open")

type Held struct {
	w      *Writer
	e      Entry
	record string
	f      *os.File
}

func (w *Writer) HoldExclusive(e Entry) (*Held, error) {
	record, err := w.Record(e)
	if err != nil {
		return nil, err
	}
	h := &Held{w: w, e: e, record: record}
	if record == "" {
		return h, nil
	}
	f, err := openForCollect(record)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return h, nil
	case err != nil:
		return nil, fmt.Errorf("hold %s: %w", e, Busy(err))
	}
	if err := filelock.Lock(f); err != nil {
		f.Close()
		if errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("hold %s: %w", e, ErrPinned)
		}
		return nil, fmt.Errorf("hold %s: %w", e, err)
	}
	h.f = f
	return h, nil
}

func (h *Held) Read() ([]byte, error) {
	if h.f == nil {
		return nil, fs.ErrNotExist
	}
	if _, err := h.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(h.f, maxRecordBytes))
}

func (h *Held) Close() error {
	if h == nil || h.f == nil {
		return nil
	}
	err := h.f.Close()
	h.f = nil
	return err
}

func (h *Held) Retire() error {
	defer h.Close()
	path, err := h.w.Path(h.e)
	if err != nil {
		return err
	}
	trash := h.w.Trash()
	if h.f != nil {
		if err := os.Rename(h.record, trash+RecordSuffix); err != nil {
			return fmt.Errorf("remove %s: %w", h.e, Busy(err))
		}
	}
	if err := os.Rename(path, trash); err != nil && !errors.Is(err, fs.ErrNotExist) {

		if h.f != nil {
			if berr := os.Rename(trash+RecordSuffix, h.record); berr != nil {
				err = errors.Join(err, fmt.Errorf("its record stays in the trash: %w", berr))
			}
		}
		return fmt.Errorf("remove %s: %w", h.e, Busy(err))
	}
	return nil
}

func (h *Held) Discard() error {
	if err := h.Retire(); err != nil {
		return err
	}
	return h.w.EmptyTrash()
}

func (w *Writer) EmptyTrash() error {
	dir := filepath.Join(w.Dir, DirTrash)
	names, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range names {
		if err := os.RemoveAll(filepath.Join(dir, n.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (w *Writer) Remove(e Entry) error {
	h, err := w.HoldExclusive(e)
	if err != nil {
		return err
	}
	return h.Discard()
}

func HeldForTest(dir string) map[string]int {
	held.mu.Lock()
	defer held.mu.Unlock()
	out := map[string]int{}
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	for path, n := range held.n {
		if strings.HasPrefix(path, prefix) {
			out[path] = n
		}
	}
	return out
}
