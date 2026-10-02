package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
)

const identityClaimName = ".identity.lock"

var errIdentityRunning = errors.New("this identity is already running in another process")

func (a *App) claimIdentity(dir string) (release func(), err error) {
	if a.identityClaim != nil {
		return func() {}, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("the identity's data directory %s: %w", dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, identityClaimName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot open the identity's claim in %s: %w", dir, err)
	}
	if err := filelock.Lock(f); err != nil {
		f.Close()
		if errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("%w: the identity whose data is in %s — this start changed nothing; stop that process, or start a different identity", errIdentityRunning, dir)
		}
		return nil, fmt.Errorf("cannot take the identity's claim in %s: %w", dir, err)
	}
	a.identityClaim = f
	return a.releaseIdentityClaim, nil
}

func (a *App) releaseIdentityClaim() {
	if a.identityClaim == nil {
		return
	}
	a.identityClaim.Close()
	a.identityClaim = nil
}
