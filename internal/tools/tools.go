// .
// .
// .
// .
// .
// .
// .
// .
package tools

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"unicode"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// .
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, args map[string]interface{}) (Result, error)
}

// .
type Result struct {
	Output    string
	Truncated bool
	Error     string
	// .
	// .
	// .
	// .
	ReasonCode string
}

// .
func (r Result) Text() string {
	if r.Error != "" {
		return "Error: " + r.Error
	}
	if r.Truncated {
		return r.Output + "\n[truncated]"
	}
	return r.Output
}

// .
// .
// .
// .
// .
// .
type ReadOnlyTool interface {
	ReadOnly() bool
}

// .
// .
// .
// .
// .
// .
type ReplaySafeTool interface {
	ReplaySafe() bool
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) ReplaySafe(name string) bool {
	r.regMu.RLock()
	t, ok := r.tools[name]
	r.regMu.RUnlock()
	if !ok {
		return false
	}
	if ro, ok := t.(ReadOnlyTool); ok && ro.ReadOnly() {
		return true
	}
	rs, ok := t.(ReplaySafeTool)
	return ok && rs.ReplaySafe()
}

// .
// .
func (r *Registry) ParallelSafe(name string) bool {
	r.regMu.RLock()
	t, ok := r.tools[name]
	r.regMu.RUnlock()
	if !ok {
		return false
	}
	ro, ok := t.(ReadOnlyTool)
	return ok && ro.ReadOnly()
}

// .
type ToolInfo struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

// .
type Registry struct {
	disMu      sync.RWMutex
	regMu      sync.RWMutex
	tools      map[string]Tool
	sources    map[string]string
	hostOnly   map[string]bool
	offered    map[string]bool
	offerOrder []string
	// .
	// .
	// .
	// .
	// .
	// .
	standing     []StandingSeat
	persistOffer func([]StandingSeat)
	told         map[string]bool
	webFetch     *WebFetchTool
	extraRoots   []string
	protected    []string
	cwd          string
	policy       *firewall.Policy
	timeouts     Timeouts
	sandbox      string
	disabled     map[string]bool

	// .
	// .
	// .
	// .
	// .
	malformedCalls atomic.Uint64

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	suspiciousPathCalls atomic.Uint64

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	duplicateArgKeys atomic.Uint64

	// .
	// .
	// .
	// .
	safeSource func() (string, bool)

	// .
	// .
	// .
	// .
	// .
	// .
	observeMu     sync.RWMutex
	fetchObserver func(url string)
}

// .
// .
var safeBlockedTools = map[string]bool{
	"write": true, "edit": true, "shell": true, "web_fetch": true,
}

// .
func (r *Registry) SetSafeSource(fn func() (string, bool)) { r.safeSource = fn }

// .
// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) SetToolEnabled(name string, enabled bool) {
	r.disMu.Lock()
	defer r.disMu.Unlock()
	r.disabled[name] = !enabled
}

// .
// .
func (r *Registry) ToolEnabled(name string) bool {
	r.disMu.RLock()
	defer r.disMu.RUnlock()
	return !r.disabled[name]
}

// .
type ToolState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// .
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
		// .
		// .
		// .
		if r.policy == nil {
			return false
		}
		return !r.policy.Check("grep", p).Allowed
	}})
	r.Register(&LsTool{})
	// .
	// .
	// .
	r.webFetch = &WebFetchTool{maxBytes: 100000, timeout: r.timeouts.webFetch(), onFetch: r.NotifyFetch}
	r.Register(r.webFetch)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
func (r *Registry) Register(t Tool) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	r.tools[t.Name()] = t
	r.sources[t.Name()] = "builtin"
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
	r.reseatLocked(t.Name())
	return nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
	r.unofferLocked(t.Name())
	return nil
}

// .
// .
// .
// .
func (r *Registry) Deregister(name string) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	if src, ok := r.sources[name]; !ok || src == "builtin" {
		return
	}
	delete(r.tools, name)
	delete(r.sources, name)
	delete(r.hostOnly, name)
	r.unofferLocked(name)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) SupersedeOrigin(origin string, set []Tool, hostOnly map[string]bool) error {
	return r.SupersedeOriginIf(origin, set, hostOnly, nil)
}

// .
// .
var ErrSupersedeRefused = errors.New("supersede refused by its guard; the registry is unchanged")

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) SupersedeOriginIf(origin string, set []Tool, hostOnly map[string]bool, allowed func() bool) error {
	if origin == "" || origin == "builtin" {
		return fmt.Errorf("supersede requires a non-builtin origin, got %q", origin)
	}
	r.regMu.Lock()
	defer r.regMu.Unlock()
	if allowed != nil && !allowed() {
		return ErrSupersedeRefused
	}
	next := make(map[string]bool, len(set))
	for _, t := range set {
		name := t.Name()
		if src, taken := r.sources[name]; taken && src != origin {
			return fmt.Errorf("tool name %q is owned by %s; supersede never crosses origins", name, src)
		}
		next[name] = true
	}
	for name, src := range r.sources {
		if src == origin && !next[name] {
			delete(r.tools, name)
			delete(r.sources, name)
			delete(r.hostOnly, name)
			r.unofferLocked(name)
		}
	}
	for _, t := range set {
		name := t.Name()
		r.tools[name] = t
		r.sources[name] = origin
		if hostOnly[name] {
			if r.hostOnly == nil {
				r.hostOnly = map[string]bool{}
			}
			r.hostOnly[name] = true
			r.unofferLocked(name)
		} else {
			delete(r.hostOnly, name)
			// .
			// .
			// .
			if r.offered[name] && !r.seatMatchesLocked(name) {
				r.unofferLocked(name)
			}
			r.reseatLocked(name)
		}
	}
	return nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
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
		// .
		// .
		// .
		// .
		// .
		if rootExposesSubstrate(abs, sandbox, protected) {
			logsink.Warn("tools.refusal", "REFUSED grant %q — it would expose the identity's own substrate", abs)
			continue
		}
		resolved = append(resolved, abs)
	}
	r.regMu.Lock()
	r.extraRoots = resolved
	r.regMu.Unlock()
}

// .
// .
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

// .
// .
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

// .
// .
// .
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

// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
func resolveForContainment(abs string) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	rest := ""
	dir := filepath.Clean(abs)
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			// .
			// .
			return filepath.Join(dir, rest)
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
	}
}

// .
func (r *Registry) lookup(name string) (Tool, bool) {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) inExtraRoot(path string) bool {
	abs := path
	if !isRooted(abs) {
		abs = filepath.Join(r.sandbox, path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	// .
	// .
	// .
	// .
	// .
	// .
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	for _, root := range r.extraRoots {
		if within(root, abs) {
			return true
		}
	}
	return false
}

// .
// .
// .
func (r *Registry) registeredNonBuiltin(name string) bool {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	_, exists := r.tools[name]
	return exists && r.sources[name] != "builtin"
}

// .
// .
// .
// .
// .
// .
func (r *Registry) ObserveFetches(fn func(url string)) {
	r.observeMu.Lock()
	defer r.observeMu.Unlock()
	r.fetchObserver = fn
}

// .
// .
// .
func (r *Registry) NotifyFetch(url string) {
	r.observeMu.RLock()
	fn := r.fetchObserver
	r.observeMu.RUnlock()
	if fn != nil {
		fn(url)
	}
}

// .
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.lookup(name)
	return t, ok
}

// .
// .
// .
// .
func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) (res Result, err error) {
	// .
	// .
	// .
	// .
	// .
	// .
	defer func() {
		if p := recover(); p != nil {
			logsink.Error("tools.error", "tool %s panicked on model-authored arguments: %v\n%s", name, p, debug.Stack())
			res, err = Result{Error: fmt.Sprintf("tool %s failed internally: %v", name, p)}, nil
		}
	}()
	if !r.ToolEnabled(name) {
		return Result{Error: "access denied: tool disabled by operator"}, nil
	}

	// .
	// .
	// .
	// .
	// .
	// .
	if msg := r.validateRequired(name, args); msg != "" {
		r.malformedCalls.Add(1)
		return Result{Error: msg}, nil
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if r.safeSource != nil && (safeBlockedTools[name] || r.registeredNonBuiltin(name)) {
		if reason, safe := r.safeSource(); safe {
			return Result{Error: fmt.Sprintf("refused: I am in safe mode — %s. Only read-only tools continue until my operator restores the record.", reason)}, nil
		}
	}

	// .
	// .
	// .
	// .
	// .
	if name == "read" || name == "write" || name == "edit" {
		if path, ok := args["file_path"].(string); ok {
			// .
			// .
			// .
			resolved := path
			if !isRooted(resolved) {
				resolved = filepath.Join(r.sandbox, resolved)
				args["file_path"] = resolved
			}
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
			resolved := path
			if !isRooted(resolved) {
				resolved = filepath.Join(r.sandbox, resolved)
				args[key] = resolved
			}
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

	// .
	// .
	// .
	// .
	// .
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

// .
// .
// .
// .
func (r *Registry) validateRequired(name string, args map[string]interface{}) string {
	t, ok := r.lookup(name)
	if !ok {
		return ""
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var reqd []string
	switch v := t.Parameters()["required"].(type) {
	case nil:
		return ""
	case []string:
		reqd = v
	case []interface{}:
		for i, e := range v {
			k, ok := e.(string)
			if !ok || k == "" {
				return fmt.Sprintf("malformed tool schema: required[%d] is not a non-empty string for %s", i, name)
			}
			reqd = append(reqd, k)
		}
	default:
		return fmt.Sprintf("malformed tool schema: required is %T, want an array of strings, for %s", v, name)
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

// .
// .
// .
func (r *Registry) MalformedCallCount() uint64 { return r.malformedCalls.Load() }

// .
// .
// .
// .
func (r *Registry) SuspiciousPathCount() uint64 { return r.suspiciousPathCalls.Load() }

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) observePathSuspicion(name, resolved string) {
	// .
	// .
	if _, err := os.Stat(resolved); err != nil {
		r.suspiciousPathCalls.Add(1)
	}
}

// .
// .
// .
func (r *Registry) CountMalformed() { r.malformedCalls.Add(1) }

// .
// .
// .
// .
// .
// .
func (r *Registry) substrateDenied(tool, path string) *firewall.Rule {
	// .
	// .
	if r.inExtraRoot(path) {
		return nil
	}
	// .
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	// .
	// .
	// .
	resolved := resolveForContainment(abs)

	// .
	for _, form := range []string{resolved, abs, path} {
		if v := r.policy.Check(tool, form); !v.Allowed {
			r.policy.Record(tool, path, v.Rule)
			return v.Rule
		}
	}
	return nil
}

// .
// .
// .
// .
func (r *Registry) isOutsideSandbox(path string) bool {
	var abs string
	if isRooted(path) {
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) floorRefusal(cmd, denied string) string {
	offender := ""
	for _, field := range strings.Fields(cmd) {
		f := strings.Trim(field, "\"'`,;()|<>")
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if v := r.policy.Check("bash", f); !v.Allowed && v.Rule.Pattern == denied {
			offender = f
			break
		}
	}
	if offender == "" {
		// .
		// .
		return "something this command expands to reaches the protected name " + denied +
			" (the sandbox is " + r.sandbox + ")"
	}
	return offender + " reaches the protected name " + denied +
		"; the sandbox is " + r.sandbox +
		" (only an ABSOLUTE path inside a granted root is exempt from the name check;" +
		" a relative path is matched on the name alone)"
}

// .
// .
// .
func (r *Registry) shellCommandEscapes(cmd string) bool { return r.shellRefusal(cmd) != "" }

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) shellRefusal(cmd string) string {
	if strings.Contains(cmd, "~") || strings.Contains(cmd, "$HOME") {
		return "the command references ~ or $HOME, which this check cannot resolve — write the path out in full (the sandbox is " + r.sandbox + ")"
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	scrubbed := cmd
	for _, field := range strings.Fields(cmd) {
		f := strings.Trim(field, "\"'`,;()|<>")
		// .
		// .
		// .
		// .
		// .
		if f != "" && r.inExtraRoot(f) {
			scrubbed = strings.ReplaceAll(scrubbed, f, "")
		}
	}
	checks := []string{scrubbed}
	if strings.ContainsAny(scrubbed, "*?[") {
		checks = append(checks, r.expandGlobs(scrubbed)...)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	for _, c := range checks {
		if v := r.policy.Check("bash", c); !v.Allowed {
			return r.floorRefusal(cmd, v.Rule.Pattern)
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	for _, field := range strings.Fields(cmd) {
		f := strings.Trim(field, "\"'`,;()|<>")
		if f == "" {
			continue
		}
		// .
		// .
		// .
		// .
		// .
		// .
		clean := filepath.Clean(f)
		if filepath.IsAbs(clean) && (strings.HasPrefix(clean, "/usr/") || strings.HasPrefix(clean, "/bin/") ||
			clean == "/usr" || clean == "/bin" || clean == "/lib" || clean == "/etc/alternatives" ||
			clean == "/dev/null" || clean == "/dev/zero" || clean == "/dev/stdin" || clean == "/dev/stdout" || clean == "/dev/stderr") {
			continue
		}
		if r.isOutsideSandbox(f) {
			return f + " is outside the sandbox " + r.sandbox
		}
	}
	return ""
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (r *Registry) expandGlobs(cmd string) []string {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var out []string
	for _, tok := range globTokens(cmd) {
		pattern := tok
		rooted := isRooted(tok)
		if !rooted {
			pattern = filepath.Join(r.sandbox, tok)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
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

// .
// .
// .
// .
// .
// .
// .
// .
func globTokens(cmd string) []string {
	return strings.FieldsFunc(cmd, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("\"'`,;()|<>&", r)
	})
}

// .
// .

// .
// .
func (r *Registry) Discover(depth int) []ToolInfo {
	var infos []ToolInfo
	for _, name := range r.PromptNames() {
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

// .
// .
// .
// .
// .
func (r *Registry) Names() []string {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if r.hostOnly[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// .
func (r *Registry) ToolDefinitions() []interface{} {
	// .
	// .
	// .
	names := r.PromptNames()
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

// .

// .
// .
// .
// .
func (r *Registry) SetLocalFetch(entries []string, refuse func(net.IP, int) bool) []string {
	scopes, rejected := LocalScopes(entries)
	if r.webFetch != nil {
		r.webFetch.local, r.webFetch.refuse = scopes, refuse
	}
	return rejected
}
