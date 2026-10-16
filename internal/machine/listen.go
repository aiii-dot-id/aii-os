package machine

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

var ErrHeld = errors.New("another machine service holds this link")

var ErrNotOwnerOnly = fileperm.ErrNotOwnerOnly

type Holder struct {
	ln   net.Listener
	lock *os.File
	path string

	managed bool
}

func Listen(dir string) (*Holder, error) {
	if err := ensureOwnerOnlyDir(dir); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the machine service's lock: %w", err)
	}
	if err := filelock.Lock(lock); err != nil {
		lock.Close()
		if errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("%w (%s)", ErrHeld, dir)
		}
		return nil, fmt.Errorf("take the machine service's lock: %w", err)
	}
	path, err := LinkPath(dir)
	var ln net.Listener
	var managed bool
	if err == nil {
		ln, managed, err = openLink(path)
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	return &Holder{ln: ln, lock: lock, path: path, managed: managed}, nil
}

func (h *Holder) Listener() net.Listener { return h.ln }

func (h *Holder) Path() string { return h.path }

func (h *Holder) Close() error {
	err := h.ln.Close()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	if !h.managed {
		err = errors.Join(err, removeLink(h.path))
	}
	return errors.Join(err, h.lock.Close())
}

func ensureOwnerOnlyDir(dir string) error {
	return fileperm.EnsureOwnerOnlyDir(dir)
}
