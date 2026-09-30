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

func containArgv(ctx context.Context, argv []string, profile *AcceleratorProfile, files nativeFiles) ([]string, supervisor.Containment, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {

		return nil, supervisor.Containment{}, fmt.Errorf("bubblewrap is not installed; native T3 plugins are not run uncontained (install bwrap)")
	}
	if len(argv) == 0 {
		return nil, supervisor.Containment{}, fmt.Errorf("nothing to contain")
	}

	if err := checkNamespace(ctx, bwrap); err != nil {
		return nil, supervisor.Containment{}, err
	}

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
	if profile != nil && (profile.Backend == "cuda" || profile.Backend == "cuda+vulkan" || profile.Backend == "vulkan+cuda") {

		wrapped = append(wrapped, "--proc", "/proc")
	}
	for _, device := range devices {
		wrapped = append(wrapped, "--dev-bind", device, device)
	}
	paths, err := files.paths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	for _, p := range paths {
		switch {
		case p.read:
			wrapped = append(wrapped, "--ro-bind", p.source, p.path)
		case p.dir:
			wrapped = append(wrapped, "--tmpfs", p.path)
		}
	}

	for _, p := range paths {
		if !p.read && p.dir {
			wrapped = append(wrapped, "--remount-ro", p.path)
		}
	}
	wrapped = append(wrapped, credentialMasks()...)
	wrapped = append(wrapped, "--")

	description := "contained (bubblewrap: no network, read-only filesystem, ssh and shadow files masked; other user-readable credentials are NOT)"
	if len(files.places) != 0 {
		description += "; existing AII OS private directories masked (selected plugin material retained); standalone files outside masked directories are NOT protected" + nativePathLimit
	}
	if len(devices) != 0 {
		description += "; " + profile.Backend + " compute devices: " + strings.Join(devices, ", ")
	}
	if profile != nil && strings.Contains(profile.Backend, "cuda") {
		description += "; private PID-namespace procfs"
	}
	return append(wrapped, argv...), supervisor.Containment{Description: description, NetworkDenied: true, FilesystemRestricted: true}, nil
}

func credentialMasks() []string {
	var m []string

	dirs := []string{"/etc/ssh", "/root/.ssh"}
	if h, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, ".ssh"))
	}
	seen := map[string]bool{}
	for _, d := range dirs {

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
