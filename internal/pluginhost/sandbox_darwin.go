//go:build darwin || ios

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const seatbeltProfile = `(version 1)
(allow default)
(deny network*)
(deny file-write*)
(allow file-write-data (literal "/dev/null"))
(deny file-read*)
(deny file-test-existence)
(deny file-search)
(allow file-read* file-test-existence (literal "/"))
(allow file-test-existence (subpath "/Library/GPUBundles"))
`

var seatbeltSystemExcept = []string{"/System/Volumes", "/usr/local"}

const seatbeltRefused = `(deny file-read* file-test-existence (regex #"^/dev/(tty|pty|ptmx|console)"))
(deny mach-lookup)
(deny iokit-open)
(deny ipc-posix-shm*)
(deny lsopen)
(deny appleevent-send)
(deny job-creation)
(deny signal)
(allow signal (target same-sandbox))
(deny file-read* file-test-existence (literal "/etc/master.passwd") (literal "/private/etc/master.passwd"))
(deny file-read* file-test-existence (subpath "/etc/ssh") (subpath "/private/etc/ssh"))
(deny file-read* file-test-existence (subpath "/var/root/.ssh") (subpath "/private/var/root/.ssh"))
(deny file-read* file-test-existence (regex #"^/Users/[^/]+/\.ssh"))
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

	files.shown = func(string) bool { return false }
	paths, err := files.paths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	own, err := files.ownPaths()
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	shown := nativeView(files.view)
	system, err := viewPaths(shown[:len(DefaultNativeView)])
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	added, err := viewPaths(shown[len(DefaultNativeView):])
	if err != nil {
		return nil, supervisor.Containment{}, err
	}
	view := append(system, added...)
	images := imageSpellings(argv[0])
	var profile strings.Builder

	var unwritable error
	quote := func(s string) string {
		q, err := firewall.SBPLString(s)
		if err != nil && unwritable == nil {
			unwritable = err
		}
		return q
	}

	filter := func(p nativePath) string {
		kind := "literal"
		if p.dir {
			kind = "subpath"
		}
		if alias := rootLinkSpelling(p.path); alias != "" {
			return "(require-any (" + kind + " " + quote(p.path) + ") (" + kind + " " + quote(alias) + "))"
		}
		return "(" + kind + " " + quote(p.path) + ")"
	}
	profile.WriteString(seatbeltProfile)

	allowed := make(map[string]bool)
	allow := func(rule string) {
		if !allowed[rule] {
			allowed[rule] = true
			profile.WriteString("(allow file-read* file-test-existence " + rule + ")\n")
		}
	}
	for _, p := range system {
		allow(filter(p))
	}
	for _, dir := range seatbeltSystemExcept {
		profile.WriteString("(deny file-read* file-test-existence (subpath " + quote(dir) + "))\n")
	}
	for _, p := range added {
		allow(filter(p))
	}
	for _, p := range own {
		allow(filter(p))
	}
	for _, image := range images {
		allow("(literal " + quote(image) + ")")
	}
	for _, p := range paths {
		if p.read {
			allow(filter(p))
		}
	}
	profile.WriteString(seatbeltRefused)
	services, drivers := seatbeltCompute(accelerator)
	for _, name := range services {
		profile.WriteString("(allow mach-lookup (global-name " + quote(name) + "))\n")
	}
	for _, class := range drivers {
		profile.WriteString("(allow iokit-open (iokit-user-client-class " + quote(class) + "))\n")
	}

	for _, p := range paths {
		if p.read {
			continue
		}
		profile.WriteString("(deny file-read* file-test-existence (require-all " + filter(p))
		for _, handed := range paths {
			if handed.read && p.dir && nativeBelow(p.path, handed.path) {
				profile.WriteString(" (require-not " + filter(handed) + ")")
			}
		}
		profile.WriteString("))\n")
	}

	looked := make(map[string]bool)
	lookup := func(path string) {
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			if alias := rootLinkSpelling(dir); alias != "" && !looked[alias] {
				looked[alias] = true
				profile.WriteString("(allow file-read-metadata file-test-existence (literal " + quote(alias) + "))\n")
			}
			if !looked[dir] {
				looked[dir] = true
				profile.WriteString("(allow file-read-metadata file-test-existence (literal " + quote(dir) + "))\n")
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	for _, p := range view {
		lookup(p.path)
	}
	for _, p := range own {
		lookup(p.path)
	}
	for _, image := range images {
		lookup(image)
	}
	for _, p := range paths {
		if p.read {
			lookup(p.path)
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
	for _, image := range images {
		start("(literal " + quote(image) + ")")
	}
	for _, p := range own {
		start(filter(p))
	}
	if unwritable != nil {

		return nil, supervisor.Containment{}, fmt.Errorf("the Seatbelt profile cannot name a path: %w; native T3 plugins are not run uncontained", unwritable)
	}
	wrapped := append([]string{sb, "-p", profile.String(), "--"}, argv...)

	description := "contained (Seatbelt: no network or local socket, nothing read but " + strings.Join(shown, ", ") + " and the plugin's own material" + withheld(added) + ", no file outside them told apart from a missing one, no volume search, nothing writable, no system service lookup, no device driver connection, no named shared memory, no terminal device, no LaunchServices open, no Apple events, no signal to a process outside, only its own programs started, ssh and password files denied; a request to launchd to start or disable a job the user already has is NOT refused)"
	if len(files.places) != 0 {
		description += "; existing AII OS paths masked where shown (selected plugin material retained)" + nativePathLimit
	}
	if len(services)+len(drivers) != 0 {
		description += "; " + accelerator.Backend + " compute services and drivers: " + strings.Join(append(services, drivers...), ", ")
	}
	return wrapped, supervisor.Containment{Description: description, NetworkDenied: true, FilesystemRestricted: true}, nil
}

func withheld(added []nativePath) string {
	var not, part []string
	for _, dir := range seatbeltSystemExcept {
		whole, inside := false, false
		for _, p := range added {
			whole = whole || p.path == dir || nativeBelow(p.path, dir)
			inside = inside || nativeBelow(dir, p.path)
		}
		switch {
		case whole:
		case inside:
			part = append(part, dir)
		default:
			not = append(not, dir)
		}
	}
	var says []string
	if len(not) != 0 {
		says = append(says, "not "+strings.Join(not, " or "))
	}
	if len(part) != 0 {
		says = append(says, "of "+strings.Join(part, " or ")+" only what is named")
	}
	if len(says) == 0 {
		return ""
	}
	return " (" + strings.Join(says, "; ") + ")"
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

func viewPaths(view []string) ([]nativePath, error) {
	var out []nativePath
	for _, path := range view {
		if !filepath.IsAbs(path) {
			return nil, &os.PathError{Op: "native view requires an absolute path", Path: path, Err: fs.ErrInvalid}
		}
		real, err := filepath.EvalSymlinks(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(real)
		if err != nil {
			return nil, err
		}
		out = append(out, nativePath{path: real, source: real, dir: info.IsDir(), read: true})
		if alias := firewall.ResolveSlot(path); alias != real {
			out = append(out, nativePath{path: alias, source: real, dir: false, read: true})
		}
	}
	return out, nil
}

func rootLinkSpelling(path string) string {
	for _, name := range []string{"var", "tmp", "etc"} {
		real := "/private/" + name
		if path != real && !strings.HasPrefix(path, real+"/") {
			continue
		}
		if target, err := os.Readlink("/" + name); err != nil || filepath.Clean(target) != "private/"+name {
			return ""
		}
		return "/" + name + strings.TrimPrefix(path, real)
	}
	return ""
}
