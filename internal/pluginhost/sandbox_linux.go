//go:build linux

package pluginhost

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"os"
	"os/exec"
	"path/filepath"
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
// .
// .
// .
// .
func containArgv(ctx context.Context, argv []string, profile *AcceleratorProfile) ([]string, supervisor.Containment, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
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
		// .
		// .
		return nil, supervisor.Containment{}, fmt.Errorf("bubblewrap is not installed; native T3 plugins are not run uncontained (install bwrap)")
	}
	if len(argv) == 0 {
		return nil, supervisor.Containment{}, fmt.Errorf("nothing to contain")
	}
	// .
	// .
	// .
	// .
	if err := checkNamespace(ctx, bwrap); err != nil {
		return nil, supervisor.Containment{}, err
	}
	// .
	// .
	// .
	devices, err := linuxAcceleratorDevices(profile, os.DirFS("/dev"))
	if err != nil {
		return nil, supervisor.Containment{}, fmt.Errorf("accelerator device admission: %w", err)
	}
	wrapped := []string{
		bwrap,
		"--unshare-all",
		"--die-with-parent",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
	}
	for _, device := range devices {
		wrapped = append(wrapped, "--dev-bind", device, device)
	}
	wrapped = append(wrapped, credentialMasks()...)
	wrapped = append(wrapped, "--")
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	description := "contained (bubblewrap: no network, read-only filesystem, ssh and shadow files masked; other user-readable credentials are NOT)"
	if len(devices) != 0 {
		description += "; Vulkan compute devices: " + strings.Join(devices, ", ")
	}
	return append(wrapped, argv...), supervisor.Containment{Description: description, NetworkDenied: true, FilesystemRestricted: true}, nil
}

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
func credentialMasks() []string {
	var m []string
	// .
	// .
	// .
	// .
	// .
	// .
	dirs := []string{"/etc/ssh", "/root/.ssh"}
	if h, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, ".ssh"))
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		// .
		// .
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			m = append(m, "--tmpfs", d)
		}
	}
	for _, f := range []string{"/etc/shadow", "/etc/gshadow"} {
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() {
			m = append(m, "--ro-bind", "/dev/null", f)
		}
	}
	return m
}
