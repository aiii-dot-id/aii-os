//go:build windows

package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

func StopChannelPresent(dir string) (bool, error) {
	return probeStopChannels(StopChannelNames(dir), func(name string) error {
		n, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		h, err := windows.OpenEvent(windows.SYNCHRONIZE, false, n)
		if err != nil {
			return err
		}
		return windows.CloseHandle(h)
	})
}

func probeStopChannels(names []string, probe func(string) error) (bool, error) {
	var failures []error
	for _, name := range names {
		err := probe(name)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			failures = append(failures, fmt.Errorf("stop channel %s: %w", name, err))
		}
	}
	return false, errors.Join(failures...)
}

func Stop(slot string) error {
	root, err := Root()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, slot)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("inspect slot %s: %w", dir, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return stopDirectory(ctx, dir)
}

func stopDirectory(ctx context.Context, dir string) error {
	return stopAndWait(ctx, dir, func() error {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		return awaitStop(ctx, func() (bool, error) { return StopChannelPresent(dir) }, tick.C)
	})
}

func stopAndWait(ctx context.Context, dir string, wait func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	found, err := probeStopChannels(StopChannelNames(dir), func(name string) error {
		n, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return err
		}
		h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, n)
		if err != nil {
			return err
		}

		return errors.Join(windows.SetEvent(h), windows.CloseHandle(h))
	})
	if err != nil {
		return fmt.Errorf("request stop for %s: %w", dir, err)
	}
	if !found {
		return ErrNotRunning
	}
	if err := wait(); err != nil {
		return fmt.Errorf("stop requested for %s, but shutdown is not confirmed (check the identity log or retry stop): %w", dir, err)
	}
	return nil
}

func awaitStop(ctx context.Context, present func() (bool, error), ticks <-chan time.Time) error {
	for {
		found, err := present()
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
		}
	}
}

var ErrNotRunning = fmt.Errorf("not running")
