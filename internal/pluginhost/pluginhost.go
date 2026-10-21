package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ModeInProcess = "in-process"

	ModeSupervised = "supervised"
)

const harnessRequestID = "h1"

const maxToolNameBytes = 64

type invoker interface {
	Invoke(ctx context.Context, frame []byte) ([]byte, error)
}

type ActivePlugin struct {
	calibration calibrationSlot

	schemaBound int

	inFlight opGate

	teardown sync.Mutex

	ID string

	Version string

	Publisher   string
	PublisherID string
	Family      string

	Title        string
	Description  string
	Interfaces   []string
	Capabilities []string
	Runtime      string
	PackageHash  string

	Settings []SettingDecl

	Webhooks []WebhookDecl

	webhookURL func(pluginID, path string) string

	Schedule []ScheduleDecl

	Subscriptions []SubscriptionDecl

	Accelerator *AcceleratorProfile
	Readiness   *Readiness

	Startup *StartupAllowance

	Models    []ModelDecl
	ModelsDir string

	ModelDecls []ModelDecl

	RuntimeRoot string

	material Material

	pins heldEntries

	Tier packagefmt.Tier

	Mode string

	VariantID string

	Channel *Channel

	Embeddings *Embeddings

	validation *packagefmt.Validation
	checked    atomic.Pointer[pluginfacility.Checks]

	Backends []string

	ToolNames []string

	pending []pendingTool

	superseded atomic.Bool

	admitted atomic.Bool

	authorized atomic.Pointer[func() bool]

	acts ActProposer

	inv       invoker
	envelope  []string
	pubMu     sync.Mutex
	published map[string]string

	module      *pluginworker.Module
	sup         *supervisor.Supervisor
	artifactDir string
	reg         *tools.Registry
	binding     *broker.Binding

	Voice          atomic.Pointer[VoiceSession]
	sessionCancel  context.CancelFunc
	sessionDone    chan struct{}
	voicePublished func(*VoiceSession)

	Contained bool
}

type Options struct {
	WebhookURL func(pluginID, path string) string

	LegacyStorageOwners func(context.Context) ([]string, error)

	StorageUpgradeReady func() error

	NativePlaces func() ([]*firewall.Rule, error)

	Roots packagefmt.TrustRoots

	Broker *broker.Host

	Facilities []string

	ResourceFacts   func(context.Context) (pluginfacility.Availability, error)
	SelectionPolicy pluginfacility.AdmissionPolicy

	Serving *ServingSet

	ChecksFailed map[string]string

	WorkerBinary string

	WorkerArgs []string

	MemoryMax map[string]uint64

	ReadyTimeout map[string]time.Duration

	StartupCeiling time.Duration

	MaxInputSchemaBytes int

	StreamMax map[string]int

	PluginDataDir string

	FilesMax map[string]int

	StoreDir string

	Materialize func(ctx context.Context, m Material) error

	AdoptModelsDir  string
	AdoptRuntimeDir string

	RuntimeLimits packagefmt.TreeLimits

	FreeDisk func(dir string) (int64, error)

	PathLimit int

	HostVersion string

	Acquirer *Acquirer

	Settings func(pluginID string) map[string]any

	Acts ActProposer
}

type opGate struct {
	once sync.Once
	ch   chan struct{}
}

func (g *opGate) slot() chan struct{} {
	g.once.Do(func() { g.ch = make(chan struct{}, 1) })
	return g.ch
}

func (g *opGate) acquire(ctx context.Context) error {
	select {
	case g.slot() <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *opGate) release() { <-g.slot() }

func (g *opGate) tryAcquire() bool {
	select {
	case g.slot() <- struct{}{}:
		return true
	default:
		return false
	}
}

func (ap *ActivePlugin) newOperationTool(name, operation, description string, desc *opDescriptor, disc tools.Discovery) *operationTool {
	inv := ap.inv
	if v := ap.Voice.Load(); v != nil {

		inv = sessionInvoker{voice: ap.Voice.Load}
	}
	t := &operationTool{
		owner:       ap,
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

	if t.parameters != nil {
		var prose bool
		if t.model, prose = tools.ModelSchema(t.parameters, name); prose {
			t.description += fmt.Sprintf(" The descriptions and titles of its arguments are plugin %s's words, each quoted.", ap.ID)
		}
	}
	return t
}

func (ap *ActivePlugin) offersOperationsFor(interfaceID string) bool {
	return interfaceID != SessionInterfaceID
}

func (ap *ActivePlugin) registerTools() error {
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()
	if !ap.isAuthorized() {
		return fmt.Errorf("plugin %s: %w", ap.ID, ErrWithdrawn)
	}

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
			return &ToolNameError{PluginID: ap.ID, Name: pt.tool.name, Reason: rerr.Error(), Taken: true}
		}
		ap.ToolNames = append(ap.ToolNames, pt.tool.name)
	}
	ap.pending = nil
	return nil
}

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

func (ap *ActivePlugin) Health(ctx context.Context) error {
	if ap.sup != nil {
		if state, cause := ap.sup.State(); state != supervisor.StateRunning {
			return &supervisor.UnavailableError{PluginID: ap.ID, State: state, Reason: cause}
		}
	}
	if v := ap.Voice.Load(); v != nil {

		if !v.Alive() {
			return fmt.Errorf("plugin %s: the candidate's session transport is not running (%s)", ap.ID, v.FaultReason())
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

var ErrWithdrawn = errors.New("this activation was withdrawn before it was published; nothing of it is reachable")

const ReasonActivationWithdrawn = "ACTIVATION_WITHDRAWN"

const ReasonNotAdmitted = "ACTIVATION_NOT_ADMITTED"

const ReasonConfirmationUnavailable = "CONFIRMATION_UNAVAILABLE"

const ReasonCancelledBeforeDispatch = "CANCELLED_BEFORE_DISPATCH"

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

var redirectSwapped = func() {}

var ErrHandOverBusy = errors.New("the release serving keeps serving: its operation in flight did not finish within the hand-over's bound, and two releases never run this plugin's work at once")

const HandOverBudget = 30 * time.Second

var ErrRetirementBusy = errors.New("retirement waits for an admitted invocation to finish")

func (ap *ActivePlugin) Redirect(prev *ActivePlugin) error { return ap.HandOver(prev, 0) }

func (ap *ActivePlugin) HandOver(prev *ActivePlugin, bound time.Duration) error {
	if prev != nil {
		release, err := prev.idleSlot(bound)
		if err != nil {
			return err
		}
		defer release()
	}

	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()

	if !ap.isAuthorized() {
		return fmt.Errorf("plugin %s: %w", ap.ID, ErrWithdrawn)
	}
	if prev != nil {

		prev.pubMu.Lock()
		defer prev.pubMu.Unlock()
	}
	set, hostOnly := ap.toolSet()

	ap.admitted.Store(true)

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

		prev.superseded.Store(true)
	}
	return nil
}

func (ap *ActivePlugin) idleSlot(bound time.Duration) (func(), error) {
	if bound <= 0 {
		if !ap.inFlight.tryAcquire() {
			return nil, ErrHandOverBusy
		}
		return ap.inFlight.release, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	if err := ap.inFlight.acquire(ctx); err != nil {
		return nil, ErrHandOverBusy
	}
	return ap.inFlight.release, nil
}

func (ap *ActivePlugin) WaitIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {

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

func (ap *ActivePlugin) CloseQuiet(ctx context.Context) error {
	ap.teardown.Lock()
	defer ap.teardown.Unlock()

	if !ap.inFlight.tryAcquire() {
		return ErrRetirementBusy
	}
	defer ap.inFlight.release()

	if ap.sessionCancel != nil {
		ap.sessionCancel()
		<-ap.sessionDone
	}
	err := ap.closeChannel(ctx)
	return errors.Join(err, ap.binding.CloseContext(ctx))
}

func (ap *ActivePlugin) Tools() []string {
	if ap == nil {
		return nil
	}
	ap.pubMu.Lock()
	defer ap.pubMu.Unlock()
	return append([]string(nil), ap.ToolNames...)
}

func (p *ActivePlugin) Deactivate(ctx context.Context) error {
	p.teardown.Lock()
	defer p.teardown.Unlock()
	if !p.inFlight.tryAcquire() {
		return ErrRetirementBusy
	}
	defer p.inFlight.release()

	if p.sessionCancel != nil {
		p.sessionCancel()
		<-p.sessionDone
	}
	for _, name := range p.Tools() {
		p.reg.Deregister(name)
	}
	err := p.closeChannel(ctx)
	return errors.Join(err, p.binding.CloseContext(ctx))
}

func (p *ActivePlugin) closeChannel(ctx context.Context) error {
	var err error
	if p.module != nil {
		err = p.module.Close(ctx)
	}
	if p.sup != nil {

		if serr := p.sup.CloseContext(ctx); serr != nil {

			return errors.Join(err, serr)
		}
	}
	if p.artifactDir != "" {
		if rerr := os.RemoveAll(p.artifactDir); err == nil {
			err = rerr
		}
		p.artifactDir = ""
	}
	p.releasePins()
	return err
}

func (p *ActivePlugin) releasePins() {
	p.pins.release()
	p.pins = heldEntries{}
}

type runtimeBinding struct {
	root  string
	entry entrypointSpec
	check func() error
}

func removeArtifactDir(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func (p *ActivePlugin) Restarts() int {
	if p.sup == nil {
		return 0
	}
	return p.sup.Restarts()
}

func (p *ActivePlugin) SupervisedPid() int {
	if p.sup == nil {
		return 0
	}
	return p.sup.Pid()
}

func (p *ActivePlugin) KillSupervisedChild() (bool, error) {
	if p.sup == nil {
		return false, nil
	}
	return p.sup.KillChild()
}

func signedCapabilitySurface(m *packagefmt.Manifest, variant *packagefmt.Variant, res *packagefmt.Result) []string {
	var envelope, variantCaps []string

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

func loadVerifiedMember(pkgPath string, res *packagefmt.Result, held map[string][]byte, rel string) ([]byte, error) {
	if _, ok := res.FileDigests[rel]; !ok {
		return nil, &EntrypointDigestError{Member: rel}
	}
	raw, ok := held[rel]
	if !ok {
		if err := walkPackage(pkgPath, []string{rel}, func(_ string, b []byte) error {
			raw = b
			return nil
		}); err != nil {
			return nil, err
		}
	}
	if want, got, ok := res.MemberDigest(rel, raw); !ok {
		return nil, &EntrypointDigestError{Member: rel, Want: want, Got: got}
	}
	return raw, nil
}

var walkPackage = packagefmt.ReadMembers

func walkMembers(pkgPath string, res *packagefmt.Result, held map[string][]byte, rels ...string) map[string][]byte {
	if held == nil {
		held = make(map[string][]byte)
	}
	var want []string
	for _, rel := range rels {
		_, listed := res.FileDigests[rel]
		if _, taken := held[rel]; listed && !taken {
			want = append(want, rel)
		}
	}
	if len(want) > 0 {
		_ = walkPackage(pkgPath, want, func(rel string, raw []byte) error {
			held[rel] = raw
			return nil
		})
	}
	return held
}

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

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func excerpt(b []byte) string {
	const max = 256
	if len(b) > max {
		return string(b[:max]) + fmt.Sprintf("… (%d bytes)", len(b))
	}
	return string(b)
}

func modelNames(decls []ModelDecl) []string {
	out := make([]string, 0, len(decls))
	for _, d := range decls {
		out = append(out, d.Name)
	}
	return out
}

func logDecision(format string, args ...any) {
	logsink.Info("plugins.decision", format, args...)
}

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

type ActProposal struct {
	Plugin, Tool, Operation, Summary, Effects string
	Args                                      map[string]any
}

type ActProposer interface {
	Propose(ctx context.Context, p ActProposal) (id string, err error)
}

type StandingConfirmer interface {
	StandingConfirmation(plugin, operation string) (OperatorAct, bool)
}

type OperationGate interface {
	AdmitOperation(plugin, operation, effects string) error
}

const ReasonOperatorGrant = "OPERATOR_GRANT_REFUSES"

type OperatorAct struct {
	ID          string
	ConfirmedAt time.Time
}

type operatorActKey struct{}

func WithOperatorAct(ctx context.Context, act OperatorAct) context.Context {
	return context.WithValue(ctx, operatorActKey{}, act)
}

func OperatorActFrom(ctx context.Context) *OperatorAct {
	if act, ok := ctx.Value(operatorActKey{}).(OperatorAct); ok && act.ID != "" {
		return &act
	}
	return nil
}
