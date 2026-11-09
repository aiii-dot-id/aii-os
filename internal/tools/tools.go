package tools

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/hold"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"syscall"

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

	AnsweredRefused bool
}

func (r Result) Failed() bool {
	return r.Error != "" || r.Interrupted || r.AnsweredRefused || r.ExitStatus != nil && *r.ExitStatus != 0
}

func Refusal(code, msg string) Result {
	return Result{Error: msg, ReasonCode: code, Refused: true}
}

const (
	ReasonNotFound        = "NOT_FOUND"
	ReasonPermission      = "PERMISSION_DENIED"
	ReasonNotADirectory   = "NOT_A_DIRECTORY"
	ReasonIsADirectory    = "IS_A_DIRECTORY"
	ReasonNotAFile        = "NOT_A_FILE"
	ReasonCancelled       = "CANCELLED"
	ReasonIO              = "IO_ERROR"
	ReasonArgumentInvalid = "ARGUMENT_INVALID"
	ReasonEditNoMatch     = "EDIT_NO_MATCH"
	ReasonEditNoChange    = "EDIT_NO_CHANGE"
	ReasonEditNotUnique   = "EDIT_NOT_UNIQUE"
)

func failedOn(err error) Result {
	return Result{Error: err.Error(), ReasonCode: osReason(err)}
}

func failed(code, msg string) Result { return Result{Error: msg, ReasonCode: code} }

func osReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ReasonNotFound
	case errors.Is(err, fs.ErrPermission):
		return ReasonPermission
	case errors.Is(err, syscall.ENOTDIR):
		return ReasonNotADirectory
	case errors.Is(err, syscall.EISDIR):
		return ReasonIsADirectory
	case errors.Is(err, errNotRegular):
		return ReasonNotAFile
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ReasonCancelled
	}
	return ReasonIO
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
	webFetch   *WebFetchTool
	shell      *ShellTool
	extraRoots []string
	cwd        string
	policy     *firewall.Policy
	timeouts   Timeouts
	sandbox    string
	afterAdmit func()
	afterOpen  func()
	disabled   map[string]bool

	malformedCalls atomic.Uint64

	suspiciousPathCalls atomic.Uint64

	duplicateArgKeys atomic.Uint64

	shellWallOuter func() ([]*firewall.Rule, error)

	shellWallSettings func() ShellWallSettings

	shellUTF8 func() bool

	webFetchCeiling func() int

	safeSource func() (hold.Hold, bool)

	observeMu     sync.RWMutex
	fetchObserver func(url string)
}

var safeContinues = []string{"read", "grep", "ls"}

func continuesInSafe(name, source string) bool {
	return source == "builtin" && slices.Contains(safeContinues, name)
}

func SafeContinues() []string { return slices.Clone(safeContinues) }

func (r *Registry) SetSafeSource(fn func() (string, bool)) {
	if fn == nil {
		r.safeSource = nil
		return
	}
	r.safeSource = func() (hold.Hold, bool) { reason, safe := fn(); return hold.Safe(reason), safe }
}

func (r *Registry) SetHoldSource(fn func() (hold.Hold, bool)) { r.safeSource = fn }

func (r *Registry) SetShellWallPlaces(fn func() ([]*firewall.Rule, error)) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	r.shellWallOuter = fn
}

func (r *Registry) SetShellWallSettings(fn func() ShellWallSettings) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	r.shellWallSettings = fn
}

func (r *Registry) SetShellWindowsUTF8(fn func() bool) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	r.shellUTF8 = fn
}

func (r *Registry) ShellWindowsUTF8() bool {
	r.regMu.RLock()
	fn := r.shellUTF8
	r.regMu.RUnlock()
	return fn == nil || fn()
}

func (r *Registry) SetWebFetchMaxBytes(fn func() int) {
	r.regMu.Lock()
	defer r.regMu.Unlock()
	r.webFetchCeiling = fn
}

func (r *Registry) webFetchMaxBytes() int {
	r.regMu.RLock()
	fn := r.webFetchCeiling
	r.regMu.RUnlock()
	if fn == nil {
		return 0
	}
	return fn()
}

func (r *Registry) shellWallData() (shellWall, error) {
	r.regMu.RLock()
	roots := append([]string(nil), r.extraRoots...)
	outer := r.shellWallOuter
	settings := r.shellWallSettings
	r.regMu.RUnlock()
	kept := r.policy.PlaceRules()
	if outer != nil {
		more, err := outer()
		if err != nil {
			return shellWall{}, err
		}
		kept = append(kept, more...)
	}
	w := shellWall{kept: dedupePlaces(kept), roots: roots, boundary: r.policy.Boundary()}
	if settings != nil {
		w.settings = settings()
	}
	return w, nil
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

func (r *Registry) inSafe() bool {
	if r.safeSource == nil {
		return false
	}
	_, safe := r.safeSource()
	return safe
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

	Plugin string `json:"plugin,omitempty"`
}

func (r *Registry) ToolStates() []ToolState {
	names := r.Names()
	out := make([]ToolState, 0, len(names))
	for _, name := range names {
		t, ok, _, origin := r.lookupWithHostOnly(name)
		if !ok {
			continue
		}
		state := ToolState{Name: name, Description: t.Description(), Enabled: r.ToolEnabled(name)}
		if origin != "builtin" {
			state.Plugin = origin
		}
		out = append(out, state)
	}
	return out
}

type builtin struct {
	tool Tool
	here bool
}

func (r *Registry) builtins(canShell, canWrite bool) []builtin {
	return []builtin{
		{&ReadTool{held: r.heldRead}, true},
		{&WriteTool{deny: func(ctx context.Context, p string) bool { return r.walkDenied(ctx, "write", p) }, kept: r.keptAsMade, held: r.heldAt}, canWrite},
		{&EditTool{held: r.heldAt, read: r.heldRead}, canWrite},
		{&ShellTool{timeout: r.timeouts.shell(), sandbox: r.sandbox, wall: r.shellWallData, windowsUTF8: r.ShellWindowsUTF8}, canShell},
		{&GrepTool{deny: func(ctx context.Context, p string) bool { return r.walkDenied(ctx, "grep", p) }}, true},
		{&LsTool{deny: func(ctx context.Context, p string) bool { return r.walkDenied(ctx, "ls", p) }}, true},

		{&WebFetchTool{maxBytes: r.webFetchMaxBytes, timeout: r.timeouts.webFetch(), onFetch: r.NotifyFetch}, true},
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
		if st, ok := b.tool.(*ShellTool); ok {
			r.shell = st
		}
	}
}

func (r *Registry) EndLeftJobs(wait time.Duration) (int, error) {
	if r.shell == nil {
		return 0, nil
	}

	defer r.shell.endWall()
	return r.shell.left.end(wait)
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
		} else {
			delete(r.hostOnly, name)
		}
	}
	return nil
}

func (r *Registry) SetExtraRoots(roots []string) {
	resolved, refused := r.readExtraRoots(roots)
	for _, abs := range refused {
		logsink.Warn("tools.refusal", "REFUSED grant %q — it would expose the identity's own substrate", abs)
	}
	r.regMu.Lock()
	was := r.extraRoots
	r.extraRoots = resolved
	r.regMu.Unlock()
	for _, root := range was {
		if !slices.Contains(resolved, root) {
			releaseGrant(root, append([]string{r.sandbox}, resolved...))
		}
	}
}

func (r *Registry) ExtraRootsMoved(roots []string) bool {
	resolved, _ := r.readExtraRoots(roots)
	_, held := r.Roots()
	return !slices.Equal(resolved, held)
}

func (r *Registry) readExtraRoots(roots []string) (resolved, refused []string) {
	sandbox, places := r.sandbox, r.policy.Places()
	resolved = make([]string, 0, len(roots))
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
			refused = append(refused, abs)
			continue
		}
		resolved = append(resolved, abs)
	}
	return resolved, refused
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
		return "would expose the identity's own substrate (ledger, keys, config, data, logs, the running program, or home) — refused"
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
	if e.Rule.Kind == firewall.KindUnplaced {
		return e.Rule.Reason
	}
	return fmt.Sprintf("protected path (%s: %s)", e.Rule.ID, e.Rule.Reason)
}

func refused(rule *firewall.Rule, err error) error {
	if err != nil {
		return err
	}
	if rule != nil {
		return &SubstrateError{Rule: rule}
	}
	return nil
}

func (r *Registry) Admit(op, path string) (dir, rel string, err error) {
	return r.admitAs(r.policy, op, path)
}

func (r *Registry) admitAs(policy *firewall.Policy, op, path string) (dir, rel string, err error) {
	dir, rel, err = r.admit(policy, op, path)
	return dir, rel, r.asTold(err)
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

func (r *Registry) asTold(err error) error {
	unchecked, ok := err.(*firewall.UncheckedError)
	if !ok {
		return err
	}
	told := *unchecked
	told.Reason = r.said(unchecked)
	return &told
}

func (r *Registry) Held(file os.FileInfo, by *os.File) error {
	return r.heldBy(r.policy, "", file, by)
}

func (r *Registry) heldBy(policy *firewall.Policy, tool string, file os.FileInfo, by *os.File) error {
	return r.asTold(refused(policy.KeptAsFor(tool, file, by)))
}

func (r *Registry) heldAt(ctx context.Context, path string, file os.FileInfo, by *os.File) error {
	return r.heldAs(ctx, "write", path, file, by)
}

func (r *Registry) heldAs(ctx context.Context, tool, path string, file os.FileInfo, by *os.File) error {
	policy := r.wallOf(ctx)
	if err := r.heldBy(policy, tool, file, by); err != nil {
		return err
	}
	if tool != "read" {
		if err := r.heldWhere(ctx, tool, by); err != nil {
			return err
		}
	}
	return r.asTold(r.substrateDenied(policy, tool, path))
}

func (r *Registry) heldRead(ctx context.Context, path string, file os.FileInfo, by *os.File) error {
	if err := r.heldWhere(ctx, "read", by); err != nil {
		return err
	}
	if firewall.Names(file, by) < 2 {
		return nil
	}
	return r.heldAs(ctx, "read", path, file, by)
}

func (r *Registry) heldWhere(ctx context.Context, op string, by *os.File) error {
	if by == nil {
		return nil
	}
	at, err := openedAt(by)
	if err != nil {
		return fmt.Errorf("where the opened file lies could not be asked (%v), so it is not read or written", err)
	}

	held, err := by.Stat()
	if err != nil {
		return err
	}
	if there, err := os.Stat(at); err != nil || !os.SameFile(there, held) {
		return fmt.Errorf("the opened file is no longer at %s, where the system says it lies, so it is not read or written", at)
	}
	_, _, err = r.admitOwn(r.wallOf(ctx), op, at)
	return err
}

type undoWall struct{}

type callLook struct{}

func (r *Registry) lookFor(t Tool) *firewall.Policy {
	if ro, ok := t.(interface{ ReadOnly() bool }); ok && ro.ReadOnly() {
		return r.policy.SteadyLook()
	}
	return r.policy.Look()
}

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
	return r.asTold(r.substrateDenied(r.wallOf(ctx), "write", path))
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
	if err := r.substrateDenied(policy, op, abs); err != nil {
		return "", "", err
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

func (r *Registry) undoTargets(unchecked *firewall.UncheckedError) []string {
	var links []string
	for _, s := range unchecked.Stops {
		if t := r.undoTarget(s); t != "" && !slices.Contains(links, t) {
			links = append(links, t)
		}
	}
	return links
}

func (r *Registry) undo(policy *firewall.Policy, unchecked *firewall.UncheckedError, path string) (link, rel string, wall *firewall.Policy, ok bool) {
	at, err := os.Lstat(r.rooted(path))
	if err != nil || at.Mode()&os.ModeSymlink == 0 {
		return "", "", nil, false
	}
	targets := r.undoTargets(unchecked)
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

func (r *Registry) said(unchecked *firewall.UncheckedError) string {
	text := unchecked.Reason
	var told []string
	tell := func(clause string) {
		if !slices.Contains(told, clause) {
			told = append(told, clause)
			text += clause
		}
	}
	for _, s := range unchecked.Stops {
		if target := r.undoTarget(s); target != "" {
			if _, _, _, ok := r.undo(r.policy, unchecked, target); ok {
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
	if err := r.substrateDenied(policy, op, abs); err != nil {
		return "", "", r.asTold(err)
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

const ReasonUnknownArgument = "UNKNOWN_ARGUMENT"

const unknownArgumentWords = "%s does not take %s; nothing ran. It takes: %s."

func UnknownArguments(schema, args map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	var unknown []string
	for k := range args {
		if _, ok := props[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func UnknownArgumentRefusal(tool string, unknown []string, schema map[string]any) Result {
	props, _ := schema["properties"].(map[string]any)
	takes := slices.Sorted(maps.Keys(props))
	return Refusal(ReasonUnknownArgument, fmt.Sprintf(unknownArgumentWords, tool, strings.Join(unknown, ", "), strings.Join(takes, ", ")))
}

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
	contextRead, _ := ctx.Value(contextSelectionKey{}).(bool)
	if contextRead && !r.contextEligible(name) {
		return Refusal("CONTEXT_NOT_SELECTED", "plugin context is not currently selected"), nil
	}

	ctx = context.WithValue(ctx, dispatchCheckKey{}, func() error {
		current, exists, _, _ := r.lookupWithHostOnly(name)
		if !exists || current != t {
			return &DispatchTargetChangedError{}
		}
		if contextRead && !r.contextEligible(name) {
			return &DispatchRefusedError{Code: "CONTEXT_NOT_SELECTED", Detail: "plugin context selection changed"}
		}
		if !r.ToolEnabled(name) {
			return &DispatchRefusedError{Code: ReasonOperatorDisabled, Detail: "tool disabled by operator"}
		}
		if r.safeSource != nil && source != "" && source != "builtin" {
			if h, safe := r.safeSource(); safe {
				return &DispatchRefusedError{Code: ReasonSafeSuspended, Detail: h.Name() + ": " + h.Reason}
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
		if !contextRead {
			r.malformedCalls.Add(1)
		}
		return Refusal(ReasonArgumentsRequired, msg), nil
	}

	if model && t != nil && source == "builtin" {
		if unknown := UnknownArguments(t.Parameters(), args); len(unknown) > 0 {
			r.malformedCalls.Add(1)
			return UnknownArgumentRefusal(name, unknown, t.Parameters()), nil
		}
	}

	if r.safeSource != nil && ok && !continuesInSafe(name, source) {
		if h, safe := r.safeSource(); safe {
			if h.Waiting {
				return Refusal(ReasonSafeSuspended, fmt.Sprintf("refused: this start is DEGRADED — %s. Only read-only tools continue %s.", h.Reason, hold.While)), nil
			}
			return Refusal(ReasonSafeSuspended, fmt.Sprintf("refused: I am in safe mode — %s. Only read-only tools continue while SAFE holds.", h.Reason)), nil
		}
	}

	look := r.lookFor(t)
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

			if unchecked, ok := err.(*firewall.UncheckedError); ok &&
				name == "write" && key == "file_path" && args["action"] == "delete" && source == "builtin" {
				if link, undone, wall, ok := r.undo(look, unchecked, resolved); ok {
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
		if r.afterAdmit != nil {
			r.afterAdmit()
		}
	}

	if name == "shell" {

		base, refused := r.shellBase(look, args)
		if refused != nil {
			return *refused, nil
		}
		if cmd, ok := args["command"].(string); ok {
			if why, outside, literal := r.shellWallWord(look, base, cmd); why != "" {

				hint := literalWordHint(literal)
				if literal == "" {
					hint = slashSearchHint(cmd, outside) + cdHint(cmd, outside, base) + notAPathHint(cmd, outside)
				}
				return Refusal(ReasonShellWall, "access denied: "+why+
					" (best-effort check; run under a container for a hard boundary)"+hint), nil
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

func (r *Registry) substrateDenied(policy *firewall.Policy, tool, path string) error {

	policy = policy.Look()

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}

	resolved := resolveForContainment(abs)

	for _, form := range distinct(resolved, abs) {
		if err := refused(policy.CoveredFor(tool, form)); err != nil {
			return err
		}
	}

	if r.inExtraRoot(path) {
		return nil
	}

	for _, form := range distinct(resolved, abs, path) {
		if v := policy.Check(tool, form); !v.Allowed {
			return refused(v.Rule, v.Err)
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
	if rule, err := policy.CoveredFor(tool, path); rule != nil || err != nil {
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
			if v := policy.Check("bash", tok.text); !v.Allowed && v.Rule != nil && v.Rule.Pattern == denied {
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

type globbed struct {
	text, glob string

	literal bool
}

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

const ownLogWord = "log"

var shellWordCommands = map[string]bool{
	"git": true, "go": true, "docker": true, "kubectl": true,
	"npm": true, "cargo": true, "systemctl": true, "brew": true,
}

var gitValueOptions = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
	"--exec-path": true, "--super-prefix": true, "--config-env": true, "--attr-source": true,
}

func gitSubcommandIndex(ws []string) int {
	for i := 1; i < len(ws); i++ {
		if !strings.HasPrefix(ws[i], "-") {
			return i
		}
		if gitValueOptions[ws[i]] {
			i++
		}
	}
	return -1
}

func shellPathlessWords(cmd string, tokens []shellToken) map[int]bool {
	out := make(map[int]bool)

	if strings.ContainsAny(cmd, ";|&`\n<>") || strings.Contains(cmd, "$(") {
		return out
	}

	var word string
	for _, v := range shellVersions(cmd) {
		ws := shellWords(cmd, v)
		if len(ws) < 2 || !shellWordCommands[ws[0]] {
			return out
		}
		sub := 1
		if ws[0] == "git" {

			if sub = gitSubcommandIndex(ws); sub < 0 {
				return out
			}
		}
		if word != "" && ws[sub] != word {
			return out
		}
		word = ws[sub]

		n := 0
		for _, w := range ws {
			if w == word {
				n++
			}
		}
		if n != 1 {
			return out
		}
	}
	if word == "" ||
		strings.ContainsAny(word, "/\\") ||
		strings.Contains(word, "..") ||
		strings.ContainsAny(word, "*?[") ||
		strings.HasPrefix(word, "-") {
		return out
	}
	for i, tok := range tokens {
		if tok.text == word && !tok.cut && !tok.fragment && !tok.literal && !tok.code {
			out[i] = true
		}
	}
	return out
}

var shellPatternCommands = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "zgrep": true, "rg": true, "ag": true, "ack": true,
}

var shellPatternSelfArgs = map[string]bool{
	"--include": true, "--exclude": true, "--exclude-dir": true, "--color": true, "--colour": true,
}

const shellPatternFlags = "cvniwxlLhHrRsqoEFGPzZaIUbUuyk"

func shellPatternWords(cmd string, tokens []shellToken) map[int]bool {
	out := make(map[int]bool)
	if strings.ContainsAny(cmd, ";&`\n<>") || strings.Contains(cmd, "$(") || strings.Contains(cmd, "||") {
		return out
	}
	var patterns []string
	for _, v := range shellVersions(cmd) {
		var found []string
		var all []string
		for _, seg := range strings.Split(cmd, "|") {

			if strings.Contains(seg, "''") || strings.Contains(seg, `""`) {
				return out
			}
			ws := shellWords(strings.TrimSpace(seg), v)
			if len(ws) == 0 {
				return out
			}
			all = append(all, ws...)
			if !shellPatternCommands[filepath.Base(ws[0])] {
				continue
			}
			pattern, after := "", len(ws)
			for i := 1; i < len(ws); i++ {
				w := ws[i]
				if w == "--" {
					if i+1 < len(ws) {
						pattern = ws[i+1]
					}
					break
				}
				if strings.HasPrefix(w, "--") {

					if name, _, has := strings.Cut(w, "="); has && shellPatternSelfArgs[name] {
						continue
					}
					return out
				}
				if strings.HasPrefix(w, "-") {
					for _, c := range w[1:] {
						if !strings.ContainsRune(shellPatternFlags, c) {
							return out
						}
					}
					continue
				}
				pattern, after = w, i+1
				break
			}
			if pattern == "" || strings.HasPrefix(pattern, "-") || strings.Contains(pattern, "..") {
				continue
			}

			for j := after; j < len(ws); j++ {
				w := ws[j]
				if w == "--" {
					break
				}
				if strings.HasPrefix(w, "--") {
					if name, _, has := strings.Cut(w, "="); has && shellPatternSelfArgs[name] {
						continue
					}
					pattern = ""
					break
				}
				if strings.HasPrefix(w, "-") {
					for _, c := range w[1:] {
						if !strings.ContainsRune(shellPatternFlags, c) {
							pattern = ""
						}
					}
				}
			}
			if pattern == "" {
				return out
			}
			found = append(found, pattern)
		}
		for _, p := range found {
			n := 0
			for _, w := range all {
				if w == p {
					n++
				}
			}
			if n != 1 {
				return out
			}
		}
		if patterns == nil {
			patterns = found
		} else if !slices.Equal(patterns, found) {
			return out
		}
	}
	for i, tok := range tokens {
		if slices.Contains(patterns, tok.text) && !tok.cut && !tok.fragment && !tok.literal && !tok.code {
			out[i] = true
		}
	}
	return out
}

func placeSaid(said, text string, rule *firewall.Rule) string {
	base := filepath.Base(rule.Path)
	if text == base {

		if !rule.Stem {
			if fi, err := os.Stat(rule.Path); err == nil {
				if fi.IsDir() {
					return said + " names the directory " + base + "/, which the host keeps: " + rule.Reason
				}
				if fi.Mode().IsRegular() {
					return said + " names the file " + base + ", which the host keeps: " + rule.Reason
				}
			}
		}
		return said + " names " + base + ", a place the host keeps: " + rule.Reason
	}
	return said + " is inside " + base + ", which the host keeps: " + rule.Reason
}

func ownLogCommand(cmd string) bool {
	if strings.ContainsAny(cmd, ";&|<>()`$\\\n") {
		return false
	}
	words := strings.Fields(cmd)
	return len(words) >= 2 && words[0] == "aii" && words[1] == ownLogWord
}

func (r *Registry) shellWallFrom(policy *firewall.Policy, base, cmd string) (why, outside string) {
	why, outside, _ = r.shellWallWord(policy, base, cmd)
	return why, outside
}

func (r *Registry) shellWallWord(policy *firewall.Policy, base, cmd string) (why, outside, literal string) {
	if referencesHome(cmd) {
		return "the command references ~ or $HOME, which this check cannot resolve — write the path out in full (the sandbox is " + r.sandbox + ")", "", ""
	}

	tokens := r.shellTokens(base, cmd)
	if ownLogCommand(cmd) {
		tokens = slices.DeleteFunc(tokens, func(t shellToken) bool { return t.text == ownLogWord })
	}

	if shellWallKeepsPlaces() {
		tokens = slices.DeleteFunc(tokens, func(t shellToken) bool { return t.code })
	}
	off := r.SandboxOff()

	for _, tok := range tokens {
		if rule := firewall.Unplaced(from(base, tok.text)); rule != nil {
			return rule.Reason, "", literalWord(tok.text, tok.literal)
		}
	}

	var checks []globbed
	for _, tok := range tokens {
		if tok.fragment || tok.cut && !tok.name || r.inExtraRoot(from(base, tok.text)) {
			continue
		}
		checks = append(checks, globbed{text: tok.text, literal: tok.literal})
		if !tok.literal {
			for _, m := range r.expandGlobsFrom(base, []string{tok.text}) {
				checks = append(checks, globbed{text: m, glob: tok.text})
			}
		}
	}

	for _, c := range checks {
		if v := policy.Check("bash", c.text); !v.Allowed {
			if v.Err != nil {
				return r.asTold(v.Err).Error(), "", ""
			}

			if v.Rule.Path != "" {
				return placeSaid(c.said(), c.text, v.Rule), "", literalWord(c.text, c.literal)
			}
			return r.floorRefusal(policy, tokens, c, v.Rule.Pattern, base), "", literalWord(c.text, c.literal)
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
			return f + " is outside the sandbox " + r.sandbox + readFrom(r.sandbox, base), f, literalWord(f, tok.literal)
		}

		if !isRooted(f) && escapeShaped(f) {
			return f + " leaves " + base + " by a relative path: a path outside your home and granted roots is written in full", f, literalWord(f, tok.literal)
		}
	}

	var refs []globbed
	pathless := shellPathlessWords(cmd, tokens)
	for i := range shellPatternWords(cmd, tokens) {
		pathless[i] = true
	}
	for i, tok := range tokens {
		if pathless[i] || tok.fragment {
			continue
		}
		refs = append(refs, globbed{text: tok.text, literal: tok.literal})

		if !tok.literal && (!tok.cut || tok.name || off && isRooted(tok.text)) {
			for _, m := range r.expandGlobsFrom(base, []string{tok.text}) {
				refs = append(refs, globbed{text: m, glob: tok.text})
			}
		}
	}
	for _, ref := range refs {
		tok := ref.text
		abs := from(base, tok)
		for _, form := range []string{resolveForContainment(abs), abs} {
			rule, err := policy.Covered(form)
			if err != nil {
				return r.asTold(err).Error(), "", ""
			}
			if rule != nil {
				return placeSaid(ref.said(), ref.text, rule), "", literalWord(ref.text, ref.literal)
			}
		}

		if !r.inExtraRoot(abs) {
			if v := policy.CheckFileIdentity("shell", abs); !v.Allowed {
				return ref.said() + " reaches the protected name " + v.Rule.Pattern + "; the sandbox is " + r.sandbox, "", literalWord(ref.text, ref.literal)
			}
		}

		if off && !invocationExempt(tok) && r.holdsSubstrate(abs) {

			if ref.literal {
				return ref.said() + " " + holdsSubstrateWhy, tok, tok
			}
			return ref.said() + " " + holdsSubstrateWhy + ", and this check cannot see what a command does inside a folder: name a folder that does neither, or use read, grep or ls, which ask about every file they reach", tok, ""
		}
	}
	return "", "", ""
}

var shellWallKeepsPlaces = wallKeepsPlaces

func literalWord(word string, literal bool) string {
	if !literal {
		return ""
	}
	return word
}

func literalWordHint(word string) string {
	switch {
	case word == "":
		return ""
	case isRooted(word):
		return literalWordWords
	}
	return literalWordRelativeWords
}

const (
	literalWordWords         = "\nThat word is inside an inline program: write it another way, or save the program as a file and run that."
	literalWordRelativeWords = "\nThat word is inside an inline program: write it another way, give the call a cwd under your work folder, or save the program as a file and run that."
)

const holdsSubstrateWhy = "holds your home or a place the host keeps (or lies in one)"

var searchPrograms = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true}

func slashSearchHint(cmd, refused string) string {
	return searchHintFor(wordsHolding(cmd, refused), refused, spelledRoot(refused))
}

func searchHintFor(words []string, refused string, spelled bool) string {
	if !strings.HasPrefix(refused, "/") && !spelled {
		return ""
	}
	for _, w := range words {
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
	if slashSearchHint(cmd, refused) != "" || !textHintOffered(shellWordsQuoted(cmd, refused), refused, spelledRoot(refused)) {
		return ""
	}
	return "\nif that is text and not a path (a division, a string in a program, a JSON body), this check cannot tell: put the program or the text in a file with the write tool and name the file in the command"
}

func textHintOffered(words []shellWord, refused string, spelled bool) bool {
	if !strings.HasPrefix(refused, "/") && !spelled {
		return false
	}
	if strings.Trim(refused, "/") == "" {
		return true
	}
	for _, w := range words {
		if w.text == refused && !(spelled && w.quoted) {
			return false
		}
	}
	return true
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
	builtins, _ := r.PromptNames(0)
	for _, name := range builtins {
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

func (r *Registry) ToolDefinitions(room int) []wire.ToolDefinition {
	return r.ToolDefinitionsIn(room, r.inSafe())
}

func (r *Registry) ToolDefinitionsIn(room int, safe bool) []wire.ToolDefinition {
	names, _ := r.PromptNamesIn(room, safe)
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

		if m, ok := t.(ModelSchemer); ok {
			if model := m.ModelParameters(); model != nil {
				params = model
			}
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
