//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

var ErrServiceRuns = errors.New("a machine service runs from this directory: it is stopped and unregistered first")

var errStopped = errors.New("the removal was stopped part way")

type Removal struct {
	Stores []StoreRemoval `json:"stores"`

	Dir     string   `json:"dir"`
	Gone    bool     `json:"gone"`
	DirGone bool     `json:"dir_gone"`
	Kept    string   `json:"kept,omitempty"`
	Left    []string `json:"left,omitempty"`

	Freed int64 `json:"freed"`
}

type StoreRemoval struct {
	Dir   string   `json:"dir"`
	Gone  bool     `json:"gone"`
	Kept  string   `json:"kept,omitempty"`
	Left  []string `json:"left,omitempty"`
	Busy  []string `json:"busy,omitempty"`
	Freed int64    `json:"freed"`
}

func (r Removal) Done() bool {
	if !r.Gone {
		return false
	}
	for _, s := range r.Stores {
		if !s.Gone {
			return false
		}
	}
	return true
}

func (r *Removal) store(dir string) *StoreRemoval {
	for i := range r.Stores {
		if r.Stores[i].Dir == filepath.Clean(dir) {
			return &r.Stores[i]
		}
	}
	return nil
}

var serviceNames = []string{linkFileName, relaunchFile, relaunchLockFile, failureFile}

func Remove(dir string) (Removal, error) { return removeSteps(dir, nil) }

func removeSteps(dir string, step func() bool) (Removal, error) {
	act := func() error {
		if step != nil && !step() {
			return errStopped
		}
		return nil
	}
	dir = filepath.Clean(dir)
	rem := Removal{Dir: dir}
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):

		pi, perr := os.Stat(filepath.Dir(dir))
		if perr != nil || !pi.IsDir() {
			return rem, fmt.Errorf("the machine service's directory %s is on a volume that is not there", dir)
		}
		rem.Gone, rem.DirGone = true, true
		def := defaultStoreDir(dir)
		if within(def, dir) {

			rem.Stores = []StoreRemoval{{Dir: def, Gone: true}}
			return rem, nil
		}
		s, err := removeStore(def, act)
		rem.Stores, rem.Freed = []StoreRemoval{s}, s.Freed
		return rem, err
	case err != nil:
		return rem, err
	case fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir():
		return rem, fmt.Errorf("the machine service's directory %s is not a directory where it lies", dir)
	}
	lock, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return rem, fmt.Errorf("open the machine service's lock: %w", err)
	}
	locked := true
	defer func() {
		if locked {
			lock.Close()
		}
	}()
	if err := filelock.Lock(lock); err != nil {
		if errors.Is(err, filelock.ErrHeld) {
			return rem, fmt.Errorf("%w (%s)", ErrServiceRuns, dir)
		}
		return rem, fmt.Errorf("take the machine service's lock: %w", err)
	}
	stores, err := StorePlaces(dir)
	if err != nil {
		return rem, err
	}
	allGone := true
	for _, d := range stores {
		s, err := removeStore(d, act)
		rem.Stores = append(rem.Stores, s)
		rem.Freed += s.Freed
		if err != nil {
			return rem, err
		}
		allGone = allGone && s.Gone
	}
	if !allGone {
		rem.Kept = "a store it names is kept, and its state is the only record of where that store lies"
		return rem, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return rem, err
	}
	for _, e := range entries {
		n := e.Name()
		switch {
		case n == stateFileName || n == lockFileName:
		case slices.Contains(serviceNames, n) || isTempOf(n, append([]string{stateFileName}, serviceNames...)):
			if err := act(); err != nil {
				return rem, err
			}
			if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
				rem.Kept = err.Error()
				return rem, nil
			}
		default:
			rem.Left = append(rem.Left, n)
		}
	}
	if err := act(); err != nil {
		return rem, err
	}
	if err := os.Remove(filepath.Join(dir, stateFileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		rem.Kept = err.Error()
		return rem, nil
	}
	locked = false
	lock.Close()
	if err := act(); err != nil {
		return rem, err
	}
	if err := os.Remove(filepath.Join(dir, lockFileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		rem.Kept = err.Error()
		return rem, nil
	}
	rem.Gone = true
	if len(rem.Left) == 0 {
		if err := act(); err != nil {
			return rem, err
		}
		if err := os.Remove(dir); err == nil {
			rem.DirGone = true
		} else if left, rerr := os.ReadDir(dir); rerr == nil {

			for _, e := range left {
				rem.Left = append(rem.Left, e.Name())
			}
		}
	}
	return rem, nil
}

func isTempOf(name string, names []string) bool {
	for _, n := range names {
		if strings.HasPrefix(name, "."+n+".tmp-") {
			return true
		}
	}
	return false
}

var storeOwnNames = []string{plugstore.DirArchives, plugstore.DirRoots, plugstore.DirBlobs, plugstore.DirViews, plugstore.DirTrash, plugstore.LockFile}

func removeStore(dir string, act func() error) (StoreRemoval, error) {
	dir = filepath.Clean(dir)
	s := StoreRemoval{Dir: dir}
	w, err := plugstore.OpenForRemoval(dir)
	switch {
	case errors.Is(err, plugstore.ErrStoreGone):
		s.Gone = true
		return s, nil
	case errors.Is(err, plugstore.ErrStoreUnreachable):
		s.Kept = "its volume is not there (the directory that holds it is missing); it is removed when uninstall runs again with the volume mounted"
		return s, nil
	case errors.Is(err, plugstore.ErrHeld):
		s.Kept = "a writer holds it: a machine service runs from it"
		return s, nil
	case err != nil:
		s.Kept = err.Error()
		return s, nil
	}
	defer w.Close()
	entries, err := storeEntries(w)
	if err != nil {
		s.Kept = "it does not list: " + err.Error()
		return s, nil
	}

	var holds []*plugstore.Held
	release := func() {
		for _, h := range holds {
			h.Close()
		}
	}
	for _, e := range entries {
		h, err := w.HoldExclusive(e)
		if err != nil {
			release()
			switch {
			case errors.Is(err, plugstore.ErrPinned):
				s.Kept = "an activation runs from " + e.String() + " (an identity runs a plugin from it): it is removed when uninstall runs again with nothing running from it"
			case errors.Is(err, plugstore.ErrBusy):
				s.Busy = append(s.Busy, e.String())
				s.Kept = "a process holds " + e.String() + " open"
			default:
				s.Kept = err.Error()
			}
			return s, nil
		}
		holds = append(holds, h)
	}
	s.Freed = storeBytes(dir)
	for i, h := range holds {
		if err := act(); err != nil {
			for _, rest := range holds[i:] {
				rest.Close()
			}
			return s, err
		}
		if err := h.Retire(); err != nil {
			if errors.Is(err, plugstore.ErrBusy) {
				s.Busy = append(s.Busy, entries[i].String())
				continue
			}
			for _, rest := range holds[i+1:] {
				rest.Close()
			}
			s.Kept = err.Error()
			return s, nil
		}
	}
	if err := act(); err != nil {
		return s, err
	}
	if err := w.EmptyTrash(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.Kept = "its trash was not emptied: " + err.Error()
		return s, nil
	}
	if len(s.Busy) > 0 {
		s.Kept = "a process holds " + strings.Join(s.Busy, ", ") + " open (an engine, a scanner): it is removed when uninstall runs again once nothing holds it"
		return s, nil
	}

	for _, kind := range []string{plugstore.DirArchives, plugstore.DirRoots, plugstore.DirBlobs, plugstore.DirViews} {
		names, err := os.ReadDir(filepath.Join(dir, kind))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			s.Kept = err.Error()
			return s, nil
		}
		for _, n := range names {
			if _, debris := debrisOwner(n.Name()); !debris {
				s.Left = append(s.Left, filepath.Join(kind, n.Name()))
				continue
			}
			if err := act(); err != nil {
				return s, err
			}
			if err := os.RemoveAll(filepath.Join(dir, kind, n.Name())); err != nil {
				s.Kept = err.Error()
				return s, nil
			}
		}
	}
	for _, d := range []string{plugstore.DirArchives, plugstore.DirRoots, plugstore.DirBlobs, plugstore.DirViews, plugstore.DirTrash} {
		if err := act(); err != nil {
			return s, err
		}
		_ = os.Remove(filepath.Join(dir, d))
	}
	top, err := os.ReadDir(dir)
	if err != nil {
		s.Kept = err.Error()
		return s, nil
	}
	for _, n := range top {
		if !slices.Contains(storeOwnNames, n.Name()) {
			s.Left = append(s.Left, n.Name())
		}
	}
	w.Close()
	if err := act(); err != nil {
		return s, err
	}
	if err := os.Remove(filepath.Join(dir, plugstore.LockFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.Kept = err.Error()
		return s, nil
	}
	s.Gone = true
	if len(s.Left) == 0 {
		if err := act(); err != nil {
			return s, err
		}
		_ = os.Remove(dir)
	}
	return s, nil
}

func storeBytes(dir string) int64 {
	var n int64
	for _, kind := range []string{plugstore.DirArchives, plugstore.DirBlobs, plugstore.DirRoots, plugstore.DirViews} {
		_ = filepath.WalkDir(filepath.Join(dir, kind), func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			if kind == plugstore.DirViews && filepath.Dir(p) != filepath.Join(dir, kind) {
				return nil
			}
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
			return nil
		})
	}
	return n
}

func ServiceRuns(dir string) (bool, error) {
	lock, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		if errors.Is(err, filelock.ErrHeld) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func StorePlaces(dir string) ([]string, error) {
	st, err := loadState(filepath.Join(dir, stateFileName))
	if err != nil {
		return nil, err
	}
	out := []string{defaultStoreDir(dir)}
	if st.Store != "" {
		out[0] = filepath.Clean(st.Store)
	}
	for _, d := range st.Draining {
		if d = filepath.Clean(d); !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out, nil
}

func StoreSize(dir string) int64 { return storeBytes(dir) }
