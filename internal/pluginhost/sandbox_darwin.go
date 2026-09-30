//go:build darwin

package pluginhost

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const seatbeltProfile = `(version 1)
(allow default)
(deny network*)
(deny file-write*)
(deny file-read* (literal "/etc/master.passwd"))
(deny file-read* (subpath "/etc/ssh"))
(deny file-read* (subpath "/var/root/.ssh"))
(deny file-read* (regex #"^/Users/[^/]+/\.ssh"))
`

func containArgv(_ context.Context, argv []string, _ *AcceleratorProfile, files nativeFiles) ([]string, supervisor.Containment, error) {
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
	profile.WriteString(seatbeltProfile)
	filter := func(p nativePath) string {
		kind := "literal"
		if p.dir {
			kind = "subpath"
		}
		return "(" + kind + " " + strconv.Quote(p.path) + ")"
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
					profile.WriteString("(allow file-read-metadata (literal " + strconv.Quote(dir) + "))\n")
					ancestors[dir] = true
				}
				if dir == p.path {
					break
				}
			}
		}
	}
	wrapped := append([]string{sb, "-p", profile.String(), "--"}, argv...)

	description := "contained (Seatbelt: no network, read-only filesystem, ssh and master.passwd denied; other user-readable credentials are NOT)"
	if len(files.places) != 0 {
		description += "; existing AII OS paths masked (selected plugin material retained)" + nativePathLimit
	}
	return wrapped, supervisor.Containment{Description: description, NetworkDenied: true, FilesystemRestricted: true}, nil
}
