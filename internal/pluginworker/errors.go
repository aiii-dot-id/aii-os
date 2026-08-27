package pluginworker

import (
	"errors"
	"fmt"
	"strings"
)

// The error taxonomy is the containment contract: every way a guest can
// fail maps to ONE typed error naming the requirement (R39 pattern), so
// the worker binary and the step-5 supervisor can act on kind, not on
// string matching. The C stack's posture is adopted: resource
// exhaustion and traps are "equivalent to a crashed native plugin"
// (ADR-033 Decision 5, line 197) — structured, fail-closed, never a
// fallback to weaker containment.

// ArtifactFormatError reports artifact bytes that violate the wasm
// binary layout the loader understands: an unrecognized preamble, a
// malformed LEB128, a truncated section, a section length running past
// its container. The unwrapping walker (component.go) feeds on
// untrusted bytes, so every malformation is this typed rejection —
// never a panic, never a guess. Offset is relative to the frame Detail
// names: the artifact itself, or one embedded module's payload.
type ArtifactFormatError struct {
	Offset int
	Detail string
}

func (e *ArtifactFormatError) Error() string {
	return fmt.Sprintf("pluginworker: malformed wasm artifact at byte %d: %s", e.Offset, e.Detail)
}

// ArtifactTooLargeError reports an artifact — or a core module
// embedded in one — above the admission ceiling. The ceiling is C
// parity: the C host refuses to even read a component file past 64 MiB
// (SEV_WASM_HOST_COMPONENT_FILE_LIMIT_BYTES, sev_wasm_host.h:68;
// wasm_host.c:1762-1764).
type ArtifactTooLargeError struct {
	What  string // "artifact" or "embedded core module"
	Size  int
	Limit int
}

func (e *ArtifactTooLargeError) Error() string {
	return fmt.Sprintf("pluginworker: %s is %d bytes, above the %d-byte admission ceiling", e.What, e.Size, e.Limit)
}

// NestedComponentError reports a component that embeds another
// component (section id 4). The walker unwraps exactly one layer by
// design: the SDK toolchain emits nested components only for exported
// interfaces (vendored wit-component encoding.rs:739-748, 941-943) and
// the ADR-033 world exports only world-level functions (wit/plugin.wit)
// — so a nested-component artifact is not an SDK plugin, and guessing
// at its instantiation graph would make this a component runtime,
// which it deliberately is not.
type NestedComponentError struct {
	Offset int // byte offset of the nested component's section id
}

func (e *NestedComponentError) Error() string {
	return fmt.Sprintf("pluginworker: component embeds a nested component (section id 4 at byte %d); the worker unwraps one layer only", e.Offset)
}

// NoCandidateModuleError reports a component none of whose embedded
// core modules exports the ADR-033 world surface. There is nothing to
// run: fail closed, naming what was found and what was required.
type NoCandidateModuleError struct {
	EmbeddedModules int
}

func (e *NoCandidateModuleError) Error() string {
	return fmt.Sprintf("pluginworker: component embeds %d core module(s), none exporting the world surface (%s)",
		e.EmbeddedModules, strings.Join(worldSurfaceExports, ", "))
}

// AmbiguousCandidateError reports a component in which MORE than one
// embedded core module exports the world surface. Exactly one guest
// admits per worker; picking one would be a silent guess about which
// code runs — fail closed and name them all.
type AmbiguousCandidateError struct {
	Modules         []int // ordinals of the matching module sections, in section order
	EmbeddedModules int
}

func (e *AmbiguousCandidateError) Error() string {
	return fmt.Sprintf("pluginworker: component embeds %d core modules and more than one (%v, by section order) exports the world surface; refusing to guess which guest runs",
		e.EmbeddedModules, e.Modules)
}

// ForbiddenImportError reports a guest module that imports anything
// outside the canonical `aiii:bbb/bbb` surface (ADR-033 Decision 3,
// lines 139-142: modules importing any additional host function are
// rejected before execution). Instantiation fails closed; the error
// names the exact import so the operator can see what the module tried
// to reach.
type ForbiddenImportError struct {
	Module string // import module name, e.g. "wasi_snapshot_preview1"
	Name   string // import field name, e.g. "fd_write"
	Reason string // why it was refused (unknown module, unknown name, wrong signature, non-function import)
}

func (e *ForbiddenImportError) Error() string {
	return fmt.Sprintf("pluginworker: forbidden import %q.%q: %s", e.Module, e.Name, e.Reason)
}

// ExportError reports a guest module missing a required export of the
// ADR-033 world, or exporting it with the wrong core signature. The
// world (aiii:plugin, wit/plugin.wit) requires
// aiii-plugin-bbb-protocol-version, aiii-plugin-smoke and
// plugin-invoke; the Legacy canonical-ABI lowering additionally
// requires the linear memory ("memory") and the allocation bridge
// ("cabi_realloc") — vendored wit-component validation.rs (Legacy
// export_memory/export_realloc).
type ExportError struct {
	Name   string
	Reason string
}

func (e *ExportError) Error() string {
	return fmt.Sprintf("pluginworker: guest export %q: %s", e.Name, e.Reason)
}

// ProtocolVersionError reports an admission failure of the
// aiii-plugin-bbb-protocol-version export. The number that governs this
// export is bbb_protocol_version — the capability-contract version,
// manifest const 2 (BBB_V2_AUDIT.md §1: "the number returned by
// aiii-plugin-bbb-protocol-version") — NOT protocol_version = 1, the
// RPC envelope version. The C host enforces the same constant
// (sev_wasm_host.h:65-66 → sev_manifest.h:351).
type ProtocolVersionError struct {
	Got uint32
}

func (e *ProtocolVersionError) Error() string {
	return fmt.Sprintf("pluginworker: guest bbb_protocol_version %d, require %d (audited manifest const; BBB_V2_AUDIT §1)",
		e.Got, RequiredProtocolVersion)
}

// SmokeCodeError reports an admission failure of the aiii-plugin-smoke
// export: the guest is linkable but not conformant. Expected code is 1
// (C host SEV_WASM_HOST_EXPECTED_SMOKE_CODE, sev_wasm_host.h:67).
type SmokeCodeError struct {
	Got uint32
}

func (e *SmokeCodeError) Error() string {
	return fmt.Sprintf("pluginworker: guest smoke code %d, require %d", e.Got, RequiredSmokeCode)
}

// TrapError reports a guest trap during execution, carrying the runtime
// reason. A trap poisons the Module: guest invariants cannot be trusted
// after one, so the instance is retired and the supervisor restarts the
// worker (PLUGIN_THREAT_MODEL.md A8; ADR-033:197 "equivalent to a
// crashed native plugin").
type TrapError struct {
	Reason string
}

func (e *TrapError) Error() string {
	return fmt.Sprintf("pluginworker: guest trap: %s", e.Reason)
}

// TimeoutError reports an invocation terminated by the context — the
// wazero WithCloseOnContextDone kill path. Err preserves the context
// cause (context.DeadlineExceeded or context.Canceled) for errors.Is.
// The termination closes the module (wazero contract), so the Module is
// unusable afterwards — kill-and-restart is the adopted model
// (DELTA_D1.md N-8: the daemon precedent is timeout + child restart).
type TimeoutError struct {
	Err error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("pluginworker: invocation terminated by deadline/cancellation: %v", e.Err)
}

func (e *TimeoutError) Unwrap() error { return e.Err }

// ResourceLimitError reports the guest hitting its resource envelope —
// the memory ceiling (ADR-033 Decision 5). Deterministic sources: a
// declared memory minimum above the cap, or the guest allocator
// refusing a host-side allocation. A trap that fires with linear memory
// pinned at the configured ceiling is also classified here: the C stack
// does not distinguish (a resource trap is a trap, ADR-033:197); the
// classification is telemetry the supervisor wants (threat model A7
// "host clamps trap on exceed"), and Cause always preserves the
// underlying trap.
type ResourceLimitError struct {
	LimitBytes uint64
	Cause      error
}

func (e *ResourceLimitError) Error() string {
	return fmt.Sprintf("pluginworker: guest exceeded resource envelope (memory cap %d bytes): %v", e.LimitBytes, e.Cause)
}

func (e *ResourceLimitError) Unwrap() error { return e.Cause }

// FrameTooLargeError reports a BBB payload above the plugin-side
// ceiling on the in-process boundary. The 1 MiB limit applies in BOTH
// directions (BBB_V2_AUDIT §2.1; the C host wrappers reject over-budget
// frames on the same boundary: wasm_host.c:1847 inbound list,
// wasm_host.c:2251 outbound response — "reject over-budget frames",
// ADR-033:218).
type FrameTooLargeError struct {
	Direction string // "host-to-guest" or "guest-to-host"
	Size      int
	Limit     int
}

func (e *FrameTooLargeError) Error() string {
	return fmt.Sprintf("pluginworker: %s payload %d bytes exceeds the %d-byte plugin-side ceiling", e.Direction, e.Size, e.Limit)
}

// AbiError reports the guest violating the canonical ABI itself —
// a return area or list pointer outside linear memory. Not a denial
// and not a resource condition: the guest is lying about memory it
// does not have. Poisons the Module.
type AbiError struct {
	Detail string
}

func (e *AbiError) Error() string {
	return fmt.Sprintf("pluginworker: guest violated canonical ABI: %s", e.Detail)
}

// ModuleUnusableError refuses work on a poisoned or closed Module,
// carrying the fatal error that retired it. The worker exits nonzero on
// the FIRST fatal; this type exists so a library caller that ignores
// that contract still fails closed instead of touching a corrupt
// instance.
type ModuleUnusableError struct {
	Cause error
}

func (e *ModuleUnusableError) Error() string {
	return fmt.Sprintf("pluginworker: module retired by earlier fatal error: %v", e.Cause)
}

func (e *ModuleUnusableError) Unwrap() error { return e.Cause }

// ErrNoOnEvent reports DeliverEvent on a guest that does not export
// on_event. The export is optional in practice (the C implementation
// never calls it — see the package doc's divergence note), so absence
// is a typed condition, not a load failure.
var ErrNoOnEvent = errors.New("pluginworker: guest does not export on_event")

// FatalDispatchError marks a dispatcher failure after which the
// dispatch CHANNEL itself is dead — a desynced or broken forward
// stream, a guest protocol violation — and the module must fault
// rather than receive a soft -32603 answer (there is nothing sane to
// answer on a dead channel). Ordinary dispatcher errors, by contrast,
// are answered -32603 and the module survives: the blast-radius rule
// of the 2026-08-19 design pass is soft-for-internal, fatal-for-dead.
type FatalDispatchError struct{ Err error }

func (e *FatalDispatchError) Error() string { return e.Err.Error() }
func (e *FatalDispatchError) Unwrap() error { return e.Err }
