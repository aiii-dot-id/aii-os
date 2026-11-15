package tools

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/hold"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, args map[string]any) (Result, error)
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

var ErrOutsideSandbox = errors.New("outside sandbox")

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

var ErrUnknownTool = errors.New("unknown tool")

var shellWordCommands = map[string]bool{
	"git": true, "go": true, "docker": true, "kubectl": true,
	"npm": true, "cargo": true, "systemctl": true, "brew": true,
}

var gitValueOptions = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true,
	"--exec-path": true, "--super-prefix": true, "--config-env": true, "--attr-source": true,
}

var shellPatternCommands = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "zgrep": true, "rg": true, "ag": true, "ack": true,
}

var shellPatternSelfArgs = map[string]bool{
	"--include": true, "--exclude": true, "--exclude-dir": true, "--color": true, "--colour": true,
}

var shellWallKeepsPlaces = wallKeepsPlaces

var searchPrograms = map[string]bool{"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true}

var cdWords = map[string]bool{"cd": true, "pushd": true, "chdir": true, "set-location": true, "push-location": true, "sl": true}

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
