//go:build linux || android

package pluginhost

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/oswall"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func containArgv(ctx context.Context, argv []string, profile *AcceleratorProfile, files nativeFiles) ([]string, supervisor.Containment, error) {
	bwrap, err := oswall.Program()
	if err != nil {

		return nil, supervisor.Containment{}, fmt.Errorf("bubblewrap is not installed; native T3 plugins are not run uncontained (install bwrap)")
	}
	if len(argv) == 0 {
		return nil, supervisor.Containment{}, fmt.Errorf("nothing to contain")
	}

	if err := checkNamespace(ctx, bwrap, oswall.StartWait(files.startSeconds)); err != nil {
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

	view := nativeView(files.view)
	wrapped := []string{
		bwrap,
		"--unshare-all",
		"--die-with-parent",

		"--new-session",

		"--seccomp", strconv.Itoa(3 + files.inherited),
	}
	wrapped = append(wrapped, oswall.View(view, false)...)
	wrapped = append(wrapped,
		"--dev", "/dev",

		"--proc", "/proc",
	)

	if os.Geteuid() == 0 {
		wrapped = append(wrapped, "--cap-drop", "ALL")
	}
	for _, device := range devices {
		wrapped = append(wrapped, "--dev-bind", device, device)
	}

	standIns := nvidiaDisplayStandIn(profile, devices)
	for _, node := range standIns {
		wrapped = append(wrapped, "--dev-bind", os.DevNull, node)
	}
	own, err := files.ownPaths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}

	var ownShown []string
	for _, o := range own {
		ownShown = append(ownShown, o.path)
	}
	files.shown = oswall.Shown(view, ownShown)
	paths, err := files.paths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}

	var layers []oswall.Layer
	var brought []string
	for _, o := range own {

		if o.path != o.source && oswall.Shown(view)(o.path) {
			continue
		}
		layers = append(layers, oswall.Layer{Path: o.path, Source: o.source})
		brought = append(brought, o.path)
	}
	if program := argv[0]; filepath.IsAbs(program) && !strings.HasPrefix(program, "/proc/") {
		if info, err := os.Stat(program); err == nil && info.Mode().IsRegular() && !oswall.Shown(view, brought)(program) {
			layers = append(layers, oswall.Bind(program, false))
			brought = append(brought, program)
		}
	}
	for _, p := range paths {
		if p.read {
			layers = append(layers, oswall.Layer{Path: p.path, Source: p.source})
			brought = append(brought, p.path)
			continue
		}
		layers = append(layers, oswall.Mask(p.path))
	}

	hidden := runtimeDirs()
	for _, dir := range hidden {
		layers = append(layers, oswall.Mask(dir))
	}
	for _, m := range credentialMasks() {
		layers = append(layers, oswall.Mask(m))
	}
	oswall.Sort(layers)
	wrapped = append(wrapped, oswall.Args(layers, oswall.Shown(view, brought))...)

	wrapped = append(wrapped, "--remount-ro", "/")

	wrapped = append(wrapped, "--", "/bin/sh", "-c", `echo `+containmentBuiltMark+` >&2; exec "$@"`, "sh")

	description := "contained (bubblewrap: no network and no socket of any kind, read-only filesystem showing only " + strings.Join(view, ", ") + " and the plugin's own material, private PID-namespace procfs, no controlling terminal, no kernel keyring, the account's runtime directory hidden, ssh and shadow files masked"
	description += ")"
	if len(files.places) != 0 {
		description += "; existing AII OS private directories and files masked where shown (selected plugin material retained)" + nativePathLimit
	}
	if packets {

		description = strings.Replace(description, "no socket of any kind", "no socket but the packet-sequenced local ones "+profile.Backend+" cannot start without (a packet-sequenced listener on the file system is in their reach)", 1)
	}
	if len(devices) != 0 {
		description += "; " + profile.Backend + " compute devices: " + strings.Join(devices, ", ")
	}
	for _, node := range standIns {
		description += fmt.Sprintf(nvidiaDisplayStandInWords, node)
	}
	return append(wrapped, argv...), supervisor.Containment{Description: description, NetworkDenied: true, FilesystemRestricted: true, BuiltMark: containmentBuiltMark, Filter: filter,

		TermIsKill: true}, nil
}

const nvidiaDisplayStandInWords = "; the null device stands at %s, where NVIDIA's Vulkan driver crashes without a node: NVIDIA's display device is not admitted"

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
			m = append(m, d)
		}
	}
	for _, f := range []string{"/etc/shadow", "/etc/gshadow"} {
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() {
			m = append(m, f)
		}
	}
	return m
}
