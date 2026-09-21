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
package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/facility"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// .
// .
const (
	// .
	// .
	// .
	// .
	ModeInProcess = "in-process"
	// .
	// .
	// .
	// .
	ModeSupervised = "supervised"
)

// .
// .
// .
// .
// .
// .
// .
const harnessRequestID = "h1"

// .
// .
// .
// .
const maxToolNameBytes = 64

// .
// .
// .
// .
// .
type invoker interface {
	Invoke(ctx context.Context, frame []byte) ([]byte, error)
}

// .
// .
// .
// .
// .
type ActivePlugin struct {
	// .
	// .
	// .
	inFlight opGate
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
	teardown sync.Mutex
	// .
	ID string
	// .
	Version string
	// .
	// .
	// .
	// .
	// .
	Publisher   string
	PublisherID string
	Family      string
	// .
	// .
	// .
	// .
	// .
	Title        string
	Description  string
	Interfaces   []string
	Capabilities []string
	Runtime      string
	PackageHash  string
	// .
	// .
	// .
	Settings []SettingDecl
	// .
	// .
	// .
	Webhooks []WebhookDecl
	// .
	// .
	// .
	Subscriptions []SubscriptionDecl
	// .
	// .
	// .
	Accelerator *AcceleratorProfile
	Readiness   *Readiness
	// .
	// .
	Startup *StartupAllowance
	// .
	// .
	Models    []ModelDecl
	ModelsDir string
	// .
	// .
	// .
	RuntimeRoot  string
	runtimeRoots *RuntimeRoots
	// .
	// .
	Tier packagefmt.Tier
	// .
	Mode string
	// .
	// .
	VariantID string
	// .
	// .
	// .
	Channel *Channel
	// .
	// .
	// .
	Backends []string
	// .
	ToolNames []string

	// .
	// .
	// .
	// .
	// .
	pending []pendingTool

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	superseded atomic.Bool

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	admitted atomic.Bool

	// .
	// .
	// .
	authorized atomic.Pointer[func() bool]

	// .
	// .
	// .
	// .
	// .
	acts ActProposer

	// .
	// .
	// .
	inv       invoker
	envelope  []string
	pubMu     sync.Mutex
	published map[string]string

	module      *pluginworker.Module
	sup         *supervisor.Supervisor
	artifactDir string
	reg         *tools.Registry
	binding     *broker.Binding

	// .
	// .
	// .
	// .
	Voice         *VoiceSession
	sessionCancel context.CancelFunc
	// .
	// .
	// .
	Contained bool
}

// .
// .
type Options struct {
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
	Roots packagefmt.TrustRoots
	// .
	// .
	// .
	// .
	// .
	Broker *broker.Host
	// .
	// .
	// .
	// .
	Facilities *facility.Set
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	WorkerBinary string
	// .
	// .
	// .
	// .
	WorkerArgs []string
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	MemoryMax map[string]uint64
	// .
	// .
	// .
	// .
	ReadyTimeout map[string]time.Duration
	// .
	// .
	// .
	StartupCeiling time.Duration
	// .
	// .
	// .
	StreamMax map[string]int
	// .
	// .
	// .
	PluginDataDir string
	// .
	// .
	// .
	FilesMax map[string]int
	// .
	// .
	// .
	// .
	PluginModelsDir string
	ModelFetcher    ModelFetcher
	// .
	// .
	// .
	// .
	// .
	// .
	PluginRuntimeDir string
	RuntimeFetcher   ModelFetcher
	RuntimeLimits    packagefmt.TreeLimits
	RuntimeRoots     *RuntimeRoots
	RuntimeRootsKept int
	// .
	// .
	// .
	HostVersion string
	// .
	// .
	// .
	// .
	// .
	// .
	Acquirer *Acquirer
	// .
	// .
	Log *log.Logger
	// .
	// .
	// .
	// .
	Settings func(pluginID string) map[string]interface{}
	// .
	// .
	// .
	// .
	Acts ActProposer
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
type pendingTool struct {
	tool   *operationTool
	hostOp bool
}

// .
// .
// .
func Activate(ctx context.Context, pkgPath string, reg *tools.Registry, opts *Options) (*ActivePlugin, error) {
	return activatePackage(ctx, pkgPath, reg, opts, true)
}

// .
// .
// .
// .
// .
// .
func ActivateShadow(ctx context.Context, pkgPath string, reg *tools.Registry, opts *Options) (*ActivePlugin, error) {
	return activatePackage(ctx, pkgPath, reg, opts, false)
}

func activatePackage(ctx context.Context, pkgPath string, reg *tools.Registry, opts *Options, registerNow bool) (*ActivePlugin, error) {
	st, err := Stage(ctx, pkgPath, opts)
	if err != nil {
		return nil, err
	}
	return st.Start(ctx, reg, registerNow)
}

// .
// .
// .
// .
// .
func (s *Staged) Start(ctx context.Context, reg *tools.Registry, registerNow bool) (*ActivePlugin, error) {
	pkgPath, opts, res, sel := s.pkgPath, s.opts, s.res, s.sel
	m := res.Manifest
	// .
	// .
	// .
	// .
	// .
	if s.material.PluginID != "" && !s.acquired {
		return nil, &NotAcquiredError{PluginID: m.ID, Version: m.Version}
	}
	host := currentHost(opts)
	variant, artifactBytes := sel.variant, sel.artifactBytes
	material, runtimeRoot := s.material, s.runtimeRoot

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	envelope := signedCapabilitySurface(m, variant, res)
	binding := opts.Broker.BindRelease(m.ID, res.Tier, envelope, broker.Release{Version: m.Version, PackageHash: res.PackageHash})
	if binding != nil {
		// .
		// .
		// .
		if cerr := binding.ClearTempScope(); cerr != nil {
			return nil, fmt.Errorf("pluginhost: clearing stale temp kv for %s: %w", m.ID, cerr)
		}
	}

	ap := &ActivePlugin{
		ID: m.ID, Version: m.Version, Tier: res.Tier, VariantID: variant.VariantID,
		Backends:  m.BackendDeclarations(variant),
		Publisher: m.Publisher, PublisherID: res.PublisherID, Family: m.PluginFamily,
		Title: m.Title, Description: m.Description,
		Interfaces: interfaceNames(m.Interfaces), Capabilities: append([]string(nil), envelope...),
		Runtime: variant.ExecutionRuntime, PackageHash: m.PackageHash,
		reg: reg, binding: binding, envelope: envelope,
	}
	if binding != nil {
		binding.SetPublisher(ap)
	}

	// .
	// .
	// .
	// .
	decls, settingsErr := loadSettings(pkgPath, res, m.ID)
	if settingsErr != nil {
		_ = binding.Close()
		return nil, settingsErr
	}
	ap.Settings = decls
	hooks, herr := loadWebhooks(pkgPath, res, m, decls)
	if herr != nil {
		_ = binding.Close()
		return nil, herr
	}
	ap.Webhooks = hooks
	subs, suberr := loadSubscriptions(pkgPath, res, m)
	if suberr != nil {
		_ = binding.Close()
		return nil, suberr
	}
	ap.Subscriptions = subs
	if binding != nil && opts.StreamMax != nil {
		binding.SetStreamCap(opts.StreamMax[m.ID])
	}
	if binding != nil && opts.PluginDataDir != "" {
		binding.SetFiles(filepath.Join(opts.PluginDataDir, sanitizeToken(m.ID)), opts.FilesMax[m.ID])
	}
	if binding != nil {
		source := opts.Settings
		binding.SetSettings(func() map[string]interface{} {
			var values map[string]interface{}
			if source != nil {
				values = source(m.ID)
			}
			return EffectiveSettings(decls, values)
		})
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
	var inv invoker
	switch {
	case host.supervised && variant.ExecutionRuntime == "wasm_component":
		ap.Mode = ModeSupervised
		wasmAllow := sel.Startup
		ap.Startup = &wasmAllow
		sup, dir, serr := startSupervisedWASM(ctx, res, variant, artifactBytes, binding, opts)
		if serr != nil {
			_ = binding.Close()
			return nil, serr
		}
		ap.sup, ap.artifactDir = sup, dir
		inv = sup
	case variant.ExecutionRuntime == "native_t3_component":
		// .
		// .
		// .
		ap.Mode = ModeSupervised
		profile, models := sel.profile, sel.models
		ap.Accelerator = profile
		allow := sel.Startup
		ap.Startup = &allow
		ap.Models = models
		ap.ModelsDir = material.ModelsDir
		decl, root := material.Runtime, runtimeRoot
		var rt *runtimeBinding
		if decl != nil {
			pluginDir, entry := material.RuntimeDir, material.entry
			rt = &runtimeBinding{root: root, entry: entry, check: runtimeSpawnCheck(root, decl, entry)}
			if opts.RuntimeRoots != nil {
				opts.RuntimeRoots.Pin(root)
				ap.runtimeRoots = opts.RuntimeRoots
				kept := opts.RuntimeRootsKept
				if kept <= 0 {
					kept = 2
				}
				if removed, err := opts.RuntimeRoots.Retire(pluginDir, kept); err != nil {
					opts.logf("plugin %s: retiring old runtime roots: %v", m.ID, err)
				} else if len(removed) > 0 {
					opts.logf("plugin %s: retired %d old runtime root(s)", m.ID, len(removed))
				}
			}
			ap.RuntimeRoot = root
		}
		sup, dir, contained, serr := startSupervisedNativeWith(ctx, res, variant, artifactBytes, binding, opts, profile, ap.ModelsDir, m.PluginFamily == "voice_interface", rt)
		if serr != nil {
			var cleanup *supervisor.ContainmentCleanupError
			if !errors.As(serr, &cleanup) {
				ap.releaseRuntimeRoot()
			}
			_ = binding.Close()
			return nil, serr
		}
		ap.sup, ap.artifactDir, ap.Contained = sup, dir, contained
		if profile != nil {
			// .
			ready := ParseReadiness(sup.ReadyLine())
			ap.Readiness = &ready
			if !ready.Real() {
				cerr := ap.closeChannel(ctx)
				_ = binding.Close()
				return nil, errors.Join(&ReadinessError{PluginID: m.ID, Line: ready.Line}, cerr)
			}
		}
		if m.PluginFamily == "voice_interface" {
			// .
			// .
			if berr := ap.bindVoiceSession(sup); berr != nil {
				cerr := ap.closeChannel(ctx)
				_ = binding.Close()
				return nil, errors.Join(berr, cerr)
			}
		}
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
		// .
		// .
		// .
		for _, name := range ap.ToolNames {
			reg.Deregister(name)
		}
		cerr := ap.closeChannel(ctx)
		_ = binding.Close()
		return nil, errors.Join(err, cerr)
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
	ap.inv = inv
	ap.acts = opts.Acts
	descs, derr := loadDescriptors(pkgPath, res, m)
	if derr != nil {
		return abort(derr)
	}
	// .
	if variant.ExecutionRuntime == "wasm_component" || variant.ExecutionRuntime == "wasm_aot_component" {
		if perr := proveDescriptor(ctx, m, artifactBytes, descs, opts); perr != nil {
			return abort(perr)
		}
	}

	seen := make(map[string]bool)
	hostDriven := ap.hostDriven()
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		if !ap.offersOperationsFor(decl.ID) {
			continue
		}
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
			desc := descs.ops[method]
			// .
			// .
			// .
			description := fmt.Sprintf("Operation %q of interface %s@%d, plugin %s (brokered: every external effect is a per-invocation capability decision; ungranted plugins are pure compute).",
				method, decl.ID, decl.Version, m.ID)
			if desc != nil && desc.summary != "" {
				description = desc.summary + " — " + description
			}
			t := ap.newOperationTool(name, method, description, desc,
				desc.discovery(m.ID, m.Version, ap.Tier.String(), method))
			// .
			// .
			// .
			// .
			// .
			// .
			hostOp := decl.ID == ChannelInterfaceID || hostDriven[method]
			ap.pending = append(ap.pending, pendingTool{tool: t, hostOp: hostOp})
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	ch, cherr := channelOf(m)
	if cherr != nil {
		return abort(cherr)
	}
	ap.Channel = ch

	// .
	// .
	// .
	// .
	if registerNow {
		if rerr := ap.registerTools(); rerr != nil {
			return abort(rerr)
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
	return ap, nil
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
type opGate struct {
	once sync.Once
	ch   chan struct{}
}

func (g *opGate) slot() chan struct{} {
	g.once.Do(func() { g.ch = make(chan struct{}, 1) })
	return g.ch
}

// .
func (g *opGate) acquire(ctx context.Context) error {
	select {
	case g.slot() <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *opGate) release() { <-g.slot() }

// .
func (g *opGate) tryAcquire() bool {
	select {
	case g.slot() <- struct{}{}:
		return true
	default:
		return false
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
func (ap *ActivePlugin) newOperationTool(name, operation, description string, desc *opDescriptor, disc tools.Discovery) *operationTool {
	inv := ap.inv
	if ap.Voice != nil {
		// .
		// .
		inv = sessionInvoker{v: ap.Voice}
	}
	return &operationTool{
		name:        name,
		operation:   operation,
		inv:         inv,
		desc:        desc,
		seam:        ap.binding,
		inFlight:    &ap.inFlight,
		gate:        &ap.admitted,
		authorized:  ap.isAuthorized,
		description: description,
		parameters:  desc.params(),
		disc:        disc,
		plugin:      ap.ID,
		acts:        ap.acts,
	}
}

func (ap *ActivePlugin) offersOperationsFor(interfaceID string) bool {
	return interfaceID != SessionInterfaceID
}

// .
// .
// .
// .
// .
// .
func (ap *ActivePlugin) registerTools() error {
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()
	if !ap.isAuthorized() {
		return fmt.Errorf("plugin %s: %w", ap.ID, ErrWithdrawn)
	}
	// .
	// .
	ap.admitted.Store(true)
	for _, pt := range ap.pending {
		register := ap.reg.RegisterDynamic
		if pt.hostOp {
			register = ap.reg.RegisterHostOp
		}
		if rerr := register(pt.tool, ap.ID); rerr != nil {
			ap.admitted.Store(false)
			for _, n := range ap.ToolNames {
				ap.reg.Deregister(n)
			}
			ap.ToolNames = nil
			return &ToolNameError{PluginID: ap.ID, Name: pt.tool.name, Reason: rerr.Error()}
		}
		ap.ToolNames = append(ap.ToolNames, pt.tool.name)
	}
	ap.pending = nil
	return nil
}

// .
// .
func (ap *ActivePlugin) toolSet() ([]tools.Tool, map[string]bool) {
	set := make([]tools.Tool, 0, len(ap.pending))
	hostOnly := map[string]bool{}
	for _, pt := range ap.pending {
		set = append(set, pt.tool)
		if pt.hostOp {
			hostOnly[pt.tool.name] = true
		}
	}
	return set, hostOnly
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
func (ap *ActivePlugin) Health(ctx context.Context) error {
	if ap.Voice != nil {
		// .
		// .
		// .
		if !ap.Voice.Alive() {
			return fmt.Errorf("plugin %s: the candidate's session transport is not running (%s)", ap.ID, ap.Voice.FaultReason())
		}
		if ap.Accelerator != nil && (ap.Readiness == nil || !ap.Readiness.Real()) {
			return fmt.Errorf("plugin %s: the candidate declares an accelerator profile but reported no real readiness", ap.ID)
		}
		return ctx.Err()
	}
	if len(ap.pending) == 0 {
		return fmt.Errorf("plugin %s: the candidate release exposes no operations — not a successor", ap.ID)
	}
	if ap.Accelerator != nil && (ap.Readiness == nil || !ap.Readiness.Real()) {
		return fmt.Errorf("plugin %s: the candidate declares an accelerator profile but reported no real readiness", ap.ID)
	}
	if err := ctx.Err(); err != nil {
		return err
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
var ErrWithdrawn = errors.New("this activation was withdrawn before it was published; nothing of it is reachable")

// .
// .
// .
// .
// .
// .
// .
// .
func (ap *ActivePlugin) Authorize(valid func() bool) {
	if valid == nil {
		ap.authorized.Store(nil)
		return
	}
	ap.authorized.Store(&valid)
}

func (ap *ActivePlugin) isAuthorized() bool {
	valid := ap.authorized.Load()
	return valid == nil || (*valid)()
}

// .
// .
// .
// .
// .
// .
// .
var redirectSwapped = func() {}

func (ap *ActivePlugin) Redirect(prev *ActivePlugin) error {
	// .
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()
	// .
	// .
	// .
	// .
	// .
	// .
	if !ap.isAuthorized() {
		return fmt.Errorf("plugin %s: %w", ap.ID, ErrWithdrawn)
	}
	if prev != nil {
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
		prev.pubMu.Lock()
		defer prev.pubMu.Unlock()
	}
	set, hostOnly := ap.toolSet()
	// .
	// .
	// .
	ap.admitted.Store(true)
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := ap.reg.SupersedeOriginIf(ap.ID, set, hostOnly, ap.isAuthorized); err != nil {
		ap.admitted.Store(false)
		if errors.Is(err, tools.ErrSupersedeRefused) {
			return fmt.Errorf("plugin %s: %w", ap.ID, ErrWithdrawn)
		}
		return err
	}
	redirectSwapped()
	ap.ToolNames = make([]string, 0, len(ap.pending))
	for _, pt := range ap.pending {
		ap.ToolNames = append(ap.ToolNames, pt.tool.name)
	}
	ap.pending = nil
	if prev != nil {
		prev.ToolNames = nil
		// .
		// .
		// .
		prev.superseded.Store(true)
	}
	return nil
}

// .
// .
// .
// .
// .
func (ap *ActivePlugin) WaitIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		// .
		// .
		// .
		if ap.inFlight.tryAcquire() {
			ap.inFlight.release()
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// .
// .
// .
// .
func (ap *ActivePlugin) CloseQuiet(ctx context.Context) error {
	if ap.sessionCancel != nil {
		ap.sessionCancel()
	}
	err := ap.closeChannel(ctx)
	if ap.binding != nil {
		if berr := ap.binding.Close(); err == nil {
			err = berr
		}
	}
	return err
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
func (ap *ActivePlugin) Tools() []string {
	if ap == nil {
		return nil
	}
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()
	return append([]string(nil), ap.ToolNames...)
}

// .
// .
// .
// .
// .
// .
func (p *ActivePlugin) Deactivate(ctx context.Context) error {
	p.teardown.Lock()
	defer p.teardown.Unlock()
	if p.sessionCancel != nil {
		p.sessionCancel()
	}
	for _, name := range p.Tools() {
		p.reg.Deregister(name)
	}
	err := p.closeChannel(ctx)
	if berr := p.binding.Close(); err == nil {
		err = berr
	}
	return err
}

// .
// .
func (p *ActivePlugin) closeChannel(ctx context.Context) error {
	var err error
	if p.module != nil {
		err = p.module.Close(ctx)
	}
	if p.sup != nil {
		// .
		// .
		// .
		if serr := p.sup.CloseContext(ctx); serr != nil {
			// .
			return errors.Join(err, serr)
		}
	}
	if p.artifactDir != "" {
		if rerr := os.RemoveAll(p.artifactDir); err == nil {
			err = rerr
		}
		p.artifactDir = ""
	}
	p.releaseRuntimeRoot()
	return err
}

// .
// .
// .
// .
func (p *ActivePlugin) releaseRuntimeRoot() {
	if p.runtimeRoots != nil && p.RuntimeRoot != "" {
		p.runtimeRoots.Release(p.RuntimeRoot)
	}
	p.runtimeRoots = nil
}

// .
type runtimeBinding struct {
	root  string
	entry entrypointSpec
	check func() error
}

// .
// .
func removeArtifactDir(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// .
// .
// .
func (p *ActivePlugin) Restarts() int {
	if p.sup == nil {
		return 0
	}
	return p.sup.Restarts()
}

// .
// .
func (p *ActivePlugin) SupervisedPid() int {
	if p.sup == nil {
		return 0
	}
	return p.sup.Pid()
}

// .

// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
func supervisorDispatcher(binding *broker.Binding) supervisor.Dispatcher {
	if binding == nil {
		return nil
	}
	return binding
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
func startSupervisedWASM(ctx context.Context, res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, binding *broker.Binding, opts *Options) (*supervisor.Supervisor, string, error) {
	dir, path, verify, err := extractArtifact(res, variant, artifactBytes, "artifact.wasm", 0o600)
	if err != nil {
		return nil, "", err
	}
	memoryMax := opts.MemoryMax[res.Manifest.ID]
	if memoryMax == 0 {
		memoryMax = pluginworker.DefaultMemoryMaxBytes
	}
	sup, err := supervisor.StartContext(ctx, supervisor.Spec{
		PluginID: res.Manifest.ID,
		Argv: append(append([]string{opts.WorkerBinary}, opts.WorkerArgs...), "-forward",
			fmt.Sprintf("-memory-max=%d", memoryMax),
			// .
			// .
			// .
			// .
			"-module-sha256="+res.FileDigests[variant.Entrypoint], path),
		ReadyMark:      "event=ready",
		ReadyTimeout:   startupAllowance(opts, res.Manifest.ID, nil).Effective,
		ReadyAllowance: startupAllowance(opts, res.Manifest.ID, nil).Sentence(),
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

// .
// .
// .
// .
// .
// .
// .
func startSupervisedNative(ctx context.Context, res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, binding *broker.Binding, opts *Options) (*supervisor.Supervisor, string, error) {
	sup, dir, _, err := startSupervisedNativeWith(ctx, res, variant, artifactBytes, binding, opts, nil, "", false, nil)
	return sup, dir, err
}

// .
// .
// .
func startSupervisedNativeWith(ctx context.Context, res *packagefmt.Result, variant *packagefmt.Variant, artifactBytes []byte, binding *broker.Binding, opts *Options, profile *AcceleratorProfile, modelsDir string, sessionMode bool, rt *runtimeBinding) (*supervisor.Supervisor, string, bool, error) {
	var dir, path string
	var verify func() error
	if rt != nil {
		// .
		// .
		// .
		path = filepath.Join(rt.root, rt.entry.Name)
		verify = rt.check
	} else {
		var err error
		name := "artifact"
		if packagefmt.HostPlatform() == "windows" {
			name += ".exe"
		}
		dir, path, verify, err = extractArtifact(res, variant, artifactBytes, name, 0o700)
		if err != nil {
			return nil, "", false, err
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if nc := hostcap.Can(hostcap.NativeChild); !nc.Available {
		removeArtifactDir(dir)
		return nil, "", false, fmt.Errorf("plugin %s: native lane unavailable: %s", res.Manifest.ID, nc.Reason)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	image, ierr := OpenVerified(path, res.FileDigests[variant.Entrypoint])
	if ierr != nil {
		removeArtifactDir(dir)
		return nil, "", false, fmt.Errorf("plugin %s: %w", res.Manifest.ID, ierr)
	}

	// .
	// .
	argv, containment, cerr := containArgv(ctx, image.Argv(), profile)
	if cerr != nil {
		image.Close()
		removeArtifactDir(dir)
		return nil, "", false, fmt.Errorf("plugin %s: cannot contain the native child: %w", res.Manifest.ID, cerr)
	}
	if rt != nil {
		containment.Description += "; runtime root " + rt.root
	}
	if opts.Log != nil {
		opts.Log.Printf("plugin %s: native child %s; image %s", res.Manifest.ID, containment, image.Binding())
	}
	spec := supervisor.Spec{
		PluginID:        res.Manifest.ID,
		ArgvContainment: containment,
		Artifact:        image,
		ReadyTimeout:    startupAllowance(opts, res.Manifest.ID, profile).Effective,
		ReadyAllowance:  startupAllowance(opts, res.Manifest.ID, profile).Sentence(),
		Argv:            argv,
		Env:             []string{"SEV_PLUGIN_SOCKET=stdio:", "SEV_PLUGIN_ID=" + res.Manifest.ID},
		RLimitASBytes:   opts.MemoryMax[res.Manifest.ID],
		ExtraFiles:      image.ExtraFiles(),
		VerifyArtifact:  verify,
		Log:             opts.Log,
	}
	spec.SessionMode = sessionMode
	// .
	// .
	spec.AudioPair = sessionMode
	if profile != nil {
		// .
		spec.ReadyMark = ReadyMark
	}
	if modelsDir != "" {
		// .
		// .
		spec.Env = append(spec.Env, "AII_MODELS_DIR="+modelsDir)
	}
	if rt != nil {
		// .
		// .
		spec.Env = append(spec.Env, "AII_RUNTIME_ROOT="+rt.root)
	}
	// .
	// .
	// .
	// .
	var grants []string
	if rt != nil {
		grants = append(grants, rt.root)
	} else if dir != "" {
		grants = append(grants, dir)
	}
	if modelsDir != "" {
		grants = append(grants, modelsDir)
	}
	spec.AppContainer = wallFor(res.Manifest.ID, grants)
	sup, err := supervisor.StartContext(ctx, spec, supervisorDispatcher(binding))
	if err != nil {
		var cleanup *supervisor.ContainmentCleanupError
		if !errors.As(err, &cleanup) {
			image.Close()
			removeArtifactDir(dir)
		}
		return nil, "", false, err
	}
	return sup, dir, sup.Containment().Isolated(), nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func signedCapabilitySurface(m *packagefmt.Manifest, variant *packagefmt.Variant, res *packagefmt.Result) []string {
	var envelope, variantCaps []string
	// .
	// .
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

// .
// .
// .
// .
// .
// .
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

// .

// .
// .
// .
// .
// .
// .
// .
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

// .

// .
// .
// .
// .
type contractSeam interface {
	BeginOperation(broker.OperationScope)
	EndOperation()
	InvokeRefused(operation, detail string)
}

// .
// .
// .
// .
// .
// .
type opDescriptor struct {
	raw          map[string]interface{}
	input        *jsonschema.Schema
	output       *jsonschema.Schema
	outputRaw    map[string]interface{}
	effects      string
	capabilities []string
	capsDeclared bool
	maxResult    int
	// .
	// .
	summary  string
	family   string
	keywords []string
	examples []string
	// .
	// .
	operatorConfirms bool
}

// .
// .
func (d *opDescriptor) discovery(plugin, version, tier, operation string) tools.Discovery {
	disc := tools.Discovery{Plugin: plugin, Version: version, Tier: tier, Operation: operation}
	if d == nil {
		return disc
	}
	disc.Family, disc.Summary, disc.Effects = d.family, d.summary, d.effects
	disc.Capabilities = append([]string(nil), d.capabilities...)
	disc.MaxResultBytes = d.maxResult
	disc.Keywords = append([]string(nil), d.keywords...)
	disc.Examples = append([]string(nil), d.examples...)
	disc.OperatorConfirms = d.operatorConfirms
	disc.OutputSchema = d.outputRaw
	return disc
}

// .
func (d *opDescriptor) params() map[string]interface{} {
	if d == nil {
		return nil
	}
	return d.raw
}

// .
// .
// .
type SchemaError struct {
	Operation string
	File      string
	Err       error
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf("pluginhost: operation %q: schema %s: %v", e.Operation, e.File, e.Err)
}

func (e *SchemaError) Unwrap() error { return e.Err }

// .
// .
// .
// .
// .
// .
// .
// .
// .
func loadDescriptors(pkgPath string, res *packagefmt.Result, m *packagefmt.Manifest) (*descriptorSet, error) {
	set := &descriptorSet{ops: make(map[string]*opDescriptor), files: make(map[string][]byte)}
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		schemaRel := fmt.Sprintf("interfaces/%s.v%d.schema.json", decl.ID, decl.Version)
		descBytes, err := loadVerifiedMember(pkgPath, res, schemaRel)
		if err != nil {
			continue
		}
		set.files[schemaRel] = descBytes
		var ops []struct {
			ID           string          `json:"id"`
			Summary      string          `json:"summary"`
			Input        string          `json:"input"`
			Output       string          `json:"output"`
			Effects      string          `json:"effects"`
			Capabilities json.RawMessage `json:"capabilities"`
			MaxResult    int             `json:"max_result_bytes"`
			// .
			// .
			Family   string   `json:"family"`
			Keywords []string `json:"keywords"`
			Examples []string `json:"examples"`
			// .
			// .
			OperatorConfirms bool `json:"operator_confirms"`
		}
		if err := json.Unmarshal(descBytes, &ops); err != nil {
			continue
		}
		for _, op := range ops {
			if !broker.KnownEffects(op.Effects) {
				return nil, &SchemaError{Operation: op.ID, File: schemaRel, Err: fmt.Errorf("effects %q is not a declared class (read.internal, read.external, write.local, write.external, exec)", op.Effects)}
			}
			d := &opDescriptor{effects: op.Effects, maxResult: op.MaxResult,
				summary: op.Summary, family: op.Family, keywords: op.Keywords, examples: op.Examples,
				operatorConfirms: op.OperatorConfirms}
			if len(op.Capabilities) > 0 {
				// .
				// .
				d.capsDeclared = true
				if err := json.Unmarshal(op.Capabilities, &d.capabilities); err != nil {
					return nil, &SchemaError{Operation: op.ID, File: schemaRel, Err: fmt.Errorf("capabilities must be a list of strings: %w", err)}
				}
			}
			if op.Input != "" {
				raw, compiled, err := loadSchema(pkgPath, res, op.Input)
				if err != nil {
					return nil, &SchemaError{Operation: op.ID, File: op.Input, Err: err}
				}
				d.raw, d.input = raw, compiled
			}
			if op.Output != "" {
				raw, compiled, err := loadSchema(pkgPath, res, op.Output)
				if err != nil {
					return nil, &SchemaError{Operation: op.ID, File: op.Output, Err: err}
				}
				d.outputRaw, d.output = raw, compiled
			}
			set.ops[op.ID] = d
		}
	}
	return set, nil
}

// .
// .
type descriptorSet struct {
	ops   map[string]*opDescriptor
	files map[string][]byte
}

// .
// .
// .
type DescriptorMismatchError struct {
	PluginID  string
	Interface string
	Detail    string
}

func (e *DescriptorMismatchError) Error() string {
	return fmt.Sprintf("pluginhost: plugin %s: the packaged descriptor for %s is not the artifact's own account: %s", e.PluginID, e.Interface, e.Detail)
}

// .
// .
// .
// .
// .
// .
// .
// .
func proveDescriptor(ctx context.Context, m *packagefmt.Manifest, artifactBytes []byte, set *descriptorSet, opts *Options) error {
	mod, err := pluginworker.Load(ctx, artifactBytes, pluginworker.Config{MemoryMaxBytes: opts.MemoryMax[m.ID]})
	if err != nil {
		return err
	}
	defer mod.Close(ctx)
	got, ok, err := mod.Describe(ctx)
	if err != nil {
		return &DescriptorMismatchError{PluginID: m.ID, Interface: "", Detail: "the artifact's describe export failed: " + err.Error()}
	}
	if !ok {
		opts.logf("plugin %s: descriptor unproven — the artifact exports no %s (an older kit); the packaged file is taken as declared", m.ID, pluginworker.ExportDescribe)
		return nil
	}
	if len(m.Interfaces.Core) != 1 {
		opts.logf("plugin %s: descriptor unproven — %d core interfaces and one describe export", m.ID, len(m.Interfaces.Core))
		return nil
	}
	decl := m.Interfaces.Core[0]
	schemaRel := fmt.Sprintf("interfaces/%s.v%d.schema.json", decl.ID, decl.Version)
	want, present := set.files[schemaRel]
	if !present {
		return &DescriptorMismatchError{PluginID: m.ID, Interface: decl.ID, Detail: "the artifact describes itself and the package carries no descriptor file for the interface"}
	}
	var a, b interface{}
	if err := json.Unmarshal(got, &a); err != nil {
		return &DescriptorMismatchError{PluginID: m.ID, Interface: decl.ID, Detail: "the artifact's account is not JSON: " + err.Error()}
	}
	if err := json.Unmarshal(want, &b); err != nil {
		return &DescriptorMismatchError{PluginID: m.ID, Interface: decl.ID, Detail: "the packaged descriptor is not JSON: " + err.Error()}
	}
	if !reflect.DeepEqual(a, b) {
		return &DescriptorMismatchError{PluginID: m.ID, Interface: decl.ID, Detail: "the packaged descriptor file and the artifact's own account differ"}
	}
	return nil
}

// .
// .
// .
func loadSchema(pkgPath string, res *packagefmt.Result, rel string) (map[string]interface{}, *jsonschema.Schema, error) {
	raw, err := loadVerifiedMember(pkgPath, res, rel)
	if err != nil {
		return nil, nil, nil
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if err := normalizeRequired(schema); err != nil {
		return nil, nil, err
	}
	compiled, err := jsonschema.Compile(schema)
	if err != nil {
		return nil, nil, err
	}
	return schema, compiled, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func normalizeRequired(schema map[string]interface{}) error {
	raw, present := schema["required"]
	if !present {
		return nil
	}
	list := []string{}
	switch v := raw.(type) {
	case []string:
		for i, s := range v {
			if s == "" {
				return fmt.Errorf("\"required\"[%d] is empty; want a non-empty string", i)
			}
		}
		list = append(list, v...)
	case []interface{}:
		for i, e := range v {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("\"required\"[%d] is %T; want a non-empty string", i, e)
			}
			if s == "" {
				return fmt.Errorf("\"required\"[%d] is empty; want a non-empty string", i)
			}
			list = append(list, s)
		}
	default:
		return fmt.Errorf("\"required\" is %T; want an array of non-empty strings", raw)
	}
	schema["required"] = list
	return nil
}

// .
// .
// .
// .
type operationTool struct {
	name        string
	description string
	operation   string
	inv         invoker
	parameters  map[string]interface{}
	desc        *opDescriptor
	seam        contractSeam
	inFlight    *opGate
	disc        tools.Discovery
	plugin      string
	acts        ActProposer
	gate        *atomic.Bool
	authorized  func() bool
}

// .
// .
func (t *operationTool) Discovery() tools.Discovery { return t.disc }

// .
// .
// .
// .
// .
func (t *operationTool) ReplaySafe() bool {
	if t.desc == nil || t.disc.OperatorConfirms {
		return false
	}
	return t.desc.effects == broker.EffectsReadInternal || t.desc.effects == broker.EffectsReadExternal
}

func (t *operationTool) Name() string        { return t.name }
func (t *operationTool) Description() string { return t.description }

// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
type invokeResult struct {
	Status          string          `json:"status"`
	OperationResult json.RawMessage `json:"operation_result"`
	Reason          string          `json:"reason"`
	ReasonCode      string          `json:"reasonCode"`
	ReasonCodeSnake string          `json:"reason_code"`
}

// .
type rpcErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ReasonCode string `json:"reasonCode"`
	} `json:"data"`
}

// .
// .
// .
// .
// .
func (t *operationTool) Execute(ctx context.Context, args map[string]interface{}) (tools.Result, error) {
	// .
	// .
	if t.gate != nil && !t.gate.Load() {
		return tools.Result{Error: fmt.Sprintf("plugin %s: this release is being admitted; its operations are not yet callable", t.plugin)}, nil
	}
	// .
	// .
	// .
	// .
	if t.authorized != nil && !t.authorized() {
		return tools.Result{Error: fmt.Sprintf("plugin %s: this release was withdrawn; its operations are not callable", t.plugin)}, nil
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
	if t.desc != nil && t.desc.input != nil {
		if verr := t.desc.input.Validate(jsonValue(args)); verr != nil {
			return tools.Result{Error: "arguments refused before the call: " + verr.Error()}, nil
		}
	}
	injected := make(map[string]interface{}, len(args)+2)
	for k, v := range args {
		if !strings.HasPrefix(k, "_host") {
			injected[k] = v
		}
	}
	injected["_host_now_ms"] = time.Now().UnixMilli()
	// .
	// .
	// .
	// .
	if gate, ok := t.acts.(OperationGate); ok && t.acts != nil {
		effects := ""
		if t.desc != nil {
			effects = t.desc.effects
		}
		if gerr := gate.AdmitOperation(t.plugin, t.operation, effects); gerr != nil {
			return tools.Result{Error: gerr.Error() + "; nothing ran", ReasonCode: ReasonOperatorGrant}, nil
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
	if t.desc != nil && t.desc.operatorConfirms {
		act := OperatorActFrom(ctx)
		// .
		// .
		if act == nil && t.acts != nil {
			if sc, ok := t.acts.(StandingConfirmer); ok {
				if standing, ok := sc.StandingConfirmation(t.plugin, t.operation); ok {
					act = &standing
				}
			}
		}
		if act == nil {
			if t.acts == nil {
				return tools.Result{Error: fmt.Sprintf("%s needs the operator's confirmation and this host has no way to ask for it; nothing ran", t.operation)}, nil
			}
			id, perr := t.acts.Propose(ctx, ActProposal{Plugin: t.plugin, Tool: t.name, Operation: t.operation, Summary: t.desc.summary, Effects: t.desc.effects, Args: args})
			if perr != nil {
				return tools.Result{Error: fmt.Sprintf("%s was not proposed: %v; nothing ran", t.operation, perr)}, nil
			}
			out, _ := json.Marshal(map[string]interface{}{
				"proposed": true, "act": id, "operation": t.operation,
				"awaiting": "the operator's confirmation on the Plugins page; nothing has run",
				"note":     "the operation runs once, with exactly these arguments, when the operator confirms; the outcome is recorded in the conversation as the operator's act — do not re-issue this call",
			})
			return tools.Result{Output: string(out)}, nil
		}
		injected["_host_operator_act"] = map[string]interface{}{"id": act.ID, "confirmed_at": act.ConfirmedAt.UTC().Format(time.RFC3339)}
	}
	frame, err := json.Marshal(invokeRequest{
		JSONRPC: "2.0", ID: harnessRequestID, Method: "invoke.call",
		Params: invokeParams{Operation: t.operation, Arguments: injected},
	})
	if err != nil {
		return tools.Result{}, fmt.Errorf("pluginhost: encode invoke.call: %w", err)
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
	if t.inFlight != nil {
		if err := t.inFlight.acquire(ctx); err != nil {
			return tools.Result{Error: "the call was cancelled while waiting for this plugin's turn; nothing ran"}, nil
		}
		defer t.inFlight.release()
		// .
		if err := ctx.Err(); err != nil {
			return tools.Result{Error: "the call was cancelled before it was dispatched; nothing ran"}, nil
		}
	}
	// .
	// .
	if t.authorized != nil && !t.authorized() {
		return tools.Result{Error: fmt.Sprintf("plugin %s: this release was withdrawn; its operations are not callable", t.plugin)}, nil
	}
	if t.seam != nil && t.desc != nil {
		t.seam.BeginOperation(broker.OperationScope{Operation: t.operation, Effects: t.desc.effects,
			Capabilities: t.desc.capabilities, Declared: t.desc.capsDeclared})
		defer t.seam.EndOperation()
	}
	reply, err := t.inv.Invoke(ctx, frame)
	if err != nil {
		return tools.Result{}, err
	}
	res, err := decodeReply(reply)
	if err != nil || res.Error != "" {
		return res, err
	}
	return t.acceptResult(res), nil
}

// .
// .
// .
// .
// .
// .
func (t *operationTool) acceptResult(res tools.Result) tools.Result {
	if t.desc == nil {
		return res
	}
	if t.desc.maxResult > 0 && len(res.Output) > t.desc.maxResult {
		return t.refuse(fmt.Sprintf("result of %d bytes exceeds the operation's declared bound of %d", len(res.Output), t.desc.maxResult))
	}
	if t.desc.output != nil {
		var v interface{}
		if err := json.Unmarshal([]byte(res.Output), &v); err != nil {
			return t.refuse("result is not JSON, and the operation declared an output schema")
		}
		if verr := t.desc.output.Validate(v); verr != nil {
			return t.refuse("result outside the declared output schema: " + verr.Error())
		}
	}
	return res
}

func (t *operationTool) refuse(detail string) tools.Result {
	if t.seam != nil {
		t.seam.InvokeRefused(t.operation, detail)
	}
	return tools.Result{Error: "result refused: " + detail}
}

// .
// .
// .
// .
func jsonValue(args map[string]interface{}) interface{} {
	raw, err := json.Marshal(args)
	if err != nil {
		return args
	}
	var v interface{}
	if json.Unmarshal(raw, &v) != nil {
		return args
	}
	return v
}

// .
// .
// .
// .
// .
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
		// .
		// .
		var eo rpcErrorObject
		if err := json.Unmarshal(errorRaw, &eo); err != nil {
			return tools.Result{}, &ResponseContractError{Requirement: "error member must be a JSON-RPC error object", Got: excerpt(reply)}
		}
		msg := fmt.Sprintf("plugin error %d: %s", eo.Code, eo.Message)
		if eo.Data.ReasonCode != "" {
			msg += fmt.Sprintf(" (reasonCode %s)", eo.Data.ReasonCode)
		}
		return tools.Result{Error: msg, ReasonCode: eo.Data.ReasonCode}, nil
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
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
		// .
		// .
		// .
		return tools.Result{Output: string(resultRaw)}, nil
	}
	switch ir.Status {
	case "", "succeeded":
		if len(ir.OperationResult) > 0 {
			return tools.Result{Output: string(ir.OperationResult)}, nil
		}
		return tools.Result{Output: string(resultRaw)}, nil
	default:
		reason := ir.Reason
		code := firstNonEmpty(ir.ReasonCode, ir.ReasonCodeSnake)
		switch {
		case reason != "" && code != "" && reason != code:
			reason = reason + " (" + code + ")"
		case reason == "":
			reason = firstNonEmpty(code, "no reason given")
		}
		return tools.Result{Error: fmt.Sprintf("operation %s: %s", ir.Status, reason), ReasonCode: code}, nil
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// .
// .
func excerpt(b []byte) string {
	const max = 256
	if len(b) > max {
		return string(b[:max]) + fmt.Sprintf("… (%d bytes)", len(b))
	}
	return string(b)
}

// .
// .
func modelNames(decls []ModelDecl) []string {
	out := make([]string, 0, len(decls))
	for _, d := range decls {
		out = append(out, d.Name)
	}
	return out
}

func (o *Options) logf(format string, args ...interface{}) {
	if o != nil && o.Log != nil {
		o.Log.Printf(format, args...)
		return
	}
	logsink.Info("plugins.decision", format, args...)
}

// .
// .
func interfaceNames(set *packagefmt.InterfaceSet) []string {
	if set == nil {
		return nil
	}
	var out []string
	for _, d := range set.Core {
		out = append(out, fmt.Sprintf("%s v%d", d.ID, d.Version))
	}
	for _, d := range set.Optional {
		out = append(out, fmt.Sprintf("%s v%d (optional)", d.ID, d.Version))
	}
	return out
}

// .
// .
// .
type ActProposal struct {
	Plugin, Tool, Operation, Summary, Effects string
	Args                                      map[string]interface{}
}

// .
// .
type ActProposer interface {
	Propose(ctx context.Context, p ActProposal) (id string, err error)
}

// .
// .
// .
// .
// .
type StandingConfirmer interface {
	StandingConfirmation(plugin, operation string) (OperatorAct, bool)
}

// .
// .
// .
// .
// .
// .
type OperationGate interface {
	AdmitOperation(plugin, operation, effects string) error
}

// .
// .
const ReasonOperatorGrant = "OPERATOR_GRANT_REFUSES"

// .
// .
// .
type OperatorAct struct {
	ID          string
	ConfirmedAt time.Time
}

type operatorActKey struct{}

// .
func WithOperatorAct(ctx context.Context, act OperatorAct) context.Context {
	return context.WithValue(ctx, operatorActKey{}, act)
}

// .
func OperatorActFrom(ctx context.Context) *OperatorAct {
	if act, ok := ctx.Value(operatorActKey{}).(OperatorAct); ok && act.ID != "" {
		return &act
	}
	return nil
}
