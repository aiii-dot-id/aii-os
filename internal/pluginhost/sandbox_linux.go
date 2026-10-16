//go:build linux || android

package pluginhost

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	packets := localPackets(profile)
	filter, err := syscallFilter(packets)
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	wrapped := []string{
		bwrap,
		"--unshare-all",
		"--die-with-parent",

		"--new-session",

		"--seccomp", strconv.Itoa(3 + files.inherited),
		"--ro-bind", "/", "/",
		"--dev", "/dev",

		"--proc", "/proc",
	}

	if os.Geteuid() == 0 {
		wrapped = append(wrapped, "--cap-drop", "ALL")
	}
	for _, device := range devices {
		wrapped = append(wrapped, "--dev-bind", device, device)
	}
	paths, err := files.paths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}

	hidden := runtimeDirs()
	for _, dir := range hidden {
		wrapped = append(wrapped, "--tmpfs", dir)
	}
	own, err := files.ownPaths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	for _, o := range own {
		under := false
		for _, dir := range hidden {
			under = under || nativeBelow(dir, o.path)
		}
		listed := false
		for _, p := range paths {
			listed = listed || (p.read && p.path == o.path)
		}
		if under && !listed {
			wrapped = append(wrapped, "--ro-bind", o.source, o.path)
		}
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

	for _, dir := range hidden {
		wrapped = append(wrapped, "--remount-ro", dir)
	}
	wrapped = append(wrapped, credentialMasks()...)

	wrapped = append(wrapped, "--", "/bin/sh", "-c", `echo `+containmentBuiltMark+` >&2; exec "$@"`, "sh")

	description := "contained (bubblewrap: no network and no socket of any kind, read-only filesystem, private PID-namespace procfs, no controlling terminal, no kernel keyring, the account's runtime directory hidden, ssh and shadow files masked; other user-readable credentials are NOT)"
	if len(files.places) != 0 {
		description += "; existing AII OS private directories masked (selected plugin material retained); standalone files outside masked directories are NOT protected" + nativePathLimit
	}
	if packets {

		description = strings.Replace(description, "no socket of any kind", "no socket but the packet-sequenced local ones "+profile.Backend+" cannot start without (a packet-sequenced listener on the file system is in their reach)", 1)
	}
	if len(devices) != 0 {
		description += "; " + profile.Backend + " compute devices: " + strings.Join(devices, ", ")
	}
	return append(wrapped, argv...), supervisor.Containment{Description: description, NetworkDenied: true, FilesystemRestricted: true, BuiltMark: containmentBuiltMark, Filter: filter}, nil
}

func runtimeDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	for _, d := range []string{os.Getenv("XDG_RUNTIME_DIR"), "/run/user/" + strconv.Itoa(os.Getuid())} {
		if !filepath.IsAbs(d) {
			continue
		}
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		if d == "/" || seen[d] {
			continue
		}
		seen[d] = true
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

const containmentBuiltMark = "event=contained"

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
