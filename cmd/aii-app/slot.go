//go:build (darwin && !ios) || (linux && !android) || windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/install"
)

var firstStartWait = 2 * time.Minute

func firstSlot(root string, create func() error) (int, bool, error) {
	unlock, err := lockFirstStart(root)
	if err != nil {
		return 0, false, err
	}
	defer unlock()
	slots, err := install.Slots(root)
	if err != nil {
		return 0, false, err
	}
	if len(slots) > 0 {
		return slots[0], false, nil
	}
	if err := create(); err != nil {
		return 0, false, err
	}

	slots, err = install.Slots(root)
	if err != nil || len(slots) == 0 {
		return 0, false, errors.New("the slot was created but cannot be found again")
	}
	return slots[0], true, nil
}

func lockFirstStart(root string) (func(), error) {

	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", root, err)
	}
	path := filepath.Join(root, install.FirstStartLock)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	deadline := time.Now().Add(firstStartWait)
	for {
		err := filelock.Lock(f)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, filelock.ErrHeld) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, errors.New("Another launch of AII OS is still setting up this account's first identity. Wait for it to finish, then open AII OS again. Nothing was changed.")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func reachable(slotDir string) error {
	if st, err := os.Stat(slotDir); err == nil && st.IsDir() {
		return nil
	}
	target, err := os.Readlink(slotDir)
	if err != nil {

		return fmt.Errorf("Your identity's folder %s cannot be reached. Restore that folder, then open AII OS again. Nothing was changed.", slotDir)
	}
	if !filepath.IsAbs(target) {

		target = filepath.Join(filepath.Dir(slotDir), target)
	}
	return fmt.Errorf("Your identity's folder %s points at %s, which cannot be reached. Connect that drive or restore that folder, then open AII OS again. Nothing was changed.", slotDir, target)
}
