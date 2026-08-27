//go:build !linux && !windows

package supervisor

// The native-T3 address-space envelope where no mechanism exists —
// recorded honestly, never silently (the five-platform law allows a
// platform-conditional no-op only when documented):
//
//   - macOS: there is no cross-process setrlimit (no prlimit
//     equivalent); a pre-exec setrlimit-and-restore in the parent
//     would race the whole daemon's own allocations against the
//     child's window — refusing that risk is the point of the no-op.
//     NO-OP, recorded. PROBED 2026-08-26 (macOS 26.5, M3 Ultra): the
//     kernel refuses even to LOWER RLIMIT_AS/RLIMIT_DATA (setrlimit
//     EINVAL from unlimited), and a 2.5x-over allocation sails through
//     an "applied" sh-wrapper ulimit — the no-op is the kernel's
//     answer, not our omission (DESIGN-WINDOWS-T3-WALLS.md appendix).
//     The eventual mechanism is launchd HardResourceLimits, tied to
//     the deferred service story.
//   - android/ios: native T3 children are never spawned there (mobile
//     is in-process bundled T3; iOS forbids exec) — the no-op is
//     unreachable by construction.
//
// Windows moved out of this file on 2026-08-26: the job-object
// mechanism lives in rlimit_windows.go, using the same plumbing
// contain_windows.go established.
//
// wasm children are NEVER enveloped this way on any platform: their
// ceiling is the worker's -memory-max, enforced in-process by wazero.

import "fmt"

// applyAddressSpaceLimit records the honest no-op. The error is always
// nil: NO MECHANISM is not the same failure as a mechanism refusing.
// Refusing to spawn here would ground every plugin on macOS over a gap
// the platform never had — the telemetry line is the honest answer,
// and it is the reason the caller logs it every time.
func applyAddressSpaceLimit(pid int, bytes uint64) (string, error) {
	return fmt.Sprintf("RLIMIT_AS %d bytes requested but NOT ENFORCED on this platform (no cross-process rlimit mechanism)", bytes), nil
}
