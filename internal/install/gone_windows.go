//go:build windows

package install

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func SlotGone(slot string) error {
	if err := runValueGone(Unit + slot); err != nil {
		return err
	}
	root, err := Root()
	if err != nil {
		return err
	}
	return stopChannelGone(filepath.Join(root, slot))
}

func MachineGone() error {
	if err := runValueGone(MachineValue); err != nil {
		return err
	}
	name, err := machineTaskName()
	if err != nil {
		return err
	}
	if _, err := schtasks("/Query", "/TN", name); err == nil {
		return fmt.Errorf("the machine service's task %s is still registered", name)
	}
	dir, err := MachineDir()
	if err != nil {
		return err
	}
	return stopChannelGone(dir)
}

func runValueGone(name string) error {
	for _, key := range []string{runKey, startupApprovedKey} {
		k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("open HKCU\\%s: %w", key, err)
		}
		_, _, err = k.GetValue(name, nil)
		k.Close()
		if err == nil {
			return fmt.Errorf("%q is still in HKCU\\%s", name, key)
		}
		if !errors.Is(err, registry.ErrNotExist) {
			return fmt.Errorf("read %q in HKCU\\%s: %w", name, key, err)
		}
	}
	return nil
}

func stopChannelGone(dir string) error {
	present, err := StopChannelPresent(dir)
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("something still runs in %s (its stop channel is held)", dir)
	}
	return nil
}
