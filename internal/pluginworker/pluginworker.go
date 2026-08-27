// Package pluginworker is the containment library of the plugin plane:
// it runs one untrusted WASM plugin under wazero with nothing ambient —
// no WASI, no clock, no environment, no random, no filesystem — and
// speaks the ADR-033 in-process transport to it (the canonical
// `plugin-invoke(list<u8>) -> list<u8>` entrypoint carrying whole BBB
// JSON-RPC frames, BBB_V2_AUDIT §10.3, DELTA_D1 N-1).
//
// This is build-order step 3 of docs/PLUGIN_FRAMEWORK.md §15 — the
// portable wall (§11): WASM isolates the plugin from the worker; the
// step-5 supervisor's process boundary isolates the worker from the
// resident. wazero is pure Go with zero transitive dependencies, so the
// wall holds on every platform the five-platform law names.
//
// Trust boundary: the HOST verifies a package — signatures, tiers,
// digests — via internal/packagefmt BEFORE a module ever reaches this
// package (build-order step 2). The worker deliberately owns no
// verification, no registry, no broker logic (KISS): it receives bytes
// the host already vouched for and owns only their containment.
//
// The ABI implemented here is the canonical-ABI core lowering of the
// ADR-033 Component Model world, adopted from the C stack and the SDK
// toolchain — see abi.go for the pinned lowering and its provenance.
// Both artifact classes admit: component binaries are unwrapped to
// their inner core module at load (component.go), and component-level
// type/adapter sections are not interpreted — the core lowering is the
// contract. WASI p2 capability mapping remains later work (delta-spec
// finding).
//
// Divergence note (finding, for the delta spec): ADR-033 line 161
// mandates the guest push export `on_event(topic_ptr, topic_len,
// payload_ptr, payload_len)`; the C implementation never defines or
// calls it (no reference anywhere under src/identity_os/src/). This
// package implements the ADR contract — DeliverEvent, with the ADR's
// bounded-reentrancy rule (never concurrent with an in-flight invoke)
// — and treats the export as optional at load so C-toolchain guests
// without it still admit.
package pluginworker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	// DefaultMemoryMaxBytes is the per-module memory ceiling when the
	// config does not name one: 64 MiB — adopted verbatim from ADR-033
	// Decision 5 (line 192: "default 64 MiB per module,
	// operator-configurable per-plugin via
	// aiiid.json plugins.<id>.resources.memory_max"; the C host smoke
	// limit agrees, wasm_host.c:55). The operator override arrives via
	// Config.MemoryMaxBytes, plumbed from plugin config by the caller.
	DefaultMemoryMaxBytes = 64 << 20

	// wasmPageBytes is the WebAssembly linear-memory page size.
	wasmPageBytes = 64 * 1024
)

// errClosed retires a Module that was closed deliberately.
var errClosed = errors.New("pluginworker: module closed")

// Config bounds one guest module. The zero value is usable: default
// memory cap, deny-all host calls.
type Config struct {
	// MemoryMaxBytes caps guest linear memory; 0 means
	// DefaultMemoryMaxBytes. Granted in whole wasm pages (64 KiB), so
	// a cap that is not page-aligned rounds DOWN — a ceiling may be
	// stricter than asked, never looser.
	MemoryMaxBytes uint64

	// Dispatcher handles guest-outgoing aiii:bbb/bbb calls. nil means
	// the fail-closed deny-all stub (every call answered with the
	// audited -32000 POLICY_DENY error object) — step 4's broker
	// replaces it through this seam.
	Dispatcher HostDispatcher
}

// Module is one loaded, admitted guest instance. All entry points
// serialize on one invocation lock — the ADR-033 line 161 bounded-
// reentrancy rule: on_event delivery never runs concurrently with an
// in-flight invoke. A Module is single-plugin, single-instance; state
// lives for the connection lifetime, which for the worker binary is the
// process lifetime (DELTA_D1 D1-1 rule 3).
type Module struct {
	rt       wazero.Runtime
	mod      api.Module
	mem      api.Memory
	invoke   api.Function
	post     api.Function // nil when the guest exports no post-return
	onEvent  api.Function // nil when the guest exports no on_event
	capBytes uint64
	class    ArtifactClass // which artifact class Load admitted

	mu    sync.Mutex
	fatal error // first fatal error; non-nil retires the Module
}

// Load compiles, contains, and admits one guest artifact — a core
// wasm module run as-is, or a component binary whose single
// world-exporting core module is first extracted (component.go). The
// admitted class is reported by Module.ArtifactClass.
//
// Fail-closed order: artifact classing and unwrapping first (typed
// rejections for oversize or malformed artifacts and for components
// without exactly one candidate module), then the import wall (any
// import outside the aiii:bbb/bbb lowering rejects with
// ForbiddenImportError naming it, before any guest code runs — ADR-033
// Decision 3 lines 139-142), then instantiation under the resource
// envelope, then the admission calls: aiii-plugin-bbb-protocol-version
// must return 2 and aiii-plugin-smoke must return 1 (abi.go pins both
// constants to their audited sources).
func Load(ctx context.Context, wasmBytes []byte, cfg Config) (m *Module, err error) {
	capBytes := cfg.MemoryMaxBytes
	if capBytes == 0 {
		capBytes = DefaultMemoryMaxBytes
	}
	pages := capBytes / wasmPageBytes
	if pages == 0 {
		return nil, fmt.Errorf("pluginworker: memory cap %d bytes is below one wasm page (%d)", capBytes, wasmPageBytes)
	}
	if pages > 65536 { // wasm32 addressing ceiling: 4 GiB
		return nil, fmt.Errorf("pluginworker: memory cap %d bytes exceeds wasm32 addressing (4 GiB)", capBytes)
	}
	dispatcher := cfg.Dispatcher
	if dispatcher == nil {
		dispatcher = denyAll{}
	}

	core, class, uerr := unwrapArtifact(wasmBytes)
	if uerr != nil {
		return nil, uerr
	}

	// WithCloseOnContextDone is the termination guarantee: a context
	// deadline or cancellation interrupts even a busy-looping guest
	// (fuel's job in the C stack, ADR-033 Decision 5; wazero's
	// equivalent kill switch). WithMemoryLimitPages is the memory
	// envelope.
	// MOBILE, checked rather than assumed (2026-08-23): NewRuntimeConfig
	// selects engineKindAuto, which resolves through
	// platform.CompilerSupported(). That switches on runtime.GOOS over
	// linux/darwin/freebsd/netbsd/windows/dragonfly/solaris/illumos and
	// returns false in its default arm — "ios" and "android" are both
	// absent — so both mobile platforms get the INTERPRETER with no
	// configuration from us. A second guard, executableMmapSupported(),
	// catches any listed platform that refuses W^X memory anyway.
	//
	// This matters because iOS forbids JIT outright, and a compiler
	// backend there would fail at RUN time, not build time: a green
	// five-platform build would prove nothing. Do not "optimise" this to
	// NewRuntimeConfigCompiler without re-reading that switch.
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(uint32(pages)))
	defer func() {
		if err != nil {
			_ = rt.Close(ctx)
		}
	}()

	compiled, cerr := rt.CompileModule(ctx, core)
	if cerr != nil {
		// A declared memory minimum above the cap fails validation
		// here ("min N pages ... over limit", wazero
		// internal/wasm/module.go:930): the envelope refusing the
		// guest before it runs, typed accordingly.
		if strings.Contains(cerr.Error(), "over limit") {
			return nil, &ResourceLimitError{LimitBytes: capBytes, Cause: cerr}
		}
		return nil, fmt.Errorf("pluginworker: compile module: %w", cerr)
	}

	if werr := checkImportWall(compiled); werr != nil {
		return nil, werr
	}

	// The one and only host module. Nothing else exists in this
	// runtime for a guest to import — no WASI is ever instantiated.
	if herr := instantiateBBBHost(ctx, rt, dispatcher); herr != nil {
		return nil, herr
	}

	// Never run wasip1's `_start`; the Legacy reactor initializer
	// `_initialize` runs iff exported (wit-component validation.rs
	// Legacy export_initialize; wazero skips absent start functions).
	mod, ierr := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().
		WithName("plugin").
		WithStartFunctions(ExportInitialize))
	if ierr != nil {
		return nil, fmt.Errorf("pluginworker: instantiate module: %w", ierr)
	}

	m = &Module{rt: rt, mod: mod, capBytes: capBytes, class: class}
	if xerr := m.bindExports(); xerr != nil {
		return nil, xerr
	}
	if aerr := m.admit(ctx); aerr != nil {
		return nil, aerr
	}
	return m, nil
}

// ArtifactClass reports which artifact class Load admitted: a raw core
// module, or a component whose extracted core module now runs.
// Telemetry for the worker's ready banner and the step-5 supervisor.
func (m *Module) ArtifactClass() ArtifactClass { return m.class }

// checkImportWall enforces ADR-033's security invariant before any
// guest code can run: modules that import exactly the BBB surface link;
// modules that import ANYTHING else are rejected, each rejection naming
// the import (Decision 3 lines 139-142). The C core-probe additionally
// requires at least one BBB import (wasm_host.c:1195-1197) — that is
// the provider conformance probe proving linkage, not module admission;
// a guest importing nothing is contained trivially and admits.
func checkImportWall(compiled wazero.CompiledModule) error {
	allowed := make(map[string]bool, len(bbbImportNames))
	for _, n := range bbbImportNames {
		allowed[n] = true
	}
	for _, def := range compiled.ImportedFunctions() {
		module, name, _ := def.Import()
		switch {
		case module != BBBWITModule:
			return &ForbiddenImportError{Module: module, Name: name,
				Reason: fmt.Sprintf("only the %s surface is provided; no WASI, no ambient host modules", BBBWITModule)}
		case !allowed[name]:
			return &ForbiddenImportError{Module: module, Name: name,
				Reason: "not one of the eight audited aiii:bbb/bbb functions"}
		case !typesEqual(def.ParamTypes(), []api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32}) ||
			len(def.ResultTypes()) != 0:
			return &ForbiddenImportError{Module: module, Name: name,
				Reason: "signature is not the canonical lowering (param i32 i32 i32) -> ()"}
		}
	}
	for _, mem := range compiled.ImportedMemories() {
		module, name, _ := mem.Import()
		return &ForbiddenImportError{Module: module, Name: name,
			Reason: "imported memories are refused; the guest owns its linear memory"}
	}
	// Imported globals/tables are not introspectable on a wazero
	// CompiledModule; they fail instantiation (nothing provides them),
	// which is still fail-closed — just without this error type.
	return nil
}

// bindExports resolves and signature-checks the required world exports.
func (m *Module) bindExports() error {
	mem := m.mod.ExportedMemory(ExportMemory)
	if mem == nil {
		return &ExportError{Name: ExportMemory, Reason: "missing (the canonical lowering exports the linear memory)"}
	}
	m.mem = mem

	i32 := api.ValueTypeI32
	need := []struct {
		name    string
		params  []api.ValueType
		results []api.ValueType
	}{
		{ExportProtocolVersion, nil, []api.ValueType{i32}},
		{ExportSmoke, nil, []api.ValueType{i32}},
		{ExportPluginInvoke, []api.ValueType{i32, i32}, []api.ValueType{i32}},
		{ExportRealloc, []api.ValueType{i32, i32, i32, i32}, []api.ValueType{i32}},
	}
	defs := m.mod.ExportedFunctionDefinitions()
	for _, want := range need {
		def, ok := defs[want.name]
		if !ok {
			return &ExportError{Name: want.name, Reason: "missing required world export"}
		}
		if !typesEqual(def.ParamTypes(), want.params) || !typesEqual(def.ResultTypes(), want.results) {
			return &ExportError{Name: want.name, Reason: "core signature does not match the canonical lowering"}
		}
	}
	m.invoke = m.mod.ExportedFunction(ExportPluginInvoke)

	// Optional exports: validate the signature iff present.
	if def, ok := defs[ExportPostReturn]; ok {
		if !typesEqual(def.ParamTypes(), []api.ValueType{i32}) || len(def.ResultTypes()) != 0 {
			return &ExportError{Name: ExportPostReturn, Reason: "post-return must be (param i32) -> ()"}
		}
		m.post = m.mod.ExportedFunction(ExportPostReturn)
	}
	if def, ok := defs[ExportOnEvent]; ok {
		if !typesEqual(def.ParamTypes(), []api.ValueType{i32, i32, i32, i32}) || len(def.ResultTypes()) != 0 {
			return &ExportError{Name: ExportOnEvent, Reason: "on_event must be (param i32 i32 i32 i32) -> () (ADR-033 line 161)"}
		}
		m.onEvent = m.mod.ExportedFunction(ExportOnEvent)
	}
	return nil
}

// admit runs the load-time conformance calls the C host runs on every
// execution (wasm_host.c:2682-2703): protocol version, then smoke.
func (m *Module) admit(ctx context.Context) error {
	got, err := m.callU32(ctx, ExportProtocolVersion)
	if err != nil {
		return fmt.Errorf("pluginworker: admission call %s: %w", ExportProtocolVersion, m.classify(err))
	}
	if got != RequiredProtocolVersion {
		return &ProtocolVersionError{Got: got}
	}
	got, err = m.callU32(ctx, ExportSmoke)
	if err != nil {
		return fmt.Errorf("pluginworker: admission call %s: %w", ExportSmoke, m.classify(err))
	}
	if got != RequiredSmokeCode {
		return &SmokeCodeError{Got: got}
	}
	return nil
}

func (m *Module) callU32(ctx context.Context, name string) (uint32, error) {
	res, err := m.mod.ExportedFunction(name).Call(ctx)
	if err != nil {
		return 0, err
	}
	return uint32(res[0]), nil
}

// Invoke runs one BBB frame through the guest's plugin-invoke under the
// canonical ABI and returns the guest's response frame bytes.
//
// The 1 MiB plugin-side ceiling holds in both directions (AUDIT §2.1;
// C parity wasm_host.c:1847, 2251). Failure taxonomy: guest trap →
// TrapError (ResourceLimitError when memory sits at the ceiling);
// context deadline/cancellation → TimeoutError; ABI violations →
// AbiError. Every failure that reached guest code poisons the Module —
// guest invariants cannot be trusted after a fault (ADR-033:197 treats
// it as a crashed plugin; the worker exits and the supervisor
// restarts, threat model A8/§7).
func (m *Module) Invoke(ctx context.Context, frame []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fatal != nil {
		return nil, &ModuleUnusableError{Cause: m.fatal}
	}
	if len(frame) == 0 {
		// The JSON layer above never produces an empty payload
		// (AUDIT §2.3); refusing here keeps the ABI unambiguous.
		return nil, errors.New("pluginworker: refusing to invoke with an empty frame")
	}
	if len(frame) > bbb.MaxControlFrameBytes {
		return nil, &FrameTooLargeError{Direction: "host-to-guest", Size: len(frame), Limit: bbb.MaxControlFrameBytes}
	}

	// Lower the request list into guest memory (canonical lift's
	// realloc option: cabi_realloc(0,0,1,len) then copy).
	reqPtr, err := guestAlloc(ctx, m.mod, uint32(len(frame)))
	if err != nil {
		return nil, m.fail(m.classify(err))
	}
	if !m.mem.Write(reqPtr, frame) {
		return nil, m.fail(&AbiError{Detail: fmt.Sprintf("cabi_realloc returned (ptr=%d,len=%d) outside linear memory", reqPtr, len(frame))})
	}

	res, err := m.invoke.Call(ctx, uint64(reqPtr), uint64(len(frame)))
	if err != nil {
		return nil, m.fail(m.classify(err))
	}

	// Lift the response list from the 8-byte return area.
	retPtr := uint32(res[0])
	respPtr, ok1 := m.mem.ReadUint32Le(retPtr)
	respLen, ok2 := m.mem.ReadUint32Le(retPtr + 4)
	if !ok1 || !ok2 {
		return nil, m.fail(&AbiError{Detail: fmt.Sprintf("plugin-invoke return area (ptr=%d) outside linear memory", retPtr)})
	}
	if respLen > bbb.MaxControlFrameBytes {
		// Over-budget guest response: the C wrapper traps the
		// instance for this (wasm_host.c:2251); poisoning matches.
		return nil, m.fail(&FrameTooLargeError{Direction: "guest-to-host", Size: int(respLen), Limit: bbb.MaxControlFrameBytes})
	}
	var out []byte
	if respLen > 0 {
		view, ok := m.mem.Read(respPtr, respLen)
		if !ok {
			return nil, m.fail(&AbiError{Detail: fmt.Sprintf("plugin-invoke response (ptr=%d,len=%d) outside linear memory", respPtr, respLen)})
		}
		out = append([]byte(nil), view...) // copy before post-return frees it
	}
	if m.post != nil {
		if _, perr := m.post.Call(ctx, uint64(retPtr)); perr != nil {
			return nil, m.fail(m.classify(perr))
		}
	}
	return out, nil
}

// HasOnEvent reports whether the guest exports the ADR-033 push
// entrypoint.
func (m *Module) HasOnEvent() bool { return m.onEvent != nil }

// DeliverEvent pushes one admitted observe.event notification into the
// guest's on_event export (ADR-033 line 161). It serializes on the same
// invocation lock as Invoke — the bounded-reentrancy rule: delivery
// never runs concurrently with an in-flight invoke. Callers that learn
// of events DURING an invoke (a future broker) queue the delivery for
// after the invoke returns; calling this from inside a Dispatch would
// deadlock by design rather than break the rule.
func (m *Module) DeliverEvent(ctx context.Context, topic string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fatal != nil {
		return &ModuleUnusableError{Cause: m.fatal}
	}
	if m.onEvent == nil {
		return ErrNoOnEvent
	}
	if topic == "" {
		return errors.New("pluginworker: refusing to deliver an event without a topic")
	}
	if len(topic) > bbb.MaxControlFrameBytes {
		return &FrameTooLargeError{Direction: "host-to-guest", Size: len(topic), Limit: bbb.MaxControlFrameBytes}
	}
	if len(payload) > bbb.MaxControlFrameBytes {
		return &FrameTooLargeError{Direction: "host-to-guest", Size: len(payload), Limit: bbb.MaxControlFrameBytes}
	}

	topicPtr, err := guestAlloc(ctx, m.mod, uint32(len(topic)))
	if err != nil {
		return m.fail(m.classify(err))
	}
	if !m.mem.Write(topicPtr, []byte(topic)) {
		return m.fail(&AbiError{Detail: "cabi_realloc returned a topic buffer outside linear memory"})
	}
	payloadPtr := uint32(0)
	if len(payload) > 0 {
		payloadPtr, err = guestAlloc(ctx, m.mod, uint32(len(payload)))
		if err != nil {
			return m.fail(m.classify(err))
		}
		if !m.mem.Write(payloadPtr, payload) {
			return m.fail(&AbiError{Detail: "cabi_realloc returned a payload buffer outside linear memory"})
		}
	}
	if _, err := m.onEvent.Call(ctx,
		uint64(topicPtr), uint64(len(topic)), uint64(payloadPtr), uint64(len(payload))); err != nil {
		return m.fail(m.classify(err))
	}
	return nil
}

// Close releases the runtime and everything in it. Further calls
// return ModuleUnusableError.
func (m *Module) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fatal == nil {
		m.fatal = errClosed
	}
	return m.rt.Close(ctx)
}

// fail retires the Module with its first fatal error and returns the
// (classified) error for the caller.
func (m *Module) fail(err error) error {
	if m.fatal == nil {
		m.fatal = err
	}
	return err
}

// classify maps a raw wazero call error onto the typed taxonomy.
func (m *Module) classify(err error) error {
	// Context termination: wazero's sys.ExitError carries the context
	// cause through errors.Is (sys/error.go:76-80).
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &TimeoutError{Err: err}
	}
	// Typed errors raised by the host-call path travel through
	// wazero's recover-and-wrap (%w, wasmdebug FromRecovered) intact.
	var fe *FrameTooLargeError
	if errors.As(err, &fe) {
		return fe
	}
	var ae *AbiError
	if errors.As(err, &ae) {
		return ae
	}
	var re *ResourceLimitError
	if errors.As(err, &re) {
		if re.LimitBytes == 0 {
			re.LimitBytes = m.capBytes
		}
		return re
	}
	var ee *ExportError
	if errors.As(err, &ee) {
		return ee
	}
	// Anything else is a guest trap. When it fired with linear memory
	// pinned at the configured ceiling, classify it as the envelope
	// doing its job (see ResourceLimitError's doc for the honesty
	// caveat; Cause always keeps the trap).
	trap := &TrapError{Reason: firstLine(err.Error())}
	if !m.mod.IsClosed() && uint64(m.mem.Size()) >= m.capFloorBytes() {
		return &ResourceLimitError{LimitBytes: m.capBytes, Cause: trap}
	}
	return trap
}

// capFloorBytes is the cap as actually granted: whole pages.
func (m *Module) capFloorBytes() uint64 {
	return (m.capBytes / wasmPageBytes) * wasmPageBytes
}

func typesEqual(got, want []api.ValueType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
