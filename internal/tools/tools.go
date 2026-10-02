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

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]interface{}
	Execute(ctx context.Context, args map[string]interface{}) (Result, error)
}

type Result struct {
	Output    string
	Truncated bool
	Error     string

	ReasonCode string

	Refused bool
}

func Refusal(code, msg string) Result {
	return Result{Error: msg, ReasonCode: code, Refused: true}
}

const (
	ReasonPathRefused = "PATH_REFUSED"
	ReasonRootItself  = "ROOT_ITSELF"
	ReasonRootInside  = "ROOT_INSIDE"
	ReasonShellWall   = "SHELL_WALL"
)

const ReasonHostOnly = "HOST_ONLY"

type untrustedPluginError struct {
	cause  error
	source string
}

func (e *untrustedPluginError) Error() string { return untrusted.Wrap(e.source, e.cause.Error()) }
func (e *untrustedPluginError) Unwrap() error { return e.cause }

func (r Result) Text() string {
	text := r.Output
	if r.Error != "" {
		text = "Error: " + r.Error
	}
	if r.Truncated {
		return text + "\n[truncated]"
	}
	return text
}

type ReadOnlyTool interface {
	ReadOnly() bool
}

type ReplaySafeTool interface {
	ReplaySafe() bool
}

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

type ToolInfo struct {
	Name        string
	Description string
	Parameters  map[string]interface{}
}

type Registry struct {
	disMu      sync.RWMutex
	regMu      sync.RWMutex
	tools      map[string]Tool
	sources    map[string]string
	hostOnly   map[string]bool
	offered    map[string]bool
	offerOrder []string

	standing     []StandingSeat
	persistOffer func([]StandingSeat)
	told         map[string]bool
	persistMu    sync.Mutex
	webFetch     *WebFetchTool
	extraRoots   []string
	cwd          string
	policy       *firewall.Policy
	timeouts     Timeouts
	sandbox      string
	disabled     map[string]bool

	malformedCalls atomic.Uint64

	suspiciousPathCalls atomic.Uint64

	duplicateArgKeys atomic.Uint64

	safeSource func() (string, bool)

	observeMu     sync.RWMutex
	fetchObserver func(url string)
}

var safeContinues = map[string]bool{"read": true, "grep": true, "ls": true}

func (r *Registry) SetSafeSource(fn func() (string, bool)) { r.safeSource = fn }

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

func (r *Registry) SetToolEnabled(name string, enabled bool) {
	r.disMu.Lock()
	defer r.disMu.Unlock()
	r.disabled[name] = !enabled
}

func (r *Registry) ToolEnabled(name string) bool {
	r.disMu.RLock()
	defer r.disMu.RUnlock()
	return !r.disabled[name]
}

type ToolState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

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

type builtin struct {
	tool Tool
	here bool
}

func (r *Registry) builtins(canShell, canWrite bool) []builtin {
	return []builtin{
		{&ReadTool{maxBytes: 51200}, true},
		{&WriteTool{deny: func(p string) bool { return r.walkDenied("write", p) }}, canWrite},
		{&EditTool{}, canWrite},
		{&ShellTool{timeout: r.timeouts.shell(), sandbox: r.sandbox}, canShell},
		{&GrepTool{deny: func(p string) bool { return r.walkDenied("grep", p) }}, true},
		{&LsTool{deny: func(p string) bool { return r.walkDenied("ls", p) }}, true},

		{&WebFetchTool{maxBytes: 100000, timeout: r.timeouts.webFetch(), onFetch: r.NotifyFetch}, true},
	}
}

func (r *Registry) registerDefaults() {
	for _, b := range r.builtins(hostcap.Can(hostcap.Shell).Available, !platformNoWrite) {
		if !b.here {
			continue
		}
		r.Register(b.tool)
		if wf, ok := b.tool.(*WebFetchTool); ok {
			r.webFetch = wf
		}
	}
}

func AllBuiltins() []string {
	return builtinNames(new(Registry).builtins(false, false))
}

func builtinNames(table []builtin) []string {
	var names []string
	for _, b := range table {
		names = append(names, b.tool.Name())
	}
	sort.Strings(names)
	return names
}

func (r *Registry) Register(t Tool) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	if _, taken := r.tools[t.Name()]; taken {
		panic(fmt.Sprintf("tools: Register(%q): the name is already registered (origin %s) and a registration never replaces", t.Name(), r.sources[t.Name()]))
	}
	r.tools[t.Name()] = t
	r.sources[t.Name()] = "builtin"
}

func (r *Registry) Builtins() []string {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	var names []string
	for name, src := range r.sources {
		if src == "builtin" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (r *Registry) RegisterDynamic(t Tool, origin string) error {
	return r.register(t, origin, false)
}

func (r *Registry) register(t Tool, origin string, hostOnly bool) error {
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
	if hostOnly {
		if r.hostOnly == nil {
			r.hostOnly = map[string]bool{}
		}
		r.hostOnly[t.Name()] = true
	}
	r.reseatLocked(t.Name())
	return nil
}

func (r *Registry) RegisterHostOp(t Tool, origin string) error {
	return r.register(t, origin, true)
}

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

func (r *Registry) SupersedeOrigin(origin string, set []Tool, hostOnly map[string]bool) error {
	return r.SupersedeOriginIf(origin, set, hostOnly, nil)
}

var ErrSupersedeRefused = errors.New("supersede refused by its guard; the registry is unchanged")

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

			if r.offered[name] && !r.seatMatchesLocked(name) {
				r.unofferLocked(name)
			}
			r.reseatLocked(name)
		}
	}
	return nil
}

func (r *Registry) SetExtraRoots(roots []string) {
	sandbox, places := r.sandbox, r.policy.Places()
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

		if rootExposesSubstrate(abs, sandbox, places) {
			logsink.Warn("tools.refusal", "REFUSED grant %q — it would expose the identity's own substrate", abs)
			continue
		}
		resolved = append(resolved, abs)
	}
	r.regMu.Lock()
	r.extraRoots = resolved
	r.regMu.Unlock()
}

func (r *Registry) RootRejectionReason(path string) string {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		return "must be an absolute path"
	}
	abs := filepath.Clean(path)
	if rr, err := filepath.EvalSymlinks(abs); err == nil {
		abs = rr
	}
	if rootExposesSubstrate(abs, r.sandbox, r.policy.Places()) {
		return "would expose the identity's own substrate (ledger, keys, config, data, the running program, or home) — refused"
	}
	return ""
}

func rootExposesSubstrate(abs, sandbox string, places []string) bool {
	if abs == "/" || abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return true
	}
	if abs == sandbox || within(abs, sandbox) {
		return true
	}
	for _, p := range places {
		if within(abs, p) || within(p, abs) {
			return true
		}
	}
	return false
}

var ErrOutsideSandbox = errors.New("outside sandbox")

type SubstrateError struct{ Rule *firewall.Rule }

func (e *SubstrateError) Error() string {
	return fmt.Sprintf("protected path (%s: %s)", e.Rule.ID, e.Rule.Reason)
}

func (r *Registry) Admit(op, path string) (dir, rel string, err error) {
	abs := r.rooted(path)
	resolved := resolveForContainment(abs)
	if dir = r.holder(resolved); dir == "" {
		return "", "", ErrOutsideSandbox
	}
	if rule := r.substrateDenied(op, abs); rule != nil {
		return "", "", &SubstrateError{Rule: rule}
	}
	var inside bool
	if rel, inside = relativeWithin(dir, resolved); !inside {
		return "", "", ErrOutsideSandbox
	}
	return dir, rel, nil
}

func (r *Registry) rooted(path string) string {
	if isRooted(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(r.sandbox, path)
}

func (r *Registry) holder(resolved string) string {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	best := ""
	for _, root := range r.extraRoots {
		if within(root, resolved) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" && within(r.sandbox, resolved) {
		best = r.sandbox
	}
	return best
}

func (r *Registry) rootTakenBy(target string) string {
	t := resolveForContainment(target)
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	for _, root := range append([]string{r.sandbox}, r.extraRoots...) {
		if within(t, resolveForContainment(root)) {
			return root
		}
	}
	return ""
}

func (r *Registry) Roots() (string, []string) {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	return r.sandbox, append([]string(nil), r.extraRoots...)
}

func within(root, abs string) bool {
	_, inside := relativeWithin(root, abs)
	return inside
}

func relativeWithin(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return rel, true
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", false
	}
	rel = "."
	for dir := filepath.Clean(abs); ; dir = filepath.Dir(dir) {
		if other, err := os.Stat(dir); err == nil && os.SameFile(info, other) {
			return rel, true
		}
		if filepath.Dir(dir) == dir {
			return "", false
		}
		rel = filepath.Join(filepath.Base(dir), rel)
	}
}

func resolveForContainment(abs string) string {
	return firewall.ResolvePath(abs)
}

func (r *Registry) lookup(name string) (Tool, bool) {
	t, ok, _, _ := r.lookupWithHostOnly(name)
	return t, ok
}

func (r *Registry) lookupWithHostOnly(name string) (Tool, bool, bool, string) {
	r.regMu.RLock()
	defer r.regMu.RUnlock()
	t, ok := r.tools[name]
	return t, ok, r.hostOnly[name], r.sources[name]
}

func (r *Registry) inExtraRoot(path string) bool {
	h := r.holder(resolveForContainment(r.rooted(path)))
	return h != "" && h != r.sandbox
}

func (r *Registry) ObserveFetches(fn func(url string)) {
	r.observeMu.Lock()
	defer r.observeMu.Unlock()
	r.fetchObserver = fn
}

func (r *Registry) NotifyFetch(url string) {
	r.observeMu.RLock()
	fn := r.fetchObserver
	r.observeMu.RUnlock()
	if fn != nil {
		fn(url)
	}
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.lookup(name)
	return t, ok
}

type dispatchOwnerKey struct{}
type dispatchCheckKey struct{}

type DispatchTargetChangedError struct{}

func (*DispatchTargetChangedError) Error() string { return "dispatch target changed" }

const ReasonDispatchTargetChanged = "DISPATCH_TARGET_CHANGED"

type DispatchRefusedError struct {
	Code   string
	Detail string
}

func (e *DispatchRefusedError) Error() string { return e.Detail }

const ReasonArgumentsRequired = "ARGUMENTS_REQUIRED"

var ErrUnknownTool = errors.New("unknown tool")

type dispatchOwned interface{ DispatchOwner() any }

func WithDispatchOwner(ctx context.Context, owner any) context.Context {
	return context.WithValue(ctx, dispatchOwnerKey{}, owner)
}
func CheckDispatch(ctx context.Context) error {
	if f, ok := ctx.Value(dispatchCheckKey{}).(func() error); ok {
		return f()
	}
	return nil
}

func (r *Registry) Execute(ctx context.Context, name string, args map[string]interface{}) (res Result, err error) {
	return r.execute(ctx, name, args, false)
}

func (r *Registry) ExecuteModel(ctx context.Context, name string, args map[string]interface{}) (Result, error) {
	return r.execute(ctx, name, args, true)
}

func (r *Registry) execute(ctx context.Context, name string, args map[string]interface{}, model bool) (res Result, err error) {
	source := ""

	defer func() {
		if p := recover(); p != nil {
			logsink.Error("tools.error", "tool %s panicked on model-authored arguments: %v\n%s", name, p, debug.Stack())
			message := fmt.Sprintf("tool %s failed internally: %v", name, p)
			if model && source != "" && source != "builtin" {
				message = untrusted.Wrap("plugin "+source, message)
			}
			res, err = Result{Error: message}, nil
		}
	}()

	t, ok, hostOnly, source := r.lookupWithHostOnly(name)
	expected := ctx.Value(dispatchOwnerKey{})
	if expected != nil {
		owned, yes := t.(dispatchOwned)
		if !yes || owned.DispatchOwner() != expected {
			return Refusal("HOST_OWNER_CHANGED", "host dispatch owner changed"), nil
		}
	}

	ctx = context.WithValue(ctx, dispatchCheckKey{}, func() error {
		current, exists, _, _ := r.lookupWithHostOnly(name)
		if !exists || current != t {
			return &DispatchTargetChangedError{}
		}
		if !r.ToolEnabled(name) {
			return &DispatchRefusedError{Code: ReasonOperatorDisabled, Detail: "tool disabled by operator"}
		}
		if r.safeSource != nil && source != "" && source != "builtin" {
			if reason, safe := r.safeSource(); safe {
				return &DispatchRefusedError{Code: ReasonSafeSuspended, Detail: "SAFE: " + reason}
			}
		}
		return nil
	})
	if model && hostOnly {
		return Refusal(ReasonHostOnly, name+" is host-only; only its host ceremony may call it"), nil
	}
	if !r.ToolEnabled(name) {
		return Refusal(ReasonOperatorDisabled, "access denied: tool disabled by operator"), nil
	}

	if msg := validateRequired(t, name, args); msg != "" {
		r.malformedCalls.Add(1)
		return Refusal(ReasonArgumentsRequired, msg), nil
	}

	if r.safeSource != nil && ok && (source != "builtin" || !safeContinues[name]) {
		if reason, safe := r.safeSource(); safe {
			return Refusal(ReasonSafeSuspended, fmt.Sprintf("refused: I am in safe mode — %s. Only read-only tools continue while SAFE holds.", reason)), nil
		}
	}

	if name == "read" || name == "write" || name == "edit" {
		for _, key := range []string{"file_path", "to"} {
			path, ok := args[key].(string)
			if !ok || key == "to" && (name != "write" || path == "") {
				continue
			}

			resolved := path
			if !isRooted(resolved) {
				resolved = filepath.Join(r.sandbox, resolved)
				args[key] = resolved
			}
			_, rel, err := r.Admit(name, resolved)
			if err != nil {
				if key == "to" {
					return Refusal(ReasonPathRefused, "access denied: to "+err.Error()), nil
				}
				return Refusal(ReasonPathRefused, "access denied: "+err.Error()), nil
			}

			if action, _ := args["action"].(string); key == "file_path" && (action == "delete" || action == "move") {
				if rel == "." {
					return Refusal(ReasonRootItself, "access denied: "+resolved+" is the home or a granted root itself; what is inside it may be "+action+"d, not the root"), nil
				}
				if root := r.rootTakenBy(resolved); root != "" {
					return Refusal(ReasonRootInside, "access denied: "+resolved+" holds "+root+", the home or a granted root, which may not be "+action+"d with it; the operator removes that grant first"), nil
				}
			}
		}
	}

	if name == "shell" {
		if cmd, ok := args["command"].(string); ok {
			if why, outside := r.shellWall(cmd); why != "" {
				return Refusal(ReasonShellWall, "access denied: "+why+
					" (best-effort check; run under a container for a hard boundary)"+slashSearchHint(cmd, outside)), nil
			}
		}
	}

	if name == "grep" || name == "ls" {
		key := "path"

		if v, given := args[key]; !given || v == nil || v == "" {
			args[key] = r.sandbox
		}
		if path, ok := args[key].(string); ok {
			resolved := path
			if !isRooted(resolved) {
				resolved = filepath.Join(r.sandbox, resolved)
				args[key] = resolved
			}

			if _, _, err := r.Admit(name, resolved); err != nil {
				return Refusal(ReasonPathRefused, "access denied: "+err.Error()), nil
			}
		}
	}

	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}

	if name == "read" || name == "grep" || name == "ls" {
		key := "file_path"
		if name == "grep" || name == "ls" {
			key = "path"
		}
		if p, ok := args[key].(string); ok && p != "" {
			r.observePathSuspicion(name, p)
		}
	}
	result, runErr := t.Execute(ctx, args)
	if result.ReasonCode == ReasonArgumentsRequired && source == "builtin" {

		r.malformedCalls.Add(1)
	}
	if !model || source == "" || source == "builtin" {
		return result, runErr
	}
	label := "plugin " + source
	if runErr != nil {
		return result, &untrustedPluginError{cause: runErr, source: label}
	}
	if result.Output != "" {
		result.Output = untrusted.Wrap(label, result.Output)
	}
	if result.Error != "" {
		result.Error = untrusted.Wrap(label, result.Error)
	}
	return result, nil
}

func validateRequired(t Tool, name string, args map[string]interface{}) string {
	if t == nil {
		return ""
	}

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

func (r *Registry) MalformedCallCount() uint64 { return r.malformedCalls.Load() }

func (r *Registry) SuspiciousPathCount() uint64 { return r.suspiciousPathCalls.Load() }

func (r *Registry) observePathSuspicion(name, resolved string) {

	if _, err := os.Stat(resolved); err != nil {
		r.suspiciousPathCalls.Add(1)
	}
}

func (r *Registry) CountMalformed() { r.malformedCalls.Add(1) }

func (r *Registry) substrateDenied(tool, path string) *firewall.Rule {

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	resolved := resolveForContainment(abs)

	for _, form := range []string{resolved, abs} {
		if rule := r.policy.Covered(form); rule != nil {
			return rule
		}
	}

	if r.inExtraRoot(path) {
		return nil
	}

	for _, form := range []string{resolved, abs, path} {
		if v := r.policy.Check(tool, form); !v.Allowed {
			return v.Rule
		}
	}
	return nil
}

func (r *Registry) walkDenied(tool, path string) bool {
	if r.policy == nil {
		return false
	}
	if r.policy.Check(tool, path).Allowed {
		return false
	}
	if r.policy.Covered(path) != nil {
		return true
	}
	return !r.inExtraRoot(path)
}

func (r *Registry) isOutsideSandbox(path string) bool {
	return r.holder(resolveForContainment(r.rooted(path))) == ""
}

func (r *Registry) floorRefusal(tokens []shellToken, denied string) string {
	offender := ""
	for _, tok := range tokens {
		if tok.fragment {
			continue
		}

		if v := r.policy.Check("bash", tok.text); !v.Allowed && v.Rule.Pattern == denied {
			offender = tok.text
			break
		}
	}
	if offender == "" {

		return "something this command expands to reaches the protected name " + denied +
			" (the sandbox is " + r.sandbox + ")"
	}
	return offender + " reaches the protected name " + denied +
		"; the sandbox is " + r.sandbox +
		" (only an ABSOLUTE path inside a granted root is exempt from the name check;" +
		" a relative path is matched on the name alone)"
}

func (r *Registry) shellRefusal(cmd string) string {
	why, _ := r.shellWall(cmd)
	return why
}

func (r *Registry) shellWall(cmd string) (why, outside string) {
	if strings.Contains(cmd, "~") || strings.Contains(cmd, "$HOME") {
		return "the command references ~ or $HOME, which this check cannot resolve — write the path out in full (the sandbox is " + r.sandbox + ")", ""
	}

	tokens := r.shellTokens(cmd)

	var seen []string
	for _, tok := range tokens {
		if tok.fragment || tok.cut && !tok.name || r.inExtraRoot(tok.text) {
			continue
		}
		seen = append(seen, tok.text)
	}
	text := strings.Join(seen, " ")
	checks := []string{text}
	if strings.ContainsAny(text, "*?[") {
		checks = append(checks, r.expandGlobs(seen)...)
	}

	for _, c := range checks {
		if v := r.policy.Check("bash", c); !v.Allowed {
			return r.floorRefusal(tokens, v.Rule.Pattern), ""
		}
	}

	for _, tok := range tokens {
		if tok.fragment {
			continue
		}
		f := tok.text

		if invocationExempt(f) {
			continue
		}
		if r.isOutsideSandbox(f) {
			return f + " is outside the sandbox " + r.sandbox, f
		}
	}

	var refs, patterns []string
	for _, tok := range tokens {
		if tok.fragment {
			continue
		}
		refs = append(refs, tok.text)
		if !tok.cut || tok.name {
			patterns = append(patterns, tok.text)
		}
	}
	if strings.ContainsAny(cmd, "*?[") {
		refs = append(refs, r.expandGlobs(patterns)...)
	}
	for _, tok := range refs {
		abs := r.rooted(tok)
		for _, form := range []string{resolveForContainment(abs), abs} {
			if rule := r.policy.Covered(form); rule != nil {
				return tok + " is inside " + filepath.Base(rule.Path) + ", which the host keeps: " + rule.Reason, ""
			}
		}

		if !r.inExtraRoot(tok) {
			if v := r.policy.CheckFileIdentity("shell", abs); !v.Allowed {
				return tok + " reaches the protected name " + v.Rule.Pattern + "; the sandbox is " + r.sandbox, ""
			}
		}
	}
	return "", ""
}

var searchPrograms = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true}

func slashSearchHint(cmd, refused string) string {
	if !strings.HasPrefix(refused, "/") {
		return ""
	}
	for _, w := range shellWords(cmd) {
		if searchPrograms[strings.TrimSuffix(filepath.Base(w), ".exe")] {
			if w == refused {

				return ""
			}
			return fmt.Sprintf("\nif that is search text and not a path, the grep tool takes any pattern: grep pattern=%q path=\"…\"", refused)
		}
	}
	return ""
}

func (r *Registry) expandGlobs(tokens []string) []string {

	var out []string
	for _, tok := range tokens {
		if !strings.ContainsAny(tok, "*?[") {
			continue
		}
		pattern := tok
		rooted := isRooted(tok)
		if !rooted {
			pattern = filepath.Join(r.sandbox, tok)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		if lower := strings.ToLower(tok); lower != tok {
			lp := lower
			if rooted {

				meta := strings.IndexAny(tok, "*?[")
				prefix := strings.LastIndexAny(tok[:meta], `/\`) + 1
				lp = tok[:prefix] + strings.ToLower(tok[prefix:])
			}
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

func (r *Registry) ToolDefinitions() []wire.ToolDefinition {
	names := r.PromptNames()
	defs := make([]wire.ToolDefinition, 0, len(names))
	for _, name := range names {
		t, ok := r.lookup(name)
		if !ok {
			continue
		}
		params := t.Parameters()
		if params == nil {
			params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		defs = append(defs, wire.ToolDefinition{Type: "function", Function: wire.ToolFunction{
			Name:        name,
			Description: t.Description(),
			Parameters:  params,
		}})
	}
	return defs
}

func (r *Registry) SetLocalFetch(entries []string, refuse func(net.IP, int) bool) []string {
	scopes, rejected := firewall.LocalScopes(entries)
	if r.webFetch != nil {
		r.webFetch.local, r.webFetch.refuse = scopes, refuse
	}
	return rejected
}
