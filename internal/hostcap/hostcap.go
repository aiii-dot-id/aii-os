// Package hostcap answers, at RUNTIME, what this host can actually do
// — the runtime half of the five-platform law. The compile matrix
// (AGENTS.md 7) proves the tree BUILDS for every platform it claims;
// this package proves what a build can DO there, because the two are
// not the same thing: exec.Command("bash", ...) is valid Go on all
// five platforms and can only ever succeed on two of them.
//
// Lineage (operator-directed review, 2026-08-26): the C stack's
// sev_facility.h (a capability is a named thing with a per-platform
// provider, degraded/absent status, and an operator-visible reason —
// report, never crash) and sev_inprocess_host.h (topology carries
// GUARANTEES — the mobile app host requires NO_SUBPROCESS, and code
// asks the topology instead of assuming). Go-idiomatic per the same
// ruling: build-tagged provider files and one oracle, not a vtable.
//
// The four capabilities are the four exec-shaped fault lines found in
// that review:
//
//   - Subprocess:  can this process exec a child at all? iOS forbids
//     it outright; the Android app sandbox (SELinux untrusted_app,
//     W^X since API 29) effectively forbids it; and our mobile
//     artifact is a gomobile library inside a host app — it does not
//     even own a process to spawn from.
//   - Shell:       is a host command shell present for the shell
//     tool? bash on unix desktops, PowerShell on Windows (R79);
//     mobile has none — both OSes forbid spawning from app sandboxes.
//   - NativeChild: may we spawn AND contain a native T3 plugin?
//     Desktop yes (bwrap / Seatbelt / job objects); mobile never —
//     mobile T3 is in-process wasm by construction.
//   - SelfReplace: may we swap our own binary and re-exec? Desktop
//     yes (R70); on mobile the platform store owns the binary
//     lifecycle and the "binary" is a library — the concept does not
//     exist. (The review found reexec_unix.go with a !windows tag
//     silently capturing android and ios, where a rollback would have
//     bricked the boot: safe by circumstance is not safe by
//     construction. This package is the construction.)
//
// Every exec-shaped site in the tree must sit behind Can(); the guard
// test in this package enforces that with an allowlist, so a new
// subprocess call cannot land ungated by accident.
package hostcap

// Capability names one thing a host may or may not be able to do.
type Capability int

const (
	// Subprocess is the ability to exec a child process at all.
	Subprocess Capability = iota
	// Shell is the presence of a host command shell for the shell tool.
	Shell
	// NativeChild is the ability to spawn and contain a native T3
	// plugin child.
	NativeChild
	// SelfReplace is the ability to swap our own binary on disk and
	// re-exec into it (the R70 rollback contract).
	SelfReplace
)

// Status is a capability's answer. Reason is ALWAYS set when
// Available is false — absence is reported in words a person can act
// on, never as a bare refusal (the sev_facility law).
type Status struct {
	Available bool
	Reason    string
}

// Can reports whether this host provides the capability. The answer
// comes from the build-tagged provider for the platform this binary
// was compiled for; it never guesses about other platforms.
func Can(c Capability) Status { return can(c) }
