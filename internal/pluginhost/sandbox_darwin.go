//go:build darwin

package pluginhost

import (
	"fmt"
	"os/exec"
)

// Containment for a native T3 child, macOS mechanism: Seatbelt, through
// sandbox-exec.
//
// SAME ENGINE THE BROWSERS USE. Seatbelt is what Chromium and Firefox
// contain their renderers with in production today — Chromium's Mac
// sandbox design was still being extended in January 2025 — and
// sandbox-exec is the command-line front end to the same
// sandbox_init(3). It wraps argv exactly as bwrap does on Linux, so the
// seam here is the one that already existed rather than a new shape.
//
// DEPRECATED, AND STILL THE ONLY OPTION. The header has carried a
// deprecation notice since 10.8. App Sandbox is what Apple points to
// instead, and it requires the process to be a signed app bundle with
// entitlements — which a helper binary extracted from a plugin package
// is not and cannot become. There is an open request to Apple to
// clarify the timeline and publish a supported replacement
// (apple/containerization#737); until one exists, the choice is this or
// nothing, and nothing is what was here.
//
// PURE EXEC, NO CGO. sandbox_init is a C API and would pull cgo into a
// tree that builds for five platforms without it. The CLI is the same
// kernel mechanism at one remove.
//
// THE PROFILE IS ALLOW-DEFAULT, DENY THE TWO THINGS THAT MATTER, which
// is deliberately the shape of what Linux delivers rather than the
// strongest profile expressible. bwrap gives a read-only view of the
// whole filesystem and no network; this gives the same. A deny-default
// profile is stronger and is a research project per plugin — every
// dylib, every sysctl, every mach service a model runtime touches has
// to be enumerated, and a profile that is subtly too tight fails as a
// plugin that mysteriously will not start. Matching the Linux guarantee
// exactly is the honest first layer.
// The credential denies mirror sandbox_linux.go's masks (R75: the
// evolution that costs a legitimate plugin nothing): the OS-defined
// credential stores only — no model file, library, or device lives
// there. SBPL is last-match-wins, so the denies follow the default.
const seatbeltProfile = `(version 1)
(allow default)
(deny network*)
(deny file-write*)
(deny file-read* (literal "/etc/master.passwd"))
(deny file-read* (subpath "/etc/ssh"))
(deny file-read* (subpath "/var/root/.ssh"))
(deny file-read* (regex #"^/Users/[^/]+/\.ssh"))
`

func containArgv(argv []string) ([]string, string, error) {
	if len(argv) == 0 {
		return nil, "", fmt.Errorf("nothing to contain")
	}
	sb, err := exec.LookPath("sandbox-exec")
	if err != nil {
		// FAIL CLOSED, the same rule the Linux path follows. macOS HAS a
		// mechanism, so its absence is a broken host rather than a
		// platform without one — and sandbox-exec ships with the OS, so
		// reaching this means something is wrong that the operator
		// should hear about rather than have papered over.
		return nil, "", fmt.Errorf("sandbox-exec is not on PATH; native T3 plugins are not run uncontained")
	}
	wrapped := append([]string{sb, "-p", seatbeltProfile, "--"}, argv...)
	return wrapped, "contained (Seatbelt: no network, read-only filesystem)", nil
}
