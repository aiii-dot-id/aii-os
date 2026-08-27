//go:build !linux && !darwin

package pluginhost

// Containment for a native T3 child where no mechanism is wired —
// recorded honestly, never silently (the five-platform law allows a
// platform-conditional no-op only when documented):
//
//   - Windows: contained, but not HERE. Windows contains a process
//     rather than a command line, so there is no argv to wrap: the job
//     object is applied after spawn, in supervisor/contain_windows.go.
//     What it does and does not enforce is stated there. The restricted
//     token that would close the filesystem and network gaps needs a
//     spawn-attribute seam and is the next layer.

//   - android/ios do NOT compile this file — GOOS implication routes
//     android to sandbox_linux.go and ios to sandbox_darwin.go (the
//     2026-08-26 review corrected this comment, which claimed they
//     landed here). Their true guard is upstream: pluginhost refuses
//     the native lane first via hostcap.NativeChild, with the
//     topology's own reason, before any sandbox file is consulted.
//
// macOS moved out of this file on 2026-08-25: Seatbelt through
// sandbox-exec wraps argv exactly as bubblewrap does, so it lives beside
// the Linux mechanism in sandbox_darwin.go.
//
// The error is always nil for the same reason bwrap-absent returns nil
// on Linux: no mechanism is not a mechanism refusing, and grounding
// every native plugin on a platform that never had one helps nobody.
func containArgv(argv []string) ([]string, string, error) {
	return argv, "no argv-level containment on this platform (see sandbox_other.go)", nil
}
