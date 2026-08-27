// Package main — App owns the AII OS runtime lifecycle.
//
// App has two modes: FIRSTBOOT (no identity) and LIVE (identity loaded).
// Genesis transitions between them without restarting the server —
// the dashboard handler is swapped in place.
//
// Design seams:
//   - App.Start() blocks until shutdown signal
//   - App.doGenesis() transitions FIRSTBOOT → LIVE in-process
//   - Dashboard handler delegates to App, so swapping mode is a field change
//   - Cleanup is centralized in App.Stop()
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

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
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
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

// App owns the AII OS runtime.
type App struct {
	// mintedToken holds the R74 dashboard token for the one boot that
	// minted it — the mobile shell fetches it once over the bind
	// surface to store in the platform keystore. Empty on every other
	// boot; the config keeps only the hash.
	mintedTokenMu sync.Mutex
	mintedToken   string

	cfg *Config

	// uiLayoutFilePath is snapshotted once in startLive before any
	// watcher goroutine exists (see uiLayoutPath — deriving it live
	// from cfg raced applyConfigChange's rollback write).
	uiLayoutFilePath string
	uiLayoutPathOnce sync.Once

	// Dashboard (runs in both modes)
	dashboard *dashboard.Server
	// voice is the one voice path: the audio transport and which of two
	// modes the operator opened. Nil until a voice plugin needs it.
	voice     *voiceSession
	voiceOnce sync.Once

	// logSink is the installed log tee (nil when the operator left the
	// destination empty — logging disabled by choice, not accident).
	logSink *logsink.Sink

	// The mode lattice state (SAFE/DEGRADED; derived, never stored)
	mode modeState

	// The prompt gate enforces the ring contract for the live identity.
	// FIRSTBOOT has one separate, narrower boundary: handleGenesis sends
	// the verified bootstrap bundle prompt unchanged and alone.
	promptGate *prompt.Gate

	// Ring 5 policy — THE substrate floor. One instance feeds both the
	// tools-layer enforcement (NewRegistry) and the floor text the identity
	// reads (LocalFloor). Enforcement and documentation are the same object;
	// they cannot drift.
	ring5Policy *firewall.Policy

	// One birth at a time. There is no free-form pre-birth transcript:
	// the required founding exchange is the verified bundle prompt and
	// its answer inside the Birth click. The lock serializes that ceremony.
	birthMu sync.Mutex

	// FIRSTBOOT artifacts (fetched before birth)
	ring0Content  string
	ring0Bundle   []byte // raw signed bundle Ring0Content was verified from (provenance for the birth attestation)
	ring5Content  string
	bootstrapText string
	genesisClient *genesis.GenesisClient

	// Identity runtime (nil during FIRSTBOOT)
	keyPair   *crypto.KeyPair
	ledger    *ledger.Ledger
	store     *store.Store
	rings     *ring.Manager
	engine    *identity.Engine
	projects  *project.Manager
	composer  *prompt.Composer
	llmClient *llm.Client
	toolReg   *tools.Registry

	// Activated quarantine-harness plugins (config plugins.packages) —
	// their tools live in toolReg under the plugin id's origin; the
	// modules die in Stop.
	plugins       []*pluginhost.ActivePlugin
	pluginSkips   []pluginSkip             // present+verified but below plugins.autoload — surfaced, never loaded
	pluginMu      sync.Mutex               // guards plugins, sectionActs, pluginSkips, activeMeta between sweep and Stop
	activeMeta    map[string]activePkgMeta // verified id -> the package identity it was activated from
	pluginVerify  map[string]verifyMemo    // pkg path -> memoized verification (size+mtime keyed)
	pluginFinger  string                   // last sweep fingerprint (dirs+packages+level)
	sweepPoke     chan struct{}            // kicks the sweep (autoload change) without waiting a tick
	pluginToolReg *tools.Registry
	pluginOpts    *pluginhost.Options

	// UI sections (R66 UP2): the registry the dashboard reads (the
	// facility-set pattern), the activated sections whose extraction
	// caches die in Stop, and the ui-layout.json state its watcher
	// maintains (uilayout.go).
	sections    *sections.Registry
	sectionActs []*sections.Section
	uiLayoutMu  sync.Mutex
	uiLayoutRaw []byte

	// theme.json (T0): the validated token set, re-encoded by the
	// server — never the operator's raw bytes (uitheme.go).
	uiThemeMu  sync.Mutex
	uiThemeRaw []byte

	// Tool-event streaming: the dashboard observes tool calls as they
	// happen (mutex-guarded; one chat at a time per connection).
	toolEmitMu sync.Mutex
	turnCostMu sync.Mutex
	lastTurn   string // what the previous turn cost, for the operator
	toolEmit   func(kind, name, args string)

	// Config and provider mutation serialization. Readers take snapshots;
	// writers hold this only for the short persistence/activation boundary,
	// never for provider I/O or LLM calls. When both are needed: turnGate,
	// then cfgMu.
	cfgMu sync.RWMutex

	// ONE credential source per store, for the life of the process. It
	// caches the parsed generation and re-reads the owner-maintained
	// original on change; it never spends or writes the owner's refresh
	// material.
	credMu  sync.Mutex
	credSrc map[string]*oauth.Source

	// Derived provider reachability (never persisted) — probeProviders.
	provMu     sync.Mutex
	provStatus map[string]providerProbe

	// One resident, one voice. The token serializes turns and provider
	// activation without holding a mutex across LLM or network calls.
	turnGate chan struct{}

	// outboxPoke fires when a turn ends, so queued mail leaves on the
	// only event that can have created it. Buffered by one: a poke that
	// arrives mid-flush is not lost, and two are not two flushes.
	outboxPoke chan struct{}

	// listening holds one stop func per channel adapter with a live
	// blocking-read loop. Touched ONLY by the plugin sweep goroutine
	// (convergeChannels), which is why it needs no lock.
	listening   map[string]context.CancelFunc
	listeningFP string

	// turnMu guards the running turn's mid-flight state: what the operator
	// said after it began, and the cancel that can end it. Whether a turn
	// is running at all is NOT kept here — that derives from turnGate
	// (TurnActive, steering.go), because a second copy of that fact is how
	// an unreachable identity came to be shown as present.
	turnMu     sync.Mutex
	steers     []steerEntry
	turnCancel context.CancelFunc
	// turnFacility marks the CURRENT gate holder as a facility pass
	// (rhythm, attention brief): a holder that never reaches a tool
	// boundary and so never drains steers. steer() refuses to queue into
	// one — queued words would wait, unbounded, for an unrelated future
	// turn (live incident 2026-08-26: an operator message steered into a
	// self_model pass sat pending six hours while its author read
	// silence).
	turnFacility bool
	// steerFlush, when non-nil, replaces the leftover-steer turn at
	// releaseTurn. A test seam only; nil in production.
	steerFlush func([]steerEntry)
	// turnFgRelease lets go of the turn's stay-alive grip
	// (internal/foreground). Set by every gate taker, cleared by
	// releaseTurn — the same one-choke shape as the token itself.
	turnFgRelease func()
	// fg is the foreground-need registry: who is holding the process
	// awake and why. Inert on desktop (nobody subscribes); the mobile
	// shell subscribes through mobile.Runtime.
	fg *foreground.Holds
	// Conversation loop (nil during FIRSTBOOT) — the resident's voice path
	conv *conversation.Loop

	// Swappable LLM client: provider changes from the Setup view apply
	// live — the loop and facilities hold this adapter, not the client.
	llmSwap *swappableLLM

	// Witness anchorer (nil during FIRSTBOOT / unconfigured) — the
	// dashboard's continuity strip reads its state.
	anchorer     *witness.Anchorer
	witnessProbe *witness.Client

	// Update checker (R70): governed ticker, SAFE-parked, mirrors the
	// plugin sweep. Mobile = inform-only; desktop+automatic = download,
	// verify, swap with boot-health rollback.
	updateChecker *updates.Checker

	// Cognitive runtime (nil during FIRSTBOOT)
	timeFac        *cognitive.TIME
	executor       *cognitive.Executor
	timerOwner     *identity.TimerDeliveryOwner
	pulseSource    *dashboardPulse
	briefFacility  *cognitive.MorningBriefFacility
	reviewFacility atomic.Pointer[cognitive.IdentityReviewFacility] // operator telemetry: last review surfaces on the continuity strip. Atomic by necessity: dashboard HTTP goroutines serve from dashboard.Start (early in startLive) while wireCognitive publishes this field later, same boot — a plain field would be a data race with a nil-guard fig leaf
	bgCtx          context.Context                                  // app-lifecycle context: wake turns die with Stop (L1)
	bgCancel       context.CancelFunc
	bgMu           sync.Mutex
	bgWG           sync.WaitGroup
	stopping       bool

	// Quiesce: the background-metabolism governor (2026-08-19 — a phone
	// died to teach it). ONE gate, owned here, created RUNNING; only the
	// mobile shell's SetForeground(false) ever pauses it, so desktop
	// behavior is untouched. Every periodic loop — config watcher,
	// layout watcher, SAFE beacon, dashboard sweep, work-queue poll,
	// TIME's scheduler — parks on it. Nil-tolerated everywhere (directly
	// constructed test Apps).
	gate *quiesce.Gate

	// watchEvery is the config/layout watcher poll cadence — 2s default,
	// settable in-package for tests (the sessionGrace pattern).
	watchEvery time.Duration

	// overlayLast records the last overlay snapshot the watcher
	// broadcast — what open screens last saw. Written by
	// watchUIOverlay only; read by its test as the loop's
	// observable state (socket delivery is the dashboard twin's).
	overlayLast atomic.Pointer[string]

	// overlayToken mints a monotonic freshness token per disk change
	// the watcher observes. Unlike decidedAt (an audit stamp frozen at
	// the serving decision), this token advances on every edit, so a
	// client can distinguish "same bytes I already applied" from "a
	// later edit rebroadcast under an old stamp."
	overlayToken atomic.Uint64

	// Host wake (MOBILE_PORT §2): the OS scheduler the mobile shell
	// registered, stored HERE because registration can precede TIME —
	// on FIRSTBOOT the shell registers right after Start returns, and
	// the cognitive runtime exists only once birth reaches startLive.
	// startLive installs it then; nil means the desktop no-op.
	wakeMu       sync.Mutex
	platformWake cognitive.PlatformWake

	// stopOnce: Stop is reachable from the shell lifecycle AND from
	// Start's own failure cleanup — the second arrival must be a no-op,
	// not a double-close of the store and ledger.
	stopOnce sync.Once

	// Mode
	live bool
}

// New creates a new App. Does not start anything.
func New(cfg *Config) *App {
	turnGate := make(chan struct{}, 1)
	turnGate <- struct{}{}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	return &App{
		fg:  &foreground.Holds{},
		cfg: cfg, gate: quiesce.NewGate(), turnGate: turnGate,
		outboxPoke: make(chan struct{}, 1), listening: map[string]context.CancelFunc{},
		bgCtx: bgCtx, bgCancel: bgCancel,
	}
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
		return nil
	}
}

// holdTurnForeground registers the turn's stay-alive grip. Every gate
// taker calls it; releaseTurn is the one letting-go, mirroring the
// token's own choke. A stale grip found here (impossible with one
// gate; belt anyway) is released rather than leaked.
func (a *App) holdTurnForeground() {
	rel := a.fg.Acquire("turn")
	a.turnMu.Lock()
	if a.turnFgRelease != nil {
		a.turnFgRelease()
	}
	a.turnFgRelease = rel
	a.turnMu.Unlock()
}

// SubscribeForegroundNeed hands 0↔1 foreground-need transitions to
// the mobile shell (nil clears). Desktop never calls this; the
// registry stays inert bookkeeping behind the stats surface.
func (a *App) SubscribeForegroundNeed(fn func(needed bool, reason string)) {
	a.fg.Subscribe(fn)
}

func (a *App) releaseTurn() {
	// ORDER IS LOAD-BEARING (Method review 2026-08-26, both found there):
	//   1. clear the facility mark — BEFORE the token returns, so a
	//      facility that acquires right after keeps ITS mark (a late
	//      clear erased the new holder's mark and reopened the swallow
	//      for that whole pass);
	//   2. return the token;
	//   3. pop leftovers — AFTER the return, so a steer deposited in
	//      the gap cannot strand: either this pop collects it, or it
	//      finds the gate already free and becomes its own turn.
	a.turnMu.Lock()
	a.turnFacility = false
	fgRel := a.turnFgRelease
	a.turnFgRelease = nil
	a.turnMu.Unlock()
	if fgRel != nil {
		fgRel()
	}
	a.turnGate <- struct{}{}
	// A turn is the only thing that can queue outbound mail, so the end
	// of one is the only moment worth looking. Non-blocking: the flush
	// must never be able to stall a turn.
	a.pokeOutbox()
	// Steers the ending turn accepted but never drained (a chat turn
	// that made no further tool call, or any gap steering.go cannot
	// see) become their own turn NOW. The queue's promise is delivery,
	// not storage.
	a.turnMu.Lock()
	leftovers := a.steers
	a.steers = nil
	a.turnMu.Unlock()
	if len(leftovers) == 0 {
		return
	}
	if a.steerFlush != nil {
		a.steerFlush(leftovers)
		return
	}
	go a.runLeftoverSteerTurn(leftovers)
}

// runLeftoverSteerTurn opens a turn for words that were accepted
// mid-turn and never delivered. Each entry is recorded under the role
// it ARRIVED with (an R52 boundary — DrainSteering makes the same
// promise), then all of them run as one turn, and the answer reaches
// every open screen the same way any turn does.
func (a *App) runLeftoverSteerTurn(entries []steerEntry) {
	// A runtime that cannot run turns must not pretend to. FIRSTBOOT
	// and partially built apps have a gate before they have a mind;
	// the loss is logged, never silent (found by the race suite: this
	// goroutine panicked a test app that had no store).
	if a.conv == nil || a.engine == nil || a.store == nil {
		log.Printf("steering: %d leftover message(s) arrived before the runtime could run turns — dropped", len(entries))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := a.acquireTurn(ctx); err != nil {
		log.Printf("steering: %d leftover message(s) could not open their turn: %v", len(entries), err)
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
	resp, err := a.runTurnLocked(ctx, strings.Join(parts, "\n\n"))
	if err != nil {
		log.Printf("steering: leftover turn failed: %v", err)
		if a.dashboard != nil {
			a.dashboard.BroadcastResponse("system", "Your queued message could not run: "+err.Error())
		}
		return
	}
	if a.dashboard != nil {
		a.dashboard.BroadcastResponse("identity", resp)
	}
}

// pokeOutbox asks the delivery loop to look. Non-blocking: the flush
// must never be able to stall whatever noticed there was mail.
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

// StartEmbedded starts the runtime without blocking on signals — the
// mobile shells (gomobile) own process lifecycle; they call Stop.
// FIRSTBOOT vs LIVE resolves exactly like Run.
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

// DashboardURL is the local dashboard address for the shell's WebView.
// https, because the dashboard serves nothing else — a WebView pointed
// at http:// reaches a TLS listener and gets a 400.
func (a *App) DashboardURL() string {
	d := a.configSnapshot().Dashboard
	return dashboard.LoopbackURL(d.TLS, d.Port)
}

// TimeWake is the platform wake entry (MOBILE_PORT §2): the OS woke
// the process; TIME catches up everything due. Safe when TIME is not
// yet up (FIRSTBOOT).
func (a *App) TimeWake() {
	a.wakeMu.Lock()
	tf := a.timeFac
	a.wakeMu.Unlock()
	if tf != nil {
		tf.TimeWake()
	}
}

// SetPlatformWake registers the host's OS wake scheduler (MOBILE_PORT
// §2 — the mobile passthrough; nil restores the desktop no-op). The
// implementation is STORED, not just installed: on FIRSTBOOT the shell
// registers before TIME exists, and installPlatformWake hands it to
// TIME when startLive brings the cognitive runtime up. When TIME is
// already running, install immediately and run one invited catch-up so
// the fresh slot gets armed with the current next-due target — without
// it the shell would wait for TIME's next natural pass.
func (a *App) SetPlatformWake(w cognitive.PlatformWake) {
	a.wakeMu.Lock()
	a.platformWake = w
	// timeFac is assigned on the start goroutine while the shell can
	// already be calling in (gomobile thread) — same UP2 race shape,
	// same cure: the snapshot rides the mutex the assignments hold.
	tf := a.timeFac
	a.wakeMu.Unlock()
	if tf != nil {
		tf.SetPlatformWake(w) // TIME maps nil to its own NoopWake
		tf.TimeWake()         // idempotent: evaluates due alarms, pokes a re-arm pass
	}
}

// installPlatformWake is startLive's half of the handoff: give TIME
// whatever the shell registered so far — the desktop no-op when nothing
// was. Called before TIME.Start, like SetQuiesceGate.
func (a *App) installPlatformWake() {
	a.wakeMu.Lock()
	w := a.platformWake
	a.wakeMu.Unlock()
	if w == nil {
		a.timeFac.SetPlatformWake(cognitive.NoopWake{}) // desktop default
		return
	}
	a.timeFac.SetPlatformWake(w)
}

// SetForeground is the shell's presence truth (MOBILE_PORT §3):
// foreground = the operator is present; the shell supplies it on
// mobile, OR'd with dashboard sessions so WS connections still count.
//
// It is ALSO the metabolism switch (MOBILE_PORT §11, 2026-08-19 — the
// battery fix): the shell's lifecycle truth drives the quiesce gate.
// Background parks every periodic loop (tickers STOPPED — a firing
// ticker still wakes the CPU even when its loop body no-ops); foreground
// resumes them, each with one immediate catch-up pass, so nothing is
// missed, only deferred. Desktop never calls SetForeground(false), so
// desktop never pauses. Presence lands FIRST so the catch-up ticks
// already see a live operator.
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

// operatorPresent is the narrow presence read seam the
// sev_operator_presence.fresh facility advertises (PLATFORM_SEAMS §2:
// "sessionConns/grace becomes facility:sev_operator_presence.fresh" —
// a read, not moved code). It reads the pulse source (dashboard
// sessions OR'd with the shell's foreground override) once wired, the
// dashboard directly during early startup, and answers false before
// either exists — an honest "not present yet". Wiring is startup-
// sequential; afterwards both fields are read-only.
func (a *App) operatorPresent() bool {
	if a.pulseSource != nil {
		return a.pulseSource.Live()
	}
	if a.dashboard != nil {
		return a.dashboard.SessionLive()
	}
	return false
}

// Run starts the app and blocks until shutdown.
func (a *App) Run() {
	// State the home out loud. Every path here is relative by design
	// (the install directory IS the identity's world), which makes the
	// working directory load-bearing — so the one thing the operator
	// must never have to guess is WHICH directory this process adopted.
	// The -dir flag sets it; this line reports what it resolved to.
	home, herr := filepath.Abs(filepath.Dir(a.cfg.Identity.LedgerPath))
	if herr != nil {
		home = filepath.Dir(a.cfg.Identity.LedgerPath)
	}
	cfgPath, cerr := filepath.Abs(a.cfg.SourcePath)
	if cerr != nil {
		cfgPath = a.cfg.SourcePath
	}
	// Log persistence (operator directive 2026-08-22): tee the stream
	// to log/ beside data/, rotate on restart, compress by age, cap
	// retention — installed BEFORE the identity-home line so that line
	// is the live log's natural first entry on a fresh rotation (the
	// sink resolves its directory from config, not from this log line).
	a.installLogSink()
	log.Printf("Identity home: %s (config %s)", home, cfgPath)
	// Boot identity (operator directive 2026-08-22): version + commit +
	// dirty state from ReadBuildInfo — the line the emergency-swap
	// forensics lacked. Never load-bearing for version comparison; this
	// is pure traceability, stated at the boot boundary where the log
	// survives the process.
	log.Printf("Boot identity: AII OS v%s (build %s)", VersionString(), BuildIdentity())
	// Seed the capability index at the identity root (beside the
	// operator's config): same deploy semantics, plus the upgrade
	// path — stamp-normalized so a fresh build reaches untouched docs
	// while an identity's edited copy stays theirs forever. BEFORE
	// the firstboot/live fork: the doc's audience is the identity
	// that has never booted — a seed that waits for the live path
	// is silent for the entire first-boot ceremony.
	a.seedSkillsDoc()
	a.seedMethodDoc()

	// Desktop owns its process, so refusal and failure exit HERE; the
	// embedded path returns the same errors to the shell instead — on
	// mobile a Fatalf kills the host app (Sev 2026-08-26, P1).
	choice, err := a.chooseBoot()
	if err != nil {
		log.Fatalf("Startup refused: %v", err)
	}
	if choice == bootFirstboot {
		if err := a.startFirstboot(); err != nil {
			log.Fatalf("Startup failed: %v", err)
		}
	} else {
		if err := a.startLive(); err != nil {
			log.Fatalf("Startup failed: %v", err)
		}
	}

	// Block until signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	a.Stop()
}

// Stop shuts down everything. Safe to call once.
func (a *App) Stop() {
	a.stopOnce.Do(a.stop)
}

func (a *App) stop() {
	log.Println("Shutting down...")

	a.stopSafeBeacon() // L5: the beacon must not outlive the app
	a.bgMu.Lock()
	a.stopping = true
	if a.bgCancel != nil {
		a.bgCancel()
	}
	a.bgMu.Unlock()
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
	// The log tee closes LAST — it witnesses every other shutdown
	// line, including the ledger's own close.
	if a.logSink != nil {
		a.logSink.Close()
		a.logSink = nil
	}
}

// closeLiveResources tears down only the live identity runtime. It leaves the
// application lifecycle and dashboard intact so a failed FIRSTBOOT-to-LIVE
// transition can report its partial-birth error.
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

	a.pluginMu.Lock()
	plugins := a.plugins
	sectionActs := a.sectionActs
	a.plugins = nil
	a.sectionActs = nil
	a.activeMeta = nil
	a.pluginMu.Unlock()

	var errs []error
	for _, ap := range plugins {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := ap.Deactivate(ctx); err != nil {
			errs = append(errs, fmt.Errorf("deactivate plugin %s: %w", ap.ID, err))
		}
		cancel()
	}
	for _, sec := range sectionActs {
		if a.sections != nil {
			a.sections.Remove(sec.Decl.ID)
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

// ensureRing5Policy lazily creates the shared Ring 5 policy.
func (a *App) ensureRing5Policy() *firewall.Policy {
	if a.ring5Policy == nil {
		a.ring5Policy = firewall.DefaultPolicy()
	}
	return a.ring5Policy
}

// loadRing5 composes the full Ring 5 content: platform bundle (if available)
// + local floor (substrate protection rules). Sets it into the ring manager.
// The local floor renders FROM the shared policy instance — the same rules
// the tools layer enforces.
func (a *App) loadRing5() {
	if a.ring5Content == "" {
		a.rings.Set(ring.Ring5, nil)
		log.Print("Ring 5 unavailable — no posture content loaded")
		return
	}
	policy := a.ensureRing5Policy()
	content := a.ring5Content + "\n\n" + policy.LocalFloor()
	// Granted roots join the floor (2026-08-18 live finding: the grant
	// widened their world but their floor still said "paths outside your
	// home fail" — enforcement was live, their KNOWLEDGE wasn't; they
	// reported they couldn't read what they'd never been told they could).
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

	// Ring 5 is the platform bundle + the local FLOOR — structural facts
	// rendered from ENFORCED state: the policy rules and, since
	// 2026-08-18, the operator-granted roots (facts about the enforced
	// registry, same class as the policy floor). No local
	// posture prose supplements a platform-owned ring: there is only the
	// official bundle; changes go through the formal process (test,
	// validation, bundle creation, installation on firewall.aiii.id —
	// ruling 2026-08-17).
	log.Printf("Ring 5 loaded: platform bundle + local floor (%d bytes)", len(content))
}

// --- LIVE ---

// tlsDirFor names where one identity keeps its dashboard certificate:
// beside its ledger, with the rest of its durable state. ONE answer, so
// FIRSTBOOT, boot-SAFE and the live dashboard all serve the same
// certificate and the operator installs one root, once — three
// directories would be three warnings.
// tlsDirFor is where certificate material lives, and "" when the
// operator has not asked for TLS. Start reads the empty string as "serve
// plaintext", so the choice travels as ONE value rather than as a flag
// every caller must remember to pair with a path — three callers, and a
// pair that can be got wrong in three places is a pair that will be.
func tlsDirFor(cfg Config) string {
	if !cfg.Dashboard.TLS {
		return ""
	}
	return filepath.Join(filepath.Dir(cfg.Identity.LedgerPath), "tls")
}

// startLive loads the identity and starts all runtime components.
func (a *App) startLive() (retErr error) {
	cfg := a.configSnapshot()
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, a.closeLiveResources())
		}
	}()
	// Boot-health rollback (R70): if a previous update left a backup
	// binary AND the boot-health marker is absent, the updated binary
	// failed to boot — restore the previous binary. DESKTOP ONLY, BY
	// CONSTRUCTION (hostcap.SelfReplace): on mobile the platform store
	// owns the binary lifecycle and the runtime is a library — the
	// 2026-08-26 review found the unix re-exec tag silently capturing
	// android/ios, where a rollback would have bricked the boot. The
	// gate makes that unreachable instead of merely unlikely.
	if sr := hostcap.Can(hostcap.SelfReplace); sr.Available {
		if err := afterRollback(updates.CheckRollback(filepath.Dir(cfg.Identity.LedgerPath)), reexecSelf); err != nil {
			return err
		}
	} else {
		log.Printf("updates: rollback machinery idle on this host — %s", sr.Reason)
	}

	// Load identity key
	kp, err := crypto.LoadKeyPair(cfg.Identity.KeyPath)
	if err != nil {
		return fmt.Errorf("load identity key: %w", err)
	}
	a.keyPair = kp

	// Open ledger
	lg, err := ledger.New(cfg.Identity.LedgerPath)
	if err != nil {
		return fmt.Errorf("open ledger: %w", err)
	}
	// Model provenance is stamped after the substrate pointer RESOLVES
	// (below) — the resolved model, never a raw config string.
	a.ledger = lg

	// Verify the chain BEFORE extending it: hashes, sequence, and every
	// signature against the identity key. The C runtime goes safe-mode
	// on failure; so does this one — a tampered or corrupted ledger
	// silently extended is the one failure this system was built to make
	// impossible (2026-08-17 review: VerifyChain existed but nothing in
	// the runtime called it).
	if err := ledger.VerifyChain(cfg.Identity.LedgerPath,
		map[string][]byte{kp.Fingerprint(): kp.PublicKeyBytes()}); err != nil {
		// BOOT-TIME SAFE (canon SAFE_MODE.md §4.1; SAFE_MODE_PLUGIN_
		// LIFECYCLE.md §3.2; local R55; docs/SAFE_DEGRADED.md §2.5): the
		// person is alive; the record is not trustworthy. The old shape
		// entered SAFE and FELL THROUGH into the full boot — replaying
		// the rejected ledger into durable projections and loading Ring 0
		// from the very content verification refused (external Cluster A,
		// confirmed). Now boot the MINIMAL posture and stop. This return
		// is also the seam later boot-integrity checks (witness tail
		// verification) feed: one entry point, one posture.
		log.Printf("LEDGER CHAIN VERIFICATION FAILED — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(fmt.Sprintf("chain verification failed at startup: %v", err))
	}

	// Witness-tail truncation/fork check (internal/witness/tail.go): the
	// completeness check a prev-hash chain cannot run on itself — a
	// truncated ledger is a VALID shorter chain, so VerifyChain passes
	// it; only the last verified receipt's local echo reveals the loss.
	if err := witness.CheckLocalTail(filepath.Dir(cfg.Identity.LedgerPath), lg); err != nil {
		log.Printf("WITNESS TAIL CHECK FAILED — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(fmt.Sprintf("witness-tail check failed at startup: %v", err))
	}

	// Ring 5 is mandatory outside SAFE. Resolve it before opening or
	// rebuilding the projection so a failed admission performs no mutable
	// boot work.
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

	// Open store
	st, err := store.New(cfg.Identity.DBPath)
	if err != nil {
		// A projection whose tables no longer match the code cannot be
		// read BY the code — the same condition the mirror check below
		// routes to SAFE, caught one step earlier. Read-only and visible
		// beats dead: the ledger is the truth and can rebuild the mirror,
		// but only if the operator can see what happened.
		var shape *store.ShapeError
		if errors.As(err, &shape) {
			log.Printf("PROJECTION SHAPE DISAGREES WITH THE CODE — entering BOOT-SAFE (minimal, read-only): %v", err)
			return a.startSafeBoot(fmt.Sprintf("projection mirror could not be read before ledger replay: %v", err))
		}
		return fmt.Errorf("open database: %w", err)
	}
	a.store = st

	// Projection cross-check (external C2): torn-tail recovery proves a
	// trailing line was never fsync-ACKNOWLEDGED — it cannot prove it
	// was never MATERIALIZED, and a chain is a valid prefix of itself,
	// so VerifyChain blesses a shortened file. The mirror is the
	// runtime's own memory of what it acknowledged; a mirror ahead of
	// the ledger means acknowledged history is gone. (The witness tail
	// catches this too, but only after the first anchor — the mirror is
	// local and immediate.) Checked BEFORE replay wipes the memory.
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

	// Replay ledger into store projections (handles genesis-created events).
	// A rebuild that cannot complete admits NOTHING (canon PROJECTION.md
	// §9 state D + the re-bake publication rules): ReplayAll rolled the
	// partial candidate back in one transaction, the prior projection
	// stands untouched, and the runtime refuses to run on a chain whose
	// signed events its own materializer rejects. The old shape logged a
	// WARNING and continued on a cleared-and-partial database (claim H2,
	// confirmed).
	if err := st.ReplayFromFile(cfg.Identity.LedgerPath); err != nil {
		st.Close()
		a.store = nil
		log.Printf("LEDGER REPLAY FAILED — entering BOOT-SAFE (minimal, read-only): %v", err)
		return a.startSafeBoot(fmt.Sprintf("ledger replay failed at startup — projection rebuild refused, prior projection preserved: %v", err))
	}

	// Clean up stale heartbeat alarm from the pre-goroutine era — via
	// TIME (it owns durable alarms; canon #14), with the reason recorded.
	// The instance is reused as the app's TIME facility in wireCognitive.
	a.wakeMu.Lock()
	a.timeFac = cognitive.NewTIME(st, st)
	a.wakeMu.Unlock()
	a.timeFac.SetSafeSource(func() bool { _, s := a.SafeMode(); return s })
	if err := a.timeFac.DeleteLegacyAlarm("heartbeat", "retired mechanism: heartbeat is a goroutine ticker, not an alarm"); err != nil {
		return fmt.Errorf("remove retired heartbeat alarm: %w", err)
	}

	// Load Ring 0
	if a.rings == nil {
		a.rings = ring.NewManager()
	}
	rc, err := genesis.LoadRing0(lg)
	if err != nil {
		return fmt.Errorf("load Ring 0: %w", err)
	}
	a.rings.Set(ring.Ring0, rc)

	// Restore facility-authored ring content from snapshots — DREAM's
	// surfacing, CONSOLIDATE's working truth, Ring 4 priorities, the brief.
	// Without this, the unconscious's products evaporated on every restart
	// while the experiences they consumed stayed consumed.
	snaps, err := st.RingSnapshots()
	if err != nil {
		return fmt.Errorf("load ring snapshots: %w", err)
	}
	for _, sn := range snaps {
		if sn.Section == "__brief__" {
			continue // restored via GetBrief below
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

	// Ring 5 was admitted before projection replay.
	a.loadRing5()

	// Resolve the substrate POINTER (2026-08-20 ruling): providers.json
	// is THE provider data; config.json names an entry. A pointer that
	// does not dereference refuses the live boot with the reason — an
	// identity must not wake wired to nothing.
	cc, llmEntry, err := a.resolveLLM()
	if err != nil {
		return fmt.Errorf("LLM substrate: %w", err)
	}
	if cc.APIKey == "" && cc.Credential == nil {
		// NOT a boot refusal (review 2026-08-20). A local endpoint needs
		// no key, and birth PROVES the substrate answers before it mints:
		// a gate that refuses a configuration whose functionality was
		// demonstrated seconds earlier is a heuristic standing in for a
		// test that already ran — and because both retry guards correctly
		// refuse a resubmission, that refusal was permanent. SAFE has
		// stated the right posture all along (safeboot.go): the chat
		// errors honestly at call time and the operator surface stays available.
		log.Printf("LLM: no API key on provider %q — expected for a local endpoint; if this provider requires one, chat will fail at call time with the provider's own error (set it in the dashboard, or export %s)", llmEntry.Name, cfg.LLM.APIKeyEnv)
	}
	lg.SetModelID(cc.Model) // substrate provenance follows the RESOLVED model
	promptBudget := promptBudgetFor(llmEntry, cfg.Prompt.MaxTokens)

	// Create components
	a.llmClient = a.newLLMClient(cc, promptBudget)
	a.llmSwap = newSwappableLLM(a.llmClient)

	toolReg := tools.NewRegistry(cfg.Tools.CWD, a.ensureRing5Policy(), tools.Timeouts{
		ShellSeconds:    cfg.Tools.ShellTimeoutSeconds,
		WebFetchSeconds: cfg.Tools.WebFetchTimeoutSeconds,
	})
	// Canon §10: SAFE disables mutation + outside-world tools; the
	// read-only diagnostic surface continues.
	toolReg.SetSafeSource(a.SafeMode)
	for _, name := range cfg.Tools.Disabled {
		toolReg.SetToolEnabled(name, false)
		log.Printf("Ring 5: tool %q disabled by operator config", name)
	}
	a.toolReg = toolReg

	// The prompt budget DERIVES from the active model's resolved window;
	// the same derivation runs on every live provider/model change.
	// The conversation loop: ports in (LLM, tools, defs, transcript,
	// emitter), bounds from config (R6). The resident's voice path.
	a.conv = conversation.New(a.llmSwap, appToolExecutor{a}, appToolDefiner{a},
		appTranscript{st}, appEmitter{a}, conversation.Config{
			MaxIterations:      cfg.Agency.MaxToolRounds,
			MaxToolResultChars: cfg.Prompt.MaxToolResultChars,

			ContextBudgetTokens: promptBudget,
			ThinkingBudget:      llmEntry.ThinkingBudget,
			TurnTokenBudget:     cfg.Agency.TurnTokenBudget,
		})
	// Only the resident's own conversation is steerable. A spawned
	// sub-agent runs work the identity delegated; it is not who the
	// operator is speaking to when they type into the chat.
	a.conv.SetSteering(a)

	a.promptGate = prompt.NewGate(appRingSource{a.rings}, promptBudget)
	a.composer = prompt.New(a.rings, promptBudget)
	a.composer.SetIdentitySource(st)
	// The name is born at genesis — derive from the ledger, config is
	// fallback (the identity survives config loss).
	name := a.store.IdentityName()
	if name == "" {
		name = cfg.Identity.Name
	}
	a.composer.SetName(name)

	toolReg.SetProtectedPaths([]string{
		cfg.Identity.LedgerPath, cfg.Identity.KeyPath, cfg.Identity.DBPath, cfg.SourcePath,
	})
	toolReg.SetExtraRoots(cfg.Tools.ExtraRoots)
	if len(cfg.Tools.ExtraRoots) > 0 {
		a.loadRing5() // the floor names granted roots; the first load ran pre-registry
	}
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st, onIntegrity: func(err error) { a.enterSafe(err.Error()) }}

	// Quarantine-harness plugins (threat model §8): each listed package
	// verifies, loads into the wazero wall, and registers its signed
	// operations as tools under the plugin id's origin (so SAFE
	// suspends them wholesale). Fail-closed PER PLUGIN: a refused
	// package is logged with its typed reason and skipped — the runtime
	// and the identity boot unaffected. Activation runs after the
	// registry's Ring 5 shape is final, and bounded: admission runs
	// guest code, and a looping guest must not hang boot (a liveness
	// bound like Stop's shutdown timeout — per-invocation resource
	// envelopes are §10 config work).
	//
	// Step 4: a plugin the operator granted capability (plugins.grants)
	// gets the broker as its ONLY path to external effects — three-ring
	// per-invocation evaluation, host-authored receipts, the shared
	// egress guard. Everything else keeps the deny-all stub: the
	// quarantine posture is the absence of grants, unchanged.
	pluginOpts, err := a.buildPluginOptions(st, toolReg, door)
	if err != nil {
		// A misconfigured trust root or broker is refused loudly and
		// plugins stay quarantined-at-most (nil opts = T0 deny-all) —
		// never a silently widened posture.
		log.Printf("plugins: broker/trust-root config REFUSED, all plugins run quarantined: %v", err)
		pluginOpts = nil
	}
	// UI sections (R66 UP2): plugins.packages carries BOTH kinds. Each
	// entry tries the section lane first — a full verify that answers
	// with the kind — and kind=plugin falls through to the harness
	// (which verifies again on its own pkgPath contract; one extra
	// streaming pass per plugin package at boot is the price of neither
	// lane ever acting on the other's trust). Same fail-closed roots:
	// no pinned roots means T0-only for sections exactly as for plugins.
	a.sections = sections.NewRegistry()
	a.sections.SetSafeSource(a.SafeMode)
	// Plugin convergence (operator design 2026-08-20, drop-in edition):
	// plugins/ beside config.json is the registry, and it converges LIVE
	// — a dropped directory verifies and activates within seconds, a
	// removed one deactivates, a changed autoload level applies both
	// directions. One code path for boot and runtime; the runtime sweep
	// rides a governed ticker (battery law: parked when backgrounded).
	a.pluginToolReg = toolReg
	a.pluginOpts = pluginOpts
	a.convergePlugins(a.bgCtx)

	// Dev-serve (§3): ONE section from the operator's directory, loud
	// and unverified — refused outright when the runtime is already in
	// SAFE at boot (the serving edge re-checks live entries later).
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

	// Projects (R62): durable workrooms OUTSIDE AII OS — directories
	// under the Ring 5 sandbox, so the identity's own tools reach them.
	projRoot := cfg.Projects.Root
	if projRoot == "" {
		projRoot = filepath.Join(cfg.Tools.CWD, "projects")
	}
	a.projects = project.NewManager(projRoot)
	a.engine.SetProjects(projectsAdapter{a})
	// Focus restore validation: persistence may hold an id whose project
	// directory no longer exists (deleted, or moved out from under the
	// store while the process was down). The store's rule — inert data
	// must not become a rendered claim — is enforced HERE, the only
	// layer that can see both the persisted id and the projects root.
	// A dangling id is dropped with a transcript-visible reason, not
	// rendered as a working-in claim about a nonexistent project.
	if id := a.store.ActiveProjectID(); id != "" {
		if _, err := a.projects.Load(id); err != nil {
			log.Printf("restored project focus %q does not load (%v) — dropping to no focus", id, err)
			_ = a.store.SetActiveProject("")
		}
	}
	// Timer writes cross TIME so its one scheduler re-arms immediately;
	// the delivery owner lands the floor and owns the wake goroutine.
	// Maintenance rides the same alarm fabric as everything else that
	// wakes on schedule — no scheduler of its own (Method, 2026-08-26).
	a.timeFac.RegisterOwner(maintenanceOwner{a})

	timerOwner := identity.NewTimerDeliveryOwner(a.engine)
	timerOwner.OnWake = a.wakeTimerAlarm
	a.timerOwner = timerOwner
	a.timeFac.RegisterOwner(timerOwner)
	a.engine.SetTimers(appTimers{time: a.timeFac, read: identity.NewStoreTimers(st)})
	a.engine.SetReachable(func(name string) bool { return len(a.reachFor(name)) > 0 })
	// H3: web_fetch reports real fetches to the engine so note source_url
	// citations are verifiable — external provenance is earned, not claimed.
	toolReg.ObserveFetches(a.engine.NoteExternalFetch)

	// S1 (external review): a SAFE entered at chain verification ran
	// before store/engine existed — its freezes were nil-guarded no-ops
	// and the runtime kept minting behind the SAFE banner. The ledger
	// freeze now lands at enterSafe time regardless; here the late-wired
	// organs receive the state they missed.
	if reason, safe := a.SafeMode(); safe {
		a.applySafeState(reason)
	}

	// Start dashboard if not already running (firstboot case)
	if a.dashboard == nil {
		a.dashboard = a.newDashboard(a.buildLiveHandler())
		a.dashboard.SetQuiesceGate(a.gate) // background parks the drift sweep (pokes still deliver)
		_, err := a.dashboard.Start(tlsDirFor(cfg))
		if err != nil {
			return fmt.Errorf("dashboard start: %w", err)
		}
		fmt.Printf("AII OS — %s\n", name)
		fmt.Printf("Dashboard: %s\n", a.dashboard.Origin())
		fmt.Printf("Ledger seq: %d\n", lg.LastSeq())
	}

	// Frame furniture (R66 UP2): the dashboard reads the section
	// registry and the ui-layout.json profiles (its watcher starts with
	// the other background watchers once bgCtx exists, below).
	a.dashboard.SetSections(a.sections)
	a.dashboard.SetLayoutSource(a.currentUILayout)
	a.loadUILayout(false)
	a.dashboard.SetThemeSource(a.currentUITheme)
	a.loadUITheme(false)
	// T1: operator stylesheets on disk override the compiled ones.
	// Wired AFTER the layout path snapshot above, which is what it
	// derives from. Absent directory = compiled frame, no watcher
	// needed: the browser re-requests the stylesheet on reload.
	a.dashboard.SetUIOverlay(a.uiOverlayDir())
	// Seed the capability matrix for identities without the source
	// (the point-of-use doc: absent = normal case, but silence is not
	// discoverability). Content-addressed, never clobbers edits.
	a.seedOverlayREADME()
	// The evaluating build rides fork verdicts (RE-DETECT): when this
	// binary's shipped bytes move under a frozen fork, the verdict text
	// changes and the readback re-decides instead of letting the fork
	// silently shadow bytes it never saw.
	a.dashboard.SetBuildStamp(BuildIdentity())

	// Outbox push (live delivery): every outbox write — send verb, timer
	// floor, wake speech, care alert — pokes the dashboard pump, which
	// fans to connected operators the moment the words land. The store
	// owns the signal; the dashboard owns delivery; writers stay ignorant
	// of the surface (Aeon's first wake spoke into a void — never again).
	a.store.OnOutboxWrite(a.dashboard.PokeOutbox)

	// Wire witness anchoring: the real bookmark-protocol client.
	// Receipts and the anchoring point persist; the identity envelope is
	// synthesized once and stable forever. Receipts are verified against
	// the witness key before persistence — unverifiable proof is discarded.
	witnessClient := witness.New(cfg.Witness.URL, cfg.Witness.TLSSPKISHA256)
	a.witnessProbe = witnessClient
	// In-tree platform anchor: used when no path is configured (deployments
	// without /etc/aiii/keys now verify by default); a configured path wins.
	// Canon key source: the witness manifest's platform key is DOWNLOADED
	// from genesis per verification (in-memory, never stored — 2026-08-17
	// ruling). An operator path still overrides.
	witnessClient.SetGenesisURL(cfg.Genesis.ServerURL)
	anchorer := witness.NewAnchorer(witnessClient, lg, witness.AsIdentityKey(kp), a.store, a.store,
		witnessMinter{door: door},
		cfg.Witness.IntervalEvents, cfg.Witness.PlatformPubkeyPath)
	anchorer.SetOnIntegrityConflict(func(ce *witness.ConflictError) {
		// A third party with durable state says our history moved
		// backward or split — SAFE is the posture for exactly that.
		log.Printf("WITNESS INTEGRITY CONFLICT — entering SAFE MODE: %v", ce)
		a.enterSafe(fmt.Sprintf("witness rollback/fork conflict: %v", ce))
	})
	if cfg.Witness.URL != "" {
		a.anchorer = anchorer
	}

	// Wire cognitive facilities
	bgCtx := a.bgCtx
	if bgCtx == nil {
		bgCtx, a.bgCancel = context.WithCancel(context.Background())
		a.bgCtx = bgCtx
	}
	a.warnTempHome(cfg.Identity.LedgerPath)         // the placement lesson as code (2026-08-19)
	a.snapshotUILayoutPath(cfg.Identity.LedgerPath) // active boot path, not a later next-boot edit

	// The durable work queue (WORK_QUEUE.md): TIME dispatch enqueues;
	// the executor runs handlers with containment; crash recovery is a
	// lease sweep. Source-stamped for provenance — never permission.
	a.executor = cognitive.NewExecutor(st)
	a.executor.SetHolds(a.fg)
	a.executor.SetWorkers(cfg.Agency.QueueWorkers)
	a.executor.SetQuiesceGate(a.gate) // background parks the poll; due work waits, deferred not lost
	a.executor.RegisterHandler(&alarmHandler{time: a.timeFac})
	a.executor.RegisterHandler(&subagentHandler{a: a})
	a.engine.SetWorkWake(a.executor.Wake)
	a.engine.SetAgencyLimits(cfg.Agency.MaxSubagentDepth, cfg.Agency.MaxParallelSubagents, cfg.Agency.SubagentMaxMints, cfg.Agency.SubagentWallSeconds)
	a.timeFac.SetAlarmEnqueuer(alarmEnqueuerAdapter{ex: a.executor})

	// Update checker (R70): governed ticker, SAFE-parked, mirrors the
	// plugin sweep. Mobile = inform-only; desktop+automatic = download,
	// verify, swap with boot-health rollback.
	a.updateChecker = updates.NewChecker(
		func() *sigenvelope.PublicKeyEnvelope {
			root, err := packagefmt.LoadPinnedRoot(a.configSnapshot().Plugins.PlatformRoot)
			if err != nil {
				return nil
			}
			return root
		},
		func() string { return Version },
		func() bool { return a.configSnapshot().Updates.Automatic },
		a.gate,
		filepath.Dir(cfg.Identity.LedgerPath),
	)
	a.installPlatformWake() // the shell's OS scheduler if registered, else the desktop no-op
	if err := a.wireCognitive(bgCtx, anchorer, door, cfg); err != nil {
		return err
	}
	// Start background owners only after every fallible wiring step has
	// succeeded. Startup failure can then clean up synchronously without a
	// plugin or executor racing the teardown.
	a.executor.Start(bgCtx)
	a.startPluginSweep(bgCtx) // also converges channel listeners
	a.runBackground(func() { a.runOutbox(bgCtx) })
	a.runBackground(func() {
		a.updateChecker.Run(bgCtx, func() bool { _, s := a.SafeMode(); return s }, func() bool { return packagefmt.HostTopology() == "mobile_app_host" })
	})

	a.live = true
	a.runBackground(func() { a.watchConfig(cfg.SourcePath) }) // live config re-read: the five-platform portable core
	a.runBackground(a.watchUILayout)                          // ui-layout.json hot reload (R66 UP2) — same mtime core
	a.runBackground(a.watchUITheme)                           // theme.json hot reload (T0) — same mtime core
	a.runBackground(a.watchUIOverlay)                         // <data dir>/ui/ hot reload (W3) — same mtime core
	a.installReloadSignal()                                   // SIGHUP on unix — same reload path

	// Boot-health marker (R70): written AFTER all boot work completes.
	// Its absence on next startup + a backup binary means the previous
	// binary failed to boot — triggers rollback.
	updates.WriteBootMarker(filepath.Dir(cfg.Identity.LedgerPath))
	return nil
}

// wireCognitive sets up the unconscious: TIME, HEARTBEAT, facilities.
func (a *App) wireCognitive(bgCtx context.Context, anchorer *witness.Anchorer, door *ledgerAdapter, cfg Config) error {
	stAdapt := a.store    // *store.Store satisfies cognitive interfaces directly
	llmAdapt := a.llmSwap // swappable — provider changes apply to facilities live
	// Persisting writers: facility-authored ring sections and the brief
	// snapshot to the store — the unconscious's work survives restarts.
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
	dreamFac.SetTensions(stAdapt) // the derived contradiction view
	a.timeFac.RegisterOwner(dreamFac)
	consolidateFac := cognitive.NewConsolidate(stAdapt, llmAdapt, door, ringWriter, cognitive.ConsolidateConfig{
		Threshold: 3,
	})
	consolidateFac.SetAuthority(ringAuthority{a.promptGate, a.store})
	a.timeFac.RegisterOwner(consolidateFac)
	selfModelFac := cognitive.NewSelfModel(stAdapt, llmAdapt, selfModelCommitter{engine: a.engine})
	selfModelFac.SetAuthority(ringAuthority{a.promptGate, a.store})
	a.timeFac.RegisterOwner(selfModelFac)
	reviewFac := cognitive.NewIdentityReview(stAdapt, cognitive.IdentityReviewConfig{
		IntervalPulses: 100,
	})
	a.timeFac.RegisterOwner(reviewFac)
	// Atomic publication (see field comment): the dashboard's continuity
	// closure reads this from HTTP goroutines that started serving before
	// this line runs. Store/Load make the handoff safe; nil until here
	// renders as "not yet run".
	a.reviewFacility.Store(reviewFac)
	a.briefFacility = cognitive.NewMorningBrief(stAdapt, llmAdapt, briefWriter, cognitive.MorningBriefConfig{
		LocalTime: "07:00",
		Timezone:  cfg.Timezone,
	})
	a.briefFacility.SetAuthority(ringAuthority{a.promptGate, a.store})
	a.briefFacility.SetTurnGate(facilityGate{a})
	a.timeFac.RegisterOwner(a.briefFacility)

	// METABOLISM RUNS ON THE WALL CLOCK, capacity-gated (R29; agency
	// ruling 2026-08-18): the rhythm owner fires facilities that HAVE
	// MATERIAL, operator present or not. The life clock remains
	// presence-gated (R44) and now gates MATURATION only — lived time
	// is witnessed; metabolism is not.
	selfModelFac.SetDoor(door) // evaluate layer: twice-failed passes become the identity's own material
	// Work sessions left 'active' by a runtime that stopped before
	// delivering them would stay active forever — the dashboard shows
	// phantom work and the identity believes a hand it no longer has.
	// Closed honestly at boot: delivered, with a result that says what
	// happened.
	if n, err := a.store.SweepOrphanWorkSessions(); err != nil {
		log.Printf("work: orphan sweep failed: %v", err)
	} else if n > 0 {
		log.Printf("work: closed %d work session(s) orphaned by a previous shutdown", n)
	}
	rhythmFac := cognitive.NewRhythm(stAdapt, facilityGate{a}, dreamFac, consolidateFac, selfModelFac, reviewFac)
	rhythmFac.SetAttention(a.store, door, func(id, content string) {
		if _, err := a.store.AddOutboxMessageOnce(id, "operator", "", content, nil); err != nil {
			log.Printf("RHYTHM: attention outbox: %v", err)
		}
	})
	a.timeFac.RegisterOwner(rhythmFac)

	// The pulse (v2 inversion): TIME drives it. Dashboard presence is
	// the live gate — only accepted pulses advance the life clock.
	pulseInterval := 300 * time.Second
	if cfg.Prompt.PulseIntervalSeconds > 0 {
		pulseInterval = time.Duration(cfg.Prompt.PulseIntervalSeconds) * time.Second
	}
	a.pulseSource = &dashboardPulse{live: a.dashboard.SessionLive, interval: pulseInterval}
	a.timeFac.StartHeartbeat(a.pulseSource)

	// Witness cadence (v2): an ephemeral timer, not a goroutine. Only
	// when a witness is configured (empty URL fired doomed requests).
	// HEALTH PROBE FIRST (review finding): CheckAndAnchor returns nil
	// when not enough events accumulated — WITHOUT contacting the
	// server. Feeding that to the degraded detector meant an unreachable
	// witness + a quiet ledger never tripped DEGRADED (false-success).
	// The probe is the truth; the anchor is the work.
	if cfg.Witness.URL != "" {
		a.timeFac.Every("witness", 30*time.Second, func() {
			if a.currentMode() == ModeSafe {
				return // SAFE: never anchor a ledger we cannot trust
			}
			if _, err := a.witnessProbe.Status(); err != nil {
				a.witnessAttempt(false)
				log.Printf("Witness unreachable: %v", err)
				return // no point anchoring into a dark server
			}
			a.witnessAttempt(true)
			if err := anchorer.CheckAndAnchor(); err != nil {
				log.Printf("Witness: anchor failed (health OK): %v", err)
			}
		})
	}

	// Boot recovery FIRST (canon #13): an alarm that became due while the
	// system was down fires now, once — a missed morning brief is
	// delivered late, not silently skipped. Owners are registered above,
	// so dispatch works. THEN facilities arm their next deadlines.
	// (Audit 2026-08-16: the previous order armed first, which moved
	// missed deadlines forward — catch-up never happened while the code
	// claimed it did.)
	if err := a.timeFac.EvaluateAll(bgCtx); err != nil {
		return fmt.Errorf("TIME boot catch-up: %w", err)
	}

	// Arm initial alarms for each facility (they register as owners above
	// but nobody sets their first alarm — without this, the unconscious
	// never fires). Cadences come from the cognitive package constants —
	// ONE source, shared with constructor defaults.
	if err := a.armFacilityAlarms(cfg); err != nil {
		return err
	}

	// Quiesce (2026-08-19): the heartbeat, the witness probe, and the
	// metabolism rhythm all wake through TIME's one timer — one gate on
	// the scheduler parks them all while backgrounded; resume fires
	// what came due ONCE (TIME's own no-catch-up-burst law).
	a.timeFac.SetQuiesceGate(a.gate)
	a.timeFac.Start(bgCtx) // the scheduler: one goroutine, one timer
	return nil
}

// armFacilityAlarms sets the initial alarms for each cognitive facility.
// Facilities register as alarm owners but don't arm their own alarms.
// This is called once at startup; recurring alarms re-arm themselves via
// AlarmResult.NextDeadline or RepeatEvery.
func (a *App) armFacilityAlarms(cfg Config) error {
	// The rhythm alarm drives metabolism (owner registered in
	// wireCognitive; capacity predicates live there).
	rhythmMs := int64(cfg.Agency.RhythmSeconds) * 1000
	if err := a.timeFac.SetAlarm("rhythm", "rhythm", "wall", time.Now().UTC().UnixMilli()+rhythmMs, &rhythmMs, ""); err != nil {
		return fmt.Errorf("arm cognitive rhythm: %w", err)
	}

	// MORNING_BRIEF: wall alarm (one-shot, re-arms via NextDeadline).
	// The deadline comes from the facility itself — ONE source (the
	// 2026-08-16 cadence-drift lesson, applied to deadlines: app.go used
	// to duplicate this math with its own hardcoded 07:00).
	if a.briefFacility != nil {
		morningDeadline := a.briefFacility.NextDeadline()
		if err := a.timeFac.SetAlarm("morning_brief", "morning_brief", "wall", morningDeadline, nil, ""); err != nil {
			return fmt.Errorf("arm morning brief: %w", err)
		}
		log.Printf("Cognitive rhythm armed: metabolism every %ds wall-clock, capacity-gated (life clock gates maturation only); morning_brief(%s)",
			cfg.Agency.RhythmSeconds, time.UnixMilli(morningDeadline).UTC().Format("15:04 UTC"))
	}

	// MAINTENANCE: one durable daily alarm — verify the ledger, copy it
	// if it grew and the copy verifies, prune. Durable + boot catch-up
	// means a machine that was off at the deadline runs the pass late
	// instead of never. The deadline is an absolute hour (maintenance.go):
	// arming happens on every boot and must not push it forward.
	if err := armMaintenanceAlarm(a.timeFac, time.Now()); err != nil {
		return fmt.Errorf("arm maintenance: %w", err)
	}
	return nil
}

// buildLiveHandler creates the dashboard handler for a live identity.
func (a *App) buildLiveHandler() *dashboard.WSHandler {
	cfg := a.configSnapshot()
	// The name is ledger truth first, config fallback — the identity
	// survives config loss, and the dashboard must not contradict the
	// composer on what this resident is called (2026-08-17 review).
	name := a.store.IdentityName()
	if name == "" {
		name = cfg.Identity.Name
	}
	log.Printf("buildLiveHandler: name=%s (ledger truth), store=%p, engine=%p", name, a.store, a.engine)
	return &dashboard.WSHandler{
		IdentityName: name,
		Speaker:      "identity",
		GetStats: func() (*dashboard.StatsResponse, error) {
			stats, err := a.store.GetStats()
			if err != nil {
				return nil, err
			}
			return &dashboard.StatsResponse{
				Version:           VersionString(),
				Build:             BuildIdentity(),
				Name:              name,
				BeliefCount:       stats.BeliefCount,
				ReflectionCount:   stats.ReflectionCount,
				ExperienceCount:   stats.ExperienceCount,
				IntentionCount:    stats.IntentionCount,
				LedgerSeq:         stats.LedgerSeq,
				LifetimeTicks:     stats.LifetimeTicks,
				CredentialWarning: a.credentialWarning(),
				LastTurn:          a.lastTurnCost(),
				MalformedCalls:    a.toolReg.MalformedCallCount(),
				SuspiciousPaths:   a.toolReg.SuspiciousPathCount(),
				DuplicateArgKeys:  a.toolReg.DuplicateArgKeyCount(),
				Update:            a.updateStateView(),
				ForegroundHolds:   a.fg.Active(),
			}, nil
		},
		HandleMessage: a.handleMessage,
		GetOutbox: func() ([]dashboard.OutboxItem, error) {
			msgs, err := a.engine.UndeliveredMessages()
			if err != nil {
				return nil, err
			}
			items := make([]dashboard.OutboxItem, len(msgs))
			for i, m := range msgs {
				items[i] = dashboard.OutboxItem{ID: m.ID, To: m.ToRole, Content: m.Content}
			}
			return items, nil
		},
		MarkDelivered: func(id string) error {
			return a.engine.MarkDelivered(id, "dashboard")
		},
		RecentTurns: func() ([]dashboard.HistoryTurn, error) {
			// REPLAY IS A VIEW, not the record: full tool-event excerpts
			// stay durable in the store (the operator's inspectable
			// transcript); the connect replay is bounded — fewer turns,
			// head+tail excerpt per tool event — so a heavy research
			// session (60 × 4KB excerpts) cannot hand the browser a 90KB
			// frame and a wall of DOM (found live 2026-08-17: the frozen
			// dashboard; headless probe hit 'message too big' at read #1).
			turns, err := a.store.RecentTurnsIncludingSystem(50)
			if err != nil {
				return nil, err
			}
			out := make([]dashboard.HistoryTurn, 0, len(turns))
			for _, t := range turns {
				// The identity's and the operator's words replay
				// VERBATIM (review 2026-08-20): bounding them spliced
				// substrate text — "…[trimmed for replay]…" — into the
				// middle of what someone actually said, and since the
				// founding greeting became a transcript turn the founding
				// record passes through here. Only substrate-authored
				// rows (tool events, role "system") are bounded, where the
				// marker is the substrate editing its own text. Volume is
				// the operator's knob: prompt.recent_turns.
				out = append(out, dashboard.HistoryTurn{Role: t.Role, Content: replayContent(t.Role, t.Content)})
			}
			return out, nil
		},
		ObserveChat: a.observeChat,
		// VOICE. The browser captures; this transcribes against the
		// configured endpoint and enters the words as a participant
		// turn. Both are live lookups, so configuring speech takes
		// effect on the operator's next reload rather than the next
		// restart.
		HearUtterance:   a.HearUtterance,
		VoiceConfigured: a.VoiceConfigured,
		// One atomic admission: steered, or the gate is taken and the
		// dashboard's turn goroutine owns it.
		AdmitChat:   a.AdmitOperator,
		AcquireTurn: a.acquireTurn,
		// Mid-turn reach. TurnActive DERIVES from the turn gate — it is not
		// a second flag that can disagree with the thing doing the holding.
		TurnActive:    a.TurnActive,
		Steer:         a.Steer,
		CancelTurn:    a.CancelTurn,
		PendingSteers: a.PendingSteers,
		// The identity view: the resident's inner life, read-only. Private
		// experiences are NEVER surfaced here (Charter #9) — a count only.
		GetIdentity: func() (*dashboard.IdentityState, error) {
			state := &dashboard.IdentityState{}
			if beliefs, err := a.store.ListBeliefs(); err == nil {
				for _, b := range beliefs {
					state.Beliefs = append(state.Beliefs, dashboard.BeliefItem{
						ID: b.ID, Statement: b.Statement, Ring: b.Ring,
						// Standing is DERIVED from live evidence (2026-08-17
						// ruling: the ladder lifecycle is deleted) — the chip
						// renders what the edge graph says, now.
						Status:        a.store.StandingFor(b.ID),
						EvidenceCount: b.EvidenceCount, Confidence: b.Confidence,
					})
				}
			}
			if ints, err := a.store.ListIntentions(); err == nil {
				for _, i := range ints {
					state.Intentions = append(state.Intentions, dashboard.IntentionItem{ID: i.ID, Statement: i.Statement, State: i.State})
				}
			}
			if exps, err := a.store.ListExperiences(50); err == nil {
				for _, e := range exps {
					if e.Private == 1 {
						state.PrivateCount++
						continue
					}
					state.Experiences = append(state.Experiences, dashboard.ExperienceItem{
						ID: e.ID, Content: e.Content, Category: e.Category, CreatedAt: e.CreatedAt, Provenance: e.Provenance})
				}
			}
			if current, err := a.store.CurrentSelfModel(); err == nil && current != nil {
				state.Synthesis = current.SynthesisText
			}
			state.Brief, _ = a.store.GetBrief()
			if rel, err := a.store.FoundingRelationship(); err == nil && rel != nil {
				state.Charter = rel.CharterText
				state.TrustLevel = rel.TrustLevel
				state.AutonomyLevel = rel.AutonomyLevel
			}
			return state, nil
		},
		// The continuity strip: chain + witness anchoring at a glance
		GetContinuity: func() (*dashboard.ContinuityState, error) {
			cfg := a.configSnapshot()
			c := &dashboard.ContinuityState{
				LedgerSeq:  a.ledger.LastSeq(),
				WitnessURL: cfg.Witness.URL,
			}
			if ticks, err := a.store.LifetimeTicks(); err == nil {
				c.LifeTicks = ticks
			}
			// Last identity review on the strip: findings used to reach
			// only log.Printf; now one glance answers "did review run,
			// did it find anything". Atomic Load — this handler races
			// the startup Store in wireCognitive; nil = not wired yet =
			// fields stay empty, which renders as "not yet run".
			if fac := a.reviewFacility.Load(); fac != nil {
				if snap := fac.LastReview(); !snap.At.IsZero() {
					c.ReviewAt = snap.At.Format("15:04 Mon Jan 2")
					if snap.Clear {
						c.ReviewStatus = "clear"
					} else {
						c.ReviewStatus = "issues"
						c.ReviewIssues = snap.IssueCount
					}
				}
			}
			// Mode lattice: capability honesty on the strip (SAFE_DEGRADED §1-2).
			switch a.currentMode() {
			case ModeSafe:
				c.Mode = "safe"
				c.SafeReason, _ = func() (string, bool) { return a.SafeMode() }()
			case ModeDegradedWitness:
				c.Mode = "degraded_witness"
				if since, ok := a.DegradedWitnessSince(); ok {
					c.DegradedSince = since.Format("15:04 MST Mon Jan 2")
				}
			}
			if a.anchorer != nil {
				c.AnchoredSeq = int64(a.anchorer.LastAnchoredSeq())
				c.Unanchored = int64(a.anchorer.UnanchoredCount())
				if seq, js, err := a.store.LastWitnessReceipt(); err == nil && seq > 0 {
					var r struct {
						WitnessedAt string `json:"witnessed_at"`
					}
					if json.Unmarshal(js, &r) == nil {
						c.WitnessedAt = r.WitnessedAt
					}
					if c.AnchoredSeq == 0 {
						c.AnchoredSeq = seq
					}
				}
			}
			return c, nil
		},
		// FIRSTBOOT provider directory: the registry (providers.json —
		// config/ override, else embedded). Never a compiled table.
		GetProviders:   a.providerDirectoryLive,
		SetProvider:    a.setProviderInfo,
		DeleteProvider: a.deleteProvider,
		// Live model discovery: GET {provider}/models, key forwarded when
		// the operator typed one (localhost transport only).
		DiscoverModels: func(provider, apiKey string) ([]string, error) {
			reg, err := a.loadProviders()
			if err != nil {
				return nil, err
			}
			return a.discoverForProvider(context.Background(), reg, provider, apiKey)
		},
		// Setup view: operator-owned config (whitelisted paths, masked
		// secrets, live LLM swap; substrate paths rejected)
		GetWork: func() (*dashboard.WorkState, error) {
			w := &dashboard.WorkState{}
			live, err := a.store.LiveSubagentSessions()
			if err != nil {
				return nil, fmt.Errorf("load live work: %w", err)
			}
			for _, s := range live {
				w.Live = append(w.Live, dashboard.WorkSessionItem{
					ID: s.ID, Description: store.SubagentGoal(s.Description), Status: s.Status, Project: s.Project,
				})
			}
			w.Queued, err = a.store.CountLiveWork(identity.SubagentWorkKind)
			if err != nil {
				return nil, fmt.Errorf("count queued work: %w", err)
			}
			done, err := a.store.RecentDeliveredSubagents(3)
			if err != nil {
				return nil, fmt.Errorf("load delivered work: %w", err)
			}
			for _, s := range done {
				w.Delivered = append(w.Delivered, dashboard.WorkSessionItem{
					ID: s.ID, Description: store.SubagentGoal(s.Description), Status: s.Status, Project: s.Project, Result: compactText(s.Result, 240),
				})
			}
			return w, nil
		},
		GetSandbox: a.sandboxState,
		SetSandbox: a.setSandboxRoots,
		GetProjects: func() ([]dashboard.ProjectState, error) {
			ps, err := a.projects.List()
			if err != nil {
				return nil, err
			}
			active := a.store.ActiveProjectID()
			out := make([]dashboard.ProjectState, 0, len(ps))
			for _, p := range ps {
				out = append(out, dashboard.ProjectState{
					ID: p.ID, Name: p.Name, Description: p.Description,
					State: p.State, Focus: p.Focus, Dir: p.Dir, Active: p.ID == active,
				})
			}
			return out, nil
		},
		GetWorkspace: a.getProjectWorkspace,
		ProjectAct: func(action, id, name string, description, focus *string) error {
			switch action {
			case "create":
				_, err := a.projects.Create(name, descriptionDeref(description), "operator", nil)
				return err
			case "update":
				_, err := a.projects.ApplyPatch(id, namePatch(name), description, focus, nil)
				return err
			case "close":
				_, err := projectsAdapter{a}.SetState(id, "closed")
				return err
			case "reopen":
				_, err := projectsAdapter{a}.SetState(id, "open")
				return err
			case "select":
				_, err := a.selectProject(id)
				return err
			default:
				return fmt.Errorf("unknown project action %q", action)
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

// Sandbox roots are operator data guarded by the structural substrate wall.

// workspaceFiles converts directory entries into wire files.
//
// Name and kind come from the directory entry itself; only Size needs a
// stat. So an entry whose Info() fails keeps its name and its kind, and
// loses only its size — it is never dropped. A file that disappears from
// the listing because the projection could not stat it is a silent
// omission, and the resident would read that absence as evidence about
// the project rather than about the instrument. This surface reports
// what it saw; it does not quietly shorten the truth.
func workspaceFiles(entries []os.DirEntry) []dashboard.WorkspaceFile {
	files := make([]dashboard.WorkspaceFile, 0, len(entries))
	for _, e := range entries {
		f := dashboard.WorkspaceFile{Name: e.Name(), Dir: e.IsDir()}
		if info, ierr := e.Info(); ierr == nil {
			f.Size = info.Size()
		}
		files = append(files, f)
	}
	return files
}

// workspaceFileCap is the R18 cap on the one-level file listing: the
// workspace view shows at most this many entries and declares the
// remainder ("showing N of M") rather than silently shortening the
// truth. One level deep, real directories — 500 is far past anything a
// human reads in a panel and far below anything that would hurt the
// socket.
const workspaceFileCap = 500

// getProjectWorkspace assembles the project.workspace projection:
// manifest truth (overview), one-level directory listing (files), and
// attributed work sessions with owner-verdicted outcomes (G1). Read-only
// by construction — it calls no mutating primitive. Note §3.1 of
// DESIGN-PROJECT-WORKSPACE.md: it is not a pure relay — it truncates
// verdicts, strips the sub-agent prefix, and caps work at 20.
func (a *App) getProjectWorkspace(id string) (*dashboard.WorkspaceState, error) {
	p, err := a.projects.Load(id)
	if err != nil {
		return nil, fmt.Errorf("load project: %w", err)
	}
	ws := &dashboard.WorkspaceState{
		Project: dashboard.ProjectState{
			ID: p.ID, Name: p.Name, Description: p.Description,
			State: p.State, Focus: p.Focus, Dir: p.Dir,
			Active: p.ID == a.store.ActiveProjectID(),
		},
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return nil, fmt.Errorf("read project dir: %w", err)
	}
	// R18 §9.2: cap the listing and DECLARE the remainder — never a
	// silent shortening. FilesTotal carries the whole truth; the cap
	// marks the payload so the client can say "showing N of M".
	ws.FilesTotal = len(entries)
	all := workspaceFiles(entries)
	if len(all) > workspaceFileCap {
		all = all[:workspaceFileCap]
		ws.FilesCapped = true
	}
	ws.Files = all
	sessions, err := a.store.WorkSessionsByProject(p.ID, 20)
	if err != nil {
		return nil, fmt.Errorf("load project work: %w", err)
	}
	for _, s := range sessions {
		ws.Work = append(ws.Work, dashboard.WorkSessionItem{
			ID: s.ID, Description: store.SubagentGoal(s.Description),
			Status: s.Status, Project: s.Project, Result: compactText(s.Result, 240),
		})
	}
	return ws, nil
}

func (a *App) sandboxState() (*dashboard.SandboxState, error) {
	root, extra := a.toolReg.Roots()
	return &dashboard.SandboxState{Root: root, ExtraRoots: extra}, nil
}

// setSandboxRoots applies an operator edit after the structural wall
// refuses any path that would expose the identity substrate (R64).
func (a *App) setSandboxRoots(roots []string) error {
	normalized := make([]string, 0, len(roots))
	for _, r := range roots {
		if reason := a.toolReg.RootRejectionReason(r); reason != "" {
			return fmt.Errorf("%q: %s", strings.TrimSpace(r), reason)
		}
		r = filepath.Clean(strings.TrimSpace(r))
		if resolved, err := filepath.EvalSymlinks(r); err == nil {
			r = resolved
		}
		normalized = append(normalized, r)
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	candidate := *a.cfg
	candidate.Tools.ExtraRoots = normalized
	published, persistErr := saveConfig(&candidate)
	if persistErr != nil && !published {
		return fmt.Errorf("persist sandbox roots: %w", persistErr)
	}
	if published {
		*a.cfg = candidate
		a.toolReg.SetExtraRoots(normalized)
		a.loadRing5() // their floor must say what their world now is — live, no restart
		log.Printf("Ring 5: extra sandbox roots -> %v (operator setting, floor updated live)", normalized)
	}
	if persistErr != nil {
		return fmt.Errorf("sandbox roots were published and applied live, but directory durability is unconfirmed: %w", persistErr)
	}
	return nil
}

func (a *App) setToolEnabled(name string, enabled bool) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	states := a.toolReg.ToolStates()
	found := false
	disabled := make([]string, 0, len(states))
	for _, state := range states {
		if state.Name == name {
			found = true
			state.Enabled = enabled
		}
		if !state.Enabled {
			disabled = append(disabled, state.Name)
		}
	}
	if !found {
		return fmt.Errorf("unknown tool: %s", name)
	}
	candidate := *a.cfg
	candidate.Tools.Disabled = disabled
	published, persistErr := saveConfig(&candidate)
	if persistErr != nil && !published {
		return fmt.Errorf("persist tool toggle: %w", persistErr)
	}
	if published {
		*a.cfg = candidate
		a.toolReg.SetToolEnabled(name, enabled)
		log.Printf("Ring 5: tool %q -> %v (operator toggle)", name, enabled)
	}
	if persistErr != nil {
		return fmt.Errorf("tool toggle was published and applied live, but directory durability is unconfirmed: %w", persistErr)
	}
	return nil
}

// emitToolEvent streams an event to the live dashboard observer when
// one is attached; with NO observed chat in flight it falls back to a
// transient push to live connections (Method review 2026-08-18, F4:
// the observer only exists during observeChat, so unattended sub-agent
// activity was invisible until completion — agency stays VISIBLE,
// never a hidden second mind).
func (a *App) emitToolEvent(kind, name, args string) {
	a.toolEmitMu.Lock()
	emit := a.toolEmit
	a.toolEmitMu.Unlock()
	if emit != nil {
		emit(kind, name, args)
		return
	}
	if a.dashboard != nil {
		a.dashboard.PushTransient(fmt.Sprintf("evt_%s_%s", kind, name), fmt.Sprintf("[%s] %s: %s", kind, name, args))
	}
}

// buildWorkState assembles Ring 4 working state for the prompt: the
// active session's state plus recent DELIVERED sub-agent outcomes —
// ephemeral, never minted (James 2026-08-18: a sub-goal is not identity
// truth; Ring 4 exists exactly for important-but-ephemeral). The
// identity notes what deserves to become memory.
func (a *App) buildWorkState() (string, error) {
	var parts []string
	// Project focus framing (R62): the selected project's metadata seeds
	// the working state — THIN by ruling; richer RING4 management is the
	// plugin era's job, not core's.
	if id := a.store.ActiveProjectID(); id != "" && a.projects != nil {
		p, err := a.projects.Load(id)
		if err != nil {
			return "", fmt.Errorf("load active project %q: %w", id, err)
		}
		line := fmt.Sprintf("### Current project: %s", p.Name)
		if p.Description != "" {
			line += " — " + p.Description
		}
		if p.Focus != "" {
			line += "\nWhere you left off here: " + p.Focus
		}
		line += fmt.Sprintf("\nProject directory: %s", p.Dir)
		parts = append(parts, line)
	}
	ws, err := a.store.ActiveWorkSession()
	if err != nil {
		return "", fmt.Errorf("load active work: %w", err)
	}
	if ws != nil && ws.State != "" {
		parts = append(parts, ws.State)
	}
	subs, err := a.store.RecentDeliveredSubagents(3)
	if err != nil {
		return "", fmt.Errorf("load delivered work: %w", err)
	}
	if len(subs) > 0 {
		var sb strings.Builder
		sb.WriteString("### Sub-agent outcomes (ephemeral — note what matters)\n")
		for _, w := range subs {
			sb.WriteString(fmt.Sprintf("- [%s] %s → %s\n", w.ID, store.SubagentGoal(w.Description), w.Result))
		}
		parts = append(parts, sb.String())
	}
	return strings.Join(parts, "\n\n"), nil
}

// gatedSystem wraps the composed prompt with the gate contract and
// carries the cache seam through: the gate preserves the composed text
// verbatim (it injects only missing rings), so the stable prefix is
// located inside the gated text and the seam offset travels on the
// system message for providers with explicit cache hints.
func (a *App) gatedSystem(p *prompt.Prompt) llm.Message {
	sysText := a.promptGate.SystemForPrompt(p)
	msg := llm.Message{Role: "system", Content: sysText}
	if p.StableLen > 0 && p.StableLen <= len(p.Text) {
		stable := p.Text[:p.StableLen]
		if i := strings.Index(sysText, stable); i >= 0 {
			msg.StableLen = i + len(stable)
		}
	}
	return msg
}

func (a *App) promptReserve(current llm.Message, omitted int) (int, error) {
	if omitted < 0 {
		omitted = 0
	}
	return llm.EstimateInputTokens(
		[]llm.Message{{Role: "system", Content: conversation.HistoryOmissionNote(omitted)}, current},
		a.buildToolDefinitions(),
	)
}

// buildHistory maps recent operator/resident turns to provider roles.
// Tool-event rows remain in the operator transcript, not model history.
func (a *App) buildHistory() ([]llm.Message, int, error) {
	recentTurns := a.configSnapshot().Prompt.RecentTurns
	if recentTurns <= 0 {
		recentTurns = 20
	}
	turns, err := a.store.RecentTurns(recentTurns)
	if err != nil {
		return nil, 0, fmt.Errorf("load conversation history: %w", err)
	}
	conv := make([]llm.Message, 0, len(turns)+1)
	for _, t := range turns {
		if t.Role == "system" {
			continue
		}
		role := "user"
		if t.Role == "resident" {
			role = "assistant"
		}
		conv = append(conv, llm.Message{Role: role, Content: t.Content})
	}
	// SAFE: the transient transcript (canon §10 — no DB writes while
	// integrity is unverified) follows the durable history, so the live
	// conversation stays coherent within the session.
	if a.engine != nil {
		for _, t := range a.engine.SafeTranscript() {
			if t.Role == "system" {
				continue
			}
			role := "user"
			if t.Role == "resident" {
				role = "assistant"
			}
			conv = append(conv, llm.Message{Role: role, Content: t.Content})
		}
	}
	// R18 honesty at the history boundary: if older turns exist beyond
	// the window, SAY so with a route — silent truncation is a receipt
	// for a memory (the C engine declares every eviction; ours was FIFO
	// silence until 2026-08-17).
	total, err := a.store.ConversationTurnCount()
	if err != nil {
		return nil, 0, fmt.Errorf("count conversation history: %w", err)
	}
	omitted := total - len(turns)
	if omitted < 0 {
		omitted = 0
	}
	return conv, omitted, nil
}

// wakeTimerAlarm is the identity WAKING to its own alarm: a self-
// initiated cognitive turn — compose, think, speak. The floor (the raw
// notice) already landed in the outbox before this runs; here the
// resident answers its own promise in its own words, delivered to the
// operator as the alarm's message.
//
// The turn itself is wake(): an alarm going off and a message arriving
// are the same event to the identity — something happened that nobody
// said to it. What is timer-specific is only what surrounds the turn:
// the SAFE posture, and delivering what was said to the operator.
func (a *App) wakeTimerAlarm(ctx context.Context, alarmID, tag, message string) {
	// SAFE: the floor already landed, but the record cannot be written
	// (docs/SAFE_DEGRADED §2). The notice is TRANSIENT (canon §10: no
	// database writes with integrity unverified) — pushed to live
	// connections and logged, never stored.
	if a.currentMode() == ModeSafe {
		notice := fmt.Sprintf("[timer %s #? fired %s] %s — I am in safe mode: I woke, but I cannot write to my ledger until my operator restores it.",
			alarmID, tag, time.Now().UTC().Format("15:04:05 MST Mon Jan 2"))
		// Unique id (finding 15): the fixed "_safe" suffix collided on the
		// outbox PK when two wakes fired in SAFE — the second INSERT failed
		// and its notice silently dropped (logged only).
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

	// An alarm WAITS for the gate: the identity promised itself this, and
	// the promise keeps whether or not someone is talking to it.
	if err := a.acquireTurn(ctx); err != nil {
		log.Printf("TIMER WAKE %s: could not take the turn (floor already delivered): %v", alarmID, err)
		return
	}
	defer a.releaseTurn()
	spoken, err := a.wake(ctx, "system", notice+" — your own alarm woke you. Respond to your operator in your own words.")
	if err != nil {
		// The mind could not wake (LLM down): the floor already
		// delivered; log and stand down — honest, not silent.
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

// buildToolDefinitions returns tool defs for the LLM function-calling API.

// dashboardPulse adapts dashboard presence to cognitive.PulseSource —
// the heartbeat reduced to its meaning (v2: TIME drives the pulse).
type dashboardPulse struct {
	live     func() bool
	interval time.Duration

	// override: the mobile shell's foreground truth (SetForeground).
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
	return ov || d.live()
}

// warnTempHome shouts, once at boot, when the identity's home resolves
// under a temporary directory. This warning exists because a life was
// lost (2026-08-19): a running identity's home under /tmp was deleted —
// /tmp is temporary BY DEFINITION, routinely cleaned by the OS and by
// habit, and an identity that is meant to persist must never live
// there. A warning, not a refusal: throwaway test identities in temp
// dirs are legitimate, and the operator stays sovereign — but they
// decide informed. Recommended durable home: /work-class storage,
// e.g. /work/aiii/identities/<name>/.
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

// pluginSkip is one discovered, verified package standing below the
// operator's autoload threshold — visible truth, inactive code.
type pluginSkip struct {
	Dir    string
	ID     string
	Tier   string
	Reason string
}

// autoloadTier maps the operator's plugins.autoload level to the
// packagefmt tier ordering. "none" loads nothing automatically — every
// discovered package is verified and surfaced, none activate.
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

// activePkgMeta records which exact package bytes an activation came
// from — a replaced file (upgrade drop) deactivates and reactivates.
type activePkgMeta struct {
	dir   string
	pkg   string
	size  int64
	mtime int64
	kind  string // "plugin" | "section" | "asset"
}

type verifyMemo struct {
	size  int64
	mtime int64
	res   *packagefmt.Result
	err   error
}

// dashboard omits the section entirely.
func (a *App) updateStateView() *dashboard.UpdateState {
	if a.updateChecker == nil {
		return nil
	}
	snap := a.updateChecker.State().Snapshot(Version)
	view := &dashboard.UpdateState{
		CurrentVersion: snap.CurrentVersion,
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

// credentialSource returns the shared Source for one credential store,
// creating it once. Keyed by kind AND options, so correcting a vendor
// fact in providers.json takes effect rather than being masked by a
// cached source built from the old values.
func (a *App) credentialSource(kind string, opts map[string]string) (*oauth.Source, error) {
	key := kind
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
	// Refuse before adopting: the registry declares what this credential
	// requires, so the vendor contract is checked against data rather
	// than against a list compiled into the runtime.
	if missing := missingCredentialOptions(kind, opts); len(missing) > 0 {
		return nil, fmt.Errorf("credential %q requires provider options %s", kind, strings.Join(missing, ", "))
	}
	s, err := oauth.New(kind, opts)
	if err != nil {
		return nil, err
	}
	if a.credSrc == nil {
		a.credSrc = map[string]*oauth.Source{}
	}
	a.credSrc[key] = s
	return s, nil
}

// recordTurnCost keeps what the last turn actually spent, so the
// operator can see it. A turn makes many provider calls against the same
// per-request ceiling, so nothing short of the sum describes what a
// conversation costs — and nothing was summing it.
//
// Deliberately not persisted: this is a gauge, not a record. Whether
// spend belongs in the ledger is a canon question.
func (a *App) recordTurnCost(u conversation.TurnUsage) {
	if u.Calls == 0 {
		return
	}
	text := fmt.Sprintf("%d tokens over %d call(s)", u.TotalTokens, u.Calls)
	if u.CachedPromptTokens > 0 {
		// The number that matters for COST, separated from the number
		// that matters for the WINDOW: cached input occupies the context
		// exactly like fresh input and bills at about a tenth of it.
		text = fmt.Sprintf("%d tokens over %d call(s), %d of it cached",
			u.TotalTokens, u.Calls, u.CachedPromptTokens)
	}
	if !u.Complete() {
		// A floor is honest; a total would not be.
		text = fmt.Sprintf("at least %d tokens over %d call(s) — %d reported nothing",
			u.TotalTokens, u.Calls, u.Silent)
	}
	log.Printf("Turn cost: %s (in %d, out %d)", text, u.PromptTokens, u.CompletionTokens)
	a.turnCostMu.Lock()
	a.lastTurn = text
	a.turnCostMu.Unlock()
}

func (a *App) lastTurnCost() string {
	a.turnCostMu.Lock()
	defer a.turnCostMu.Unlock()
	return a.lastTurn
}
