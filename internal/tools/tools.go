// Package tools implements built-in capabilities for an AII OS identity.
//
// Seven tools (inspired by Pi, pi.dev): read, write, edit, shell, grep, ls, web_fetch.
// These are NOT plugins — no plugin machinery, no C ABI, no WASM. When the plugin
// system arrives (P1), these become the first plugins — or stay built-in.
//
// Tools are invoked directly by the LLM via function calling.
// Tool output is always honest about truncation (R18).
package tools

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// Tool is the interface every built-in capability implements.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, args map[string]interface{}) (Result, error)
}

// Result is what a tool returns.
type Result struct {
	Output    string
	Truncated bool
	Error     string
}

// Text returns the result text for the LLM (output + error if any).
func (r Result) Text() string {
	if r.Error != "" {
		return "Error: " + r.Error
	}
	if r.Truncated {
		return r.Output + "\n[truncated]"
	}
	return r.Output
}

// ToolInfo is a tool's discovery information at a given depth.
type ToolInfo struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// Registry holds all available tools.
type Registry struct {
	disMu      sync.RWMutex
	regMu      sync.RWMutex // guards tools+sources: Register is public and the plugin era registers LIVE while chat goroutines read
	tools      map[string]Tool
	sources    map[string]string // tool name -> origin: "builtin" via Register; a plugin id when the plugin runtime cuts its entry point
	hostOnly   map[string]bool   // tool name -> the HOST drives this one; it never appears in Names() (see RegisterHostOp)
	extraRoots []string          // operator-granted additional sandbox roots (config tools.extra_roots) — regMu-guarded, settable live
	protected  []string          // substrate paths (ledger/key/db/config) no grant may expose (Sev H1/M1 2026-08-18)
	cwd        string
	policy     *firewall.Policy // THE substrate floor: enforcement AND floor-text derive from this one instance
	timeouts   Timeouts         // operator execution ceilings (config tools.*_timeout_seconds)
	sandbox    string           // Ring 5 root: the ONLY tree the identity's tools may touch
	disabled   map[string]bool  // operator kill-switch per tool (dashboard checkboxes)

	// malformedCalls counts tool calls rejected before execution because their
	// arguments were malformed: unparseable JSON at the dispatch seam or a
	// schema-required key missing at validation. Exposed via MalformedCallCount()
	// so operators see corruption rates engine-side, not only in transcript
	// forensics. (P3 telemetry, external review 2026-08-17.)
	malformedCalls atomic.Uint64

	// suspiciousPathCalls counts read-family calls (read/grep/ls) whose
	// resolved target does not exist — the on-disk fingerprint of
	// content corruption that passes schema validation: fluent,
	// well-formed calls pointing at files that were never there
	// (forensics 2026-08-21/22: replay.py-for-replay.go, ioi-os-for-
	// aii-os, truncated-splice paths). Every observed instance of the
	// channel was a read-side miss; write/edit misses are excluded
	// because writing into a not-yet-created directory is legitimate
	// workflow, not corruption. Observed only AFTER all gates pass —
	// denials have their own surfaces and are not corruption. The call
	// still executes and returns its honest downstream error; this
	// counter makes the corruption RATE visible engine-side.
	// (P3 telemetry family, extended 2026-08-22.)
	suspiciousPathCalls atomic.Uint64

	// duplicateArgKeys counts tool calls whose argument object repeated a
	// key. Go's encoding/json silently keeps the LAST value for a repeated
	// key, so such a call dispatches on whichever copy landed last — the
	// corrupted tail, in every observed instance (forensics 2026-08-24:
	// read({"file_path":X, ... x22 ..., "file_path":"sections_s-model"}),
	// a valid-JSON call no other counter can see). Target-miss telemetry
	// catches only the subset whose last value also fails to exist; a
	// repeated key is the corruption's own fingerprint, visible even when
	// the surviving value is a real file. Counts and logs, never blocks:
	// the call proceeds exactly as before. (P3 telemetry family.)
	duplicateArgKeys atomic.Uint64

	// safeSource reports SAFE mode (canon IDENTITY_SEMANTICS §10: in SAFE
	// no fs.write, no process.run, no external surfaces — only the
	// read-only diagnostic surface continues). Wired by the app; nil =
	// never safe (tests, firstboot).
	safeSource func() (string, bool)

	// fetchObserver receives every successfully fetched URL from EVERY
	// egress consumer — builtin web_fetch and brokered plugin
	// net.outbound alike (H3: the provenance ladder credits fetches that
	// really happened, whichever organ performed them). Guarded: the app
	// wires it after activation has already handed brokers a NotifyFetch
	// path, and chat goroutines read it live.
	observeMu     sync.RWMutex
	fetchObserver func(url string)
}

// safeBlockedTools: what canon §10 disables in SAFE — mutation and the
// outside world. read/grep/ls stay: the read-only diagnostic surface.
var safeBlockedTools = map[string]bool{
	"write": true, "edit": true, "shell": true, "web_fetch": true,
}

// SetSafeSource wires the SAFE-mode check (app.SafeMode).
func (r *Registry) SetSafeSource(fn func() (string, bool)) { r.safeSource = fn }

// Timeouts carries the operator's tool-execution ceilings (config
// tools.shell_timeout_seconds / tools.webfetch_timeout_seconds — the
// 2026-08-18 ruling: durations are config grouped with their subject,
// never code). The zero value means defaults; a zero-value Timeouts is
// a fully working configuration.
type Timeouts struct {
	ShellSeconds    int
	WebFetchSeconds int
}

func (t Timeouts) shell() time.Duration {
	if t.ShellSeconds <= 0 {
		return 120 * time.Second
	}
	return time.Duration(t.ShellSeconds) * time.Second
}

func (t Timeouts) webFetch() time.Duration {
	if t.WebFetchSeconds <= 0 {
		return 30 * time.Second
	}
	return time.Duration(t.WebFetchSeconds) * time.Second
}

// NewRegistry creates a tool registry confined to the given working directory.
//
// Ring 5 model: a sandbox ROOT (allow-list — everything outside is denied)
// plus a substrate deny-list inside it (the identity may not read its own
// ledger/keys/config even though they live in its home). The deny-list IS
// the firewall.Policy — the same instance whose rules render the identity's
// floor text (LocalFloor) and collect the denial audit (Record). One
// source of truth: enforcement and documentation cannot drift. A nil
// policy defaults to firewall.DefaultPolicy().
//
// The first real birth taught this lesson: a deny-list alone let a newborn
// rummage the operator's home, other identities' workspaces, and shell
// history, because "not on the list" is not "forbidden".
//
// String-level shell confinement is BEST-EFFORT and documented as such.
// For a hard boundary, run the process in a container/VM and/or set
// "tools": {"shell": false} to remove the shell entirely.
func NewRegistry(cwd string, policy *firewall.Policy, timeouts Timeouts) *Registry {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if policy == nil {
		policy = firewall.DefaultPolicy()
	}
	r := &Registry{
		tools:    make(map[string]Tool),
		sources:  make(map[string]string),
		cwd:      cwd,
		disabled: make(map[string]bool),
		policy:   policy,
		timeouts: timeouts,
		sandbox:  abs,
	}
	r.registerDefaults()
	return r
}

// SetToolEnabled is the operator kill-switch per tool (dashboard checkbox).
// Disabling a tool refuses its execution; it stays registered so the
// operator can re-enable it. The identity verbs (note/recall/send/work/
// commit) are NOT toggleable — they are the identity's organs (R34: note
// is never gated).
//
// Lock-guarded: toggles arrive on the dashboard connection while chats
// execute tools on another — an unsynchronized map here is a runtime
// panic under two tabs (2026-08-17 review).
func (r *Registry) SetToolEnabled(name string, enabled bool) {
	r.disMu.Lock()
	defer r.disMu.Unlock()
	r.disabled[name] = !enabled
}

// ToolEnabled reports a read under the toggle lock (Execute reads this
// from chat goroutines).
func (r *Registry) ToolEnabled(name string) bool {
	r.disMu.RLock()
	defer r.disMu.RUnlock()
	return !r.disabled[name]
}

// ToolState describes a tool for the operator surface (dashboard checkbox).
type ToolState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// ToolStates lists all registered tools with their toggle state.
func (r *Registry) ToolStates() []ToolState {
	names := r.Names()
	out := make([]ToolState, 0, len(names))
	for _, name := range names {
		t, ok := r.lookup(name)
		if !ok {
			continue
		}
		out = append(out, ToolState{Name: name, Description: t.Description(), Enabled: r.ToolEnabled(name)})
	}
	return out
}

func (r *Registry) registerDefaults() {
	r.Register(&ReadTool{maxBytes: 51200})
	if !platformNoWrite {
		r.Register(&WriteTool{})
		r.Register(&EditTool{})
	}
	if sh := hostcap.Can(hostcap.Shell); sh.Available {
		r.Register(&ShellTool{timeout: r.timeouts.shell(), sandbox: r.sandbox})
	}
	r.Register(&GrepTool{deny: func(p string) bool {
		// The walk's paths are already resolved under the root, and
		// WalkDir does not follow symlinks, so the pattern check alone is
		// sufficient here and avoids an EvalSymlinks per file.
		if r.policy == nil {
			return false
		}
		return !r.policy.Check("grep", p).Allowed
	}})
	r.Register(&LsTool{})
	// onFetch is bound to the registry's guarded observer at construction
	// — no later mutation of a live tool (the old ObserveFetches wrote
	// the field while chat goroutines could read it).
	r.Register(&WebFetchTool{maxBytes: 100000, timeout: r.timeouts.webFetch(), onFetch: r.NotifyFetch})
}

// The shell tool's availability is hostcap.Shell — one truth, with a
// reason, instead of a constant per platform file. platformNoWrite
// remains per-platform (it gates write/edit on mobile, which is store
// policy, not an exec capability).
// platformNoWrite is declared per platform
// (platform_unix / platform_windows / platform_mobile) — they stopped
// being derivable from each other the day Windows arrived (Phase A,
// 2026-08-18: no shell there then; R79 2026-08-27: PowerShell now —
// hostcap.Shell says so, write/edit stay).

// Register adds a tool to the registry as a BUILTIN - trusted code
// compiled into this binary, whose read-only members stay alive in SAFE
// mode. A dynamically sourced tool (the plugin era) carries its origin
// through RegisterDynamic instead, so SAFE can suspend it wholesale.
func (r *Registry) Register(t Tool) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	r.tools[t.Name()] = t
	r.sources[t.Name()] = "builtin"
}

// RegisterDynamic is the plugin era's entry point — cut the day its
// first real consumer arrived (internal/pluginhost, the quarantine
// harness). The tool carries a non-builtin origin (the plugin id), so
// origin-gated SAFE suspends it wholesale (SAFE_MODE_PLUGIN_LIFECYCLE:
// total, exception-free) and the operator surface can attribute it.
//
// Fail-closed admission, both ways: an origin claiming "builtin" would
// dodge the SAFE origin gate, and an existing name is never replaced —
// a plugin shadowing `read` (or another plugin's tool) would be a
// hijack, so the collision is the caller's activation failure, not a
// silent rename (PLUGIN_FRAMEWORK §2: the Registry stays mechanical;
// admission decisions surface as typed refusals).
func (r *Registry) RegisterDynamic(t Tool, origin string) error {
	if origin == "" || origin == "builtin" {
		return fmt.Errorf("dynamic tool %q requires a non-builtin origin, got %q", t.Name(), origin)
	}
	r.regMu.Lock()
	defer r.regMu.Unlock()
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("tool name %q is already registered (origin %s); dynamic registration never replaces", t.Name(), r.sources[t.Name()])
	}
	r.tools[t.Name()] = t
	r.sources[t.Name()] = origin
	return nil
}

// RegisterHostOp registers a tool THE HOST CALLS AND THE IDENTITY NEVER
// SEES. It executes like any other tool and is absent from Names(), so
// it reaches neither the model's function list, nor the tools verb, nor
// the operator's toggle list.
//
// A channel adapter's send/receive/describe are what this exists for.
// They are plumbing the host drives: send resolves through the address
// book, and receive's output must pass internal/untrusted before it can
// reach a prompt. Registering them like ordinary tools handed the
// identity the raw pipe beside the governed one — it could address a
// stranger directly, bypassing "name a person, never an address", and
// it could pull foreign text into its own context UNWRAPPED, straight
// through the R49 boundary. Found in review 2026-08-24, before any
// channel adapter existed to exploit it.
//
// The operator's lever for a channel adapter is the PLUGIN, not its
// three methods: disabling send while receive keeps running is not a
// state anyone wants.
func (r *Registry) RegisterHostOp(t Tool, origin string) error {
	if err := r.RegisterDynamic(t, origin); err != nil {
		return err
	}
	r.regMu.Lock()
	defer r.regMu.Unlock()
	if r.hostOnly == nil {
		r.hostOnly = map[string]bool{}
	}
	r.hostOnly[t.Name()] = true
	return nil
}

// Deregister removes a dynamically registered tool (plugin
// deactivation). Builtin tools are the identity's organs — never
// removable through this seam (the operator kill-switch is
// SetToolEnabled); an unknown name is a no-op.
func (r *Registry) Deregister(name string) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	if src, ok := r.sources[name]; !ok || src == "builtin" {
		return
	}
	delete(r.tools, name)
	delete(r.sources, name)
	delete(r.hostOnly, name)
}

// SetExtraRoots installs operator-granted ADDITIONAL sandbox roots
// (config tools.extra_roots; dashboard Settings -> Sandbox). Ring 5's
// shape is operator-owned: widening the identity's world — letting a
// chosen identity work on out-of-tree projects like AII OS itself —
// is the operator's deliberate act, never the identity's. Paths are
// resolved exactly like the primary root; relative and empty entries
// are dropped. The substrate deny-list and SAFE gates apply inside
// EVERY root. The in-process wall stays best-effort: on Linux the
// structural wall (namespace wrapper) must bind these paths too.
func (r *Registry) SetExtraRoots(roots []string) {
	r.regMu.RLock()
	sandbox, protected := r.sandbox, append([]string(nil), r.protected...)
	r.regMu.RUnlock()
	resolved := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		abs := filepath.Clean(root)
		if rr, err := filepath.EvalSymlinks(abs); err == nil {
			abs = rr
		}
		// THE choke point (Sev H1/M1): a grant that would expose the
		// identity's own substrate is refused here, so every door —
		// dashboard, config watcher, boot — inherits it. The identity
		// can never re-expose its ledger/keys via a grant, whatever
		// reaches this function.
		if rootExposesSubstrate(abs, sandbox, protected) {
			log.Printf("Ring 5: REFUSED grant %q — it would expose the identity's own substrate", abs)
			continue
		}
		resolved = append(resolved, abs)
	}
	r.regMu.Lock()
	r.extraRoots = resolved
	r.regMu.Unlock()
}

// SetProtectedPaths registers the substrate files no grant may expose
// (the app passes ledger/key/db/config). Resolved like grants.
func (r *Registry) SetProtectedPaths(paths []string) {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if rr, err := filepath.EvalSymlinks(abs); err == nil {
			abs = rr
		}
		out = append(out, abs)
	}
	r.regMu.Lock()
	r.protected = out
	r.regMu.Unlock()
}

// RootRejectionReason returns "" if a grant is safe, else why it is
// refused — the dashboard surfaces this to the operator.
func (r *Registry) RootRejectionReason(path string) string {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		return "must be an absolute path"
	}
	abs := filepath.Clean(path)
	if rr, err := filepath.EvalSymlinks(abs); err == nil {
		abs = rr
	}
	r.regMu.RLock()
	sandbox, protected := r.sandbox, append([]string(nil), r.protected...)
	r.regMu.RUnlock()
	if rootExposesSubstrate(abs, sandbox, protected) {
		return "would expose the identity's own substrate (ledger, keys, config, or home) — refused"
	}
	return ""
}

// rootExposesSubstrate: a grant is unsafe if it is the filesystem root,
// contains (or equals) the sandbox home, or contains any substrate
// file. within(grant, x) is true when x lies inside grant.
func rootExposesSubstrate(abs, sandbox string, protected []string) bool {
	if abs == "/" || abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return true
	}
	if abs == sandbox || within(abs, sandbox) {
		return true
	}
	for _, p := range protected {
		if abs == p || within(abs, p) {
			return true
		}
	}
	return false
}

// Roots reports the primary sandbox root and any operator-granted
// extra roots (dashboard read surface).
func (r *Registry) Roots() (string, []string) {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	return r.sandbox, append([]string(nil), r.extraRoots...)
}

func within(root, abs string) bool {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolveForContainment resolves symlinks for a containment check even
// when the path does not fully exist: the DEEPEST EXISTING ancestor is
// resolved with EvalSymlinks and the not-yet-existing remainder is
// re-attached lexically. The old shape fell back to the UNRESOLVED path
// whenever EvalSymlinks failed — which it always does for a new file —
// so sandbox/link/new.txt with link→outside passed lexical containment
// and the write followed the link out of the sandbox (external claim
// H3, confirmed; probe: TestWriteThroughSymlinkedParentRejected).
// abs must already be absolute and Clean (filepath.Join delivers both).
func resolveForContainment(abs string) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	rest := ""
	dir := filepath.Clean(abs)
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			// Hit the root without resolving anything — keep the lexical
			// form; within() still judges it.
			return filepath.Join(dir, rest)
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
	}
}

// lookup reads a tool under the registration lock.
func (r *Registry) lookup(name string) (Tool, bool) {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// inExtraRoot reports a path inside an operator-granted root and NOT
// inside the primary home tree. The substrate deny-list protects THEIR
// OWN home (its patterns are home-relative substrings: ledger.jsonl,
// config/, the binary name); matched naked against granted-root paths
// they false-positive on innocent files — live 2026-08-18: a granted
// AII OS checkout made nearly every path contain "aii-os", so read and
// bash refused while ls (which never consults the list) worked. Inside
// a granted root the operator's grant is the authority.
func (r *Registry) inExtraRoot(path string) bool {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(r.sandbox, path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	// A grant may be NESTED inside the home tree (James 2026-08-18:
	// the runtime directory is a natural place for project dirs — the
	// runtime's substrate stays pattern-protected, and a granted
	// subdirectory is a deliberate whitelisted opening in it). The
	// first version refused grants inside home; that made a grant of
	// <runtime>/work silently inert.
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	for _, root := range r.extraRoots {
		if within(root, abs) {
			return true
		}
	}
	return false
}

// registeredNonBuiltin reports a tool that exists but did NOT arrive
// through the builtin path. Unknown names return false so they keep
// flowing to the honest "unknown tool" error.
func (r *Registry) registeredNonBuiltin(name string) bool {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	_, exists := r.tools[name]
	return exists && r.sources[name] != "builtin"
}

// ObserveFetches wires the callback for every successfully fetched URL
// (H3: the identity engine verifies note source_url citations against
// fetches that really happened). One registry-owned observation point:
// builtin web_fetch reports here, and the plugin broker's net.outbound
// reports here through NotifyFetch — so a plugin-fetched URL earns the
// same provenance a builtin fetch does, through the same seam.
func (r *Registry) ObserveFetches(fn func(url string)) {
	r.observeMu.Lock()
	defer r.observeMu.Unlock()
	r.fetchObserver = fn
}

// NotifyFetch reports one successfully fetched URL into the observer.
// Safe before ObserveFetches wires anything (no-op) — brokers built at
// activation hold this method and the app wires the engine after.
func (r *Registry) NotifyFetch(url string) {
	r.observeMu.RLock()
	fn := r.fetchObserver
	r.observeMu.RUnlock()
	if fn != nil {
		fn(url)
	}
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.lookup(name)
	return t, ok
}

// Execute runs a tool by name with the given arguments.
// Enforces the Ring 5 deny-list for filesystem tools (path-resolved, symlink-aware).
// Bash deny-list is advisory (documented) — string-matching commands cannot prevent
// all indirection. For full sandboxing, run the process in a container or chroot.
func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) (Result, error) {
	if !r.ToolEnabled(name) {
		return Result{Error: "access denied: tool disabled by operator"}, nil
	}

	// Schema validation (P2): reject calls missing required arguments BEFORE
	// any policy gate runs, so the model sees the real failure ("missing
	// required argument") instead of a downstream denial that reads like a
	// sandbox flake. Shallow by design: presence of schema-required keys only —
	// not a JSON-Schema engine; optional keys and value types are unchecked.
	// A call failing here was malformed at birth; count it (P3).
	if msg := r.validateRequired(name, args); msg != "" {
		r.malformedCalls.Add(1)
		return Result{Error: msg}, nil
	}

	// SAFE gate (canon IDENTITY_SEMANTICS §10): with identity integrity
	// unverified, mutation and outside-world surfaces are disabled —
	// write, edit, shell (process.run), web_fetch. The read-only
	// diagnostic surface (read/grep/ls) continues. ORIGIN-gated too,
	// not just name-gated (SAFE_MODE_PLUGIN_LIFECYCLE: suspension is
	// total and exception-free): a tool that did not arrive through
	// the builtin path is suspended wholesale, so SAFE fails CLOSED
	// for surfaces this name list has never heard of.
	if r.safeSource != nil && (safeBlockedTools[name] || r.registeredNonBuiltin(name)) {
		if reason, safe := r.safeSource(); safe {
			return Result{Error: fmt.Sprintf("refused: I am in safe mode — %s. Only read-only tools continue until my operator restores the record.", reason)}, nil
		}
	}

	// Allow-list confinement: every path-bearing tool resolves against the
	// sandbox root; anything outside is denied before the tool runs.
	// Resolve BEFORE checking — a check against the CWD-relative path
	// examines a different file than the one the tool opens (red-team
	// finding A3.14: check/path divergence when CWD != sandbox).
	if name == "read" || name == "write" || name == "edit" {
		if path, ok := args["file_path"].(string); ok {
			if !filepath.IsAbs(path) {
				args["file_path"] = filepath.Join(r.sandbox, path)
			}
			resolved := args["file_path"].(string)
			if r.isOutsideSandbox(resolved) {
				return Result{Error: "access denied: outside sandbox"}, nil
			}
			if rule := r.substrateDenied(name, resolved); rule != nil {
				return Result{Error: fmt.Sprintf("access denied: protected path (%s: %s)", rule.ID, rule.Reason)}, nil
			}
		}
	}

	if name == "shell" {
		if cmd, ok := args["command"].(string); ok {
			if why := r.shellRefusal(cmd); why != "" {
				return Result{Error: "access denied: " + why +
					" (best-effort check; run under a container for a hard boundary)"}, nil
			}
		}
	}

	if name == "grep" || name == "ls" {
		key := "path"
		if path, ok := args[key].(string); ok && path != "" {
			if !filepath.IsAbs(path) {
				args[key] = filepath.Join(r.sandbox, path)
			}
			resolved := args[key].(string)
			if r.isOutsideSandbox(resolved) {
				return Result{Error: "access denied: outside sandbox"}, nil
			}
			if name == "grep" {
				if rule := r.substrateDenied(name, resolved); rule != nil {
					return Result{Error: fmt.Sprintf("access denied: protected path (%s: %s)", rule.ID, rule.Reason)}, nil
				}
			}
		}
	}

	t, ok := r.lookup(name)
	if !ok {
		return Result{}, fmt.Errorf("unknown tool: %s", name)
	}

	// P3 corrupted-content telemetry, placed AFTER all gates: the read
	// family's resolved target must exist or the miss is counted as a
	// corruption-rate signal. Denials have their own surfaces (policy
	// audit, error returns) and are not corruption — so only calls that
	// cleared every gate contribute to the rate.
	if name == "read" || name == "grep" || name == "ls" {
		key := "file_path"
		if name == "grep" || name == "ls" {
			key = "path"
		}
		if p, ok := args[key].(string); ok && p != "" {
			r.observePathSuspicion(name, p)
		}
	}
	return t.Execute(ctx, args)
}

// validateRequired returns an error message when args is missing a key the
// tool's schema marks required, or "" when the call may proceed. Presence
// only: value typing stays with each tool's own Execute, which already
// handles typed fallbacks honestly.
func (r *Registry) validateRequired(name string, args map[string]interface{}) string {
	t, ok := r.lookup(name)
	if !ok {
		return "" // unknown-tool handling stays where it was
	}
	reqd, ok := t.Parameters()["required"].([]string)
	if !ok {
		return ""
	}
	var missing []string
	for _, k := range reqd {
		if v, present := args[k]; !present || v == nil {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	return fmt.Sprintf("malformed arguments: missing required %s for %s", strings.Join(missing, ", "), name)
}

// MalformedCallCount reports how many tool calls were rejected as malformed
// at the dispatch seams since process start. Zero means no corruption OR no
// traffic; read it alongside call volume. (P3 telemetry.)
func (r *Registry) MalformedCallCount() uint64 { return r.malformedCalls.Load() }

// SuspiciousPathCount returns the count of read-family calls whose
// resolved target did not exist — the engine-side corruption rate for
// the corrupted-content channel. Zero means no corruption OR no traffic;
// read it alongside call volume. (P3 telemetry family.)
func (r *Registry) SuspiciousPathCount() uint64 { return r.suspiciousPathCalls.Load() }

// observePathSuspicion implements the corrupted-content extension of the
// P3 telemetry family. Read family only (read/grep/ls), called after all
// gates pass and just before dispatch — denials have their own surfaces
// (policy audit, error returns) and are not corruption. The fingerprint:
// the resolved TARGET does not exist. That is the on-disk shape of a
// schema-valid but corrupted argument — replay.py-for-replay.go sits
// beside replay.go, so the parent exists and only the file is wrong;
// ioi-os-for-aii-os, digit-spliced filenames, same shape. The call still
// executes and returns its honest downstream error; this counter makes
// the corruption RATE visible engine-side, so the operator sees the
// channel's frequency, not just its individual failures.
func (r *Registry) observePathSuspicion(name, resolved string) {
	// Never blocks, never rewrites — counts. Post-gate, read family
	// only, target-miss only. (See field comment and SuspiciousPathCount.)
	if _, err := os.Stat(resolved); err != nil {
		r.suspiciousPathCalls.Add(1)
	}
}

// CountMalformed increments the malformed-call counter from external seams —
// e.g. the app's dispatch path, which rejects unparseable JSON before the
// registry is ever reached. (P3 telemetry.)
func (r *Registry) CountMalformed() { r.malformedCalls.Add(1) }

// substrateDenied checks a resolved path against the Ring 5 policy and,
// on denial, records it to the policy's audit trail. Returns the denying
// rule (nil if allowed). Uses filepath.Abs + filepath.EvalSymlinks to
// prevent symlink bypass; checks the resolved, absolute, and raw forms
// (a check against only one form examines a different file than the one
// the tool opens).
func (r *Registry) substrateDenied(tool, path string) *firewall.Rule {
	// Operator-granted territory: home-relative substrate patterns do
	// not apply (see inExtraRoot).
	if r.inExtraRoot(path) {
		return nil
	}
	// Resolve to absolute path
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	// Resolve symlinks — through the deepest existing ancestor when the
	// target itself does not exist yet (same containment law as
	// isOutsideSandbox: a new file's parent decides where it really lands).
	resolved := resolveForContainment(abs)

	// Check every form the tool might ultimately open
	for _, form := range []string{resolved, abs, path} {
		if v := r.policy.Check(tool, form); !v.Allowed {
			r.policy.Record(tool, path, v.Rule)
			return v.Rule
		}
	}
	return nil
}

// isOutsideSandbox reports whether a user-supplied path resolves outside the
// sandbox root. Relative paths resolve against the sandbox. Symlinks are
// ALWAYS resolved — for a destination that does not exist yet, through
// its deepest existing ancestor.
func (r *Registry) isOutsideSandbox(path string) bool {
	var abs string
	if filepath.IsAbs(path) {
		abs = path
	} else {
		abs = filepath.Join(r.sandbox, path)
	}
	abs = resolveForContainment(abs)
	if within(r.sandbox, abs) {
		return false
	}
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	for _, root := range r.extraRoots {
		if within(root, abs) {
			return false
		}
	}
	return true
}

// shellCommandEscapes is a BEST-EFFORT string check for the sandbox boundary
// and substrate floor. It catches direct references (~, $HOME, absolute
// paths outside the sandbox, protected filenames, ..-walks out of root)
// and — since red-team A3.3 — expands glob metacharacters against the
// sandbox before matching, in BOTH cases (bash's nocaseglob), so neither
// "cat data/led*" nor "cat Provi*.json" dodges the substrate floor by
// never spelling the protected name. It scans the command TEXT, so
// indirection that still writes the name out IS caught: measured, "cat
// $(echo providers.json)", "p=providers.json; cat $p" and "echo
// providers.json | xargs cat" are all refused, and a glob keeps being
// expanded when it is glued to an operator ("cat provi*.json|head").
// What escapes is a name ASSEMBLED by the parser and never appearing
// whole — quote removal (pro"vi"ders.json), backslash escapes, brace
// expansion ({p,P}roviders.json). That is why the shell also runs scrubbed
// (HOME=sandbox, minimal env) and why the honest boundary is a container.
// This check exists to stop the curious, not the adversarial.
//
// The substrate match comes from the ring5 Policy (Policy.Check) — the
// same instance, and the same canonicalisation, the read/write path uses,
// so a name refused there cannot be reached from here by respelling it.
// floorRefusal says WHICH part of the command reached a protected name.
//
// Naming only the pattern is worse than it looks. The floor protects bare
// names, and "aii-os" is one of them — so an identity whose own checkout
// is .../work/aii-os (allowed, because absolute paths inside a granted
// root are scrubbed before this scan) gets told "aii-os is protected"
// while it is standing in a directory by that name. It reads as "your
// repo is forbidden", which is false, and there is nothing in it to
// correct toward. Observed cost: nine consecutive retries varying grep
// -n, grep -rn, cat|grep, grep -c, ls against one unchanged path.
//
// So: the offending FIELD, the name it reached, and where the allowed
// tree is — enough to fix the path instead of guessing at the command.
func (r *Registry) floorRefusal(cmd, denied string) string {
	offender := ""
	for _, field := range strings.Fields(cmd) {
		f := strings.Trim(field, "\"'`,;()|<>")
		// Ask the policy rather than strings.Contains: the match is
		// case-canonical there, and a raw scan could not find
		// "Providers.json" for the pattern that had just refused it — the
		// refusal fell through to "something this command expands to",
		// which names nothing the identity can correct. Keyed to the
		// pattern that decided, so a granted root scrubbed out of the
		// scan above (it may carry another protected NAME innocently) is
		// not blamed for a refusal it did not cause.
		if v := r.policy.Check("bash", f); !v.Allowed && v.Rule.Pattern == denied {
			offender = f
			break
		}
	}
	if offender == "" {
		// The match came from a glob expansion, so no literal field
		// carries it. Say that rather than inventing a path.
		return "something this command expands to reaches the protected name " + denied +
			" (the sandbox is " + r.sandbox + ")"
	}
	return offender + " reaches the protected name " + denied +
		"; the sandbox is " + r.sandbox +
		" (only an ABSOLUTE path inside a granted root is exempt from the name check;" +
		" a relative path is matched on the name alone)"
}

// shellCommandEscapes reports whether the command is refused. The reason
// it is refused lives in shellRefusal; this derives from that one answer
// rather than computing the same thing a second way.
func (r *Registry) shellCommandEscapes(cmd string) bool { return r.shellRefusal(cmd) != "" }

// shellRefusal returns why the command is refused, or "" to allow it.
//
// It returns the REASON and not a bool because the caller has to tell the
// identity what to change. A refusal that will not say which path
// offended leaves only the command to vary, and that is what an identity
// does: thirty denials in one session, nine of them the same out-of-tree
// path retried as grep -n, grep -rn, cat|grep, grep -c, ls — the tool
// changing every time and the path never, because the path was never
// named. Roughly a third of a bounded round budget, spent guessing at a
// fact this function already knows.
func (r *Registry) shellRefusal(cmd string) string {
	if strings.Contains(cmd, "~") || strings.Contains(cmd, "$HOME") {
		return "the command references ~ or $HOME, which this check cannot resolve — write the path out in full (the sandbox is " + r.sandbox + ")"
	}
	// Substrate floor still applies inside the sandbox — match against
	// the raw command AND its glob-expanded variants (A3.3: "data/led*"
	// expands to data/ledger.jsonl inside the shell, dodging the raw match)
	// Scrub absolute references into granted roots BEFORE the pattern
	// scan: a command touching /granted/aii-os/config/x must not trip
	// home-substrate patterns. Home-relative references (no absolute
	// path) and primary-sandbox paths stay fully checked.
	scrubbed := cmd
	for _, field := range strings.Fields(cmd) {
		f := strings.Trim(field, "\"'`,;()|<>")
		// No IsAbs guard: inExtraRoot already joins a relative path onto
		// the sandbox and resolves symlinks. Requiring absolute here made
		// the grant depend on SPELLING — "cd /granted/repo" exempt while
		// "cd repo" was matched on the bare name — so the same directory
		// gave two answers.
		if f != "" && r.inExtraRoot(f) {
			scrubbed = strings.ReplaceAll(scrubbed, f, "")
		}
	}
	checks := []string{scrubbed}
	if strings.ContainsAny(scrubbed, "*?[") {
		checks = append(checks, r.expandGlobs(scrubbed)...)
	}
	// Policy.Check owns the match: the same substring rule DenyPatterns
	// feeds, but canonical in case and separator on BOTH sides. This scan
	// used to iterate DenyPatterns and compare RAW, so the floor answered
	// by SPELLING — read and grep refuse providers.json in any case,
	// while "cat Providers.json" passed here, joined the sandbox, ran
	// with cmd.Dir inside it, and a case-insensitive filesystem (macOS,
	// Windows) opened the real file: the operator's API keys printed into
	// the transcript. One rule, one owner, both paths.
	for _, c := range checks {
		if v := r.policy.Check("bash", c); !v.Allowed {
			return r.floorRefusal(cmd, v.Rule.Pattern)
		}
	}
	// References that leave the sandbox, in EITHER spelling.
	//
	// This loop used to skip every relative token, so "cat
	// ../../../etc/passwd" was allowed while "cat /etc/passwd" was
	// refused — the same file, and the escape was the cheaper one to
	// write. The doc comment above claimed ..-walks out of root were
	// caught; they were not, and that is the class this codebase keeps
	// finding: a stated invariant that stops holding when its context
	// changes, here the context being which spelling the path arrived in.
	//
	// isOutsideSandbox already owns the resolution — it joins a relative
	// path onto the sandbox and calls resolveForContainment, which
	// resolves the deepest EXISTING ancestor through symlinks and
	// re-attaches the rest. Removing the guard therefore does not add a
	// second resolver; it lets the one that exists see the paths it was
	// being kept from. Relative tokens now get the symlink containment
	// they were bypassing.
	//
	// Non-path tokens are harmless: a flag or a search literal joins onto
	// the sandbox and lands INSIDE it, so nothing new is refused for not
	// being a path.
	for _, field := range strings.Fields(cmd) {
		f := strings.Trim(field, "\"'`,;()|<>")
		if f == "" {
			continue
		}
		// Clean BEFORE the exemption test. It was a lexical prefix match
		// on the raw token, so "/usr/../etc/passwd" wore the /usr/ prefix
		// out of the sandbox and never reached the containment check at
		// all — the same class the comment above names, the context here
		// being the path's SPELLING. The exemption may only ever apply to
		// a path that really is under those roots.
		clean := filepath.Clean(f)
		if filepath.IsAbs(clean) && (strings.HasPrefix(clean, "/usr/") || strings.HasPrefix(clean, "/bin/") ||
			clean == "/usr" || clean == "/bin" || clean == "/lib" || clean == "/etc/alternatives" ||
			clean == "/dev/null" || clean == "/dev/zero" || clean == "/dev/stdin" || clean == "/dev/stdout" || clean == "/dev/stderr") {
			continue // system binaries and devices are invocation, not data
		}
		if r.isOutsideSandbox(f) {
			return f + " is outside the sandbox " + r.sandbox
		}
	}
	return ""
}

// expandGlobs best-effort expands a command's glob tokens against the
// sandbox root. Used only for deny-list matching; the shell still
// performs its own expansion at run time. Failures return nothing (raw
// match still applies).
//
// SECURITY (2026-08-17 review, finding 3): the model's command is
// UNTRUSTED INPUT and must NEVER be interpolated into a shell string.
// The previous form — `bash -c "cd … && for tok in <cmd>; do …"` —
// executed `;`/`&&`/`$()` fragments as code DURING the denial check
// itself, before the command was (rightly) refused: the checker was an
// execution oracle for the very commands it existed to deny. Tokens are
// now split in Go and passed as ARGV; bash receives them via "$@" and
// never re-parses them as code.
func (r *Registry) expandGlobs(cmd string) []string {
	// PURE GO, third form (2026-08-26). The first form interpolated
	// untrusted input into a shell string and executed injection
	// fragments DURING the denial check; the second passed argv to
	// bash safely — and still assumed a bash, which Windows does not
	// have and mobile cannot spawn (hostcap: this is a denial CHECK,
	// and a checker that silently no-ops on three platforms is a
	// checker that lies there). filepath.Glob matches compgen -G for
	// the patterns this scan cares about (* ? [] — neither does
	// globstar), needs no subprocess, no timeout, and no PATH.
	var out []string
	for _, tok := range globTokens(cmd) {
		pattern := tok
		rooted := filepath.IsAbs(tok)
		if !rooted {
			pattern = filepath.Join(r.sandbox, tok)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue // a malformed pattern expands to nothing, as before
		}
		// NOCASEGLOB. filepath.Glob is case-sensitive; bash under
		// `shopt -s nocaseglob` is not, so "cat Provi*.json" expanded to
		// nothing here and to the real providers.json in the shell — the
		// same B4 escape the literal scan had just been closed against,
		// reopened one metacharacter later, printing the operator's API
		// keys. The protected names are canonical-lower on disk, so the
		// lowered spelling is the one that reaches them. Only the TOKEN
		// is lowered: the sandbox prefix is a real path that may carry
		// uppercase, and lowering it would match nothing where the
		// filesystem is case-sensitive.
		if lower := strings.ToLower(tok); lower != tok {
			lp := lower
			if !rooted {
				lp = filepath.Join(r.sandbox, lower)
			}
			if lm, err := filepath.Glob(lp); err == nil {
				matches = append(matches, lm...)
			}
		}
		for _, m := range matches {
			if !rooted {
				if rel, err := filepath.Rel(r.sandbox, m); err == nil {
					m = rel
				}
			}
			out = append(out, m)
		}
	}
	return out
}

// globTokens cuts a command into the tokens a glob can hide in. Fields
// splits on whitespace ALONE, so a pattern glued to an operator —
// "cat provi*.json|head" — reached filepath.Glob with the pipe still
// attached, matched nothing, and walked past the substrate floor into
// the real file. Trim cannot fix that: it strips edges, and the glued
// token ends in "d". The separator set is the one the boundary scans
// already strip, for the same reason they strip it: these runes end a
// word in every shell dialect this tool speaks.
func globTokens(cmd string) []string {
	return strings.FieldsFunc(cmd, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("\"'`,;()|<>&", r)
	})
}

// (shQuote deleted with the injected-form expandGlobs — argv passing
// needs no shell quoting of untrusted input, which was the point.)

// Discover returns tool information at the given depth (R24).
// Depth 1: names only. Depth 2: name + description. Depth 3: full schema.
func (r *Registry) Discover(depth int) []ToolInfo {
	var infos []ToolInfo
	for _, name := range r.Names() {
		t, ok := r.lookup(name)
		if !ok {
			continue
		}
		info := ToolInfo{Name: name}
		if depth >= 2 {
			info.Description = t.Description()
		}
		infos = append(infos, info)
	}
	return infos
}

// Names returns all tool names, sorted.
// Names returns every tool the identity and operator may see, sorted.
// Host ops (RegisterHostOp) are deliberately absent: this is the one
// choke point the model's function list, the tools verb, and the
// operator's toggle list all derive from.
func (r *Registry) Names() []string {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if r.hostOnly[name] {
			continue // host plumbing: executable, never advertised
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ToolDefinitions returns tool definitions for the LLM function calling API.
func (r *Registry) ToolDefinitions() []interface{} {
	// Return as generic interface to avoid importing llm package
	names := r.Names()
	defs := make([]interface{}, 0, len(names))
	for _, name := range names {
		t, ok := r.lookup(name)
		if !ok {
			continue
		}
		defs = append(defs, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        name,
				"description": t.Description(),
				"parameters":  t.Parameters(),
			},
		})
	}
	return defs
}

// --- ReadTool ---
