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
	"slices"
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
	Parameters() map[string]any
	Execute(ctx context.Context, args map[string]any) (Result, error)
}

type ResultImage struct {
	Path      string
	MediaType string

	Width, Height int
}

type Result struct {
	Output string

	Image     *ResultImage
	Truncated bool
	Error     string

	ReasonCode string

	Refused bool

	Interrupted bool

	ExitStatus *int
}

func (r Result) Failed() bool { return r.Error != "" || r.Interrupted }

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
	Parameters  map[string]any
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

	shellWallOuter func() ([]*firewall.Rule, error)

	safeSource func() (string, bool)

	observeMu     sync.RWMutex
	fetchObserver func(url string)
}

var safeContinues = map[string]bool{"read": true, "grep": true, "ls": true}

func (r *Registry) SetSafeSource(fn func() (string, bool)) { r.safeSource = fn }

func (r *Registry) SetShellWallPlaces(fn func() ([]*firewall.Rule, error)) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	r.shellWallOuter = fn
}

func (r *Registry) shellWallData() (shellWall, error) {
	r.regMu.RLock()
	roots := append([]string(nil), r.extraRoots...)
	outer := r.shellWallOuter
	r.regMu.RUnlock()
	kept := r.policy.PlaceRules()
	if outer != nil {
		more, err := outer()
		if err != nil {
			return shellWall{}, err
		}
		kept = append(kept, more...)
	}
	return shellWall{kept: dedupePlaces(kept), roots: roots, boundary: r.policy.Boundary()}, nil
}

func dedupePlaces(rules []*firewall.Rule) []*firewall.Rule {
	seen := map[string]bool{}
	out := make([]*firewall.Rule, 0, len(rules))
	for _, r := range rules {
		if r == nil || r.Path == "" {
			continue
		}
		key := firewall.ResolvePath(r.Path)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

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

	if _, err := os.Stat(abs); err == nil {
		abs = firewall.ResolvePath(abs)
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
		{&ReadTool{maxBytes: 51200, held: r.heldRead}, true},
		{&WriteTool{deny: func(ctx context.Context, p string) bool { return r.walkDenied(ctx, "write", p) }, kept: r.keptAsMade, held: r.heldAt}, canWrite},
		{&EditTool{held: r.heldAt, read: r.heldRead}, canWrite},
		{&ShellTool{timeout: r.timeouts.shell(), sandbox: r.sandbox, wall: r.shellWallData}, canShell},
		{&GrepTool{deny: func(ctx context.Context, p string) bool { return r.walkDenied(ctx, "grep", p) }}, true},
		{&LsTool{deny: func(ctx context.Context, p string) bool { return r.walkDenied(ctx, "ls", p) }}, true},

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
		if _, err := os.Stat(abs); err == nil {
			abs = firewall.ResolvePath(abs)
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
	if _, err := os.Stat(abs); err == nil {
		abs = firewall.ResolvePath(abs)
	}

	places := r.policy.Places()
	if _, granted := r.Roots(); !r.policy.Boundary() && !slices.Contains(granted, abs) {
		places = r.policy.Kept()
	}
	if rule := firewall.Unplaced(abs); rule != nil {
		return rule.Reason + " — refused"
	}
	if rootExposesSubstrate(abs, r.sandbox, places) {
		return "would expose the identity's own substrate (ledger, keys, config, data, the running program, or home) — refused"
	}
	return ""
}

func rootExposesSubstrate(abs, sandbox string, places []string) bool {
	if abs == "/" || abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return true
	}
	if firewall.Unplaced(abs) != nil {
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

func (r *Registry) SetSandboxOff(off bool) { r.policy.SetBoundary(!off) }

func (r *Registry) SandboxOff() bool {
	if r.policy.Boundary() {
		return false
	}
	if r.safeSource != nil {
		if _, safe := r.safeSource(); safe {
			return false
		}
	}
	return true
}

var ErrOutsideSandbox = errors.New("outside sandbox")

type SubstrateError struct{ Rule *firewall.Rule }

func (e *SubstrateError) Error() string {
	if e.Rule.Kind == firewall.KindUnasked || e.Rule.Kind == firewall.KindUnplaced {
		return e.Rule.Reason
	}
	return fmt.Sprintf("protected path (%s: %s)", e.Rule.ID, e.Rule.Reason)
}

func (r *Registry) Admit(op, path string) (dir, rel string, err error) {
	return r.admitAs(r.policy, op, path)
}

func (r *Registry) admitAs(policy *firewall.Policy, op, path string) (dir, rel string, err error) {
	dir, rel, err = r.admit(policy, op, path)
	if denied, ok := err.(*SubstrateError); ok {
		denied.Rule = r.asTold(denied.Rule)
	}
	return dir, rel, err
}

type Gate interface {
	Admit(op, path string) (dir, rel string, err error)
	Held(file os.FileInfo, by *os.File) error
}

func (r *Registry) Look() Gate {
	return lookGate{r: r, policy: r.policy.Look()}
}

type lookGate struct {
	r      *Registry
	policy *firewall.Policy
}

func (g lookGate) Admit(op, path string) (dir, rel string, err error) {
	return g.r.admitAs(g.policy, op, path)
}

func (g lookGate) Held(file os.FileInfo, by *os.File) error {
	return g.r.heldBy(g.policy, "", file, by)
}

func (r *Registry) asTold(rule *firewall.Rule) *firewall.Rule {
	if rule.Kind != firewall.KindUnasked {
		return rule
	}
	told := *rule
	told.Reason = r.said(rule)
	return &told
}

func (r *Registry) Held(file os.FileInfo, by *os.File) error {
	return r.heldBy(r.policy, "", file, by)
}

func (r *Registry) heldBy(policy *firewall.Policy, tool string, file os.FileInfo, by *os.File) error {
	if rule := policy.KeptAsFor(tool, file, by); rule != nil {
		return &SubstrateError{Rule: r.asTold(rule)}
	}
	return nil
}

func (r *Registry) heldAt(ctx context.Context, path string, file os.FileInfo, by *os.File) error {
	return r.heldAs(ctx, "write", path, file, by)
}

func (r *Registry) heldAs(ctx context.Context, tool, path string, file os.FileInfo, by *os.File) error {
	policy := r.wallOf(ctx)
	if err := r.heldBy(policy, tool, file, by); err != nil {
		return err
	}
	if rule := r.substrateDenied(policy, tool, path); rule != nil {
		return &SubstrateError{Rule: r.asTold(rule)}
	}
	return nil
}

func (r *Registry) heldRead(ctx context.Context, path string, file os.FileInfo, by *os.File) error {
	if firewall.Names(file, by) < 2 {
		return nil
	}
	return r.heldAs(ctx, "read", path, file, by)
}

type undoWall struct{}

type callLook struct{}

func (r *Registry) wallOf(ctx context.Context) *firewall.Policy {
	if wall, ok := ctx.Value(undoWall{}).(*firewall.Policy); ok && wall != nil {
		return wall
	}
	if wall, ok := ctx.Value(callLook{}).(*firewall.Policy); ok && wall != nil {
		return wall
	}
	return r.policy
}

func (r *Registry) keptAsMade(ctx context.Context, path string) error {
	if rule := r.substrateDenied(r.wallOf(ctx), "write", path); rule != nil {
		return &SubstrateError{Rule: r.asTold(rule)}
	}
	return nil
}

func (r *Registry) admit(policy *firewall.Policy, op, path string) (dir, rel string, err error) {
	abs := r.rooted(path)

	if rule := firewall.Unplaced(abs); rule != nil {
		return "", "", &SubstrateError{Rule: rule}
	}
	resolved := resolveForContainment(abs)
	if dir = r.holder(resolved); dir == "" {
		return "", "", ErrOutsideSandbox
	}
	if rule := r.substrateDenied(policy, op, abs); rule != nil {
		return "", "", &SubstrateError{Rule: rule}
	}
	var inside bool
	if rel, inside = relativeWithin(dir, resolved); !inside {
		return "", "", ErrOutsideSandbox
	}
	return dir, rel, nil
}

func (r *Registry) undoTarget(s firewall.Stop) string {
	inHome := func(path string) bool {
		rel, inside := relativeWithin(r.sandbox, path)
		return inside && rel != "."
	}
	if s.Link {
		if inHome(s.Path) {
			return s.Path
		}
		return ""
	}
	for at := filepath.Dir(s.Path); inHome(at); at = filepath.Dir(at) {
		if info, err := os.Lstat(at); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return at
		}
	}
	return ""
}

func (r *Registry) undoTargets(rule *firewall.Rule) []string {
	var links []string
	for _, s := range rule.Stops {
		if t := r.undoTarget(s); t != "" && !slices.Contains(links, t) {
			links = append(links, t)
		}
	}
	return links
}

func (r *Registry) undo(policy *firewall.Policy, rule *firewall.Rule, path string) (link, rel string, wall *firewall.Policy, ok bool) {
	at, err := os.Lstat(r.rooted(path))
	if err != nil || at.Mode()&os.ModeSymlink == 0 {
		return "", "", nil, false
	}
	targets := r.undoTargets(rule)
	for _, t := range targets {
		if l, err := os.Lstat(t); err == nil && os.SameFile(at, l) {
			link = t
			break
		}
	}
	if link == "" {
		return "", "", nil, false
	}
	all := policy.Without(targets...)
	var deletable []string
	for _, t := range targets {
		if _, _, err := r.admit(all, "write", t); err == nil {
			deletable = append(deletable, t)
		}
	}
	if !slices.Contains(deletable, link) {
		return "", "", nil, false
	}
	wall = policy.Without(deletable...)
	_, rel, err = r.admit(wall, "write", link)
	return link, rel, wall, err == nil
}

func (r *Registry) said(rule *firewall.Rule) string {
	text := rule.Reason
	var told []string
	tell := func(clause string) {
		if !slices.Contains(told, clause) {
			told = append(told, clause)
			text += clause
		}
	}
	for _, s := range rule.Stops {
		if target := r.undoTarget(s); target != "" {
			if _, _, _, ok := r.undo(r.policy, rule, target); ok {
				rel, _ := relativeWithin(r.sandbox, target)
				tell("; " + rel + ", in your home, is a link that leads where the host cannot look: deleting it clears this")
				continue
			}
		}
		if rel, inside := relativeWithin(r.sandbox, s.Path); inside && rel != "." {
			tell("; " + rel + ", in your home, is where the host cannot look")
		}
	}
	return text
}

func (r *Registry) admitOwn(policy *firewall.Policy, op, path string) (dir, rel string, err error) {
	dir, rel, err = r.admitAs(policy, op, path)
	if !errors.Is(err, ErrOutsideSandbox) || !r.SandboxOff() {
		return dir, rel, err
	}
	abs := r.rooted(path)
	if rule := r.substrateDenied(policy, op, abs); rule != nil {
		return "", "", &SubstrateError{Rule: r.asTold(rule)}
	}
	resolved := resolveForContainment(abs)
	return filepath.Dir(resolved), filepath.Base(resolved), nil
}

func (r *Registry) holdsSubstrate(abs string) bool {
	resolved := resolveForContainment(abs)
	return r.holder(resolved) == "" && rootExposesSubstrate(resolved, r.sandbox, r.policy.Kept())
}

func (r *Registry) rooted(path string) string { return from(r.sandbox, path) }

func from(base, path string) string {
	if isRooted(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(base, path)
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

func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (res Result, err error) {
	return r.execute(ctx, name, args, false)
}

func (r *Registry) ExecuteModel(ctx context.Context, name string, args map[string]any) (Result, error) {
	return r.execute(ctx, name, args, true)
}

func (r *Registry) execute(ctx context.Context, name string, args map[string]any, model bool) (res Result, err error) {
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

	look := r.policy.Look()
	ctx = context.WithValue(ctx, callLook{}, look)

	if name == "read" || name == "write" || name == "edit" {
		for _, key := range []string{"file_path", "to"} {
			path, ok := args[key].(string)
			if !ok || key == "to" && (name != "write" || path == "") {
				continue
			}

			resolved := r.rooted(path)
			args[key] = resolved
			_, rel, err := r.admitOwn(look, name, resolved)

			if denied, ok := err.(*SubstrateError); ok && denied.Rule.Kind == firewall.KindUnasked &&
				name == "write" && key == "file_path" && args["action"] == "delete" && source == "builtin" {
				if link, undone, wall, ok := r.undo(look, denied.Rule, resolved); ok {
					resolved, args[key] = link, link
					rel, err = undone, nil

					ctx = context.WithValue(ctx, undoWall{}, wall)
				}
			}
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

		base, refused := r.shellBase(look, args)
		if refused != nil {
			return *refused, nil
		}
		if cmd, ok := args["command"].(string); ok {
			if why, outside := r.shellWallFrom(look, base, cmd); why != "" {
				return Refusal(ReasonShellWall, "access denied: "+why+
					" (best-effort check; run under a container for a hard boundary)"+
					slashSearchHint(cmd, outside)+cdHint(cmd, outside, base)+notAPathHint(cmd, outside)), nil
			}
		}
	}

	if name == "grep" || name == "ls" {
		key := "path"

		if v, given := args[key]; !given || v == nil || v == "" {
			args[key] = r.sandbox
		}
		if path, ok := args[key].(string); ok {
			resolved := r.rooted(path)
			args[key] = resolved

			if _, _, err := r.admitOwn(look, name, resolved); err != nil {
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

func validateRequired(t Tool, name string, args map[string]any) string {
	if t == nil {
		return ""
	}

	var reqd []string
	switch v := t.Parameters()["required"].(type) {
	case nil:
		return ""
	case []string:
		reqd = v
	case []any:
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

func (r *Registry) substrateDenied(policy *firewall.Policy, tool, path string) *firewall.Rule {

	policy = policy.Look()

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	resolved := resolveForContainment(abs)

	for _, form := range distinct(resolved, abs) {
		if rule := policy.CoveredFor(tool, form); rule != nil {
			return rule
		}
	}

	if r.inExtraRoot(path) {
		return nil
	}

	for _, form := range distinct(resolved, abs, path) {
		if v := policy.Check(tool, form); !v.Allowed {
			return v.Rule
		}
	}
	return nil
}

func distinct(forms ...string) []string {
	var out []string
	for _, f := range forms {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

func (r *Registry) walkDenied(ctx context.Context, tool, path string) bool {
	if r.policy == nil {
		return false
	}
	policy := r.wallOf(ctx)
	if policy.Check(tool, path).Allowed {
		return false
	}
	if policy.CoveredFor(tool, path) != nil {
		return true
	}
	return !r.inExtraRoot(path)
}

func (r *Registry) isOutsideSandbox(path string) bool {
	return r.holder(resolveForContainment(r.rooted(path))) == ""
}

func (r *Registry) floorRefusal(policy *firewall.Policy, tokens []shellToken, c globbed, denied, base string) string {
	if c.glob == "" {

		for _, tok := range tokens {
			if tok.fragment {
				continue
			}
			if v := policy.Check("bash", tok.text); !v.Allowed && v.Rule.Pattern == denied {
				c.text = tok.text
				break
			}
		}
		return c.text + " reaches the protected name " + denied + "; the sandbox is " + r.sandbox +
			" (only an ABSOLUTE path inside a granted root is exempt from the name check;" +
			" a relative path is matched on the name alone)"
	}
	why := c.said() + " reaches the protected name " + denied + "; the sandbox is " + r.sandbox
	where := "there"
	if base != r.sandbox {
		where = base
	}
	return why + " (a glob is expanded from " + where + " as the shell expands it, in either case;" +
		" if it is text in a program and not a path, this check cannot tell: give the program to its" +
		" interpreter in a here-document with a quoted delimiter, python3 - <<'EOF', which the shell" +
		" does not expand, or in a file the command names)"
}

type globbed struct{ text, glob string }

func (g globbed) said() string {
	if g.glob == "" {
		return g.text
	}
	return g.glob + " expands to " + g.text + ", which"
}

func (r *Registry) shellRefusal(cmd string) string {
	why, _ := r.shellWall(cmd)
	return why
}

func (r *Registry) shellWall(cmd string) (why, outside string) {
	return r.shellWallFrom(r.policy.Look(), r.sandbox, cmd)
}

func (r *Registry) shellBase(policy *firewall.Policy, args map[string]any) (string, *Result) {
	v, given := args["cwd"]
	if !given || v == nil || v == "" {
		delete(args, "cwd")
		return r.sandbox, nil
	}
	refuse := func(code, msg string) (string, *Result) {
		res := Refusal(code, msg)
		return "", &res
	}
	cwd, ok := v.(string)
	if !ok {
		return refuse(ReasonArgumentsRequired, "cwd must be a string: the directory to run in")
	}
	dir, rel, err := r.admitOwn(policy, "shell", cwd)
	if err != nil {
		return refuse(ReasonPathRefused, "access denied: cwd "+cwd+": "+err.Error())
	}
	resolved := filepath.Join(dir, rel)

	if r.holdsSubstrate(resolved) {
		return refuse(ReasonPathRefused, "access denied: cwd "+cwd+" "+holdsSubstrateWhy+": the shell starts in a folder that does neither")
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() {
		return refuse(ReasonPathRefused, "access denied: cwd "+cwd+" is not a directory: the shell starts in a directory that exists (the sandbox is "+r.sandbox+")")
	}
	args["cwd"] = resolved
	return resolved, nil
}

func referencesHome(cmd string) bool {
	if strings.Contains(cmd, "$HOME") {
		return true
	}
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '~' {
			continue
		}
		if i == 0 {
			return true
		}
		switch p := cmd[i-1]; {
		case p >= 'a' && p <= 'z', p >= 'A' && p <= 'Z', p >= '0' && p <= '9',
			p == '^', p == '@', p == '_', p == '.', p == '-':
		default:
			return true
		}
	}
	return false
}

func (r *Registry) shellWallFrom(policy *firewall.Policy, base, cmd string) (why, outside string) {
	if referencesHome(cmd) {
		return "the command references ~ or $HOME, which this check cannot resolve — write the path out in full (the sandbox is " + r.sandbox + ")", ""
	}

	tokens := r.shellTokens(base, cmd)
	off := r.SandboxOff()

	for _, tok := range tokens {
		if rule := firewall.Unplaced(from(base, tok.text)); rule != nil {
			return rule.Reason, ""
		}
	}

	var checks []globbed
	for _, tok := range tokens {
		if tok.fragment || tok.cut && !tok.name || r.inExtraRoot(from(base, tok.text)) {
			continue
		}
		checks = append(checks, globbed{text: tok.text})
		if !tok.literal {
			for _, m := range r.expandGlobsFrom(base, []string{tok.text}) {
				checks = append(checks, globbed{m, tok.text})
			}
		}
	}

	for _, c := range checks {
		if v := policy.Check("bash", c.text); !v.Allowed {
			if v.Rule.Kind == firewall.KindUnasked {
				return r.said(v.Rule), ""
			}

			if v.Rule.Path != "" {
				return c.said() + " is inside " + filepath.Base(v.Rule.Path) + ", which the host keeps: " + v.Rule.Reason, ""
			}
			return r.floorRefusal(policy, tokens, c, v.Rule.Pattern, base), ""
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
		if !r.isOutsideSandbox(from(base, f)) {
			continue
		}
		if !off {
			return f + " is outside the sandbox " + r.sandbox + readFrom(r.sandbox, base), f
		}

		if !isRooted(f) && escapeShaped(f) {
			return f + " leaves " + base + " by a relative path: a path outside your home and granted roots is written in full", f
		}
	}

	var refs []globbed
	for _, tok := range tokens {
		if tok.fragment {
			continue
		}
		refs = append(refs, globbed{text: tok.text})

		if !tok.literal && (!tok.cut || tok.name || off && isRooted(tok.text)) {
			for _, m := range r.expandGlobsFrom(base, []string{tok.text}) {
				refs = append(refs, globbed{m, tok.text})
			}
		}
	}
	for _, ref := range refs {
		tok := ref.text
		abs := from(base, tok)
		for _, form := range []string{resolveForContainment(abs), abs} {
			if rule := policy.Covered(form); rule != nil {
				if rule.Kind == firewall.KindUnasked {
					return r.said(rule), ""
				}
				return ref.said() + " is inside " + filepath.Base(rule.Path) + ", which the host keeps: " + rule.Reason, ""
			}
		}

		if !r.inExtraRoot(abs) {
			if v := policy.CheckFileIdentity("shell", abs); !v.Allowed {
				return ref.said() + " reaches the protected name " + v.Rule.Pattern + "; the sandbox is " + r.sandbox, ""
			}
		}

		if off && !invocationExempt(tok) && r.holdsSubstrate(abs) {
			return ref.said() + " " + holdsSubstrateWhy + ", and this check cannot see what a command does inside a folder: name a folder that does neither, or use read, grep or ls, which ask about every file they reach", tok
		}
	}
	return "", ""
}

const holdsSubstrateWhy = "holds your home or a place the host keeps (or lies in one)"

var searchPrograms = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true}

func slashSearchHint(cmd, refused string) string {
	if !strings.HasPrefix(refused, "/") {
		return ""
	}
	for _, w := range wordsHolding(cmd, refused) {
		if searchPrograms[strings.TrimSuffix(filepath.Base(w), ".exe")] {
			if w == refused {

				return ""
			}
			return fmt.Sprintf("\nif that is search text and not a path, the grep tool takes any pattern: grep pattern=%q path=\"…\"", refused)
		}
	}
	return ""
}

func readFrom(home, base string) string {
	if base == home {
		return ""
	}
	return " (relative paths are read from the cwd you gave, " + base + ")"
}

var cdWords = map[string]bool{"cd": true, "pushd": true, "chdir": true, "set-location": true, "push-location": true, "sl": true}

func cdHint(cmd, refused, base string) string {
	if refused == "" || isRooted(refused) {
		return ""
	}
	for _, w := range wordsHolding(cmd, refused) {
		if w == refused {
			return ""
		}
		if cdWords[strings.ToLower(w)] {
			return "\nrelative paths are read from " + base + ", not from where a cd inside the command goes: give the shell cwd=<that directory> and write the path from there, or write the path in full"
		}
	}
	return ""
}

func notAPathHint(cmd, refused string) string {
	if !strings.HasPrefix(refused, "/") || slashSearchHint(cmd, refused) != "" {
		return ""
	}
	if strings.Trim(refused, "/") != "" {
		if slices.Contains(wordsHolding(cmd, refused), refused) {
			return ""
		}
	}
	return "\nif that is text and not a path (a division, a string in a program, a JSON body), this check cannot tell: put the program or the text in a file with the write tool and name the file in the command"
}

func (r *Registry) expandGlobs(tokens []string) []string {
	return r.expandGlobsFrom(r.sandbox, tokens)
}

func (r *Registry) expandGlobsFrom(base string, tokens []string) []string {

	var out []string
	for _, tok := range tokens {
		if !strings.ContainsAny(tok, "*?[") {
			continue
		}
		pattern := tok
		rooted := isRooted(tok)
		if !rooted {
			pattern = filepath.Join(base, tok)
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
				lp = filepath.Join(base, lower)
			}
			if lm, err := filepath.Glob(lp); err == nil {
				matches = append(matches, lm...)
			}
		}
		for _, m := range matches {
			if !rooted {
				if rel, err := filepath.Rel(base, m); err == nil {
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
			params = map[string]any{"type": "object", "properties": map[string]any{}}
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
