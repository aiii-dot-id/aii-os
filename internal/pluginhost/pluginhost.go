// Package pluginhost is the quarantine harness — the milestone the
// framework promised before any plugin surface may promote
// (PLUGIN_THREAT_MODEL.md §8: "plugins run only in a quarantine
// harness that grants no external capabilities — the framework can be
// built and its isolation proven without ever admitting a marketplace
// plugin to a live identity"). It composes build-order steps 1–3
// (PLUGIN_FRAMEWORK.md §15) into the promised path:
//
//	identity → tools.Registry → BBB frame → wazero worker → result
//
// One verified .aiiospkg loads its guest into the wazero wall and its
// signed manifest's operations become identity-callable tools — with
// ZERO capabilities: the worker keeps its fail-closed deny-all
// HostDispatcher, so every guest-outgoing aiii:bbb/bbb call is answered
// with the audited POLICY_DENY denial and nothing else happens.
//
// Step 5 adds the process boundary on top: predicate-matched variant
// selection (select.go; PLATFORM_SEAMS §3), and a SUPERVISED activation
// mode where the wazero wall runs in a child process behind
// internal/supervisor — framed BBB stdio (D1-1), guest hostcalls
// forwarded upstream into the same broker binding, crash = restart
// while the identity survives (threat model A8). The in-process mode
// stays byte-identical for the mobile shells and the harness.
//
// What this deliberately is NOT (KISS; the build order owns the rest):
// no lifecycle beyond active/closed (the supervised channel's
// crash/restart states live inside the supervisor and never become
// harness state), no manager daemon, no hot update. The harness feeds
// the EXISTING Registry (PLUGIN_FRAMEWORK §2: "the plugin runtime does
// not replace the Registry; it feeds it") — registration at
// activation, deregistration at deactivation, origin = the plugin id,
// so origin-gated SAFE suspends plugin tools wholesale for free.
package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/facility"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// Activation modes: the ONE contract over two bindings
// (PLUGIN_FRAMEWORK §12.3 — plugin logic must not see the difference).
const (
	// ModeInProcess: the wazero wall lives in this process (the step-3
	// posture). The default on the mobile shells (no exec on iOS; the
	// OS app sandbox is the wall there) and wherever no worker binary
	// is configured.
	ModeInProcess = "in-process"
	// ModeSupervised: the wall lives behind the step-5 process
	// boundary — a supervised child speaking framed BBB on stdio
	// (D1-1), guest hostcalls forwarded upstream to the same broker
	// seam. The desktop default once plugins.worker_binary resolves.
	ModeSupervised = "supervised"
)

// harnessRequestID is the JSON-RPC id of every harness→guest request.
// A constant string id is correct here, not lazy: the worker's module
// lock serializes invocations (one in flight per module, ADR-033
// bounded reentrancy), so an id exists only to be echoed back within
// the same call — per-session id minting joins with the broker. String
// form because that is the daemon→plugin precedent (BBB_V2_AUDIT §4:
// daemon→plugin requests use a string id).
const harnessRequestID = "h1"

// maxToolNameBytes is the providers' function-name ceiling: LLM
// function-calling names must match ^[a-zA-Z0-9_-]{1,64}$ (OpenAI and
// Anthropic tool-name grammar). The sanitizer guarantees the charset;
// this guards the length.
const maxToolNameBytes = 64

// invoker is one BBB invocation seam: a frame in, the response frame
// out. The in-process wall (pluginworker.Module) and the process
// boundary (supervisor.Supervisor) both satisfy it — the tool proxy
// cannot tell which wall answered, which is the two-bindings-one-
// contract rule made structural.
type invoker interface {
	Invoke(ctx context.Context, frame []byte) ([]byte, error)
}

// ActivePlugin is one activated plugin: a live invocation channel
// (in-process module or supervised child) and the tool names it
// registered. Two states only — active, then closed by Deactivate;
// crash/restart lifecycle inside the supervised channel is the
// supervisor's own and never becomes harness state.
type ActivePlugin struct {
	// ID is the manifest id — also the tools' registry origin.
	ID string
	// Tier is the evidence-derived trust tier (T0 under the harness's
	// empty roots: unsigned only).
	Tier packagefmt.Tier
	// Mode is ModeInProcess or ModeSupervised.
	Mode string
	// VariantID names the predicate-selected variant this activation
	// runs.
	VariantID string
	// Channel is non-nil when this plugin is a communications adapter:
	// the registry names its send/receive/describe operations answer to.
	// nil for every other family.
	Channel *Channel
	// Backends carries the selected variant's backend: declarations —
	// what the variant brings, recorded for receipts and conformance
	// (PLATFORM_SEAMS §3.3), never host-matched.
	Backends []string
	// ToolNames are the registered tool names, in registration order.
	ToolNames []string

	module      *pluginworker.Module   // in-process mode
	sup         *supervisor.Supervisor // supervised mode
	artifactDir string                 // supervised mode: extracted-artifact dir, removed at Deactivate
	reg         *tools.Registry
	binding     *broker.Binding // nil = deny-all quarantine (no operator grant)
}

// Options widens the harness beyond its zero-value posture — each field
// an explicit operator act, absent by default.
type Options struct {
	// Roots are the pinned AIII plugin trust roots (config
	// plugins.certifier_root / reviewer_root / platform_root, loaded by
	// packagefmt.LoadPinnedRoot — the same validation `aii plugin
	// verify` runs). Zero value = the T0-only harness: signed evidence
	// without a pinned root REJECTS (unverifiable is not unsigned).
	// Roots.Revocation carries the revocation-status set the app loads
	// FRESH at boot (packagefmt.LoadRevocationStatus over the trust
	// dir) — every Activate is a launch-time consultation point
	// (PLUGIN_REVOCATION_DESIGN §2.2): a snapshot that appeared since
	// install makes the next launch reject with the revocation reason,
	// and the package rides the existing refused-activation path.
	Roots packagefmt.TrustRoots
	// Broker is the process-wide capability broker (step 4). nil — or a
	// plugin with no plugins.grants entry — keeps the worker's deny-all
	// stub: the quarantine posture IS the absence of grants.
	Broker *broker.Host
	// Facilities is the host facility set the app assembled per
	// platform (PLATFORM_SEAMS §3.2). nil advertises nothing: only
	// variants requiring at most the structurally-always
	// facility:sev_transport.local remain selectable — fail-closed.
	Facilities *facility.Set
	// WorkerBinary is the resolved aii-plugin-worker executable. Non-
	// empty turns on the supervised lane (ModeSupervised for wasm
	// variants, and the native T3 exec lane with it); empty keeps
	// every activation in-process — the mobile/harness posture. The
	// app resolves it from config plugins.worker_binary, defaulting to
	// the file next to the daemon executable; agents and tests pass a
	// path they built.
	WorkerBinary string
	// MemoryMax maps plugin id → memory ceiling in bytes (config
	// plugins.resources.<id>.memory_max_bytes, the §10 envelope's
	// first field). Zero/absent = the worker's ADR-033 64 MiB default
	// for wasm variants; for native T3 children the value becomes the
	// address-space envelope and absent means UNENVELOPED (a native
	// voice variant legitimately maps model files; the ceiling is the
	// operator's call, never an invented default).
	MemoryMax map[string]uint64
	// Log receives supervised-mode child telemetry (crash lines,
	// restart decisions — threat model §7). nil = log.Default().
	Log *log.Logger
}

// Activate verifies the .aiiospkg at pkgPath, loads its wasm_component
// entrypoint into the wazero wall, and registers each signed interface
// method as an identity-callable tool on reg. Every failure is typed
// and total: nothing stays registered, nothing stays loaded. opts nil
// means the zero-value Options — the step-3 quarantine posture,
// byte-for-byte.
func Activate(ctx context.Context, pkgPath string, reg *tools.Registry, opts *Options) (*ActivePlugin, error) {
	if opts == nil {
		opts = &Options{}
	}
	res, err := packagefmt.VerifyFile(pkgPath, opts.Roots)
	if err != nil {
		return nil, err
	}
	m := res.Manifest

	// Predicate-matched selection (select.go): platform/arch/topology
	// match ∧ runtime lane ∧ tier eligibility ∧ every required
	// predicate — the C precedence among survivors. Zero selectable is
	// a typed refusal naming every missing predicate per variant.
	host := currentHost(opts)
	variant, serr := selectVariant(res, host)
	if serr != nil {
		return nil, serr
	}

	artifactBytes, err := loadVerifiedMember(pkgPath, res, variant.Entrypoint)
	if err != nil {
		return nil, err
	}

	// The step-4 broker binding — built HERE, from verified evidence
	// only: the manifest id, the evidence-derived tier, and the SIGNED
	// capability surface (top-level envelope ∩ the running variant's
	// variant_capabilities ∩, at T2, the reviewer-attested list — each
	// layer narrows, none widens). A plugin the operator granted nothing
	// binds nil and keeps the deny-all stub: zero-config stays the
	// step-3 quarantine, unchanged — in BOTH activation modes.
	envelope := signedCapabilitySurface(m, variant, res)
	binding := opts.Broker.Bind(m.ID, res.Tier, envelope)
	if binding != nil {
		// A fresh activation must not inherit temp-scoped RING4 rows a
		// crashed predecessor never cleared (the uncertified-tier
		// lifetime is exactly one activation).
		if cerr := binding.ClearTempScope(); cerr != nil {
			return nil, fmt.Errorf("pluginhost: clearing stale temp kv for %s: %w", m.ID, cerr)
		}
	}

	ap := &ActivePlugin{
		ID: m.ID, Tier: res.Tier, VariantID: variant.VariantID,
		Backends: m.BackendDeclarations(variant),
		reg:      reg, binding: binding,
	}

	// The wall goes up — one of two walls, same contract:
	//
	//   in-process: wazero in this process (step 3; mobile + harness).
	//   supervised: the process boundary (step 5) — a child speaking
	//     framed BBB stdio, guest hostcalls forwarded upstream into
	//     the SAME broker binding (or the same deny-all absence).
	//
	// The Dispatcher on either wall is the binding when the operator
	// granted one, else nil — the fail-closed deny-all.
	var inv invoker
	switch {
	case host.supervised && variant.ExecutionRuntime == "wasm_component":
		ap.Mode = ModeSupervised
		sup, dir, serr := startSupervisedWASM(res, variant, artifactBytes, binding, opts)
		if serr != nil {
			_ = binding.Close()
			return nil, serr
		}
		ap.sup, ap.artifactDir = sup, dir
		inv = sup
	case variant.ExecutionRuntime == "native_t3_component":
		// Selection admits native T3 only on the supervised lane and
		// only with T3-proven evidence (select.go); the child is the
		// verified artifact itself on the D1-1 stdio binding.
		ap.Mode = ModeSupervised
		sup, dir, serr := startSupervisedNative(res, variant, artifactBytes, binding, opts)
		if serr != nil {
			_ = binding.Close()
			return nil, serr
		}
		ap.sup, ap.artifactDir = sup, dir
		inv = sup
	default:
		ap.Mode = ModeInProcess
		workerCfg := pluginworker.Config{MemoryMaxBytes: opts.MemoryMax[m.ID]}
		if binding != nil {
			workerCfg.Dispatcher = binding
		}
		mod, lerr := pluginworker.Load(ctx, artifactBytes, workerCfg)
		if lerr != nil {
			_ = binding.Close()
			return nil, lerr
		}
		ap.module = mod
		inv = mod
	}

	abort := func(err error) (*ActivePlugin, error) {
		// Activation is all-or-nothing: deregister anything already
		// admitted and drop the channel. A half-activated plugin would
		// be a lifecycle state the design refuses to have.
		for _, name := range ap.ToolNames {
			reg.Deregister(name)
		}
		ap.closeChannel(ctx)
		_ = binding.Close()
		return nil, err
	}

	// Tool definitions derive STATICALLY from the signed manifest's
	// interfaces — each declared method is one tool. The manifest is
	// signed truth (§1.4: a manifest is a request, not a grant — and
	// under quarantine the granted surface is compute only); a live
	// plugin.register_interface session, which must restate exactly
	// this declaration, joins with the broker.

	// Load the interface schema file (the descriptor JSON) from the
	// verified package. The descriptor carries per-operation "input"
	// paths (e.g. "schemas/get_in.json") pointing to JSON Schema files
	// packaged alongside it. We load each input schema and forward it
	// to the LLM via Parameters() so typed parameters (integer, string,
	// etc.) arrive correctly.
	inputSchemas, iserr := loadInputSchemas(pkgPath, res, m)
	if iserr != nil {
		return abort(iserr)
	}

	seen := make(map[string]bool)
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		for _, method := range decl.Methods {
			name, terr := toolName(m.ID, method)
			if terr != nil {
				return abort(terr)
			}
			if seen[name] {
				return abort(&ToolNameError{PluginID: m.ID, Name: name,
					Reason: "two declared methods sanitize to the same tool name; refusing to guess which operation the identity would call"})
			}
			seen[name] = true
			t := &operationTool{
				name:      name,
				operation: method,
				inv:       inv,
				description: fmt.Sprintf("Operation %q of interface %s@%d, plugin %s (brokered: every external effect is a per-invocation capability decision; ungranted plugins are pure compute).",
					method, decl.ID, decl.Version, m.ID),
				parameters: inputSchemas[method],
			}
			// The EXISTING admission seam, origin = plugin id: SAFE
			// suspends these wholesale (origin-gated, pre-dispatch) and
			// a name collision — builtin or another plugin — fails the
			// whole activation closed, never a silent rename.
			//
			// A CHANNEL ADAPTER'S METHODS ARE THE HOST'S, NOT THE
			// IDENTITY'S. Decided here, at the one place a plugin tool
			// is born, from the interface the plugin itself declared —
			// so it cannot be true in one lane and false in another.
			register := reg.RegisterDynamic
			if decl.ID == ChannelInterfaceID {
				register = reg.RegisterHostOp
			}
			if rerr := register(t, m.ID); rerr != nil {
				return abort(&ToolNameError{PluginID: m.ID, Name: name, Reason: rerr.Error()})
			}
			ap.ToolNames = append(ap.ToolNames, name)
		}
	}

	// A channel adapter is recognised HERE, once its operations exist in
	// the registry. Fails closed both ways: a plugin claiming the family
	// without the interface, or declaring the interface under another
	// family, is refused rather than activated as something it is not.
	// Until this existed, plugin_family "channel_adapter" was validated
	// by two repos and consumed by neither.
	ch, cherr := channelOf(m)
	if cherr != nil {
		return abort(cherr)
	}
	ap.Channel = ch

	// A VOICE INTERFACE NO LONGER HAS A FIXED OPERATION SET. There was a
	// listen/speak/describe contract here, validated at activation, for
	// a design where PCM travelled to a contained plugin and back. That
	// plane is gone (docs/VOICE_FIRST_PRINCIPLES.md) — the speech model
	// is an endpoint like every other model the identity uses, and the
	// browser holds the microphone.
	//
	// WHAT IT MAY SAY IS STILL EXACTLY BOUNDED, just not here: broker
	// voice.observe runs the same rings it always did. A plugin reaches
	// it from INSIDE a handler and only there — the BBB control pair is
	// one lane, single-goroutine, a frame read and answered before the
	// next (aiiosdk/serve.go), so a plugin cannot push and never could.
	// It speaks while the host is invoking it.
	//
	// So plugin_family "voice_interface" stays a valid manifest value
	// and names no operations: which ones a voice plugin offers is the
	// author's to declare, and enforcing three of them here bought
	// nothing once the audio stopped travelling to them.
	return ap, nil
}

// Deactivate deregisters the plugin's tools, closes its invocation
// channel (module or supervised child — the child gets the D1-1 clean
// shutdown: stdin EOF, then escalation), and ends the broker binding
// (temp-scoped RING4 rows die with the activation). Idempotent; after
// it, tool calls that raced past deregistration fail closed on the
// retired channel.
func (p *ActivePlugin) Deactivate(ctx context.Context) error {
	for _, name := range p.ToolNames {
		p.reg.Deregister(name)
	}
	err := p.closeChannel(ctx)
	if berr := p.binding.Close(); err == nil {
		err = berr
	}
	return err
}

// closeChannel tears down whichever wall this activation runs and
// removes the extracted artifact.
func (p *ActivePlugin) closeChannel(ctx context.Context) error {
	var err error
	if p.module != nil {
		err = p.module.Close(ctx)
	}
	if p.sup != nil {
		// The caller's deadline reaches the child's graces: ten fixed
		// seconds used to ignore a five-second context (D24, Sev
		// 2026-08-26).
		if serr := p.sup.CloseContext(ctx); err == nil {
			err = serr
		}
	}
	if p.artifactDir != "" {
		if rerr := os.RemoveAll(p.artifactDir); err == nil {
			err = rerr
		}
		p.artifactDir = ""
	}
	return err
}

// Restarts reports the supervised channel's restart count (0 for
// in-process activations) — telemetry for the operator surface and
// the restart tests.
func (p *ActivePlugin) Restarts() int {
	if p.sup == nil {
		return 0
	}
	return p.sup.Restarts()
}

// SupervisedPid reports the live child pid in supervised mode (0
// otherwise) — test and telemetry seam, never authority.
func (p *ActivePlugin) SupervisedPid() int {
	if p.sup == nil {
		return 0
	}
	return p.sup.Pid()
}

// --- the supervised lane ---

// extractArtifact writes the verified artifact bytes into a fresh
// host-owned directory and returns (dir, path, verify) where verify
// re-checks the file against the verified digest. The closure runs
// before EVERY spawn (restarts included): the file sits on disk
// between spawns and a same-user adversary (A5) can rewrite what the
// host will re-exec — the verified-bytes-are-loaded-bytes invariant
// must hold at every exec, not just the first.
func extractArtifact(res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, filename string, mode os.FileMode) (dir, path string, verify func() error, err error) {
	dir, err = os.MkdirTemp("", "aii-plugin-"+sanitizeToken(res.Manifest.ID)+"-")
	if err != nil {
		return "", "", nil, fmt.Errorf("pluginhost: artifact dir: %w", err)
	}
	path = filepath.Join(dir, filename)
	if err := os.WriteFile(path, artifactBytes, mode); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", nil, fmt.Errorf("pluginhost: write artifact: %w", err)
	}
	want := res.FileDigests[variant.Entrypoint]
	verify = func() error {
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return &EntrypointDigestError{Member: variant.Entrypoint, Want: want}
		}
		sum := sha256.Sum256(raw)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != want {
			return &EntrypointDigestError{Member: variant.Entrypoint, Want: want, Got: got}
		}
		return nil
	}
	return dir, path, verify, nil
}

// supervisorDispatcher narrows the binding to the supervisor's seam:
// a nil *broker.Binding must become a nil INTERFACE so the supervisor
// keeps its deny-all default (a typed-nil interface would smuggle a
// dead binding past the nil check).
func supervisorDispatcher(binding *broker.Binding) supervisor.Dispatcher {
	if binding == nil {
		return nil
	}
	return binding
}

// startSupervisedWASM runs the worker binary on the extracted wasm
// artifact: -forward wires guest hostcalls upstream, -memory-max
// carries the operator's envelope (the worker enforces it in-process
// with wazero — the correct wall for a wasm ceiling).
func startSupervisedWASM(res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, binding *broker.Binding, opts *Options) (*supervisor.Supervisor, string, error) {
	dir, path, verify, err := extractArtifact(res, variant, artifactBytes, "artifact.wasm", 0o600)
	if err != nil {
		return nil, "", err
	}
	memoryMax := opts.MemoryMax[res.Manifest.ID]
	if memoryMax == 0 {
		memoryMax = pluginworker.DefaultMemoryMaxBytes
	}
	sup, err := supervisor.Start(supervisor.Spec{
		PluginID: res.Manifest.ID,
		Argv: []string{opts.WorkerBinary, "-forward",
			fmt.Sprintf("-memory-max=%d", memoryMax), path},
		ReadyMark:      "event=ready",
		VerifyArtifact: verify,
		ExitMeaning:    supervisor.WorkerExitMeaning,
		Log:            opts.Log,
	}, supervisorDispatcher(binding))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", err
	}
	return sup, dir, nil
}

// startSupervisedNative runs a T3 native artifact directly as the
// supervised child on the D1-1 stdio binding (SEV_PLUGIN_SOCKET=stdio:
// — the one new endpoint VALUE, no new variable). Its memory envelope
// is the address-space limit where the platform has a mechanism
// (rlimit files in internal/supervisor); readiness is process
// liveness — the native admission protocol (rpc.connect with the
// launch token) is step-6 conformance work, recorded, not faked here.
func startSupervisedNative(res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, binding *broker.Binding, opts *Options) (*supervisor.Supervisor, string, error) {
	dir, path, verify, err := extractArtifact(res, variant, artifactBytes, "artifact", 0o700)
	if err != nil {
		return nil, "", err
	}
	// The native lane is the one place a plugin runs as ordinary host
	// code. TOPOLOGY BEFORE MECHANISM (hostcap review nit, 2026-08-26):
	// on the mobile app host the linux/darwin sandbox files still
	// compile (GOOS implication — android matches linux, ios matches
	// darwin), and their bwrap/sandbox-exec refusals would fire with
	// advice that is nonsense on a phone. The true reason speaks first.
	if nc := hostcap.Can(hostcap.NativeChild); !nc.Available {
		_ = os.RemoveAll(dir)
		return nil, "", fmt.Errorf("plugin %s: native lane unavailable: %s", res.Manifest.ID, nc.Reason)
	}
	// Contain it before it starts — see sandbox_linux.go for the
	// mechanism and sandbox_other.go for the honest no-op record.
	argv, containment, cerr := containArgv([]string{path})
	if cerr != nil {
		_ = os.RemoveAll(dir)
		return nil, "", fmt.Errorf("plugin %s: cannot contain the native child: %w", res.Manifest.ID, cerr)
	}
	if opts.Log != nil {
		opts.Log.Printf("plugin %s: native child %s", res.Manifest.ID, containment)
	}
	sup, err := supervisor.Start(supervisor.Spec{
		PluginID:       res.Manifest.ID,
		Argv:           argv,
		Env:            []string{"SEV_PLUGIN_SOCKET=stdio:", "SEV_PLUGIN_ID=" + res.Manifest.ID},
		RLimitASBytes:  opts.MemoryMax[res.Manifest.ID],
		VerifyArtifact: verify,
		Log:            opts.Log,
	}, supervisorDispatcher(binding))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, "", err
	}
	return sup, dir, nil
}

// signedCapabilitySurface intersects the manifest's signed capability
// declarations into the one envelope the broker evaluates: the
// top-level capability_envelope ∩ the running variant's
// variant_capabilities ∩ (when reviewer-attested) the
// reviewed_capabilities list. Intersection is the only combinator the
// lattice knows — a capability missing from ANY signed layer was never
// granted by the publisher/reviewer, so it never reaches the operator
// ring at all (dev guide: "T2 reviewer evidence must contain both").
func signedCapabilitySurface(m *packagefmt.Manifest, variant *packagefmt.Variant, res *packagefmt.Result) []string {
	var envelope, variantCaps []string
	// Both lists were schema-validated at verify; a decode failure here
	// yields nil — an empty surface, fail closed.
	_ = json.Unmarshal(m.CapabilityEnvelope, &envelope)
	_ = json.Unmarshal(variant.VariantCapabilities, &variantCaps)

	inVariant := make(map[string]bool, len(variantCaps))
	for _, c := range variantCaps {
		inVariant[c] = true
	}
	var reviewed map[string]bool
	if res.ReviewedCapabilities != nil {
		reviewed = make(map[string]bool, len(res.ReviewedCapabilities))
		for _, c := range res.ReviewedCapabilities {
			reviewed[c] = true
		}
	}
	var out []string
	for _, c := range envelope {
		if !inVariant[c] {
			continue
		}
		if reviewed != nil && !reviewed[c] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// loadVerifiedMember extracts one install-root member and enforces the
// verified-bytes-are-loaded-bytes invariant: the extracted bytes must
// hash to the digest the verified Result recorded. Verify never
// materializes artifacts (streaming-bounded), so extraction is a
// second pass over the file — and this comparison is what makes a file
// swapped between the passes a refusal instead of a loaded payload.
func loadVerifiedMember(pkgPath string, res *packagefmt.Result, rel string) ([]byte, error) {
	want, ok := res.FileDigests[rel]
	if !ok {
		return nil, &EntrypointDigestError{Member: rel}
	}
	raw, err := packagefmt.ReadMember(pkgPath, rel)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if got := "sha256:" + hex.EncodeToString(sum[:]); got != want {
		return nil, &EntrypointDigestError{Member: rel, Want: want, Got: got}
	}
	return raw, nil
}

// --- tool naming ---

// toolName builds the registry name for one plugin operation:
// pl_<sanitized plugin id>_<sanitized method>. Sanitization maps every
// byte outside the providers' name charset to '_' — never dropped, so
// distinct inputs stay distinguishable where the charset allows. Over
// the 64-byte ceiling is a typed refusal, never a truncation (a
// truncated name could collide with — or masquerade as — another
// operation).
func toolName(pluginID, method string) (string, error) {
	name := "pl_" + sanitizeToken(pluginID) + "_" + sanitizeToken(method)
	if len(name) > maxToolNameBytes {
		return "", &ToolNameError{PluginID: pluginID, Name: name,
			Reason: fmt.Sprintf("%d bytes exceeds the %d-byte provider tool-name ceiling", len(name), maxToolNameBytes)}
	}
	return name, nil
}

func sanitizeToken(s string) string {
	out := []byte(s)
	for i := 0; i < len(out); i++ {
		c := out[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			out[i] = '_'
		}
	}
	return string(out)
}

// --- the proxy tool: one operation, spoken over BBB ---

// loadInputSchemas reads the interface schema file (the descriptor JSON)
// from the verified package, parses each operation's "input" field as a
// relative path to a JSON Schema file, loads that file via the verified-
// bytes-are-loaded-bytes seam, and returns a map of operation ID →
// parsed JSON Schema. Operations with no "input" field or whose schema
// file is not in the package get no entry — the tool falls back to the
// open-object schema, which is the prior behavior.
func loadInputSchemas(pkgPath string, res *packagefmt.Result, m *packagefmt.Manifest) (map[string]map[string]interface{}, error) {
	out := make(map[string]map[string]interface{})
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		schemaRel := fmt.Sprintf("interfaces/%s.v%d.schema.json", decl.ID, decl.Version)
		descBytes, err := loadVerifiedMember(pkgPath, res, schemaRel)
		if err != nil {
			// No descriptor file in the package — skip, fall back to
			// open schema for all operations. This is the prior
			// behavior and safe.
			continue
		}
		var ops []struct {
			ID    string `json:"id"`
			Input string `json:"input"`
		}
		if err := json.Unmarshal(descBytes, &ops); err != nil {
			// Descriptor is not the expected array — skip.
			continue
		}
		for _, op := range ops {
			if op.Input == "" {
				continue
			}
			schemaBytes, err := loadVerifiedMember(pkgPath, res, op.Input)
			if err != nil {
				// Schema file not in package — skip, fall back to open.
				continue
			}
			var schema map[string]interface{}
			if err := json.Unmarshal(schemaBytes, &schema); err != nil {
				continue
			}
			out[op.ID] = schema
		}
	}
	return out, nil
}

// operationTool proxies one manifest-declared operation over BBB to the
// guest (PLUGIN_FRAMEWORK §2: "a plugin operation is one Tool
// implementation that proxies over BBB to the worker") — through
// whichever wall the activation stood up (the invoker seam).
type operationTool struct {
	name        string
	description string
	operation   string
	inv         invoker
	parameters  map[string]interface{} // per-operation input schema (from packaged schema files); nil = open schema
}

func (t *operationTool) Name() string        { return t.name }
func (t *operationTool) Description() string { return t.description }

// Parameters returns the per-operation input schema loaded from the
// plugin's packaged schema files. When a schema is available, the LLM
// sees the exact parameter types the plugin expects (e.g. "id" is an
// integer), so it serializes arguments correctly. When no schema is
// packaged (older packages, or a descriptor without an "input" field),
// the open-object schema is the safe fallback — arguments pass through
// whole and the guest owns its own reading.
func (t *operationTool) Parameters() map[string]interface{} {
	if t.parameters != nil {
		return t.parameters
	}
	return map[string]interface{}{
		"type":                 "object",
		"description":          "Arguments forwarded verbatim to the plugin operation.",
		"properties":           map[string]interface{}{},
		"additionalProperties": true,
	}
}

// invokeRequest is the harness→guest frame, the audited invoke.call
// shape (BBB_V2_AUDIT §6.3: operation string + arguments object; §11:
// operations are data, not methods — a new operation changes no wire
// method).
type invokeRequest struct {
	JSONRPC string       `json:"jsonrpc"`
	ID      string       `json:"id"`
	Method  string       `json:"method"`
	Params  invokeParams `json:"params"`
}

type invokeParams struct {
	Operation string                 `json:"operation"`
	Arguments map[string]interface{} `json:"arguments"`
}

// invokeResult is the audited §6.3 result vocabulary the harness reads,
// the Go-SDK client's decode rule: status string (absent ⇒ succeeded),
// operation_result as the payload, reason + reasonCode (camel first,
// snake fallback — §8) on failure. external_receipt is deliberately NOT
// a field here: a guest response carrying one is refused before this
// decode runs (the daemon-injects rule — see decodeReply).
type invokeResult struct {
	Status          string          `json:"status"`
	OperationResult json.RawMessage `json:"operation_result"`
	Reason          string          `json:"reason"`
	ReasonCode      string          `json:"reasonCode"`
	ReasonCodeSnake string          `json:"reason_code"`
}

// rpcErrorObject is the audited §8 error-object shape.
type rpcErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ReasonCode string `json:"reasonCode"`
	} `json:"data"`
}

// Execute speaks one invoke.call round trip to the guest. Guest-level
// outcomes (result or error object) are DATA for the identity —
// returned in the Result; a reply that is not a well-formed response,
// and any containment fault (trap, deadline, poisoned module), is a Go
// error the dispatch layer renders honestly.
func (t *operationTool) Execute(ctx context.Context, args map[string]interface{}) (tools.Result, error) {
	// Host-time injection (operator-approved 2026-08-19): plugins have
	// no ambient clock BY DESIGN — the wall withholds it — so trusted
	// time arrives as DATA, the receipts discipline applied to
	// arguments. The _host* key namespace is HOST-RESERVED: any
	// caller-supplied _host* key is dropped, never trusted, then the
	// host writes its own. A copy keeps the caller's map unmutated.
	injected := make(map[string]interface{}, len(args)+1)
	for k, v := range args {
		if !strings.HasPrefix(k, "_host") {
			injected[k] = v
		}
	}
	injected["_host_now_ms"] = time.Now().UnixMilli()
	frame, err := json.Marshal(invokeRequest{
		JSONRPC: "2.0", ID: harnessRequestID, Method: "invoke.call",
		Params: invokeParams{Operation: t.operation, Arguments: injected},
	})
	if err != nil {
		return tools.Result{}, fmt.Errorf("pluginhost: encode invoke.call: %w", err)
	}
	reply, err := t.inv.Invoke(ctx, frame)
	if err != nil {
		return tools.Result{}, err // the wall's typed taxonomy (pluginworker's or the supervisor's), unwrapped for the caller
	}
	return decodeReply(reply)
}

// decodeReply enforces the response contract (BBB_V2_AUDIT §4): a JSON
// object with jsonrpc "2.0", the id echoed verbatim, no method member,
// and exactly one of result|error. Anything else — including a guest
// that echoes the request back — is a typed ResponseContractError
// naming what came back.
func decodeReply(reply []byte) (tools.Result, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(reply, &members); err != nil {
		return tools.Result{}, &ResponseContractError{Requirement: "reply must be a JSON object", Got: excerpt(reply)}
	}
	var version string
	if err := json.Unmarshal(members["jsonrpc"], &version); err != nil || version != "2.0" {
		return tools.Result{}, &ResponseContractError{Requirement: `jsonrpc must be exactly "2.0"`, Got: excerpt(reply)}
	}
	if _, hasMethod := members["method"]; hasMethod {
		return tools.Result{}, &ResponseContractError{Requirement: "a response carries no method member (a request is not a response)", Got: excerpt(reply)}
	}
	var id interface{}
	if err := json.Unmarshal(members["id"], &id); err != nil {
		return tools.Result{}, &ResponseContractError{Requirement: "id must be present", Got: excerpt(reply)}
	}
	if id != harnessRequestID {
		return tools.Result{}, &ResponseContractError{Requirement: fmt.Sprintf("id must echo %q verbatim", harnessRequestID), Got: excerpt(reply)}
	}
	resultRaw, hasResult := members["result"]
	errorRaw, hasError := members["error"]
	if hasResult == hasError {
		return tools.Result{}, &ResponseContractError{Requirement: "exactly one of result|error", Got: excerpt(reply)}
	}

	if hasError {
		// A structured denial/failure is data the identity should see
		// (§8: a denial is classified by reasonCode, never hidden).
		var eo rpcErrorObject
		if err := json.Unmarshal(errorRaw, &eo); err != nil {
			return tools.Result{}, &ResponseContractError{Requirement: "error member must be a JSON-RPC error object", Got: excerpt(reply)}
		}
		msg := fmt.Sprintf("plugin error %d: %s", eo.Code, eo.Message)
		if eo.Data.ReasonCode != "" {
			msg += fmt.Sprintf(" (reasonCode %s)", eo.Data.ReasonCode)
		}
		return tools.Result{Error: msg}, nil
	}

	// The daemon-injects rule (BBB_V2_AUDIT §6.4; invoke_contract.c:
	// 598-628: "the plugin MUST NOT include external_receipt"): only the
	// HOST authors receipts, injecting them when it performed the
	// effect. A guest response smuggling one is forging the proof plane
	// (A3) — refused whole, typed, with the evidence, BEFORE any decode
	// branch could pass it through as output. Checked on the raw member
	// map so a result too malformed for the typed decode cannot smuggle
	// it either.
	var resultMembers map[string]json.RawMessage
	if json.Unmarshal(resultRaw, &resultMembers) == nil {
		if _, forged := resultMembers["external_receipt"]; forged {
			return tools.Result{}, &ResponseContractError{
				Requirement: "a plugin response must not carry external_receipt (receipts are host-authored; the daemon injects them)",
				Got:         excerpt(reply),
			}
		}
	}

	var ir invokeResult
	if err := json.Unmarshal(resultRaw, &ir); err != nil {
		// A result of unexpected shape is still the guest's result —
		// return it whole; the quarantine polices the boundary, not
		// the payload.
		return tools.Result{Output: string(resultRaw)}, nil
	}
	switch ir.Status {
	case "", "succeeded":
		if len(ir.OperationResult) > 0 {
			return tools.Result{Output: string(ir.OperationResult)}, nil
		}
		return tools.Result{Output: string(resultRaw)}, nil
	default: // "failed" | "denied" (and any future status: not success)
		reason := ir.Reason
		code := firstNonEmpty(ir.ReasonCode, ir.ReasonCodeSnake)
		switch {
		case reason != "" && code != "" && reason != code:
			reason = reason + " (" + code + ")"
		case reason == "":
			reason = firstNonEmpty(code, "no reason given")
		}
		return tools.Result{Error: fmt.Sprintf("operation %s: %s", ir.Status, reason)}, nil
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// excerpt bounds evidence carried inside errors: enough to see what
// the guest actually sent, never an unbounded reply dump.
func excerpt(b []byte) string {
	const max = 256
	if len(b) > max {
		return string(b[:max]) + fmt.Sprintf("… (%d bytes)", len(b))
	}
	return string(b)
}
