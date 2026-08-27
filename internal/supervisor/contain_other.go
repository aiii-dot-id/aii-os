//go:build !windows

package supervisor

// Post-spawn containment where the mechanism is applied to the command
// line instead of to the process.
//
// Linux and macOS both wrap argv — bubblewrap and sandbox-exec are
// programs you run your program under — so their containment is already
// in place before this could run, and lives in pluginhost's
// sandbox_linux.go and sandbox_darwin.go. Android and iOS never spawn a
// native child at all: mobile T3 is in-process bundled and iOS forbids
// exec, so this is unreachable there by construction.
//
// Nil cleanup, empty string, nil error: nothing to do, nothing held,
// and nothing wrong. The telemetry line comes from the argv-wrapping
// half on the platforms that have one; the address-space envelope is
// rlimit_linux.go's prlimit on Linux (the rlimitASBytes parameter is
// consumed by the Windows one-job containment only — D19).
func containProcess(pid int, rlimitASBytes uint64) (func(), string, error) {
	return nil, "", nil
}
