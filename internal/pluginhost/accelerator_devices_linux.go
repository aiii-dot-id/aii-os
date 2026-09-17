//go:build linux

package pluginhost

import (
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func linuxAcceleratorDevices(profile *AcceleratorProfile, dev fs.FS) ([]string, error) {
	if profile == nil || profile.Backend != "vulkan" {
		return nil, nil
	}
	entries, err := fs.ReadDir(dev, ".")
	if err != nil {
		return nil, fmt.Errorf("read /dev: %w", err)
	}
	var devices []string
	nvidiaGPU, nvidiaControl := false, false
	add := func(name string, entry fs.DirEntry) error {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect /dev/%s: %w", name, err)
		}
		if info.Mode()&fs.ModeType != fs.ModeDevice|fs.ModeCharDevice {
			return fmt.Errorf("/dev/%s must be a character device, not a symlink or another file type", name)
		}
		devices = append(devices, "/dev/"+name)
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case name == "dri":
			if !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
				return nil, fmt.Errorf("/dev/dri must be a real directory")
			}
			renders, err := fs.ReadDir(dev, name)
			if err != nil {
				return nil, fmt.Errorf("read /dev/dri: %w", err)
			}
			for _, render := range renders {
				if numberedDevice(render.Name(), "renderD") {
					if err := add(path.Join(name, render.Name()), render); err != nil {
						return nil, err
					}
				}
			}
		case name == "nvidiactl" || numberedDevice(name, "nvidia"):
			if err := add(name, entry); err != nil {
				return nil, err
			}
			nvidiaControl = nvidiaControl || name == "nvidiactl"
			nvidiaGPU = nvidiaGPU || name != "nvidiactl"
		}
	}
	if nvidiaGPU != nvidiaControl {
		return nil, fmt.Errorf("NVIDIA Vulkan requires both /dev/nvidiactl and a numbered GPU device")
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("Vulkan hardware device nodes unavailable; software/CPU substitution is not device admission")
	}
	slices.Sort(devices)
	return devices, nil
}

func numberedDevice(name, prefix string) bool {
	suffix, ok := strings.CutPrefix(name, prefix)
	if !ok || suffix == "" {
		return false
	}
	for _, ch := range suffix {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
