package app

import (
	"context"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/foreground"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/googleoidc"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/updates"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

type App struct {
	dash dashState

	embeddedAuthentication bool

	cfg *Config

	dashboard *dashboard.Server

	logSink *logsink.Sink

	mode modeState

	promptGate *prompt.Gate

	ring5Policy *firewall.Policy

	birthMu sync.Mutex

	ring0Content  string
	ring0Bundle   []byte
	ring5Content  string
	bootstrapText string

	ring0Why, ring5Why, bootstrapWhy string
	genesisClient                    *genesis.GenesisClient

	identityClaim *os.File

	keyPair      *crypto.KeyPair
	ledger       *ledger.Ledger
	store        *store.Store
	databaseView atomic.Pointer[databaseView]
	rings        *ring.Manager
	engine       *identity.Engine
	projects     *project.Manager
	composer     *prompt.Composer
	llmClient    *llm.Client
	toolReg      *tools.Registry

	pluginState

	audio         *audio.Plane
	audioOnce     sync.Once
	voiceSeq      atomic.Uint64
	voiceModes    sync.Map
	voiceSessions sync.Map
	voiceOpens    openTurn

	voiceModePub atomic.Pointer[VoiceModeConfig]

	voiceSpeakOffRev atomic.Uint64

	voicePending sync.Map

	speakerPending sync.Map

	heardWords   spokenLedger
	heardHistory heardHistory
	acts         pluginActs

	asks askRegistry

	voiceSafeDropped atomic.Uint64

	voiceEventSink  func(dashboard.VoiceEvent)
	voiceFanDropped atomic.Uint64

	voiceControlSink  func(dashboard.VoiceEvent) error
	voiceStaleReplies atomic.Uint64
	voiceReplySink    func(dashboard.VoiceReplyRef, string)
	voiceHushSink     func(dashboard.VoiceHush)

	speakerWithheldFinals   atomic.Uint64
	speakerWithheldPartials atomic.Uint64
	spokenMu                sync.Mutex
	spoken                  map[string]*spokenReply
	speechReserveMu         sync.Mutex
	speechReservedChars     int
	speechReservedHeard     time.Duration
	voiceObs                map[*pluginhost.VoiceSession]bool
	voiceObsMu              sync.Mutex
	sweepPoke               chan struct{}
	restartCh               chan struct{}
	restartOnce             sync.Once
	restarting              atomic.Bool
	startAttempt            int
	startRetryTimer         *time.Timer
	stage                   stageProbe
	pluginToolReg           *tools.Registry

	pluginOpts *pluginhost.Options
	catalog    plugincatalog.Catalog
	pkgFetch   packageFetch

	storeMaterialize func(context.Context, pluginhost.Material, pluginhost.ModelFetcher) error

	lateModels *pluginhost.Acquirer

	trustDir    string
	trustGuard  packagefmt.EpochGuard
	trustFinger string

	sections *sections.Registry

	turn turnState

	queueWake func()

	composedInterrupted string

	composedWithheld     []string
	composedWithheldText string

	toldModel, composedModel, composedModelText string

	bootInterrupted []string

	bootSwept []store.WorkSession

	continuity *continuity.Runtime

	bootChain string

	bootAttested     int64
	bootAttestedHash string

	cfgMu sync.RWMutex

	credentialPathRead func()

	keyOrigin atomic.Pointer[string]

	signInTimeout time.Duration

	oauthTransport  http.RoundTripper
	oauthGuard      func(context.Context, string) error
	brokerTransport http.RoundTripper
	brokerGuard     func(context.Context, string) error

	providers providerState

	memory memoryState

	project projectState

	turnGate chan struct{}

	composedUnharvested []composedOutcome

	outcomeTooLarge map[string]int

	composedEvents    []store.TurnEvent
	composedEventText string
	carriedEvents     bool

	outboxPoke chan struct{}

	listening    map[string]*channelListener
	listeningSet []*pluginhost.ActivePlugin

	channels channelState

	scheduling map[string]*scheduleRunner

	scheduleFirst time.Duration
	scheduleEvery func(pluginhost.ScheduleDecl) time.Duration

	wakeParticipantFn func(ctx context.Context, fact string) (string, error)

	recordInboundFn func(rowID, channel, from, body string) (bool, error)

	googleOIDC oidcVerifier

	turnMu          sync.Mutex
	interactionTurn string
	steers          []steerEntry

	turnVoice []*voiceBinding

	voiceReplyShown atomic.Bool

	turnCancel context.CancelCauseFunc

	turnFacility bool

	steerFlush func([]steerEntry)

	steerBeforeQueue func()

	turnAfterHandoff func()

	turnFgRelease func()

	fg *foreground.Holds

	conv *conversation.Loop

	safeTools *safeToolRecord

	llmSwap *swappableLLM

	toolRefusals *llm.ToolRefusals

	sentRequests *llm.SentRequests

	modalities modalityMemo

	speechTrouble speechTrouble

	voiceOpened map[string]*pluginhost.VoiceSession

	specMu   sync.Mutex
	specSaid map[string][]speechItem

	anchorer     *witness.Anchorer
	witnessProbe *witness.Client

	door *ledgerAdapter

	pn *publicname.Runtime

	updateChecker *updates.Checker

	machine machineLinkState

	timeFac       *cognitive.TIME
	executor      *cognitive.Executor
	timerOwner    *identity.TimerDeliveryOwner
	pulseSource   *dashboardPulse
	briefFacility *cognitive.MorningBriefFacility

	cognition cognitionFacilities

	bgCtx    context.Context
	bgCancel context.CancelFunc

	bgStop   context.CancelCauseFunc
	bgMu     sync.Mutex
	bgWG     sync.WaitGroup
	stopping bool

	gate *quiesce.Gate

	watchEvery time.Duration

	projectsPush     func(id string, ws *dashboard.WorkspaceState)
	projectsListPush func()

	wakeMu       sync.Mutex
	platformWake cognitive.PlatformWake

	stopOnce sync.Once
	stopDone chan struct{}
	stopErr  error

	live atomic.Bool
}

func New(cfg *Config) *App {
	turnGate := make(chan struct{}, 1)
	turnGate <- struct{}{}
	bgCtx, bgStop := context.WithCancelCause(context.Background())
	a := &App{
		bootAttested: -1,
		toolRefusals: llm.NewToolRefusals(),
		fg:           &foreground.Holds{},
		restartCh:    make(chan struct{}),
		cfg:          cfg, gate: quiesce.NewGate(), turnGate: turnGate,
		outboxPoke: make(chan struct{}, 1), listening: map[string]*channelListener{}, scheduling: map[string]*scheduleRunner{},

		sweepPoke: make(chan struct{}, 1),
		bgCtx:     bgCtx, bgCancel: func() { bgStop(nil) }, bgStop: bgStop,
		googleOIDC: googleoidc.New(&http.Client{Timeout: 15 * time.Second}),
	}
	if cfg != nil {
		a.memory.limits = limitsOf(cfg.Memory)
		a.publishVoiceMode(cfg.Speech.Mode)
	}
	a.sentRequests = llm.NewSentRequests(a.sentLimits)
	a.continuity = continuity.New(a.continuityWires())
	a.pn = publicname.New(a.publicNameWires())
	return a
}
