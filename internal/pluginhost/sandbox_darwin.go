//go:build darwin || ios

package pluginhost

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"os/exec"
	"path/filepath"
	"strings"
)

const seatbeltProfile = `(version 1)
(allow default)
(deny network*)
(deny file-write*)
(allow file-write-data (literal "/dev/null"))
(deny file-read* (regex #"^/dev/(tty|pty|ptmx|console)"))
(deny mach-lookup)
(deny iokit-open)
(deny ipc-posix-shm*)
(deny lsopen)
(deny appleevent-send)
(deny job-creation)
(deny signal)
(allow signal (target same-sandbox))
(deny file-read* (literal "/etc/master.passwd"))
(deny file-read* (subpath "/etc/ssh"))
(deny file-read* (subpath "/var/root/.ssh"))
(deny file-read* (regex #"^/Users/[^/]+/\.ssh"))
`

func seatbeltCompute(profile *AcceleratorProfile) (services, drivers []string) {
	if profile == nil {
		return nil, nil
	}
	for _, part := range strings.Split(profile.Backend, "+") {
		if part == "metal" {
			return []string{"com.apple.MTLCompilerService"}, []string{"AGXDeviceUserClient"}
		}
	}
	return nil, nil
}

func containArgv(_ context.Context, argv []string, accelerator *AcceleratorProfile, files nativeFiles) ([]string, supervisor.Containment, error) {
	if len(argv) == 0 {
		return nil, supervisor.Containment{}, fmt.Errorf("nothing to contain")
	}
	sb, err := exec.LookPath("sandbox-exec")
	if err != nil {

		return nil, supervisor.Containment{}, fmt.Errorf("sandbox-exec is not on PATH; native T3 plugins are not run uncontained")
	}
	paths, err := files.paths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	var profile strings.Builder

	var unwritable error
	quote := func(s string) string {
		q, err := firewall.SBPLString(s)
		if err != nil && unwritable == nil {
			unwritable = err
		}
		return q
	}
	profile.WriteString(seatbeltProfile)
	services, drivers := seatbeltCompute(accelerator)
	for _, name := range services {
		profile.WriteString("(allow mach-lookup (global-name " + quote(name) + "))\n")
	}
	for _, class := range drivers {
		profile.WriteString("(allow iokit-open (iokit-user-client-class " + quote(class) + "))\n")
	}
	filter := func(p nativePath) string {
		kind := "literal"
		if p.dir {
			kind = "subpath"
		}
		return "(" + kind + " " + quote(p.path) + ")"
	}
	for _, p := range paths {
		if p.read {
			continue
		}
		profile.WriteString("(deny file-read* (require-all " + filter(p))
		for _, own := range paths {
			if own.read && p.dir && nativeBelow(p.path, own.path) {
				profile.WriteString(" (require-not " + filter(own) + ")")
			}
		}
		profile.WriteString("))\n")

		ancestors := make(map[string]bool)
		for _, own := range paths {
			if !p.dir || !own.read || !nativeBelow(p.path, own.path) {
				continue
			}
			for dir := filepath.Dir(own.path); dir == p.path || nativeBelow(p.path, dir); dir = filepath.Dir(dir) {
				if !ancestors[dir] {
					profile.WriteString("(allow file-read-metadata (literal " + quote(dir) + "))\n")
					ancestors[dir] = true
				}
				if dir == p.path {
					break
				}
			}
		}
	}

	profile.WriteString("(deny process-exec*)\n")
	started := make(map[string]bool)
	start := func(rule string) {
		if !started[rule] {
			started[rule] = true
			profile.WriteString("(allow process-exec* " + rule + ")\n")
		}
	}
	for _, image := range imageSpellings(argv[0]) {
		start("(literal " + quote(image) + ")")
	}
	own, err := files.ownPaths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	for _, p := range own {
		start(filter(p))
	}
	if unwritable != nil {

		return nil, supervisor.Containment{}, fmt.Errorf("the Seatbelt profile cannot name a path: %w; native T3 plugins are not run uncontained", unwritable)
	}
	wrapped := append([]string{sb, "-p", profile.String(), "--"}, argv...)

	description := "contained (Seatbelt: no network or local socket, read-only filesystem, no system service lookup, no device driver connection, no named shared memory, no terminal device, no LaunchServices open, no Apple events, no signal to a process outside, only its own programs started; ssh and master.passwd denied; other user-readable credentials are NOT; a request to launchd to start or disable a job the user already has is NOT refused)"
	if len(files.places) != 0 {
		description += "; existing AII OS paths masked (selected plugin material retained)" + nativePathLimit
	}
	if len(services)+len(drivers) != 0 {
		description += "; " + accelerator.Backend + " compute services and drivers: " + strings.Join(append(services, drivers...), ", ")
	}
	return wrapped, supervisor.Containment{Description: description, NetworkDenied: true, FilesystemRestricted: true}, nil
}

func imageSpellings(image string) []string {
	out := []string{image}
	if real, err := filepath.EvalSymlinks(image); err == nil && real != image {
		out = append(out, real)
	}
	if alias := firewall.ResolveSlot(image); alias != image && alias != out[len(out)-1] {
		out = append(out, alias)
	}
	return out
}
