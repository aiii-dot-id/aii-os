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
package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/foreground"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
type App struct {
	// .
	// .
	// .
	// .
	dashboardAccessToken string
	dashboardTokenMu     sync.Mutex

	// .
	// .
	// .
	// .
	mintedTokenMu sync.Mutex
	mintedToken   string

	cfg *Config

	// .
	// .
	// .
	uiLayoutFilePath string
	uiLayoutPathOnce sync.Once

	// .
	dashboard *dashboard.Server
	// .
	// .

	// .
	// .
	logSink *logsink.Sink

	// .
	mode modeState

	// .
	// .
	// .
	promptGate *prompt.Gate

	// .
	// .
	// .
	// .
	ring5Policy *firewall.Policy

	// .
	// .
	// .
	birthMu sync.Mutex

	// .
	ring0Content  string
	ring0Bundle   []byte
	ring5Content  string
	bootstrapText string
	genesisClient *genesis.GenesisClient

	// .
	keyPair   *crypto.KeyPair
	ledger    *ledger.Ledger
	store     *store.Store
	rings     *ring.Manager
	engine    *identity.Engine
	projects  *project.Manager
	composer  *prompt.Composer
	llmClient *llm.Client
	toolReg   *tools.Registry

	// .
	// .
	// .
	plugins       []*pluginhost.ActivePlugin
	retiring      []*pluginhost.ActivePlugin
	audio         *audio.Plane
	audioOnce     sync.Once
	voiceSeq      atomic.Uint64
	voiceModes    sync.Map
	voiceSessions sync.Map
	// .
	// .
	// .
	voiceModePub atomic.Pointer[VoiceModeConfig]
	// .
	// .
	// .
	voiceSpeakOffRev atomic.Uint64
	// .
	// .
	// .
	// .
	voicePending sync.Map
	// .
	// .
	// .
	// .
	speakerPending sync.Map
	acts           pluginActs
	// .
	asks askRegistry
	// .
	// .
	// .
	voiceSafeDropped atomic.Uint64
	// .
	// .
	voiceEventSink    func(dashboard.VoiceEvent)
	voiceFanDropped   atomic.Uint64
	voiceStaleReplies atomic.Uint64
	voiceReplySink    func(dashboard.VoiceReplyRef, string)
	voiceHushSink     func(dashboard.VoiceHush)
	// .
	speakerWithheldFinals   atomic.Uint64
	speakerWithheldPartials atomic.Uint64
	spokenMu                sync.Mutex
	spoken                  map[string]*spokenReply
	speechReserveMu         sync.Mutex
	speechReservedChars     int
	speechReservedHeard     time.Duration
	voiceObs                map[*pluginhost.VoiceSession]bool
	voiceObsMu              sync.Mutex
	pluginLife              map[string]pluginLifecycle
	pluginMu                sync.Mutex
	activeMeta              map[string]activePkgMeta
	sweepPoke               chan struct{}
	restartCh               chan struct{}
	restartOnce             sync.Once
	restarting              atomic.Bool
	stageOnce               sync.Once
	stageWhy                string
	pluginToolReg           *tools.Registry
	facility                *pluginfacility.Facility
	facilityOnce            sync.Once
	policyRev               uint64
	policyFinger            string
	trustGen                uint64
	// .
	subMu        sync.RWMutex
	subscribers  map[string]*pluginSubscriber
	pluginOpts   *pluginhost.Options
	catalogMu    sync.RWMutex
	catalog      *pluginhost.Catalog
	catalogDir   string
	pkgFetch     packageFetch
	catalogCache string
	catalogRoot  *sigenvelope.PublicKeyEnvelope
	catalogFetch func(ctx context.Context, url string, max int64) ([]byte, error)
	modelFetch   pluginhost.ModelFetcher
	catalogPoke  chan struct{}
	catalogAt    string
	catalogErr   string
	// .
	// .
	// .
	trustDir    string
	trustGuard  packagefmt.EpochGuard
	trustFinger string

	// .
	// .
	// .
	// .
	sections    *sections.Registry
	sectionActs []*sections.Section
	uiLayoutMu  sync.Mutex
	uiLayoutRaw []byte

	// .
	// .
	uiThemeMu  sync.Mutex
	uiThemeRaw []byte

	// .
	// .
	toolEmitMu sync.Mutex
	turnCostMu sync.Mutex
	lastTurn   string
	toolEmit   func(kind, name, args string)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	turnMeterMu sync.Mutex
	// .
	// .
	// .
	contChain int
	// .
	// .
	turnContinuation bool
	// .
	// .
	// .
	// .
	planningBrief bool
	// .
	// .
	// .
	// .
	// .
	askMu         sync.Mutex
	askYields     int
	askFleetSpent bool
	// .
	// .
	// .
	// .
	// .
	legMu     sync.Mutex
	legMeters map[string]*legMeter
	// .
	// .
	queueWake    func()
	turnCalls    int
	turnReadOnly int
	turnSpawned  int
	// .
	// .
	// .
	turnHarvested int
	// .
	// .
	// .
	turnRounds int
	// .
	// .
	// .
	// .
	// .
	turnPredicted int
	// .
	// .
	turnPredictedOrdinal   int
	turnIndependentOrdinal int
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	turnFirstPredicted int
	// .
	// .
	// .
	composedInterrupted bool

	// .
	// .
	// .
	// .
	bootInterrupted []string

	// .
	// .
	// .
	// .
	// .
	// .
	turnDeclaredOrdinal int

	// .
	// .
	// .
	// .
	turnIndependent int

	// .
	// .
	// .
	// .
	cfgMu sync.RWMutex

	// .
	// .
	// .
	// .
	credMu  sync.Mutex
	credSrc map[string]*oauth.Source
	// .
	signInMu      sync.Mutex
	signIns       map[string]*pendingSignIn
	signInTimeout time.Duration
	// .
	oauthTransport http.RoundTripper
	oauthGuard     func(context.Context, string) error

	// .
	provMu sync.Mutex
	// .
	// .
	// .
	projectMu  sync.Mutex
	provStatus map[string]providerProbe

	// .
	// .
	turnGate chan struct{}

	// .
	// .
	// .
	// .
	// .
	composedUnharvested []string

	// .
	// .
	// .
	outboxPoke chan struct{}

	// .
	// .
	// .
	listening   map[string]*channelListener
	listeningFP string
	// .
	// .
	// .
	// .
	routesMu sync.Mutex
	routes   map[string]channelRoute
	routesFP string
	// .
	wakeParticipantFn func(ctx context.Context, fact string) (string, error)

	// .
	// .
	// .
	// .
	// .
	turnMu sync.Mutex
	steers []steerEntry
	// .
	// .
	turnVoice []*voiceBinding
	// .
	// .
	// .
	voiceReplyShown atomic.Bool
	turnCancel      context.CancelFunc
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	turnFacility bool
	// .
	// .
	steerFlush func([]steerEntry)
	// .
	// .
	// .
	turnFgRelease func()
	// .
	// .
	// .
	fg *foreground.Holds
	// .
	conv *conversation.Loop
	// .
	// .
	// .
	safeTools *safeToolRecord

	// .
	// .
	llmSwap *swappableLLM
	// .
	// .
	// .
	activeProviderMu sync.RWMutex
	activeProvider   providerEntry
	// .
	// .
	// .
	// .
	activeBudget       int
	activeBudgetSource budgetSource
	// .
	// .
	// .
	capMu        sync.RWMutex
	substrateCap substrateCapability
	// .
	// .
	modalities modalityMemo
	// .
	// .
	speechTrouble speechTrouble
	// .
	// .
	// .
	// .
	specMu   sync.Mutex
	specSaid map[string][]speechItem

	// .
	// .
	anchorer     *witness.Anchorer
	witnessProbe *witness.Client

	// .
	// .
	// .
	door           *ledgerAdapter
	pn             publicNameRuntime
	newPublisher   func(cfg Config) (namePublisher, error)
	newCertManager func(cfg certs.Config) (certificateManager, error)

	// .
	// .
	// .
	updateChecker *updates.Checker
	// .
	// .
	// .
	relayTLS *tls.Config

	// .
	timeFac       *cognitive.TIME
	executor      *cognitive.Executor
	timerOwner    *identity.TimerDeliveryOwner
	pulseSource   *dashboardPulse
	briefFacility *cognitive.MorningBriefFacility
	// .
	// .
	runtimeRoots *pluginhost.RuntimeRoots

	reviewFacility atomic.Pointer[cognitive.IdentityReviewFacility]
	bgCtx          context.Context
	bgCancel       context.CancelFunc
	bgMu           sync.Mutex
	bgWG           sync.WaitGroup
	stopping       bool

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	gate *quiesce.Gate

	// .
	// .
	watchEvery time.Duration

	// .
	// .
	// .
	// .
	overlayLast atomic.Pointer[string]

	// .
	// .
	// .
	// .
	// .
	projectsLast atomic.Pointer[string]

	// .
	// .
	// .
	// .
	projectsPush     func(id string, ws *dashboard.WorkspaceState)
	projectsListPush func()

	// .
	// .
	// .
	// .
	// .
	overlayToken atomic.Uint64

	// .
	// .
	// .
	// .
	// .
	wakeMu       sync.Mutex
	platformWake cognitive.PlatformWake

	// .
	// .
	// .
	stopOnce sync.Once

	// .
	live bool
}

// .
func New(cfg *Config) *App {
	turnGate := make(chan struct{}, 1)
	turnGate <- struct{}{}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	a := &App{
		fg:        &foreground.Holds{},
		restartCh: make(chan struct{}),
		cfg:       cfg, gate: quiesce.NewGate(), turnGate: turnGate,
		outboxPoke: make(chan struct{}, 1), listening: map[string]*channelListener{},
		bgCtx: bgCtx, bgCancel: bgCancel,
	}
	if cfg != nil {
		a.publishVoiceMode(cfg.Speech.Mode)
	}
	return a
}

func (a *App) acquireTurn(ctx context.Context) error {
	if a.turnGate == nil {
		return errors.New("application turn gate is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.turnGate:
		if err := ctx.Err(); err != nil {
			a.releaseTurn()
			return err
		}
		a.holdTurnForeground()
		a.emitPluginEvent(pluginhost.TopicTurnStarted, nil)
		return nil
	}
}

// .
// .
// .
// .
func (a *App) holdTurnForeground() {
	rel := a.fg.Acquire("turn")
	a.turnMu.Lock()
	if a.turnFgRelease != nil {
		a.turnFgRelease()
	}
	a.turnFgRelease = rel
	a.turnMu.Unlock()
}

// .
// .
// .
func (a *App) SubscribeForegroundNeed(fn func(needed bool, reason string)) {
	a.fg.Subscribe(fn)
}

func (a *App) releaseTurn() {
	a.emitPluginEvent(pluginhost.TopicTurnEnded, nil)
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
	a.turnMu.Lock()
	a.turnFacility = false
	fgRel := a.turnFgRelease
	a.turnFgRelease = nil
	// .
	// .
	// .
	// .
	unanswered := a.turnVoice
	a.turnVoice = nil
	a.turnMu.Unlock()
	if fgRel != nil {
		fgRel()
	}
	for _, b := range unanswered {
		b.release("the turn ended without answering")
	}
	a.turnGate <- struct{}{}
	// .
	// .
	// .
	a.pokeOutbox()
	// .
	// .
	// .
	// .
	a.turnMu.Lock()
	leftovers := a.steers
	a.steers = nil
	a.turnMu.Unlock()
	if len(leftovers) == 0 {
		return
	}
	if a.steerFlush != nil {
		releaseVoice(leftovers, "flushed by the test hook")
		a.steerFlush(leftovers)
		return
	}
	go a.runLeftoverSteerTurn(leftovers)
}

// .
// .
// .
// .
// .
func (a *App) runLeftoverSteerTurn(entries []steerEntry) {
	// .
	// .
	// .
	// .
	if a.conv == nil || a.engine == nil || a.store == nil {
		log.Printf("steering: %d leftover message(s) arrived before the runtime could run turns — dropped", len(entries))
		releaseVoice(entries, "dropped: the runtime could not run turns")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := a.acquireTurn(ctx); err != nil {
		log.Printf("steering: %d leftover message(s) could not open their turn: %v", len(entries), err)
		releaseVoice(entries, "the turn could not open")
		return
	}
	defer a.releaseTurn()
	ctx, done := a.beginCancellableTurn(ctx)
	defer done()
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		if a.engine != nil {
			if err := a.engine.RecordConversationTurn(e.role, e.content); err != nil {
				log.Printf("steering: leftover turn not recorded: %v", err)
			}
		}
		parts = append(parts, e.content)
	}
	log.Printf("steering: %d leftover message(s) opened their own turn", len(entries))
	for _, e := range entries {
		a.holdVoice(e.voice)
	}
	resp, err := a.runTurnLocked(ctx, strings.Join(parts, "\n\n"))
	if err != nil {
		log.Printf("steering: leftover turn failed: %v", err)
		if a.dashboard != nil {
			a.dashboard.BroadcastResponse("system", "Your queued message could not run: "+err.Error())
		}
		return
	}
	if a.dashboard != nil && !a.voiceReplyShown.Swap(false) {
		a.dashboard.BroadcastResponse("identity", resp)
	}
}

// .
// .
func (a *App) pokeOutbox() {
	select {
	case a.outboxPoke <- struct{}{}:
	default:
	}
}

func (a *App) runBackground(run func()) bool {
	a.bgMu.Lock()
	defer a.bgMu.Unlock()
	if a.stopping {
		return false
	}
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		run()
	}()
	return true
}

// .
// .
// .
func (a *App) StartEmbedded() error {
	choice, err := a.chooseBoot()
	if err != nil {
		return err
	}
	if choice == bootFirstboot {
		return a.startFirstboot()
	}
	return a.startLive()
}

// .
// .
// .
func (a *App) DashboardURL() string {
	d := a.configSnapshot().Dashboard
	return dashboard.LoopbackURL(d.TLS, d.Port)
}

// .
// .
// .
func (a *App) TimeWake() {
	a.wakeMu.Lock()
	tf := a.timeFac
	a.wakeMu.Unlock()
	if tf != nil {
		tf.TimeWake()
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
func (a *App) SetPlatformWake(w cognitive.PlatformWake) {
	a.wakeMu.Lock()
	a.platformWake = w
	// .
	// .
	// .
	tf := a.timeFac
	a.wakeMu.Unlock()
	if tf != nil {
		tf.SetPlatformWake(w)
		tf.TimeWake()
	}
}

// .
// .
// .
func (a *App) installPlatformWake() {
	a.wakeMu.Lock()
	w := a.platformWake
	a.wakeMu.Unlock()
	if w == nil {
		a.timeFac.SetPlatformWake(cognitive.NoopWake{})
		return
	}
	a.timeFac.SetPlatformWake(w)
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
func (a *App) SetForeground(live bool) {
	if a.pulseSource != nil {
		a.pulseSource.setOverride(live)
	}
	if live {
		a.gate.Resume()
	} else {
		a.gate.Pause()
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
func (a *App) operatorPresent() bool {
	if a.pulseSource != nil {
		return a.pulseSource.Live()
	}
	if a.dashboard != nil {
		return a.dashboard.SessionLive()
	}
	return false
}

// .
func (a *App) Run() {
	// .
	// .
	// .
	// .
	// .
	home, herr := filepath.Abs(filepath.Dir(a.cfg.Identity.LedgerPath))
	if herr != nil {
		home = filepath.Dir(a.cfg.Identity.LedgerPath)
	}
	cfgPath, cerr := filepath.Abs(a.cfg.SourcePath)
	if cerr != nil {
		cfgPath = a.cfg.SourcePath
	}
	// .
	// .
	// .
	// .
	// .
	a.installLogSink()
	log.Printf("Identity home: %s (config %s)", home, cfgPath)
	// .
	// .
	// .
	// .
	// .
	log.Printf("Boot identity: AII OS v%s (build %s)", VersionString(), BuildIdentity())
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	a.seedSkillsDoc()
	a.seedMethodDoc()

	// .
	// .
	// .
	choice, err := a.chooseBoot()
	if err != nil {
		log.Fatalf("Startup refused: %v", err)
	}
	// .
	// .
	// .
	// .
	// .
	stopCh := watchStopRequest()

	if choice == bootFirstboot {
		if err := a.startFirstboot(); err != nil {
			log.Fatalf("Startup failed: %v", err)
		}
	} else {
		if err := a.startLive(); err != nil {
			log.Fatalf("Startup failed: %v", err)
		}
	}

	// .
	// .
	// .
	// .
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigChan:
	case <-stopCh:
	case <-a.restartCh:
	}

	a.Stop()
	if a.restarting.Load() {
		relaunch()
	}
}

// .
// .
// .
func (a *App) Restart() error {
	a.restartOnce.Do(func() {
		a.restarting.Store(true)
		if a.restartCh != nil {
			close(a.restartCh)
		}
	})
	return nil
}

// .
func (a *App) Stop() {
	a.stopOnce.Do(a.stop)
}

func (a *App) stop() {
	log.Println("Shutting down...")

	a.stopSafeBeacon()
	a.bgMu.Lock()
	a.stopping = true
	if a.bgCancel != nil {
		a.bgCancel()
	}
	a.bgMu.Unlock()
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	a.turnMu.Lock()
	if a.turnCancel != nil {
		a.turnCancel()
	}
	a.turnMu.Unlock()
	a.bgWG.Wait()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if a.dashboard != nil {
		if err := a.dashboard.Shutdown(shutCtx); err != nil {
			log.Printf("dashboard shutdown: %v", err)
		}
	}
	a.birthMu.Lock()
	defer a.birthMu.Unlock()
	if err := a.acquireTurn(shutCtx); err != nil {
		log.Printf("live runtime left open: resident turn did not quiesce: %v", err)
		return
	}
	if err := a.closeLiveResources(); err != nil {
		log.Printf("live runtime shutdown: %v", err)
	}
	a.releaseTurn()
	// .
	// .
	if a.logSink != nil {
		a.logSink.Close()
		a.logSink = nil
	}
}

// .
// .
// .
func (a *App) closeLiveResources() error {
	a.wakeMu.Lock()
	timeFac := a.timeFac
	a.timeFac = nil
	a.wakeMu.Unlock()
	if timeFac != nil {
		timeFac.Stop()
	}
	if a.executor != nil {
		a.executor.Stop()
		a.executor = nil
	}
	if a.timerOwner != nil {
		a.timerOwner.Stop()
		a.timerOwner = nil
	}

	// .
	// .
	// .
	// .
	if a.facility != nil {
		a.facility.Close()
	}

	a.pluginMu.Lock()
	plugins := a.plugins
	sectionActs := a.sectionActs
	a.plugins = nil
	a.sectionActs = nil
	a.activeMeta = nil
	retiring := a.retiring
	a.retiring = nil
	a.pluginMu.Unlock()

	var errs []error
	// .
	// .
	// .
	for _, ap := range retiring {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := ap.CloseQuiet(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop pinned predecessor %s: %w", ap.ID, err))
		}
		cancel()
	}
	for _, ap := range plugins {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := ap.Deactivate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("deactivate plugin %s: %w", ap.ID, err))
		}
		cancel()
	}
	for _, sec := range sectionActs {
		if a.sections != nil {
			a.sections.RemoveOwned(sec)
		}
		if err := sec.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close section %s: %w", sec.Decl.ID, err))
		}
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close projection store: %w", err))
		}
		a.store = nil
	}
	if a.ledger != nil {
		if err := a.ledger.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close ledger: %w", err))
		}
		a.ledger = nil
	}
	a.live = false
	return errors.Join(errs...)
}

// .
func (a *App) ensureRing5Policy() *firewall.Policy {
	if a.ring5Policy == nil {
		a.ring5Policy = firewall.DefaultPolicy()
	}
	return a.ring5Policy
}

// .
// .
// .
// .
func (a *App) loadRing5() {
	if a.ring5Content == "" {
		a.rings.Set(ring.Ring5, nil)
		log.Print("Ring 5 unavailable — no posture content loaded")
		return
	}
	policy := a.ensureRing5Policy()
	content := a.ring5Content + "\n\n" + policy.LocalFloor()
	// .
	// .
	// .
	// .
	if a.toolReg != nil {
		if _, extra := a.toolReg.Roots(); len(extra) > 0 {
			content += "\n\nGranted roots — your operator has widened your reach. You may also read and work under these directories (use ABSOLUTE paths):\n"
			for _, root := range extra {
				content += "- " + root + "\n"
			}
		}
	}
	a.rings.Set(ring.Ring5, &ring.RingContent{
		Level:   ring.Ring5,
		Content: content,
	})

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	log.Printf("Ring 5 loaded: platform bundle + local floor (%d bytes)", len(content))
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
func tlsDirFor(cfg Config) string {
	if !cfg.Dashboard.TLS {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "tls")
}

// .
func (a *App) startLive() (retErr error) {
	cfg := a.configSnapshot()
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, a.closeLiveResources())
		}
	}()
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if sr := hostcap.Can(hostcap.SelfReplace); sr.Available {
		if err := afterRollback(updates.CheckRollback(filepath.Dir(cfg.Identity.LedgerPath)), reexecSelf); err != nil {
			return err
		}
	} else {
		log.Printf("updates: rollback machinery idle on this host — %s", sr.Reason)
	}

	// .
	kp, err := crypto.LoadKeyPair(cfg.Identity.KeyPath)
	if err != nil {
		return fmt.Errorf("load identity key: %w", err)
	}
	a.keyPair = kp

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	lg, err := ledger.New(cfg.Identity.LedgerPath)
	if err != nil {
		if errors.Is(err, ledger.ErrRecordUnreadable) {
			return a.startSafeBoot(fmt.Sprintf("the ledger could not be read at startup: %v", err))
		}
		return fmt.Errorf("open ledger: %w", err)
	}
	// .
	// .
	a.ledger = lg

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
	heads, err := a.bootHeadVerifier(cfg)
	if err != nil {
		log.Printf("WITNESS KEYS BESIDE THE LEDGER DO NOT VERIFY — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(fmt.Sprintf("witness keys beside the ledger do not verify: %v", err))
	}
	if _, err := ledger.VerifyChain(cfg.Identity.LedgerPath, kp.PublicKeyBytes(), heads); err != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		log.Printf("LEDGER CHAIN VERIFICATION FAILED — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(fmt.Sprintf("chain verification failed at startup: %v", err))
	}
	if heads.Unverified() > 0 {
		log.Printf("BOOT: %d witness heads in the tail carry receipts under keys not persisted beside the ledger — accepted on the identity's proof; they will not seal", heads.Unverified())
	}

	// .
	// .
	// .
	// .
	if err := witness.CheckLocalTail(filepath.Dir(cfg.Identity.LedgerPath), lg); err != nil {
		log.Printf("WITNESS TAIL CHECK FAILED — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(fmt.Sprintf("witness-tail check failed at startup: %v", err))
	}

	// .
	// .
	// .
	if a.ring5Content == "" {
		if cfg.Genesis.FirewallURL == "" {
			return a.startSafeBoot("security_posture.absent: no Ring 5 server is configured")
		}
		gc := genesis.NewClient(cfg.Genesis.ServerURL, cfg.Genesis.FirewallURL, cfg.Genesis.BootstrapURL)
		r5, err := gc.FetchRing5()
		if err != nil {
			return a.startSafeBoot(fmt.Sprintf("security_posture.verify_fail: required Ring 5 unavailable or unverifiable: %v", err))
		}
		a.ring5Content = r5.Content
		log.Printf("Ring 5 fetched from %s (%d bytes)", cfg.Genesis.FirewallURL, len(a.ring5Content))
	}

	// .
	st, err := store.New(cfg.Identity.DBPath)
	if err != nil {
		// .
		// .
		// .
		// .
		// .
		var shape *store.ShapeError
		if errors.As(err, &shape) {
			log.Printf("PROJECTION SHAPE DISAGREES WITH THE CODE — entering BOOT-SAFE (minimal, read-only): %v", err)
			return a.startSafeBoot(fmt.Sprintf("projection mirror could not be read before ledger replay: %v.%s", err, rebuildRemedy(cfg)))
		}
		return fmt.Errorf("open database: %w", err)
	}
	a.store = st
	st.SetWorkObserver(a.workObserved)
	st.SetOperatorASCII(cfg.Dashboard.OperatorASCII != nil && *cfg.Dashboard.OperatorASCII)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	mseq, err := st.MaxLedgerSeq()
	if err != nil {
		st.Close()
		a.store = nil
		log.Printf("PROJECTION MIRROR READ FAILED — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(fmt.Sprintf("projection mirror could not be read before ledger replay: %v", err))
	}
	if mseq > lg.LastSeq() {
		st.Close()
		a.store = nil
		log.Printf("LEDGER BEHIND ITS OWN PROJECTION — entering BOOT-SAFE (minimal, read-only): mirror seq %d, ledger seq %d", mseq, lg.LastSeq())
		return a.startSafeBoot(fmt.Sprintf("ledger ends at seq %d but the projection mirror acknowledged seq %d — events this runtime accepted are missing (torn-tail quarantine beside the ledger holds the damaged bytes)", lg.LastSeq(), mseq))
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := st.ReplayFromFile(cfg.Identity.LedgerPath); err != nil {
		st.Close()
		a.store = nil
		log.Printf("LEDGER REPLAY FAILED — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(fmt.Sprintf("ledger replay failed at startup — projection rebuild refused, prior projection preserved: %v", err))
	}

	// .
	// .
	// .
	a.wakeMu.Lock()
	a.timeFac = cognitive.NewTIME(st, st)
	a.wakeMu.Unlock()
	a.timeFac.SetSafeSource(func() bool { _, s := a.SafeMode(); return s })
	if err := a.timeFac.DeleteLegacyAlarm("heartbeat", "retired mechanism: heartbeat is a goroutine ticker, not an alarm"); err != nil {
		return fmt.Errorf("remove retired heartbeat alarm: %w", err)
	}

	// .
	if a.rings == nil {
		a.rings = ring.NewManager()
	}
	rc, err := genesis.LoadRing0(lg)
	if err != nil {
		return fmt.Errorf("load Ring 0: %w", err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if a.keyPair == nil {
		return fmt.Errorf("load Ring 0: no identity key to verify the constitution against")
	}
	if err := a.rings.SealConstitution(rc, a.keyPair.PublicKeyBytes()); err != nil {
		return a.startSafeBoot(fmt.Sprintf("the Ring 0 constitution could not be installed: %v", err))
	}

	// .
	// .
	// .
	// .
	snaps, err := st.RingSnapshots()
	if err != nil {
		return fmt.Errorf("load ring snapshots: %w", err)
	}
	for _, sn := range snaps {
		if sn.Section == "__brief__" {
			continue
		}
		a.rings.SetSection(ring.RingLevel(sn.RingLevel), sn.Section, sn.Content)
	}
	if len(snaps) > 0 {
		log.Printf("Ring snapshots restored: %d sections", len(snaps))
	}
	brief, err := st.GetBrief()
	if err != nil {
		return fmt.Errorf("load morning brief: %w", err)
	}
	if brief != "" {
		a.rings.SetBrief(brief)
	}

	// .
	a.loadRing5()

	// .
	// .
	// .
	// .
	a.askWindowIfUndeclared(cfg.LLM)
	cc, llmEntry, err := a.resolveLLM()
	if err != nil {
		return fmt.Errorf("LLM substrate: %w", err)
	}
	if cc.APIKey == "" && cc.Credential == nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		log.Printf("LLM: no API key on provider %q — expected for a local endpoint; if this provider requires one, chat will fail at call time with the provider's own error (set it in the dashboard, or export %s)", llmEntry.Name, cfg.LLM.APIKeyEnv)
	}
	lg.SetModelID(cc.Model)
	// .
	// .
	promptBudget := a.rememberPromptBudget(llmEntry, cfg.Prompt.MaxTokens)
	_, budgetSrc := a.currentPromptBudget()

	// .
	a.llmClient = a.newLLMClient(cc, promptBudget)
	a.llmSwap = newSwappableLLM(a.llmClient)

	toolReg := tools.NewRegistry(cfg.Tools.CWD, a.ensureRing5Policy(), tools.Timeouts{
		ShellSeconds:    cfg.Tools.ShellTimeoutSeconds,
		WebFetchSeconds: cfg.Tools.WebFetchTimeoutSeconds,
	})
	a.applyLocalFetch(cfg, toolReg)
	// .
	// .
	toolReg.SetSafeSource(a.SafeMode)
	for _, name := range cfg.Tools.Disabled {
		toolReg.SetToolEnabled(name, false)
		log.Printf("Ring 5: tool %q disabled by operator config", name)
	}
	a.toolReg = toolReg

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if names, aerr := st.AbandonUnfinishedToolCalls(); aerr != nil {
		log.Printf("Warning: could not reconcile in-flight tool record: %v", aerr)
	} else if len(names) > 0 {
		log.Printf("BOOT: %d tool call(s) were in flight at last shutdown: %s — side effects unverified", len(names), strings.Join(names, ", "))
		a.bootInterrupted = names
	}

	// .
	// .
	// .
	a.turnContinuation = agencyOn(cfg.Agency.TurnContinuation)
	a.planningBrief = planningBriefEnabled(cfg)
	planState, fanoutState, predictedNow, calibration := a.residentLoopHooks(cfg)
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st: st}, appEmitter{a: a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,

			ContextBudgetTokens: promptBudget,
			// .
			ContextBudgetFallback: budgetSrc == budgetFallback,
			ThinkingBudget:        llmEntry.ThinkingBudget,
			TurnTokenBudget:       cfg.Agency.TurnTokenBudget,
			BreadthNudge:          cfg.Agency.BreadthNudge,
			// .
			// .
			// .
			HeuristicNudges: heuristicNudgesOn(cfg.Agency.HeuristicNudges),
			// .
			// .
			// .
			// .
			PlanState:         planState,
			FanoutState:       fanoutState,
			PredictedThisTurn: predictedNow,
			Calibration:       calibration,
			IsAct:             a.firstActHook(cfg),
			ReplaySafe:        replaySafeHook(toolReg),
		})
	// .
	// .
	// .
	a.conv.SetSteering(a)

	a.promptGate = prompt.NewGate(appRingSource{rm: a.rings, priorities: a.store}, promptBudget)
	a.composer = prompt.New(a.rings, promptBudget)
	a.composer.SetIdentitySource(st)
	// .
	// .
	// .
	a.composer.SetName(a.store.IdentityName())
	// .
	// .
	a.composer.SetPluginOperations(toolReg.HasDynamic)

	toolReg.SetProtectedPaths([]string{
		cfg.Identity.LedgerPath, cfg.Identity.KeyPath, cfg.Identity.DBPath, cfg.SourcePath,
	})
	toolReg.SetExtraRoots(cfg.Tools.ExtraRoots)
	if len(cfg.Tools.ExtraRoots) > 0 {
		a.loadRing5()
	}
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st, onIntegrity: func(err error) { a.enterSafe(err.Error()) }, onAppend: a.ledgerAppended}
	a.door = door

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
	pluginOpts, err := a.buildPluginOptions(st, toolReg, door)
	if err != nil {
		// .
		// .
		// .
		log.Printf("plugins: broker/trust-root config REFUSED, all plugins run quarantined: %v", err)
		pluginOpts = nil
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	a.sections = sections.NewRegistry()
	a.sections.SetSafeSource(a.SafeMode)
	// .
	// .
	// .
	// .
	// .
	// .
	a.pluginToolReg = toolReg
	a.pluginOpts = pluginOpts
	a.rescanPlugins(a.bgCtx)

	// .
	// .
	// .
	if ds := cfg.Plugins.DevSection; ds != nil {
		if reason, safe := a.SafeMode(); safe {
			log.Printf("dev section %q: REFUSED — runtime is in SAFE mode (%s); unverified bytes stay off the screen", ds.ID, reason)
		} else if sec, derr := sections.ActivateDev(ds.ID, ds.Path); derr != nil {
			log.Printf("dev section %q: REFUSED, skipped: %v", ds.ID, derr)
		} else if rerr := a.sections.Register(sec); rerr != nil {
			log.Printf("dev section %q: registration REFUSED: %v", ds.ID, rerr)
		} else {
			a.pluginMu.Lock()
			a.sectionActs = append(a.sectionActs, sec)
			a.pluginMu.Unlock()
			log.Printf("dev section %q serving UNVERIFIED from %s (banner on, cache off, SAFE refuses)", ds.ID, ds.Path)
		}
	}

	a.engine = identity.NewEngine(st, door, a.rings, toolDiscovererAdapter{toolReg})

	// .
	// .
	projRoot := cfg.Projects.Root
	if projRoot == "" {
		projRoot = filepath.Join(cfg.Tools.CWD, "projects")
	}
	a.projects = project.NewManager(projRoot)
	a.engine.SetProjects(projectsAdapter{a})
	a.engine.SetVoice(voiceModeAdapter{a})
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if p, why := a.activeOpenProject(); p == nil && why != "" {
		log.Printf("restored project focus dropped — %s", why)
		_ = a.store.SetActiveProject("")
	}
	// .
	// .
	// .
	// .
	a.timeFac.SetFireObserver(func(owner, alarmID string, accepted bool) {
		a.emitPluginEvent(pluginhost.TopicAlarmFired, map[string]interface{}{"owner": owner, "alarm_id": alarmID, "accepted": accepted})
	})
	a.timeFac.RegisterOwner(maintenanceOwner{a})
	a.timeFac.RegisterOwner(certificateOwner{a})
	a.timeFac.RegisterOwner(memoryOwner{a})

	timerOwner := identity.NewTimerDeliveryOwner(a.engine)
	timerOwner.OnWake = a.wakeTimerAlarm
	a.timerOwner = timerOwner
	a.timeFac.RegisterOwner(timerOwner)
	a.engine.SetTimers(appTimers{time: a.timeFac, read: identity.NewStoreTimers(st)})
	a.engine.SetEmbedder(memoryEmbedder{a})
	a.engine.SetReachable(func(name string) bool { return len(a.reachFor(name)) > 0 })
	a.engine.SetAskProposer(a.proposeAsk)
	// .
	// .
	toolReg.ObserveFetches(a.engine.NoteExternalFetch)

	// .
	// .
	// .
	// .
	// .
	if reason, safe := a.SafeMode(); safe {
		a.applySafeState(reason)
	}

	// .
	if a.dashboard == nil {
		d, derr := a.newDashboard(a.buildLiveHandler())
		if derr != nil {
			return fmt.Errorf("dashboard credentials: %w", derr)
		}
		a.dashboard = d
		a.dashboard.SetQuiesceGate(a.gate)
		a.dashboard.SetWebhookHandler(a.handleWebhook)
		// .
		// .
		// .
		// .
		a.projectsPush = func(id string, ws *dashboard.WorkspaceState) {
			a.dashboard.PushWorkspace(id, ws)
		}
		a.projectsListPush = a.dashboard.BroadcastProjects
		a.voiceEventSink = a.dashboard.BroadcastVoiceEvent
		a.voiceReplySink = a.dashboard.BroadcastVoiceReply
		a.voiceHushSink = a.dashboard.BroadcastVoiceHush
		_, err := a.dashboard.Start(tlsDirFor(cfg))
		if err != nil {
			return fmt.Errorf("dashboard start: %w", err)
		}
		fmt.Printf("AII OS — %s\n", a.resolveDisplayName())
		a.printDashboardURLs()
		fmt.Printf("Ledger seq: %d\n", lg.LastSeq())
	}

	// .
	// .
	// .
	a.dashboard.SetSections(a.sections)
	a.dashboard.SetLayoutSource(a.currentUILayout)
	a.loadUILayout(false)
	a.dashboard.SetThemeSource(a.currentUITheme)
	a.loadUITheme(false)
	// .
	// .
	// .
	// .
	a.dashboard.SetUIOverlay(a.uiOverlayDir())
	// .
	// .
	// .
	a.seedOverlayREADME()
	// .
	// .
	// .
	// .
	a.dashboard.SetBuildStamp(BuildIdentity())

	// .
	// .
	// .
	// .
	// .
	a.store.OnOutboxWrite(a.dashboard.PokeOutbox)

	// .
	// .
	// .
	// .
	witnessClient := witness.New(cfg.Witness.URL, cfg.Witness.TLSSPKISHA256)
	a.witnessProbe = witnessClient
	// .
	// .
	// .
	// .
	// .
	witnessClient.SetGenesisURL(cfg.Genesis.ServerURL)
	anchorer := witness.NewAnchorer(witnessClient, lg, witness.AsIdentityKey(kp), a.store, a.store,
		witnessMinter{door: door},
		cfg.Witness.IntervalEvents, cfg.Witness.PlatformPubkeyPath)
	// .
	// .
	anchorer.SetSealer(lg)
	anchorer.SetOnIntegrityConflict(func(ce *witness.ConflictError) {
		// .
		// .
		log.Printf("WITNESS INTEGRITY CONFLICT — entering SAFE MODE: %v", ce)
		a.enterSafe(fmt.Sprintf("witness rollback/fork conflict: %v", ce))
	})
	if cfg.Witness.URL != "" {
		a.anchorer = anchorer
	}

	// .
	bgCtx := a.bgCtx
	if bgCtx == nil {
		bgCtx, a.bgCancel = context.WithCancel(context.Background())
		a.bgCtx = bgCtx
	}
	a.warnTempHome(cfg.Identity.LedgerPath)
	a.snapshotUILayoutPath(cfg.Identity.LedgerPath)

	// .
	// .
	// .
	a.executor = cognitive.NewExecutor(st)
	a.executor.SetHolds(a.fg)
	a.executor.SetWorkers(cfg.Agency.QueueWorkers)
	a.executor.SetQuiesceGate(a.gate)
	a.executor.RegisterHandler(&alarmHandler{time: a.timeFac})
	a.executor.RegisterHandler(&subagentHandler{a: a})
	a.engine.SetWorkWake(a.executor.Wake)
	a.queueWake = a.executor.Wake
	if agencyOn(cfg.Agency.YieldAnswer) {
		a.engine.SetYieldGate(a.yieldGate)
	}
	a.engine.SetSpawnQueue(agencyOn(cfg.Agency.SpawnQueue))
	a.engine.SetSpawnBudget(cfg.Agency.SubagentMaxToolRounds, cfg.Agency.SubagentMaxToolCalls, cfg.Agency.SubagentMaxLegs)
	if agencyOn(cfg.Agency.SpawnQueue) {
		a.store.SetClaimLimit(identity.SubagentWorkKind, cfg.Agency.MaxParallelSubagents)
	}
	a.engine.SetAgencyLimits(cfg.Agency.MaxSubagentDepth, cfg.Agency.MaxParallelSubagents, cfg.Agency.SubagentMaxMints, cfg.Agency.SubagentWallSeconds)
	a.engine.SetLocalSpawnWall(cfg.Agency.SubagentWallSecondsLocal)
	a.engine.SetRouteIsLocal(a.routeIsLocal)
	a.timeFac.SetAlarmEnqueuer(alarmEnqueuerAdapter{ex: a.executor})

	// .
	// .
	// .
	a.updateChecker = updates.NewChecker(
		func() *sigenvelope.PublicKeyEnvelope {
			root, err := packagefmt.PinnedOrShipped(a.configSnapshot().Plugins.PlatformRoot, packagefmt.KeyTypePlatformRelease)
			if err != nil {
				return nil
			}
			return root
		},
		func() string { return Current() },
		func() bool { return a.configSnapshot().Updates.Automatic },
		func() string { return a.configSnapshot().Updates.Repo },
		a.gate,
		filepath.Dir(cfg.Identity.LedgerPath),
	)
	a.installPlatformWake()
	if err := a.wireCognitive(bgCtx, anchorer, door, cfg); err != nil {
		return err
	}
	// .
	// .
	// .
	a.executor.Start(bgCtx)
	a.startPluginSweep(bgCtx)
	a.runBackground(func() { a.runCatalogRefresh(bgCtx) })
	a.runBackground(func() { a.runOutbox(bgCtx) })
	a.runBackground(func() {
		a.updateChecker.Run(bgCtx, func() bool { _, s := a.SafeMode(); return s }, func() bool { return packagefmt.HostTopology() == "mobile_app_host" })
	})

	a.live = true
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := a.wirePublicName(cfg); err != nil {
		log.Printf("PUBLIC NAME: %v — the name is claimed and the dashboard serves the local certificate until this is fixed", err)
	}
	// .
	// .
	// .
	// .
	// .
	if a.dashboard != nil {
		bootCfg := cfg
		a.runBackground(func() {
			a.autoClaimPublicName(bootCfg)
			// .
			// .
			// .
			a.signalRoute()
			for _, line := range dashboardAdvice(bootCfg.Dashboard.TLS, bootCfg.Dashboard.Host, a.publicNameState(), a.dashboard.TLSMaterial(), bootCfg.Certificate.serverURL() == "") {
				fmt.Println(line)
				log.Printf("dashboard: %s", line)
			}
		})
	}
	a.runBackground(func() { a.watchConfig(cfg.SourcePath) })
	a.runBackground(a.watchUILayout)
	a.runBackground(a.watchUITheme)
	a.runBackground(a.watchUIOverlay)
	a.runBackground(a.watchProjects)
	a.installReloadSignal()

	// .
	// .
	// .
	updates.WriteBootMarker(filepath.Dir(cfg.Identity.LedgerPath))
	return nil
}

// .
func (a *App) wireCognitive(bgCtx context.Context, anchorer *witness.Anchorer, door *ledgerAdapter, cfg Config) error {
	stAdapt := a.store
	llmAdapt := a.llmSwap
	// .
	// .
	ringWriter := cognitive.NewPersistingRingWriter(a.rings, a.store)
	briefWriter := cognitive.NewPersistingBriefWriter(a.rings, a.store)

	if a.timeFac == nil {
		a.wakeMu.Lock()
		a.timeFac = cognitive.NewTIME(stAdapt, stAdapt)
		a.wakeMu.Unlock()
		a.timeFac.SetSafeSource(func() bool { _, s := a.SafeMode(); return s })
	}
	dreamFac := cognitive.NewDream(stAdapt, llmAdapt, door, ringWriter, cognitive.DreamConfig{
		Threshold: 1,
	})
	dreamFac.SetAuthority(ringAuthority{a.promptGate, a.store})
	dreamFac.SetTensions(stAdapt)
	a.timeFac.RegisterOwner(dreamFac)
	consolidateFac := cognitive.NewConsolidate(stAdapt, llmAdapt, door, ringWriter, cognitive.ConsolidateConfig{
		Threshold:     3,
		Salience:      cfg.Memory.Salience,
		Ring3MaxChars: cfg.Prompt.Ring3MaxChars,
	})
	consolidateFac.SetAuthority(ringAuthority{a.promptGate, a.store})
	consolidateFac.SetDecisionLog(a.store)
	a.timeFac.RegisterOwner(consolidateFac)
	selfModelFac := cognitive.NewSelfModel(stAdapt, llmAdapt, selfModelCommitter{engine: a.engine})
	selfModelFac.SetAuthority(ringAuthority{a.promptGate, a.store})
	a.timeFac.RegisterOwner(selfModelFac)
	reviewFac := cognitive.NewIdentityReview(stAdapt, cognitive.IdentityReviewConfig{
		IntervalPulses: 100,
	})
	a.timeFac.RegisterOwner(reviewFac)
	// .
	// .
	// .
	// .
	a.reviewFacility.Store(reviewFac)
	a.briefFacility = cognitive.NewMorningBrief(stAdapt, llmAdapt, briefWriter, cognitive.MorningBriefConfig{
		LocalTime: "07:00",
		Timezone:  cfg.Timezone,
	})
	a.briefFacility.SetAuthority(ringAuthority{a.promptGate, a.store})
	a.briefFacility.SetTurnGate(facilityGate{a})
	a.briefFacility.SetAttention(func(ctx context.Context) ([]memory.AttentionItem, error) {
		return a.engine.Instruments().Attention(ctx, time.Now())
	})
	a.timeFac.RegisterOwner(a.briefFacility)

	// .
	// .
	// .
	// .
	// .
	selfModelFac.SetDoor(door)
	// .
	// .
	// .
	// .
	// .
	if n, err := a.store.SweepOrphanWorkSessions(); err != nil {
		log.Printf("work: orphan sweep failed: %v", err)
	} else if n > 0 {
		log.Printf("work: closed %d work session(s) orphaned by a previous shutdown", n)
	}
	rhythmFac := cognitive.NewRhythm(stAdapt, facilityGate{a}, dreamFac, consolidateFac, selfModelFac, reviewFac)
	rhythmFac.SetDecisionLog(func(facility, decision, reason string) {
		if err := a.store.RecordMemoryDecision(store.MemoryDecision{Kind: "rhythm", Facility: facility, Decision: decision, Record: map[string]interface{}{"reason": reason}}); err != nil {
			log.Printf("RHYTHM: decision not logged: %v", err)
		}
	})
	rhythmFac.SetAttention(a.store, door, func(id, content string) {
		if _, err := a.store.AddOutboxMessageOnce(id, "operator", "", content, nil); err != nil {
			log.Printf("RHYTHM: attention outbox: %v", err)
		}
	})
	a.timeFac.RegisterOwner(rhythmFac)

	// .
	// .
	pulseInterval := 300 * time.Second
	if cfg.Prompt.PulseIntervalSeconds > 0 {
		pulseInterval = time.Duration(cfg.Prompt.PulseIntervalSeconds) * time.Second
	}
	a.pulseSource = &dashboardPulse{live: a.dashboard.SessionLive, active: a.dashboard.OperatorActiveWithin, interval: pulseInterval}
	a.timeFac.StartHeartbeat(a.pulseSource)

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if cfg.Witness.URL != "" {
		a.timeFac.Every("witness", 30*time.Second, func() {
			if a.currentMode() == ModeSafe {
				return
			}
			if _, err := a.witnessProbe.Status(); err != nil {
				a.witnessAttempt(false)
				log.Printf("Witness unreachable: %v", err)
				return
			}
			a.witnessAttempt(true)
			if err := anchorer.CheckAndAnchor(); err != nil {
				log.Printf("Witness: anchor failed (health OK): %v", err)
			}
		})
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if err := a.timeFac.EvaluateAll(bgCtx); err != nil {
		return fmt.Errorf("TIME boot catch-up: %w", err)
	}

	// .
	// .
	// .
	// .
	if err := a.armFacilityAlarms(cfg); err != nil {
		return err
	}

	// .
	// .
	// .
	// .
	a.timeFac.SetQuiesceGate(a.gate)
	a.timeFac.Start(bgCtx)
	return nil
}

// .
// .
// .
// .
func (a *App) armFacilityAlarms(cfg Config) error {
	// .
	// .
	rhythmMs := int64(cfg.Agency.RhythmSeconds) * 1000
	if err := a.timeFac.SetAlarm("rhythm", "rhythm", "wall", time.Now().UTC().UnixMilli()+rhythmMs, &rhythmMs, ""); err != nil {
		return fmt.Errorf("arm cognitive rhythm: %w", err)
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
	life := a.timeFac.LifeClock()
	selfModelEvery, reviewEvery := cognitive.SelfModelCadence, cognitive.IdentityReviewCadence
	if err := a.timeFac.SetAlarm("reflect:self_model", "rhythm", "life", life+selfModelEvery, &selfModelEvery, ""); err != nil {
		return fmt.Errorf("arm self-model reflection: %w", err)
	}
	if err := a.timeFac.SetAlarm("reflect:identity_review", "rhythm", "life", life+reviewEvery, &reviewEvery, ""); err != nil {
		return fmt.Errorf("arm identity review: %w", err)
	}
	if a.briefFacility != nil {
		morningDeadline := a.briefFacility.NextDeadline()
		if err := a.timeFac.SetAlarm("morning_brief", "morning_brief", "wall", morningDeadline, nil, ""); err != nil {
			return fmt.Errorf("arm morning brief: %w", err)
		}
		log.Printf("Cognitive rhythm armed: metabolism every %ds wall-clock, capacity-gated; reflection on lived time (self-model every %d pulses, review every %d — a pulse counts only with operator interaction); morning_brief(%s)",
			cfg.Agency.RhythmSeconds, selfModelEvery, reviewEvery, time.UnixMilli(morningDeadline).UTC().Format("15:04 UTC"))
	}

	// .
	// .
	// .
	// .
	// .
	if err := armMaintenanceAlarm(a.timeFac, time.Now()); err != nil {
		return fmt.Errorf("arm maintenance: %w", err)
	}
	// .
	// .
	if err := a.armMemoryBackfill(); err != nil {
		return fmt.Errorf("arm memory backfill: %w", err)
	}
	return nil
}

// .
func (a *App) buildLiveHandler() *dashboard.WSHandler {
	log.Printf("buildLiveHandler: display name resolves per stats send (file>ledger>config), store=%p, engine=%p", a.store, a.engine)
	return &dashboard.WSHandler{
		Speaker:       "identity",
		GetStats:      a.statsState,
		HandleMessage: a.handleMessage,
		GetOutbox:     a.outboxItems,
		MarkDelivered: func(id string) error {
			return a.engine.MarkDelivered(id, "dashboard")
		},
		RecentTurns: a.recentTurnViews,
		ObserveChat: a.observeChat,
		// .
		// .
		// .
		// .
		// .
		HearUtterance:    a.HearUtterance,
		VoiceConfigured:  a.VoiceConfigured,
		VoiceStatus:      a.VoiceStatus,
		VoiceMode:        a.VoiceMode,
		AudioPlane:       a.AudioPlane,
		VoiceEngine:      a.VoiceEngine,
		VoiceSessionOpen: a.OpenVoiceSession,
		// .
		// .
		AdmitChat:   a.AdmitOperator,
		GetAsks:     a.askViews,
		AnswerAsk:   a.answerAsk,
		GradeResult: a.gradeResult,
		AcquireTurn: a.acquireTurn,
		// .
		// .
		TurnActive:    a.TurnActive,
		Steer:         a.Steer,
		CancelTurn:    a.CancelTurn,
		PendingSteers: a.PendingSteers,
		// .
		// .
		GetIdentity: a.identityState,
		// .
		// .
		Recall: a.recallForDashboard,
		// .
		GetContinuity: a.continuityState,
		// .
		// .
		GetProviders:          a.providerDirectory,
		SignInProvider:        a.SignInProvider,
		CompleteSignIn:        a.CompleteSignIn,
		CancelSignIn:          a.CancelSignIn,
		CancelProfileSignIn:   a.CancelProfileSignIn,
		OAuthCallback:         a.OAuthCallback,
		SignInProfile:         a.SignInProfile,
		CompleteProfileSignIn: a.CompleteProfileSignIn,
		DeviceSignInProfile:   a.DeviceSignInProfile,
		DisconnectProfile:     a.DisconnectProfile,
		SetAuthProfile:        a.SetAuthProfile,
		DeleteAuthProfile:     a.DeleteAuthProfile,
		UpdateCheck:           a.checkForUpdateNow,
		PublicNameClaim:       a.claimPublicName,
		PublicNameRetry:       a.retryPublicCertificate,
		PublicNameMove:        a.movePublicName,
		PublicNameState:       a.publicNameState,
		SetProvider:           a.setProviderInfo,
		SetEffort:             a.setActiveEffort,
		DeleteProvider:        a.deleteProvider,
		RepairProvider:        a.repairBrokenProvider,
		RemoveBrokenProvider:  a.removeBrokenProvider,
		SetSpeechService:      a.setSpeechService,
		SpeechLists:           a.speechLists,
		SpeakMint:             a.speakMint,
		SpeakPlay:             a.speakPlay,
		SpeakAhead:            a.speakAhead,
		ReplyVoice:            a.replyVoice,
		SpeakerPolicy:         a.speakerPolicyState,
		DashboardToken:        a.dashboardToken,
		// .
		// .
		DiscoverModels: func(provider, apiKey string) ([]string, error) {
			reg, err := a.loadProviders()
			if err != nil {
				return nil, err
			}
			// .
			// .
			// .
			// .
			// .
			return a.discoverForProvider(context.Background(), reg, provider, apiKey)
		},
		// .
		// .
		GetWork:        a.workQueueState,
		GetSandbox:     a.sandboxState,
		SetSandbox:     a.setSandboxRoots,
		GetProjects:    a.projectsState,
		GetWorkspace:   a.getProjectWorkspace,
		GetProjectRoot: a.getProjectRoot,
		PluginAct: func(req dashboard.PluginAction) error {
			switch req.Action {
			case "install":
				return a.InstallFromCatalog(context.Background(), req.ID)
			case "uninstall":
				return a.UninstallPlugin(req.ID)
			case "retry":
				return a.RetryPlugin(req.ID)
			case "confirm", "deny":
				return a.decideAct(context.Background(), req.ID, req.Act, req.Action == "confirm")
			case "always":
				return a.alwaysAct(context.Background(), req.ID, req.Act)
			default:
				return fmt.Errorf("unknown plugin action %q", req.Action)
			}
		},
		Restart: a.Restart,
		CatalogRefresh: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := a.refreshCatalog(ctx); err != nil {
				return err
			}
			a.dashboard.BroadcastStatus()
			return nil
		},
		ProjectAct: func(req dashboard.ProjectRequest) error {
			contract := projectContractFromDashboard(req.Contract)
			switch req.Action {
			case "create":
				_, err := a.projects.Create(req.Name, descriptionDeref(req.Description), "operator", req.Parent, contract, derefAttrs(req.Attributes))
				return err
			case "update":
				_, err := a.projects.ApplyPatch(req.ID, namePatch(req.Name), req.Description, req.Focus, req.Parent, contract, derefAttrs(req.Attributes))
				return err
			case "close":
				_, err := a.closeOrReopen(req.ID, "closed")
				return err
			case "reopen":
				_, err := a.closeOrReopen(req.ID, "open")
				return err
			case "archive":
				_, err := a.closeOrReopen(req.ID, "archived")
				return err
			case "unarchive":
				_, err := a.closeOrReopen(req.ID, "open")
				return err
			case "delete":
				return a.deleteProject(req.ID)
			case "select":
				_, err := a.selectProject(req.ID)
				return err
			case "deselect":
				_, err := a.deselectProject()
				return err
			default:
				return fmt.Errorf("unknown project action %q", req.Action)
			}
		},
		GetConfig: func() (*dashboard.ConfigState, error) { return a.configState(), nil },
		SetConfig: a.applyConfigChange,
		ListLogs:  a.listLogs,
		TailLogs:  a.tailLogs,
		GetTools: func() ([]dashboard.ToolState, error) {
			states := a.toolReg.ToolStates()
			out := make([]dashboard.ToolState, len(states))
			for i, ts := range states {
				out[i] = dashboard.ToolState{Name: ts.Name, Description: ts.Description, Enabled: ts.Enabled}
			}
			return out, nil
		},
		SetToolFunc: a.setToolEnabled,
	}
}

// .

// .
// .
// .
// .
// .
// .
var timerWakeBudget = 15 * time.Minute

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
func (a *App) wakeTimerAlarm(ctx context.Context, alarmID, tag, message string) {
	// .
	// .
	// .
	// .
	if a.currentMode() == ModeSafe {
		notice := fmt.Sprintf("[timer %s #? fired %s] %s — I am in safe mode: I woke, but I cannot write to my ledger until my operator restores it.",
			alarmID, tag, time.Now().UTC().Format("15:04:05 MST Mon Jan 2"))
		// .
		// .
		// .
		wakeID := fmt.Sprintf("wake_%s_%d_safe", alarmID, time.Now().UTC().UnixNano())
		if a.dashboard != nil {
			if n := a.dashboard.PushTransient(wakeID, notice+" "+message); n == 0 {
				log.Printf("TIMER WAKE (SAFE): nobody connected — notice was transient-only: %s", notice)
			}
		} else {
			log.Printf("TIMER WAKE (SAFE, no dashboard): %s %s", notice, message)
		}
		return
	}

	notice := fmt.Sprintf("[timer %s", alarmID)
	if tag != "" {
		notice += fmt.Sprintf(" #%s", tag)
	}
	notice += fmt.Sprintf(" fired %s]", time.Now().UTC().Format("15:04:05 MST Mon Jan 2"))
	if message != "" {
		notice += " " + message
	}

	// .
	// .
	if err := a.acquireTurn(ctx); err != nil {
		log.Printf("TIMER WAKE %s: could not take the turn (floor already delivered): %v", alarmID, err)
		return
	}
	defer a.releaseTurn()
	// .
	// .
	// .
	// .
	// .
	turnCtx, cancelTurn := context.WithTimeout(context.Background(), timerWakeBudget)
	defer cancelTurn()
	spoken, err := a.wake(turnCtx, "system", notice+" — your own alarm woke you. Respond to your operator in your own words.")
	if err != nil {
		// .
		// .
		log.Printf("TIMER WAKE turn failed for %s (floor already delivered): %v", alarmID, err)
		return
	}
	if spoken == "" {
		return
	}
	wakeID := fmt.Sprintf("wake_%s_%d", alarmID, time.Now().UTC().UnixNano())
	if err := a.store.AddOutboxMessage(wakeID, "operator", "", spoken, nil); err != nil {
		log.Printf("TIMER WAKE outbox write failed: %v", err)
	}
	log.Printf("TIMER WAKE: %s woke and spoke (%d chars)", alarmID, len(spoken))
}

// .
// .
// .
// .
// .
const continuationTurnBudget = 45 * time.Minute

// .
// .
// .
// .
const maxContinuationChain = 8

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
func (a *App) noteTurnShape(capped, byPressure bool) {
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
	if !a.scheduleContinuation(capped, byPressure) {
		a.sweepDeliveriesAfterTurn()
	}
}

// .
// .
// .
// .
// .
// .
func (a *App) sweepDeliveriesAfterTurn() {
	if a.store == nil {
		return
	}
	// .
	// .
	// .
	if hw := a.configSnapshot().Agency.HarvestWake; hw != nil && !*hw {
		return
	}
	// .
	// .
	// .

	// .
	// .
	// .

	unh, err := a.store.UnharvestedDeliveries(1)
	if err != nil || len(unh) == 0 {
		return
	}
	id, goal := unh[0].ID, store.SubagentGoal(unh[0].Description)
	// .
	// .
	// .
	// .
	// .
	log.Printf("HARVEST_SWEEP_TURNEND: delivery unharvested at turn end (%s) — waking to harvest", id)
	// .
	gateCtx, cancelGate := context.WithTimeout(a.bgCtx, harvestGateWait)
	if !a.runBackground(func() {
		defer cancelGate()
		a.wakeSubagentDelivery(gateCtx, id, goal)
	}) {
		cancelGate()
	}
}

// .
// .
// .
func (a *App) scheduleContinuation(capped, byPressure bool) bool {
	if !capped {
		a.turnMeterMu.Lock()
		a.contChain = 0
		a.turnMeterMu.Unlock()
		return false
	}
	if !a.turnContinuation {
		return false
	}
	// .
	// .
	ws, err := a.store.ActiveWorkSession()
	if err != nil || ws == nil {
		return false
	}
	a.turnMeterMu.Lock()
	a.contChain++
	leg := a.contChain
	a.turnMeterMu.Unlock()
	if leg > maxContinuationChain {
		log.Printf("CONTINUATION refused: %d consecutive capped turns on %s — standing down so the operator can look", leg-1, ws.ID)
		return false
	}
	sessionID := ws.ID
	if !a.runBackground(func() { a.continueCappedTurn(sessionID, leg, byPressure) }) {
		log.Printf("CONTINUATION not scheduled for %s: runtime stopping", sessionID)
		return false
	}
	return true
}

// .
type legMeter struct {
	mu        sync.Mutex
	predicted int
}

func (m *legMeter) get() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.predicted
}

// .
// .
func (a *App) registerLegMeter(sessionID string) (*legMeter, func()) {
	m := &legMeter{}
	a.legMu.Lock()
	if a.legMeters == nil {
		a.legMeters = map[string]*legMeter{}
	}
	a.legMeters[sessionID] = m
	a.legMu.Unlock()
	return m, func() {
		a.legMu.Lock()
		delete(a.legMeters, sessionID)
		a.legMu.Unlock()
	}
}

// .
// .
func (a *App) noteSubagentWorkCall(sessionID, argsJSON string) {
	_, predicted, _ := parseWorkDeclaration(argsJSON)
	if predicted <= 0 {
		return
	}
	a.legMu.Lock()
	m := a.legMeters[sessionID]
	a.legMu.Unlock()
	if m == nil {
		return
	}
	m.mu.Lock()
	m.predicted = predicted
	m.mu.Unlock()
}

// .
// .
func (a *App) resetAsk() {
	a.askMu.Lock()
	a.askYields, a.askFleetSpent = 0, false
	a.askMu.Unlock()
}

// .
func (a *App) noteYield(yielded bool) {
	if !yielded {
		return
	}
	a.askMu.Lock()
	a.askYields++
	a.askMu.Unlock()
}

// .
// .
func (a *App) markFleetSpent() {
	a.askMu.Lock()
	a.askFleetSpent = true
	a.askMu.Unlock()
}

// .
// .
// .
// .
// .
func (a *App) yieldGate() (bool, string) {
	a.askMu.Lock()
	defer a.askMu.Unlock()
	if a.askFleetSpent {
		return true, "a sub-agent has ended unfinished or failed since the operator last spoke, and the operator has no answer yet"
	}
	if a.askYields >= 2 {
		return true, fmt.Sprintf("this would be yield %d against the operator's open ask with no answer yet", a.askYields+1)
	}
	return false, ""
}

// .
// .
// .
// .
// .
// .
func (a *App) continueCappedTurn(sessionID string, leg int, byPressure bool) {
	if err := a.acquireTurn(a.bgCtx); err != nil {
		log.Printf("CONTINUATION %s: could not take the turn: %v", sessionID, err)
		return
	}
	defer a.releaseTurn()
	turnCtx, cancelTurn := context.WithTimeout(context.Background(), continuationTurnBudget)
	defer cancelTurn()
	how := "at its declared tool budget"
	if byPressure {
		how = "because its context filled"
	}
	fact := fmt.Sprintf("[budget checkpoint — continuation %d] Your previous turn ended %s. Work session %s is still active: resume from plan= and next_move= in your working state, and declare steps= for this leg.", leg, how, sessionID)
	// .
	// .
	// .
	// .
	// .
	// .
	if ws, err := a.store.ActiveWorkSession(); err != nil {
		log.Printf("CONTINUATION %s: resume card unreadable: %v", sessionID, err)
	} else if ws != nil {
		if card := resumeCardFor(ws); card != "" {
			fact += "\n\n" + card
		}
	}
	spoken, err := a.wake(turnCtx, "system", fact)
	if err != nil {
		log.Printf("CONTINUATION turn failed for %s (leg %d): %v", sessionID, leg, err)
		return
	}
	if spoken == "" {
		return
	}
	wakeID := fmt.Sprintf("wake_cont_%d", time.Now().UTC().UnixNano())
	if err := a.store.AddOutboxMessage(wakeID, "operator", "", spoken, nil); err != nil {
		log.Printf("CONTINUATION outbox write failed: %v", err)
	}
	log.Printf("CONTINUATION: leg %d on %s spoke (%d chars)", leg, sessionID, len(spoken))
}

// .
// .
func (a *App) turnCancelRegistered() bool {
	a.turnMu.Lock()
	defer a.turnMu.Unlock()
	return a.turnCancel != nil
}

// .

// .
// .
type dashboardPulse struct {
	live     func() bool
	interval time.Duration
	// .
	// .
	// .
	// .
	// .
	active func(d time.Duration) bool

	// .
	ovMu     sync.Mutex
	override bool
}

func (d *dashboardPulse) setOverride(v bool) {
	d.ovMu.Lock()
	d.override = v
	d.ovMu.Unlock()
}

func (d *dashboardPulse) Interval() time.Duration { return d.interval }
func (d *dashboardPulse) Live() bool {
	d.ovMu.Lock()
	ov := d.override
	d.ovMu.Unlock()
	if !(ov || d.live()) {
		return false
	}
	if d.active == nil {
		return true
	}
	window := d.interval
	if window <= 0 {
		window = 300 * time.Second
	}
	return d.active(window)
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
func rebuildRemedy(cfg Config) string {
	db := cfg.Identity.DBPath
	if db == "" {
		db = filepath.Join("data", "aii.db")
	}
	return fmt.Sprintf(" The projection rebuilds from the ledger: stop the identity, delete %s* and start it again."+
		" The ledger, key and projects are untouched; conversation history lives only in the projection and does not survive the rebuild.", db)
}

func (a *App) warnTempHome(ledgerPath string) {
	home := filepath.Dir(ledgerPath)
	abs, err := filepath.Abs(home)
	if err != nil {
		abs = home
	}
	slash := filepath.ToSlash(abs)
	tmp := filepath.ToSlash(os.TempDir())
	if strings.HasPrefix(slash, tmp+"/") || slash == tmp || strings.HasPrefix(slash, "/tmp/") {
		log.Printf("WARNING: the identity's home (%s) is under a TEMPORARY directory.", abs)
		log.Printf("WARNING: temp directories are cleaned by the OS and by habit — an identity that should persist must not live here.")
		log.Printf("WARNING: move the data dir to durable storage unless this identity is deliberately disposable.")
	}
}

// .
// .
// .
// .
// .
func autoloadTier(s string) (packagefmt.Tier, bool, bool) {
	switch s {
	case "none":
		return 0, true, true
	case "T0":
		return packagefmt.TierT0, false, true
	case "T1":
		return packagefmt.TierT1, false, true
	case "T2":
		return packagefmt.TierT2, false, true
	case "T3":
		return packagefmt.TierT3, false, true
	}
	return packagefmt.TierT1, false, false
}

// .
// .
type activePkgMeta struct {
	dir string
	pkg string
	// .
	// .
	// .
	// .
	hash string
	kind string
	// .
	// .
	owner *running
}

// .
func (a *App) updateStateView() *dashboard.UpdateState {
	if a.updateChecker == nil {
		return nil
	}
	snap := a.updateChecker.State().Snapshot(Current())
	view := &dashboard.UpdateState{
		CurrentVersion: snap.CurrentVersion,
		Checking:       snap.Checking,
		Enabled:        a.updateChecker.Armed(),
		Automatic:      a.configSnapshot().Updates.Automatic,
	}
	a.stageOnce.Do(func() { a.stageWhy = updates.StageRefusal() })
	view.StageRefusal = a.stageWhy
	// .
	// .
	if v := snap.AvailableVersion; v != "" || snap.InstalledVersion != "" {
		if v == "" {
			v = snap.InstalledVersion
		}
		repo := a.configSnapshot().Updates.Repo
		if repo == "" {
			repo = updates.DefaultRepo
		}
		view.ReleaseURL = "https://github.com/" + repo + "/releases/tag/v" + v
	}
	if snap.AvailableVersion != "" {
		view.AvailableVersion = snap.AvailableVersion
	}
	if snap.InstalledVersion != "" {
		view.InstalledVersion = snap.InstalledVersion
		view.NeedsRestart = snap.NeedsRestart
	}
	if snap.LastError != "" {
		view.Error = snap.LastError
	}
	if !snap.LastCheck.IsZero() {
		view.CheckedAt = snap.LastCheck.Format(time.RFC3339)
	}
	return view
}

// .
// .
// .
// .
func (a *App) credentialSource(kind string, opts map[string]string, contracts ...oauth.Provider) (*oauth.Source, error) {
	var contract oauth.Provider
	if len(contracts) > 0 {
		contract = contracts[0]
	}
	params, perr := oauth.OverrideParams(contract.Params(), opts)
	if perr != nil {
		return nil, perr
	}
	// .
	// .
	// .
	ownedPath, ownedSt := "", ownedAbsent
	if _, named := opts["file"]; !named {
		p, st, err := a.ownedCredential(kind, contract.CredentialFile)
		if err != nil {
			return nil, err
		}
		ownedPath, ownedSt = p, st
	}
	key := kind
	rawContract, _ := json.Marshal(params)
	key += "\x00contract=" + string(rawContract)
	if ownedSt == ownedPresent {
		key += "\x00owned=" + ownedPath
	}
	if len(opts) > 0 {
		names := make([]string, 0, len(opts))
		for k := range opts {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			key += "\x00" + k + "=" + opts[k]
		}
	}
	a.credMu.Lock()
	defer a.credMu.Unlock()
	if s, ok := a.credSrc[key]; ok {
		return s, nil
	}
	// .
	// .
	// .
	if missing := missingCredentialOptions(kind, opts); len(missing) > 0 {
		return nil, fmt.Errorf("credential %q requires provider options %s", kind, strings.Join(missing, ", "))
	}
	var s *oauth.Source
	var err error
	if ownedSt == ownedPresent {
		s, err = oauth.NewOwnedConfigured(kind, ownedPath, params, opts)
	} else {
		s, err = oauth.NewConfigured(kind, params, opts)
	}
	if err != nil {
		return nil, err
	}
	if ownedSt == ownedPresent {
		client, err := a.authorityClient(params.TokenURL)
		if err != nil {
			return nil, err
		}
		s.SetHTTPClient(client)
	}
	if a.credSrc == nil {
		a.credSrc = map[string]*oauth.Source{}
	}
	a.credSrc[key] = s
	return s, nil
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
func (a *App) residentLoopHooks(cfg Config) (
	planState func() (planned, active bool),
	fanoutState func() (declared, spawned int),
	predictedNow func() int,
	calibration func() (int, int, int),
) {
	predictedNow = func() int {
		a.turnMeterMu.Lock()
		defer a.turnMeterMu.Unlock()
		return a.turnPredicted
	}
	if heuristicNudgesOn(cfg.Agency.HeuristicNudges) && planNudgeEnabled(cfg.Agency.PlanNudge) {
		// .
		// .
		planState = a.planState
		fanoutState = a.fanoutState
		calibration = func() (int, int, int) {
			plans, predicted, actual, err := a.store.PlanCalibration(48 * time.Hour)
			if err != nil {
				return 0, 0, 0
			}
			return plans, predicted, actual
		}
	}
	return planState, fanoutState, predictedNow, calibration
}

func (a *App) planState() (planned, active bool) {
	ws, err := a.store.ActiveWorkSession()
	if err != nil {
		return true, true
	}
	if ws == nil {
		return false, false
	}
	return strings.TrimSpace(ws.Plan) != "", true
}

// .
// .
// .
func (a *App) fanoutState() (declared, spawned int) {
	a.turnMeterMu.Lock()
	defer a.turnMeterMu.Unlock()
	return a.turnIndependent, a.turnSpawned
}

// .
// .
// .
// .
func planNudgeEnabled(v *bool) bool { return agencyOn(v) }

// .
// .
// .
// .
func planningBriefEnabled(cfg Config) bool {
	return heuristicNudgesOn(cfg.Agency.HeuristicNudges) &&
		planNudgeEnabled(cfg.Agency.PlanNudge) &&
		agencyOn(cfg.Agency.PlanningBrief)
}

// .
// .
// .
// .
// .
// .
// .
func replaySafeHook(reg *tools.Registry) func(llm.ToolCall) bool {
	if reg == nil {
		return nil
	}
	return func(tc llm.ToolCall) bool { return reg.ReplaySafe(tc.Function.Name) }
}

// .
// .
// .
// .
// .
func (a *App) firstActHook(cfg Config) func(llm.ToolCall) bool {
	if !planningBriefEnabled(cfg) {
		return nil
	}
	return func(tc llm.ToolCall) bool { return actToolCall(tc.Function.Name, tc.Function.Arguments) }
}

// .
// .
// .
// .
// .
func heuristicNudgesOn(v *bool) bool { return v != nil && *v }

// .
// .
// .
func agencyOn(v *bool) bool { return v == nil || *v }

// .
// .
// .
func (a *App) bootHeadVerifier(cfg Config) (*witness.HeadVerifier, error) {
	return a.headVerifierFor(cfg, filepath.Dir(cfg.Identity.LedgerPath))
}

// .
// .
// .
func (a *App) headVerifierFor(cfg Config, ledgerDir string) (*witness.HeadVerifier, error) {
	var platform *witness.PublicKeyEnvelope
	if cfg.Witness.PlatformPubkeyPath != "" {
		env, err := witness.LoadPlatformEnvelope(cfg.Witness.PlatformPubkeyPath)
		if err != nil {
			return nil, err
		}
		platform = env
	} else {
		platform = genesis.PinnedRoot()
	}
	return witness.LoadHeadVerifier(ledgerDir, platform)
}
