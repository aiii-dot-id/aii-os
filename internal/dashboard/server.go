// Package dashboard implements the web dashboard — the one interface for AII OS.
//
// Serves HTTP on 127.0.0.1, serves embedded HTML/CSS/JS frontend, and
// provides a WebSocket for real-time chat and status updates.
// There is no CLI. This is the only interface.
package dashboard

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/coder/websocket"
)

//go:embed static/*
var staticFS embed.FS

// Server is the web dashboard server.
type Server struct {
	// Live WS connections — shutdown closes them so http.Server.Shutdown
	// cannot block forever on a hijacked (never-idle) socket (found live
	// 2026-08-16: ^C with the dashboard open hung the process).
	wsMu    sync.Mutex
	wsConns map[*websocket.Conn]*wsClient

	// Outbox push: the app wires Store.OnOutboxWrite -> PokeOutbox; one
	// pump goroutine fans undelivered messages to every live connection
	// the moment they land (event-driven — the 5s per-conn poller this
	// replaces was the quick fix; push-on-write is the right shape).
	// The 60s sweep is drift insurance, not the delivery path.
	outboxSignal chan struct{}
	pumpCancel   context.CancelFunc
	pumpDone     chan struct{}
	// turnCtx is the dashboard's own lifetime context for chat turns.
	// A turn is the IDENTITY's work, not a property of whichever socket
	// opened it: deriving the turn's context from r.Context() (one
	// browser connection) meant a page reload mid-turn cancelled the
	// running LLM call — "LLM call abandoned (caller ended it): context
	// canceled" (live 2026-08-25). Responses are broadcast precisely so
	// a reloaded page gets them; the turn's LIFETIME deserved the same
	// ownership rule as its replies. Born in Start(), cancelled in
	// Shutdown(); the operator's explicit cancel (CancelTurn) still ends
	// any turn, so detachment grants immortality to no one.
	turnCtx       context.Context
	turnCtxCancel context.CancelFunc

	// R74 access control, set before Start (SetAccessToken): when
	// required, wsAuthorized also demands the aii_token cookie whose
	// SHA-256 matches authTokenHash. Required with no valid hash on
	// record refuses every session — never fail-open.
	authRequired  bool
	authTokenHash string
	sweepEvery    time.Duration // drift-sweep cadence; default 60s; settable in-package for tests

	// Quiesce (2026-08-19, the battery fix): the app's background-
	// metabolism governor. Set before Start (nil = always-on — tests and
	// embedders without an app). Governs ONLY the drift sweep: pokes are
	// event-driven work and keep delivering while backgrounded.
	gate *quiesce.Gate

	// Session presence: the heartbeat's live-session source reads this.
	// Live = at least one open connection, or activity within the grace
	// window (a reconnecting dashboard must not stop the life clock
	// mid-conversation). Guarded by sessionMu.
	sessionMu    sync.Mutex
	sessionConns int
	lastActivity time.Time
	sessionGrace time.Duration // default 90s; settable in-package for tests

	// ONE UTTERANCE IN FLIGHT AT A TIME, IDENTITY-WIDE.
	//
	// handleVoiceFrame launched a goroutine per frame with nothing
	// bounding them. The memory was the smaller half: each holds up to
	// maxVoiceFrameBytes, so a page could put a lot of audio in the air
	// at once. The larger half is that transcription takes as long as it
	// takes, so two utterances racing through it can reach
	// AdmitParticipant in the opposite order to the one they were SPOKEN
	// in — a conversation recorded out of sequence, which no later
	// reader can detect or repair.
	//
	// A REFUSAL, NOT A QUEUE. A queue would preserve order and hide the
	// wait; the operator would speak into a microphone that appears to
	// be listening and find their words arriving late behind someone
	// else's. Being told to wait is the honest version.
	//
	// THE SLOT IS HELD PAST TRANSCRIPTION, and whoever comes next should
	// know it. In conversation mode observeVoice keeps the turn gate
	// until the identity has finished answering, so the microphone
	// refuses for that whole time — the natural shape of push-to-talk
	// without interruption, and exactly the WRONG shape once barge-in
	// exists, because barge-in IS speaking while the identity answers.
	// This bound is therefore deliberately crude and belongs to the
	// push-to-talk era: streaming replaces it rather than inheriting it.
	//
	// The zero value is "free", so this needs nothing from New().
	voiceMu   sync.Mutex
	voiceBusy bool

	port   int
	addr   string
	server *http.Server
	// tlsDir holds the dashboard's certificate material. Empty only in
	// tests that drive the handler directly and never listen.
	tlsDir    string
	startedAt time.Time
	handler   *WSHandler
	mu        sync.RWMutex

	// WS/HTTP auth (2026-08-17 external review H2; simplified by the
	// 2026-08-18 Method pass): behind this one unauthenticated WebSocket
	// sat chat (mints ledger events), config_set, tool_toggle, and
	// genesis. The two LOAD-BEARING layers, both fail-closed: hostGate
	// rejects any request whose Host isn't this server's own loopback
	// address (kills DNS rebinding at every route — a rebound page's
	// browser sends the attacker's Host), and the WS handshake requires
	// a present, parseable, same-host Origin (kills cross-origin pages
	// and header-less drive-bys; browsers send Origin unforgeably).
	// A third layer — a per-process token injected into the page — was
	// cut by the Method pass as DECORATIVE: every principal that passes
	// hostGate can fetch the page and read the token, so it gated no one
	// the other layers don't, while breaking legitimate stale tabs on
	// every restart (R14/R15: a layer that only ever fails the honest
	// path is theater). Set in Start() before Serve, read-only after.
	allowedHosts map[string]bool
	host         string
	boundAddr    string // what the listener actually bound; s.addr is a request when the port is 0
	tls          bool   // whether Start served TLS; the source for Scheme()
	anyHostPort  string // wildcard bind: gate matches the port only

	// UI sections (R66 UP2, UI_FRAME.md §3): the registry the app
	// assembled — nil is today's frame-only dashboard — and the
	// ui-layout.json source. Both settable after Start (the firstboot
	// path creates the server before the live wiring exists).
	secMu        sync.RWMutex
	secReg       *sections.Registry
	layoutSource func() []byte
	themeSource  func() []byte

	// overlayDir is the operator's frame overlay directory (T1,
	// docs/THREAT_MODEL-ui-disk-overlay.md). Empty — the default —
	// serves the compiled frame and never touches disk.
	overlayDir string
	// buildStamp names the evaluating build in fork verdicts, so each
	// decision line answers "diverged from WHICH build?" and the dedup
	// key re-keys when the shipped bytes move under a frozen fork.
	// Wired by the app via SetBuildStamp from BuildIdentity(); empty
	// (unwired, tests) omits the stamp from the verdict text.
	buildStamp string
	// overlayReported dedupes the accepted/rejected/inert readback so a
	// per-request decision is logged once, not once per page load.
	overlayReported map[string]bool
	// overlayEvents is the same readback, kept for the human (W2): the
	// ordered, bounded outcome list behind the ui.overlay query. The
	// log line is for the log; this is for the operator's screen.
	overlayEvents []OverlayEvent
}

// OverlayEvent is one overlay outcome as the operator's screen sees it
// (W2): path, the full accepted/rejected/inert outcome text, and the
// time the server decided it. Ordered oldest-first, bounded so a
// misbehaving surface cannot grow it without limit.
type OverlayEvent struct {
	Path      string `json:"path"`
	Outcome   string `json:"outcome"`
	DecidedAt string `json:"decided_at"` // RFC3339
}

// HistoryTurn is one replayed conversation turn (page refresh must not
// erase the relationship — the transcript lives in the store).
type HistoryTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolState is one tool's operator-toggle state.
type ToolState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// --- Identity view: the resident's inner life, read-only ---

// BeliefItem is one belief for the identity view.
type BeliefItem struct {
	ID            string  `json:"id"`
	Statement     string  `json:"statement"`
	Ring          int     `json:"ring"`
	Status        string  `json:"status"`
	EvidenceCount int     `json:"evidence_count"`
	Confidence    float64 `json:"confidence"`
}

// IntentionItem is one intention.
type IntentionItem struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	State     string `json:"state"`
}

// ExperienceItem is one recent non-private experience.
type ExperienceItem struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	CreatedAt  string `json:"created_at"`
	Provenance string `json:"provenance,omitempty"`
}

// IdentityState is the whole identity view payload.
type IdentityState struct {
	Beliefs       []BeliefItem     `json:"beliefs"`
	Intentions    []IntentionItem  `json:"intentions"`
	Experiences   []ExperienceItem `json:"experiences"`
	Synthesis     string           `json:"synthesis"`
	Brief         string           `json:"brief"`
	Charter       string           `json:"charter"`
	TrustLevel    string           `json:"trust_level"`
	AutonomyLevel string           `json:"autonomy_level"`
	PrivateCount  int              `json:"private_count"`
}

// --- Setup view: operator-owned configuration ---

// LLMConfigState is the substrate section: the config POINTER (llm.provider /
// llm.model) plus the RESOLVED provider/model and provider data, read-only.
// Keeping both matters: an empty pointer means "follow the provider default";
// replacing it with the resolved values merely by opening and saving Settings
// silently destroys that behavior.
// Endpoint, masked key, window and budgets come from the providers.json
// entry the pointer selects and are edited THERE (provider_set), never
// here. A pointer that fails to resolve fills Error and leaves the data
// fields zero — honest, never invented.
type LLMConfigState struct {
	Provider         string `json:"provider"`          // config pointer; empty = default-flagged entry
	Model            string `json:"model"`             // config pointer; empty = provider's default_model
	ResolvedProvider string `json:"resolved_provider"` // entry actually in use (empty when unresolved)
	ResolvedModel    string `json:"resolved_model"`    // model actually in use (empty when unresolved)
	TimeoutSeconds   int    `json:"timeout_seconds"`   // config transport knob (operator-settable here)
	Endpoint         string `json:"endpoint"`          // resolved, read-only
	APIKeyMasked     string `json:"api_key_masked"`    // resolved, e.g. "••••7f2a"; never the key
	ThinkingBudget   int    `json:"thinking_budget"`   // resolved (visible-floor clamp applied)
	ContextLength    int    `json:"context_length"`
	ReasoningEffort  string `json:"reasoning_effort"`
	MaxOutputTokens  int    `json:"max_output_tokens"`
	Error            string `json:"error,omitempty"` // why the pointer does not resolve, when it doesn't
}

// WitnessConfigState is the witness section.
type WitnessConfigState struct {
	URL                string `json:"url"`
	IntervalEvents     int    `json:"interval_events"`
	PlatformPubkeyPath string `json:"platform_pubkey_path"`
	TLSSPKISHA256      string `json:"tls_spki_sha256"`
}

// GenesisConfigState is the founding-artifact sources section.
type GenesisConfigState struct {
	ServerURL    string `json:"server_url"`
	FirewallURL  string `json:"firewall_url"`
	BootstrapURL string `json:"bootstrap_url"`
}

// PromptConfigState is the prompt budgeting section.
type PromptConfigState struct {
	MaxTokens          int `json:"max_tokens"`
	RecentTurns        int `json:"recent_turns"`
	MaxToolResultChars int `json:"max_tool_result_chars"`
}

// ProviderInfo is one FIRSTBOOT-offerable provider (served from config —
// the UI carries no provider table).
type ProviderInfo struct {
	Name     string `json:"name"`
	APIType  string `json:"api_type,omitempty"` // "openai" (default) | "anthropic" — the llm dialect
	Endpoint string `json:"endpoint"`
	// APIKey is INBOUND ONLY — the operator types a key here and it is
	// stored. It is never sent outbound: the UI needs to know only THAT
	// one is stored (HasKey), and shipping the secret to a browser for a
	// boolean put it on the wire and in devtools history for nothing.
	APIKey          string          `json:"api_key,omitempty"`
	HasKey          bool            `json:"has_key,omitempty"`         // outbound: key stored; inbound with blank api_key: true=keep, false=clear
	Credential      string          `json:"credential,omitempty"`      // adopted credential store: "claude-code" | "codex" | "" (use the key)
	CredentialInfo  *CredentialInfo `json:"credential_info,omitempty"` // derived now-fact about the adopted credential
	StatusReason    string          `json:"status_reason,omitempty"`   // why the status is what it is
	Preselect       bool            `json:"preselect,omitempty"`       // the birth form should open on this one
	PreselectWhy    string          `json:"preselect_why,omitempty"`
	APIKeyEnv       string          `json:"api_key_env,omitempty"`       // per-entry env fallback for the key
	DefaultModel    string          `json:"default_model,omitempty"`     // preselected model for this provider
	ContextLength   int             `json:"context_length,omitempty"`    // model window, tokens (operator-entered)
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"` // reserved for the reply
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`  // OpenAI-compat reasoning_effort
	ThinkingBudget  int             `json:"thinking_budget,omitempty"`   // thinking-token budget
	ThinkingMode    string          `json:"thinking_mode,omitempty"`
	ThinkingDisplay string          `json:"thinking_display,omitempty"` // adaptive (default) | budget (pre-4.6) | off
	Temperature     *float64        `json:"temperature,omitempty"`      // nil = unset (omit on the wire); 0 is a valid temperature
	TopP            *float64        `json:"top_p,omitempty"`            // nil = unset
	// Extra is the OpenAI-path passthrough (merged verbatim into the
	// request top level; typed fields win on collision).
	Extra            map[string]any `json:"extra,omitempty"`
	Status           string         `json:"status,omitempty"` // derived NOW-fact: ok | auth_required | no_credential | unreachable | invalid_url — never persisted
	SubscribeURL     string         `json:"subscribe_url,omitempty"`
	Default          bool           `json:"default,omitempty"`
	Models           []string       `json:"models,omitempty"`            // live discovery when available; picker data
	ConfiguredModels []string       `json:"configured_models,omitempty"` // operator's static fallback; editor data
}

// ConfigState is the whole setup payload. RestartRequired lists fields
// whose changes are saved but only apply on next boot (witness/genesis/
// prompt/timezone) — stated, never silently ignored.
// DashboardState is the bind address as configured — both fields are
// operator-editable (restart to apply).
// CredentialInfo is what an adopted credential says about itself —
// derived on read, never persisted.
type CredentialInfo struct {
	Kind      string `json:"kind"`
	Plan      string `json:"plan,omitempty"`
	Tier      string `json:"tier,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Expired   bool   `json:"expired,omitempty"`
	IsAPIKey  bool   `json:"is_api_key,omitempty"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
}

// PluginsState is the plugin plane's operator policy view.
type PluginsState struct {
	Autoload string           `json:"autoload"` // none | T0 | T1 | T2 | T3
	Skips    []PluginSkipView `json:"skips,omitempty"`
}

// PluginSkipView is one discovered, verified package standing below the
// autoload threshold — present, inactive, and visible to the operator.
type PluginSkipView struct {
	Dir    string `json:"dir"`
	ID     string `json:"id"`
	Tier   string `json:"tier"`
	Reason string `json:"reason"`
}

type DashboardState struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	TLS  bool   `json:"tls"`
}

// UpdatesConfigState is the operator's update preference (R70).
// UpdatesConfigState is the update-preference section.
type UpdatesConfigState struct {
	Automatic bool `json:"automatic"`
}

// AgencyConfigState is the operator's one agency preference — THE
// CHECKBOX. It rides in the readback because a control the readback does
// not carry renders unchecked after a save the server accepted, and the
// save banner then waits forever for an echo that can never match.
type AgencyConfigState struct {
	PreferLocalForRoles bool `json:"prefer_local_for_roles"`
}

// LogsConfigState is the log-persistence section: where the engine's
// log stream lands, rotation cap, compression age. All next-boot.
type LogsConfigState struct {
	Dir          string `json:"dir"`
	MaxBackups   int    `json:"max_backups"`
	CompressDays int    `json:"compress_days"`
}

// UpdateState is the checker's current result, surfaced in the status
// view so the operator sees "v0.2.0 available" or "installed, restart
// to apply" (R70). Zero values = no update known.
type UpdateState struct {
	CurrentVersion   string `json:"current_version"`
	AvailableVersion string `json:"available_version,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"` // version that was swapped in
	NeedsRestart     bool   `json:"needs_restart,omitempty"`     // installed but not yet restarted
	Error            string `json:"error,omitempty"`             // last check/apply failure
	CheckedAt        string `json:"checked_at,omitempty"`        // RFC3339 of last successful check
}

type ConfigState struct {
	LLM       LLMConfigState `json:"llm"`
	Dashboard DashboardState `json:"dashboard"`
	Plugins   PluginsState   `json:"plugins"`
	// CredentialKinds are the credential stores this PLATFORM can adopt.
	// Empty on mobile, where another app's files are unreachable by
	// design — the picker must not offer what cannot work.
	CredentialKinds []string           `json:"credential_kinds,omitempty"`
	Witness         WitnessConfigState `json:"witness"`
	Genesis         GenesisConfigState `json:"genesis"`
	Prompt          PromptConfigState  `json:"prompt"`
	Agency          AgencyConfigState  `json:"agency"`
	Updates         UpdatesConfigState `json:"updates"`
	Logs            LogsConfigState    `json:"logs"`
	Timezone        string             `json:"timezone"`
	RestartRequired []string           `json:"restart_required"`
}

// ContinuityState is the continuity strip: chain + witness anchoring.
type ContinuityState struct {
	LedgerSeq   uint64 `json:"ledger_seq"`
	AnchoredSeq int64  `json:"anchored_seq"`
	WitnessedAt string `json:"witnessed_at"`
	Unanchored  int64  `json:"unanchored"`
	WitnessURL  string `json:"witness_url"`
	LifeTicks   int64  `json:"lifetime_ticks"`
	// Mode lattice surfacing (SAFE_DEGRADED build step 4): capability
	// honesty, operator-visible.
	Mode          string `json:"mode"`           // normal | degraded_witness | safe
	SafeReason    string `json:"safe_reason"`    // "" unless safe
	DegradedSince string `json:"degraded_since"` // witness-dark since ("" unless degraded)
	// Last identity review (operator telemetry; the facility's findings
	// used to reach only log.Printf). Volatile by design — in-memory in
	// the facility, zero store writes (R30: no resident-visible review
	// channel; this is the operator surface, at a glance).
	ReviewAt     string `json:"review_at,omitempty"`     // last run, "" = never
	ReviewStatus string `json:"review_status,omitempty"` // clear | issues
	ReviewIssues int    `json:"review_issues,omitempty"` // count when issues
}

// WSHandler handles WebSocket connections.
//
// CONCURRENCY CONTRACT: every callback may be invoked concurrently — from
// each connection's handler goroutine and from the outbox pump.
// Implementations must be safe under concurrent calls (the store-backed
// ones are; SQLite serializes). Outbox delivery is AT-LEAST-ONCE across
// connections: connect-time delivery and the pump can both push the same
// item before either marks it, so MarkDelivered must be idempotent and a
// message may render in two open tabs. For wake speech to a live operator
// that is the chosen failure direction — a message shown twice beats a
// message voided (see runOutboxPump's zero-connections rule).
type WSHandler struct {
	// Dependencies injected by main
	IdentityName string
	// Speaker is who HandleMessage answers AS: "identity" for a born
	// mind, "system" for the firstboot handler, whose replies are the
	// substrate pointing at the form. It travels onto every response
	// frame so the browser never has to guess.
	Speaker       string
	GetStats      func() (*StatsResponse, error)
	HandleMessage func(ctx context.Context, msg string) (string, error) // operator message → identity response
	GetOutbox     func() ([]OutboxItem, error)
	MarkDelivered func(id string) error
	HandleGenesis func(ctx context.Context, req *GenesisRequest) (string, error) // FIRSTBOOT

	// LIVE-mode extensions
	RecentTurns func() ([]HistoryTurn, error)                                                             // replayed on connect
	GetTools    func() ([]ToolState, error)                                                               // operator tool panel
	SetToolFunc func(name string, enabled bool) error                                                     // toggle + persist
	ObserveChat func(ctx context.Context, msg string, emit func(kind, name, args string)) (string, error) // streams tool events during chat

	// MID-TURN REACH. A turn owns the identity from start to finish, and
	// until these existed nothing could reach it: this loop ran the whole
	// turn inline, so a second message was not queued, it was never READ.
	//
	// Steer reports (delivered, err). delivered=false with err=nil means
	// there was no turn to steer and the message is an ordinary one.
	TurnActive func() bool
	Steer      func(text string) (bool, error)
	// AdmitChat is Steer and "claim the turn" as ONE step, and it is what
	// the chat path uses. Steer alone leaves a window: this loop returns
	// to ReadMessage the instant it launches the turn goroutine, so the
	// next message asks a gate the previous turn has not taken yet, is
	// told "no turn", and becomes a second turn instead of joining the
	// first. steered=false means THE GATE IS HELD and the turn goroutine
	// owns it. Nil in FIRSTBOOT, which has no turn gate to claim.
	AdmitChat func(text string) (bool, error)
	// AcquireTurn BLOCKS until the turn gate frees (or ctx ends). The
	// parked-message path uses it: when AdmitChat refuses with
	// ErrBusyInternal, a goroutine waits here, then runs the turn with
	// the gate already held — exactly as AdmitChat's own winners do.
	AcquireTurn func(ctx context.Context) error
	CancelTurn  func() bool
	// PendingSteers is what has been accepted but has NOT yet reached the
	// model. Server-authoritative on purpose: a client that tracked its
	// own sends would show an empty queue after a reconnect while words
	// were still waiting, which is the confusion this surface exists to
	// remove.
	PendingSteers func() []string

	// VOICE. The browser holds the microphone — in every deployment that
	// exists the operator is at a browser and the identity is elsewhere
	// — so one complete utterance arrives here as a binary frame and the
	// host transcribes it against a configured endpoint. Nil when no
	// speech endpoint is configured, and then the browser is told so and
	// offers no control rather than offering one that cannot work.
	HearUtterance   func(ctx context.Context, pcm []byte, sampleRate, channels int) error
	VoiceConfigured func() bool

	// Read-only inner-life surfaces (identity view + continuity strip)
	GetIdentity   func() (*IdentityState, error)
	GetContinuity func() (*ContinuityState, error)

	// FIRSTBOOT provider directory (registry-owned; nil = UI shows the
	// unconfigured placeholder).
	GetProviders func() []ProviderInfo
	// Provider registry CRUD — providers.json is the operator's file;
	// these are the UI's pen. Both reply with the refreshed directory.
	SetProvider    func(ProviderInfo) error
	DeleteProvider func(name string) error
	// Live model discovery (FIRSTBOOT): GET {provider}/models with an
	// optional operator-typed key (localhost transport only).
	DiscoverModels func(provider, apiKey string) ([]string, error)

	// Operator-owned configuration (Setup view). SetConfig receives a
	// whitelisted change map; the handler validates, persists, applies
	// what can apply live, and reports restart-required fields.
	// Projects (R62): durable operator+identity workrooms. GetProjects
	// lists them (Active marks the focus); ProjectAct performs
	// create/update/close/reopen/select.
	// Work (Ring 4 agency): live sub-agents, queue depth, recent outcomes.
	GetWork func() (*WorkState, error)

	// Sandbox (Ring 5 reach): the operator views and edits roots; the
	// tools registry structurally refuses substrate exposure.
	GetSandbox func() (*SandboxState, error)
	SetSandbox func(roots []string) error

	GetProjects func() ([]ProjectState, error)
	ProjectAct  func(action, id, name string, description, focus *string) error

	// Workspace (project.workspace projection): GetWorkspace assembles
	// one project's overview/files/work. Nil = not available.
	GetWorkspace func(id string) (*WorkspaceState, error)

	GetConfig func() (*ConfigState, error)
	SetConfig func(changes map[string]interface{}) (*ConfigState, error)

	// Logs (query "logs"): ListLogs with Name empty, TailLogs with Name
	// set. Both nil = logging disabled — the viewer renders that state
	// honestly instead of erroring. The app adapts logsink types here;
	// the dashboard never imports the filesystem.
	ListLogs func() ([]LogFileState, error)
	TailLogs func(name string) (*LogTailState, error)
}

// StatsResponse is the status view data.
type StatsResponse struct {
	Name            string `json:"name"`
	BeliefCount     int    `json:"belief_count"`
	ReflectionCount int    `json:"reflection_count"`
	ExperienceCount int    `json:"experience_count"`
	IntentionCount  int    `json:"intention_count"`
	LedgerSeq       uint64 `json:"ledger_seq"`
	LifetimeTicks   int64  `json:"lifetime_ticks"`
	Uptime          string `json:"uptime"`
	// ForegroundHolds lists why the process must stay alive right now
	// (internal/foreground): a turn in flight, a claimed work item.
	// Mobile shells hear the same truth as transitions; this field is
	// the operator's read of it.
	ForegroundHolds []string `json:"foreground_holds,omitempty"`
	// Corruption telemetry (P3 family, extended 2026-08-22): rates at a
	// glance for the operator. malformed = calls that never parsed;
	// suspicious = calls that parsed and passed gates but referenced a
	// read target that does not exist — the writer-side corruption
	// channel (wrong path splice, digit splice) as seen engine-side.
	MalformedCalls  uint64 `json:"malformed_calls"`
	SuspiciousPaths uint64 `json:"suspicious_paths"`
	// duplicate = calls that parsed and validated but repeated an argument
	// key, so encoding/json dispatched them on the last copy: the corrupted
	// tail. Valid JSON no other counter can see (forensics 2026-08-24).
	DuplicateArgKeys uint64 `json:"duplicate_arg_keys"`
	// Version and Build bind this PROCESS to the source it was built
	// from. Both were computed at boot and stated only in the log, so a
	// running identity could not answer "which build am I?" without
	// reading a file it is not allowed to read — one resident resorted to
	// hashing the assets it was serving. AGENTS.md 9 requires commit,
	// configuration, executable and process to be bound independently
	// before an artifact is called current; without this the binding
	// cannot be performed at all. Build is "unknown" when the toolchain
	// left no VCS data — never a fabricated hash.
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
	// VoiceMaxFrameBytes is the ceiling on one utterance, told to the
	// page rather than left for it to discover. The socket's read limit
	// enforces this by CLOSING THE CONNECTION, so a browser that did not
	// know the number met it as a mysterious disconnect mid-sentence
	// with the words lost and nothing said. Zero means no microphone.
	VoiceMaxFrameBytes int          `json:"voice_max_frame_bytes,omitempty"`
	Update             *UpdateState `json:"update,omitempty"`
	// CredentialWarning is operator-facing and empty almost always. An
	// adopted credential cannot be refreshed by this runtime (custody
	// law), so when it lapses the identity simply stops thinking — and
	// cannot say so, because saying anything needs the credential that
	// just failed. The warning has to arrive BEFORE that, on a channel
	// the LLM is not part of.
	CredentialWarning string `json:"credential_warning,omitempty"`
	// LastTurn is what the previous turn cost, in provider-reported
	// tokens. Nothing measured this before: a turn makes many calls and
	// only the loop can add them up. Empty until a turn has run.
	LastTurn string `json:"last_turn,omitempty"`
	// Voice reports whether a speech endpoint is configured, so the page
	// can offer a microphone or not offer one. ASKED, NOT ASSUMED: an
	// operator who configures speech and reloads gets the control
	// without restarting the identity, and one who has not configured it
	// is never shown a button that answers with an error.
	Voice bool `json:"voice"`
}

// OutboxItem is an undelivered message.
type OutboxItem struct {
	ID      string `json:"id"`
	To      string `json:"to"`
	Content string `json:"content"`
}

// WorkSessionItem is one work session as the dashboard sees it.
type WorkSessionItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
	Project     string `json:"project,omitempty"`
}

// WorkState is the identity's live agency: what is running right now,
// what waits in the queue, what recently came back.
type WorkState struct {
	Live      []WorkSessionItem `json:"live"`
	Queued    int               `json:"queued"`
	Delivered []WorkSessionItem `json:"delivered"`
}

// WorkspaceState is the project.workspace projection: one project's
// overview (manifest truth), files (one level), and attributed work
// sessions with owner-verdicted outcomes (G1). Read-only, assembled
// by the app layer — the dashboard renders, it does not derive.
type WorkspaceState struct {
	Project ProjectState      `json:"project"`
	Files   []WorkspaceFile   `json:"files,omitempty"`
	Work    []WorkSessionItem `json:"work,omitempty"`
	// FilesTotal is the whole entry count of the directory, before the
	// cap. FilesCapped is true when the listing was truncated to
	// workspaceFileCap (R18 §9.2: a cap without its declaration is the
	// dishonest choice; showing N of M is the route pattern).
	FilesTotal  int  `json:"files_total,omitempty"`
	FilesCapped bool `json:"files_capped,omitempty"`
}

// WorkspaceFile is one entry in the project directory, one level deep.
type WorkspaceFile struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

// SandboxState is the identity's operator-managed filesystem reach.
type SandboxState struct {
	Root       string   `json:"root"`
	ExtraRoots []string `json:"extra_roots"`
}

// ProjectState is one project as the dashboard sees it.
type ProjectState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"`
	Focus       string `json:"focus,omitempty"`
	Dir         string `json:"dir"`
	Active      bool   `json:"active"`
}

// ProjectRequest is a project action from the dashboard. Description
// and Focus carry PATCH semantics: absent means "leave the field
// untouched" and an explicit empty string means "clear it" — the JSON
// native distinction the stringly path could not express, which is
// why the focus note could never be cleared (§8.6). Name stays plain:
// it is not clearable (ApplyPatch refuses an empty name).
type ProjectRequest struct {
	Action      string  `json:"action"` // create | update | close | reopen | select
	ID          string  `json:"id,omitempty"`
	Name        string  `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Focus       *string `json:"focus,omitempty"`
}

// ClientMessage is a message from the dashboard to the server.
type ClientMessage struct {
	RequestID string                 `json:"request_id,omitempty"`
	Type      string                 `json:"type"`               // "chat", "query", "genesis", "tool_toggle"
	Message   string                 `json:"message"`            // for "chat"
	Query     string                 `json:"query"`              // for "query" (status, outbox, tools)
	Genesis   *GenesisRequest        `json:"genesis"`            // for "genesis"
	Tool      string                 `json:"tool"`               // for "tool_toggle"
	Config    map[string]interface{} `json:"config"`             // for "config_set" (whitelisted paths only)
	Enabled   bool                   `json:"enabled"`            // for "tool_toggle"
	Project   *ProjectRequest        `json:"project,omitempty"`  // for "project"
	Roots     []string               `json:"roots,omitempty"`    // for "sandbox_set"
	Provider  string                 `json:"provider,omitempty"` // for query "discover"
	APIKey    string                 `json:"api_key,omitempty"`  // for query "discover" (operator-typed; localhost transport)
	Name      string                 `json:"name,omitempty"`     // for query "logs": empty = list, set = tail that file
	Entry     *ProviderInfo          `json:"entry,omitempty"`    // for "provider_set" (add and update are one act, keyed by name)
}

// GenesisRequest is sent from the FIRSTBOOT flow to create an identity.
// (L6: the key/ledger/db path fields are GONE — the handler always
// ignored them, and attacker-visible dead fields on the genesis wire
// read as a substrate-path override that doesn't exist.)
type GenesisRequest struct {
	Name         string `json:"name"`
	OperatorName string `json:"operator_name"`
	Provider     string `json:"provider"` // providers.json entry name the form selected — becomes the config pointer's target after birth
	Model        string `json:"model"`
	APIKey       string `json:"api_key"`
	Endpoint     string `json:"endpoint"`
}

// SectionState is one registered UI section as the frame sees it
// (R66 UP2): everything the frame needs to mount and police the
// bridge — the declared command/topic allowlists ride along because
// the FRAME enforces them per section (UX layer; the server gates
// stay the wall).
type SectionState struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Slot     string   `json:"slot"`
	Commands []string `json:"commands"`
	Topics   []string `json:"topics"`
	Entry    string   `json:"entry"`
	// Dev marks the dev-serve section: the frame renders a persistent
	// UNVERIFIED banner on its container.
	Dev bool `json:"dev,omitempty"`
}

// ServerMessage is a message from the server to the dashboard.
type ServerMessage struct {
	RequestID  string           `json:"request_id,omitempty"`
	Type       string           `json:"type"` // "response", "status", "outbox", "error", "event", "history", "tools"
	Message    string           `json:"message,omitempty"`
	Stats      *StatsResponse   `json:"stats,omitempty"`
	Outbox     []OutboxItem     `json:"outbox,omitempty"`
	Projects   []ProjectState   `json:"projects,omitempty"`
	Sandbox    *SandboxState    `json:"sandbox,omitempty"`
	Work       *WorkState       `json:"work,omitempty"`
	Workspace  *WorkspaceState  `json:"workspace,omitempty"`
	History    []HistoryTurn    `json:"history,omitempty"`
	Tools      []ToolState      `json:"tools,omitempty"`
	Identity   *IdentityState   `json:"identity,omitempty"`
	Continuity *ContinuityState `json:"continuity,omitempty"`
	Config     *ConfigState     `json:"config,omitempty"`
	Providers  []ProviderInfo   `json:"providers,omitempty"`
	Provider   string           `json:"provider,omitempty"`
	ModelList  []string         `json:"model_list,omitempty"` // discovered models for the requested provider
	// Pending is the steering queue: operator words accepted into a
	// running turn that have not yet reached the model. Empty means
	// delivered, and the difference is the whole point — "queued" and
	// "heard" must never look the same.
	Pending []string `json:"pending,omitempty"`
	Kind    string   `json:"kind,omitempty"` // event: "tool_call"
	Name    string   `json:"name,omitempty"` // event: tool name
	Args    string   `json:"args,omitempty"` // event: tool args (truncated)
	// Role is the SPEAKER of a conversational frame: "identity" (the
	// mind's own words, out of the model or out of its transcript),
	// "operator", or "system" (the substrate speaking as itself). The
	// browser has NO default: an absent or unknown role never renders in
	// the identity's voice. Before this, the live frame carried no
	// speaker and the browser inferred one from whether an identity
	// existed — that single inference is how substrate text ended up in
	// the identity's bubble, under the identity's name (review
	// 2026-08-20). The history frame has carried HistoryTurn.Role all
	// along; this closes the asymmetry.
	Role   string `json:"role,omitempty"`
	Stream bool   `json:"stream,omitempty"`
	Done   bool   `json:"done,omitempty"`
	// R66 UP2 frame furniture: the registered sections (type
	// "sections"; Message carries the SAFE reason when the list is
	// suppressed) and the raw ui-layout.json profiles (type "layout";
	// absent = no sections laid out — the frame-only default).
	Sections []SectionState  `json:"sections,omitempty"`
	Layout   json.RawMessage `json:"layout,omitempty"`
	Theme    json.RawMessage `json:"theme,omitempty"`
	// Overlay answers query "ui.overlay" (W2): the readback the log
	// already carries, delivered to the human. Absent = nothing to
	// report — overlay never configured, or no outcome yet. AUDIT
	// ONLY: retained outcomes with decidedAt stamps, never a live
	// invalidation (a page that applied these would reload on every
	// reconnect — the P1 loop, 2026-08-25).
	Overlays []OverlayEvent `json:"overlays,omitempty"`
	// Token/Paths carry the live invalidation (type "overlay_changed"):
	// Token is a monotonic counter the app increments per detected
	// disk change (fresh by construction, unlike a readback stamp),
	// Paths is the digest diff — exactly what moved. A freshly loaded
	// page needs nothing: current bytes are served at request time.
	// Only an ALREADY-OPEN page consumes this.
	Token uint64   `json:"token,omitempty"`
	Paths []string `json:"paths,omitempty"`
	// LogsList answers query "logs" (nil = logging disabled: empty dir
	// or sink not installed); LogsTail answers query "logs" with Name
	// set — the last lines of one file, read through the app's adapter
	// (the dashboard never touches the filesystem itself).
	LogsList []LogFileState `json:"logs_list,omitempty"`
	LogsTail *LogTailState  `json:"logs_tail,omitempty"`
}

// LogFileState is one rotated log file as the viewer sees it.
type LogFileState struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	ModAt string `json:"mod_at"`
}

// LogTailState is the tail of one log file (bounded).
type LogTailState struct {
	Name  string   `json:"name"`
	Lines []string `json:"lines"`
}

// SchemeFor is the scheme a dashboard answers on, and the only place
// this codebase writes either literal down.
//
// It drifted twice while there was one scheme and eight copies of it:
// the operator banner still said http:// after TLS landed, so the URL
// handed to the operator answered 400, and the browser frame still
// dialled ws:// at a TLS port, so every open tab retried forever behind
// a UI that never connected. Now that the scheme is the operator's
// choice, a second copy would not merely drift — it would be wrong for
// half the installations.
//
// Read it through Scheme(), Origin() or LoopbackURL(). The browser frame
// reads none of them: it derives from location.protocol, which cannot
// disagree with the page that served it.
func SchemeFor(tls bool) string {
	if tls {
		return "https"
	}
	return "http"
}

// Scheme is what this server answers on.
func (s *Server) Scheme() string { return SchemeFor(s.tls) }

// Origin is this server's own origin — scheme, host and port. Build a
// URL for this dashboard from here and from nowhere else.
func (s *Server) Origin() string {
	addr := s.boundAddr
	if addr == "" {
		addr = s.addr // not started yet: the configured address is the best truth there is
	}
	// A WILDCARD BIND IS NOT A URL. 0.0.0.0 and :: name every address and
	// therefore none: "https://[::]:8180" is what the operator was being
	// handed, and no browser opens it. Which address they will actually
	// use is theirs to know and ours not to — so advertise the one that
	// is always correct from the machine itself, and leave the "bound to
	// … reachable from the whole network" line to say the rest.
	if host, port, err := net.SplitHostPort(addr); err == nil && isWildcard(host) {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	return s.Scheme() + "://" + addr
}

// isWildcard reports whether a bind names every address rather than one.
func isWildcard(host string) bool {
	if host == "0.0.0.0" || host == "::" || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// LoopbackURL is the dashboard's URL on loopback, for a shell WebView
// that reaches the server locally whatever address it binds.
func LoopbackURL(tls bool, port int) string {
	return fmt.Sprintf("%s://127.0.0.1:%d/", SchemeFor(tls), port)
}

// isLoopback reports whether this bind reaches only this machine. It is
// the line between "HTTP is fine" and "HTTP publishes the conversation":
// a browser already treats a loopback origin as secure, so plaintext
// there costs nothing and crosses no wire.
func isLoopback(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost", "":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// New creates a dashboard server.
func New(host string, port int, handler *WSHandler) *Server {
	// Port 0 = EPHEMERAL (the OS picks): tests and embedders that don't
	// care get a collision-free listener. The 8080 operator default lives
	// in ONE place — the app's applyDefaults — not duplicated here: the
	// old `0 → 8080` made every `Port: 0` test bind 8080, and parallel
	// test packages collided on it intermittently (found 2026-08-17,
	// full-suite flake: app tests vs dashboard tests, EADDRINUSE).
	s := &Server{
		handler:      handler,
		port:         port,
		wsConns:      make(map[*websocket.Conn]*wsClient),
		outboxSignal: make(chan struct{}, 1),
		pumpDone:     make(chan struct{}),
		sessionGrace: 90 * time.Second, // reconnect window: presence survives brief drops
		sweepEvery:   60 * time.Second,
		startedAt:    time.Now(),
	}

	mux := http.NewServeMux()

	// Serve the embedded UI. Since UP1 (the R66 frame plan's first step)
	// the dashboard is browser-native ES modules: index.html plus sibling
	// .js/.css files, all embedded, all same-origin relative paths — no
	// CDN, no build step, nothing external (the H2 posture: the only
	// origin that exists is this loopback server, on every platform the
	// binary ships to). Content-Type is set from the extension switch
	// below, deliberately NOT mime.TypeByExtension: that consults OS
	// tables (the Windows registry can remap .js to text/plain), and
	// browsers hard-refuse ES modules served with a non-JavaScript type —
	// the switch is the deterministic source of truth. Only the three
	// UI extensions are servable; everything else 404s.
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			p = "/index.html"
		}
		var ctype string
		switch path.Ext(p) {
		case ".html":
			ctype = "text/html; charset=utf-8"
		case ".js":
			ctype = "text/javascript; charset=utf-8"
		case ".css":
			ctype = "text/css; charset=utf-8"
		default:
			http.NotFound(w, r)
			return
		}
		// T1 overlay: an operator file on disk takes precedence over
		// the compiled one. FULL RE-FORM by operator ruling (2026-08-24):
		// all three frame extensions are overridable. Any failure at
		// all falls through to the embed: the frame as shipped is the
		// safe default, so a bad overlay degrades the view, never the
		// app (docs/THREAT_MODEL-ui-disk-overlay.md).
		data, ok := s.overlayAsset(p)
		if !ok {
			// The mux cleans the URL path and embed.FS refuses
			// non-canonical paths, so "static"+p cannot escape the
			// embedded tree.
			var err error
			data, err = staticFS.ReadFile("static" + p)
			if err != nil {
				if p == "/index.html" {
					// The shell is compiled in; unreachable in a shipped binary.
					http.Error(w, "cannot read index.html", http.StatusInternalServerError)
					return
				}
				http.NotFound(w, r)
				return
			}
		}
		// One header block for both sources: an overlaid stylesheet
		// ships under exactly the policy the compiled one does. Two
		// copies of a policy is how the section fence diverged.
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Security-Policy", uiCSP)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// no-cache (W3): the frame's own hot-reload story. The overlay
		// seam ends in a href-swap that fetches the asset AGAIN, and a
		// heuristically-cached byte from a prior load would answer that
		// fetch stale — the swap succeeds, the screen shows the old
		// bytes, and the operator concludes the reload doesn't work.
		// no-cache still caches: it just RE-VALIDATES every use, so the
		// common case stays one 304 round-trip instead of a full body,
		// and the swap's second GET always sees current disk truth.
		w.Header().Set("Cache-Control", "no-cache")
		if p == "/index.html" && s.authRequired {
			// R74: the page learns ONE bit — a token is required — so
			// ws.js can ask the operator once instead of reconnect-
			// looping into a wall it cannot see. The bit is a data
			// ATTRIBUTE on <head>, readable by page code under uiCSP.
			// Its first carrier was an inline <script>, and script-src
			// 'self' refused to run it — the flag existed in the bytes
			// and never in the page (D75). Injected only when required:
			// the default page stays byte-identical.
			data = []byte(strings.Replace(string(data), "<head>", `<head data-aii-token-required="1">`, 1))
		}
		w.Write(data)
	})

	// Section files (R66 UP2): registered sections' cached files only,
	// behind the sandbox walls (strict CSP, framing pinned to this
	// page). More specific than "GET /", so the frame tree and the
	// section trees never overlap.
	mux.HandleFunc("GET /sections/{id}/{path...}", s.handleSectionFile)

	// WebSocket endpoint — uses s.handler, which SwapHandler can update
	mux.HandleFunc("GET /ws", s.handleWS)

	if host == "" {
		host = "127.0.0.1"
	}
	s.host = host
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", s.port))
	s.server = &http.Server{
		Addr:    addr,
		Handler: s.hostGate(mux),
		// Slow-header and idle-connection bounds (D48, Sev 2026-08-26):
		// a widened bind must not be exhaustible by half-sent headers or
		// parked keep-alives. WebSockets are untouched — Accept hijacks
		// the connection out from under these timeouts.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 16,
	}
	s.addr = addr

	return s
}

// hostAllowed answers the ONE question both gates ask: is this host:port
// an address this server is actually reachable at? A wildcard bind cannot
// enumerate its names, so it matches the port only (D48).
//
// It is one function because the page gate and the WS Origin gate had
// two copies of the rule and only one learned D48: on 0.0.0.0 a browser
// at the LAN address fetched the page and then had every socket 403'd,
// which the UI shows as a permanent reconnect loop — and with R74 armed,
// as a prompt blaming the access token for an origin refusal.
//
// SAY THE DELTA OUT LOUD: a port check is not a rebinding guard. On a
// wildcard bind, what refuses evil.tld is a TLS certificate that will
// not match their name, or the R74 access token — and a plaintext wide
// bind with require_token false has neither.
//
// AND IT STAYS THAT WAY, DELIBERATELY (operator ruling 2026-08-27, R74
// amended): a wildcard bind does NOT force the token on. Beta 1 wants
// low friction, and making one setting silently rewrite another is the
// invented authority this project refuses — the operator who binds wide
// is exposing the network they chose, is told so at boot, and turns on
// TLS or the token when they want them. Do not add a gate here.
func (s *Server) hostAllowed(hostPort string) bool {
	if s.allowedHosts[hostPort] {
		return true
	}
	if s.anyHostPort == "" {
		return false
	}
	_, p, err := net.SplitHostPort(hostPort)
	return err == nil && p == s.anyHostPort
}

// hostGate applies hostAllowed to every route, and logs what it refused.
func (s *Server) hostGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			log.Printf("dashboard: refused request with foreign Host %q", r.Host)
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Start begins serving. Returns the actual address (port may be 0 = random).
// Start listens and serves. tlsDir is where the certificate material
// lives, and it is a PARAMETER rather than a setter because a field that
// must be set before Start is a thing a caller can forget — and the
// dashboard serves HTTPS only, so forgetting it means not serving.
func (s *Server) Start(tlsDir string) (string, error) {
	s.tlsDir = tlsDir
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return "", fmt.Errorf("listen failed: %w", err)
	}

	actualAddr := ln.Addr().String()
	s.boundAddr = actualAddr
	// Admission ceiling (D48): one connection flood must not exhaust
	// the daemon's file descriptors. At the cap, Accept waits and the
	// OS accept queue absorbs the burst; existing sockets are untouched.
	ln = newLimitListener(ln, maxConcurrentConns)
	// THE KERNEL'S ANSWER, NOT THE CONFIG STRING. Both predicates below
	// used to re-parse s.host with net.ParseIP, which is strict, while
	// net.Listen binds through the resolver, which accepts inet_aton
	// shorthand: "0", "0.0", "0x0" and "00000000" all bind [::] — every
	// address on the network — and ParseIP calls each of them nothing.
	// The gate then refused the operator's own LAN Host (B7, still live
	// for those spellings), and the plaintext warning cried wolf on
	// "127.1", which binds loopback. ln.Addr() cannot disagree with what
	// was bound, so ask it. Origin() at :857 already did.
	actualHost, actualPort, err := net.SplitHostPort(actualAddr)
	if err != nil {
		ln.Close()
		return "", fmt.Errorf("split listen addr %q: %w", actualAddr, err)
	}
	// The Hosts this server answers to: itself, by its own names. A
	// wildcard bind cannot enumerate its names — the gate then matches
	// the port only, and the DNS-rebinding guard is correspondingly
	// weaker: binding wide is the operator exposing the network they
	// chose (logged at start, stated in the settings UI).
	s.allowedHosts = map[string]bool{
		actualAddr:                           true,
		net.JoinHostPort(s.host, actualPort): true,
	}
	if s.host == "127.0.0.1" || s.host == "::1" || s.host == "localhost" {
		s.allowedHosts["127.0.0.1:"+actualPort] = true
		s.allowedHosts["localhost:"+actualPort] = true
	}
	if isWildcard(actualHost) {
		s.anyHostPort = actualPort
		log.Printf("dashboard: bound to %s — reachable from the whole network; the Host gate matches the port only", actualAddr)
	}

	// TLS IS THE OPERATOR'S CHOICE, and an empty tlsDir means they did
	// not make it. That is the right default on the 127.0.0.1 bind this
	// ships with: a browser already treats a loopback origin as a secure
	// context, so plaintext there costs nothing, crosses no wire, and
	// still grants the microphone.
	//
	// It stops being free the moment the bind widens. Then every word
	// between an identity and its operator — tool results and secrets
	// included — is on the network in the clear, and the browser refuses
	// getUserMedia because a LAN address over http:// is not a secure
	// context on any browser. That combination is the one worth saying
	// out loud, so it is said here rather than silently served.
	if s.tlsDir == "" {
		s.tls = false
		if !isLoopback(actualHost) {
			log.Printf("dashboard: serving PLAIN HTTP on %s — every word between this identity and its operator crosses the network in the clear, and the browser will refuse the microphone. Settings → Dashboard → HTTPS.", actualAddr)
		}
		s.startTurnLifecycles()
		go func() {
			if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("dashboard server error: %v", err)
			}
		}()
		return actualAddr, nil
	}
	s.tls = true
	mat, terr := EnsureTLS(s.tlsDir, s.host)
	if terr != nil {
		ln.Close()
		return "", fmt.Errorf("dashboard TLS: %w", terr)
	}
	// ONE ACTION, ONCE PER MACHINE, printed where the operator is
	// already looking, and printed ONLY on the run that minted it —
	// repeating it at every start would make it noise, and noise is
	// what an operator learns to skip.
	//
	// The commands are named because the obvious one is WRONG for the
	// browser: update-ca-certificates feeds /etc/ssl/certs, which
	// OpenSSL, curl and Go read and CHROME AND FIREFOX DO NOT. They
	// keep their own NSS database, so a root installed the obvious way
	// leaves the browser exactly as untrusting as before — which is
	// the failure the operator hits first and can least diagnose.
	//
	// All platforms are printed rather than branching on GOOS: this is
	// a string, and the operator knows which machine they are on.
	if mat.Regenerated {
		log.Printf("dashboard: minted a TLS certificate for %s", s.host)
		log.Printf("dashboard: install this root ONCE for a browser with no warnings: %s", mat.CACertPath)
		log.Printf("dashboard:   Chrome/Firefox on Linux (needs libnss3-tools; the system store is NOT enough):")
		log.Printf("dashboard:     certutil -d sql:$HOME/.pki/nssdb -A -t \"C,,\" -n \"AII OS %s\" -i %s", s.host, mat.CACertPath)
		log.Printf("dashboard:   macOS: sudo security add-trusted-cert -d -k /Library/Keychains/System.keychain %s", mat.CACertPath)
		log.Printf("dashboard:   Windows: certutil -addstore -f Root %s", mat.CACertPath)
	} else {
		log.Printf("dashboard: TLS root (install once for a clean browser): %s", mat.CACertPath)
	}

	// LOAD THE PAIR HERE, NOT IN THE GOROUTINE. ServeTLS opens these
	// files itself, on the other side of a `go`, so a missing key or one
	// that belongs to a different certificate became a log line nobody
	// was reading — while Start had already returned an address and the
	// operator had already been handed a URL. The dashboard was dead and
	// boot said it was fine.
	//
	// LoadX509KeyPair also answers the question usable() cannot: it
	// checks that this key belongs to this certificate. Reusing material
	// whose halves do not match is exactly what a half-finished copy or a
	// partial restore leaves behind.
	pair, perr := tls.LoadX509KeyPair(mat.LeafCert, mat.LeafKey)
	if perr != nil {
		ln.Close()
		return "", fmt.Errorf("dashboard TLS: certificate and key are not usable together (%s, %s): %w", mat.LeafCert, mat.LeafKey, perr)
	}
	s.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{pair}}
	s.startTurnLifecycles()
	go func() {
		// Empty paths: the pair above is already loaded and verified.
		if err := s.server.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("dashboard server error: %v", err)
		}
	}()

	return actualAddr, nil
}

// startTurnLifecycles arms the server-scoped lifetimes — the outbox
// pump's context and the turn context — and starts the pump. Called
// once per Start, on BOTH serve paths, before the serve goroutine
// (D42, Sev 2026-08-26): the plaintext path never armed the turn
// context at all, so every chat turn ran on context.Background and
// survived Shutdown; the TLS path armed it after ServeTLS was already
// accepting, racing the first connection with an unlocked write
// against serverTurnCtx's locked read. Both contexts are reaped in
// Shutdown; Background here is the server's own lifecycle root, not
// an escape from one.
func (s *Server) startTurnLifecycles() {
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	turnCtx, turnCancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.pumpCancel = pumpCancel
	s.turnCtx = turnCtx
	s.turnCtxCancel = turnCancel
	s.mu.Unlock()
	go s.runOutboxPump(pumpCtx)
}

// serverTurnCtx returns the context a chat turn runs under: the
// SERVER's lifetime, not the socket that opened it. The turn is the
// identity's work; a reload drops the connection, not the work
// ("LLM call abandoned (caller ended it)" on every mid-turn reload,
// 2026-08-25). Born in Start, reaped in Shutdown before sockets close.
// The nil fallback answers tests that construct Server without Start:
// Background is never-cancellable, and a test turn that cannot be
// cancelled is the pre-fix behavior those tests already assume.
func (s *Server) serverTurnCtx() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.turnCtx != nil {
		return s.turnCtx
	}
	return context.Background()
}

// PokeOutbox signals the pump: outbox messages just landed. Non-blocking,
// coalescing (cap-1 channel) — a burst of writes is one delivery pass.
func (s *Server) PokeOutbox() {
	select {
	case s.outboxSignal <- struct{}{}:
	default:
	}
}

// SetQuiesceGate wires the background-metabolism governor; call before
// Start (the pump reads it once, at ticker birth). The narrowest seam
// that exists: New's signature stays (every test construction keeps
// working, nil = always-on), and the sweep is the only periodic thing
// this server owns — pokes and WS traffic are event-driven and stay
// live while parked.
func (s *Server) SetQuiesceGate(g *quiesce.Gate) { s.gate = g }

// runOutboxPump delivers undelivered outbox messages to every live
// connection on signal (or the 60s drift sweep), then marks them
// delivered once. Connect-time delivery remains the fallback for
// messages that landed while nobody was connected. The sweep ticker is
// GOVERNED (quiesce, 2026-08-19): backgrounded it parks — drift
// insurance can wait for the operator; the foreground catch-up tick is
// the insurance pass for whatever aged while parked.
func (s *Server) runOutboxPump(ctx context.Context) {
	defer close(s.pumpDone)
	sweep := quiesce.NewTicker(s.gate, s.sweepEvery)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.outboxSignal:
		case <-sweep.C:
		}
		// drain any coalesced extra signals
		select {
		case <-s.outboxSignal:
		default:
		}

		h := s.currentHandler()
		if h == nil || h.GetOutbox == nil {
			continue
		}
		items, err := h.GetOutbox()
		if err != nil || len(items) == 0 {
			continue
		}
		msg := ServerMessage{Type: "outbox", Outbox: items}
		s.wsMu.Lock()
		conns := make([]*websocket.Conn, 0, len(s.wsConns))
		for c := range s.wsConns {
			conns = append(conns, c)
		}
		s.wsMu.Unlock()
		if len(conns) == 0 {
			// NOBODY IS CONNECTED: marking delivered here would void the
			// message — the operator would never see it anywhere (caught
			// by TestTimerWakeEndToEnd: the floor landed, the pump
			// "delivered" it to zero connections, and it disappeared from
			// the undelivered set). Leave it; connect-time delivery (or a
			// later signal with someone present) carries it.
			continue
		}
		delivered := false
		for _, c := range conns {
			if s.sendMsg(ctx, c, msg) == nil {
				delivered = true
			}
		}
		// Mark only when at least one write actually landed: with every
		// present connection failing (a lone stalled tab being dropped),
		// marking would void the message — the same failure direction
		// the zero-connections rule above exists to prevent.
		if delivered && h.MarkDelivered != nil {
			for _, item := range items {
				h.MarkDelivered(item.ID)
			}
		}
	}
}

// PushTransient fans a message to every LIVE connection without touching
// the store — the SAFE-mode notification path (canon §10: operator
// notification is IMMEDIATE, but no database write is admitted while
// identity integrity is unverified). Nobody connected → the log line is
// the only record, and that is the honest ceiling of SAFE.
func (s *Server) PushTransient(id, content string) int {
	msg := ServerMessage{Type: "outbox", Outbox: []OutboxItem{{ID: id, To: "operator", Content: content}}}
	s.wsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for c := range s.wsConns {
		conns = append(conns, c)
	}
	s.wsMu.Unlock()
	for _, c := range conns {
		// Bounded (the WS-hang lesson, Method pass 2026-08-18): a stalled
		// client must not wedge the SAFE wake path under writeMu/chatMu.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		s.sendMsg(ctx, c, msg)
		cancel()
	}
	return len(conns)
}

// Shutdown stops the server. Open WebSocket connections are CLOSED FIRST
// (StatusGoingAway) — a hijacked WS socket never goes idle, so
// http.Server.Shutdown alone blocks forever with a connected dashboard
// (the 2026-08-16 freeze). If graceful drain still exceeds the context,
// the listener is force-closed: shutdown must terminate.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	pumpCancel, turnCancel := s.pumpCancel, s.turnCtxCancel
	s.mu.RUnlock()
	if pumpCancel != nil {
		pumpCancel()
		select {
		case <-s.pumpDone:
		case <-time.After(2 * time.Second):
		}
	}
	// Reap the turn context BEFORE closing sockets: an in-flight turn
	// must be cancelled at shutdown, not leak past it. Bounded by the
	// caller's ctx; the app-level turn gate drains the goroutine.
	if turnCancel != nil {
		turnCancel()
	}
	s.wsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for c := range s.wsConns {
		conns = append(conns, c)
	}
	s.wsMu.Unlock()
	for _, c := range conns {
		// CloseNow, not Close: Close performs the closing handshake and
		// WAITS for the peer's echo — an idle dashboard never echoes, so
		// Close itself hangs. CloseNow drops the TCP connection; the
		// handler's Read errors out and the connection goes idle.
		c.CloseNow()
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.server.Shutdown(ctx) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		// drain exceeded the deadline — force close the listener
		_ = s.server.Close()
		return ctx.Err()
	}
}

// SwapHandler replaces the active handler. Used for FIRSTBOOT → LIVE transition.
// Every message re-resolves the current handler, so existing connections
// pick up the swap immediately (a birth on the same connection must not
// leave that connection talking to the dead FIRSTBOOT handler).
func (s *Server) SwapHandler(h *WSHandler) {
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
}

func (s *Server) currentHandler() *WSHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handler
}

// SessionLive reports whether a live session exists: at least one open
// dashboard connection, or traffic within the grace window. This is the
// HEARTBEAT live-session source (canon HEARTBEAT_FACILITY.md §2 — only
// accepted live pulses advance the life clock; fixed 2026-08-16 after the
// defect had been documented instead of solved).
func (s *Server) SessionLive() bool {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	if s.sessionConns > 0 {
		return true
	}
	if s.sessionGrace > 0 && !s.lastActivity.IsZero() {
		return time.Since(s.lastActivity) < s.sessionGrace
	}
	return false
}

// wsAuthorized enforces the WS handshake policy (H2): a present,
// parseable Origin on the same allowed host — empty Origin is refused
// (the old accept path let header-less clients straight through).
//
// THE SCHEME MUST BE https, and this line said http. The dashboard
// served plaintext when that was written, so it was right then and
// became a whole-dashboard outage the moment TLS landed: a browser on
// an https page sends "Origin: https://…", every handshake would have
// answered 403, and the UI would connect to nothing. Caught by the
// tests, which is the entire reason they speak the real transport
// rather than a convenient one.
//
// Only https is accepted, not both. The dashboard has one scheme now,
// so an http Origin arriving here is not a client of this server.
func (s *Server) wsAuthorized(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != s.Scheme() || !s.hostAllowed(u.Host) {
		return false
	}
	if s.authRequired {
		c, cerr := r.Cookie("aii_token")
		if cerr != nil {
			return false
		}
		want, derr := hex.DecodeString(s.authTokenHash)
		if derr != nil || len(want) != sha256.Size {
			// Required with nothing verifiable on record (a failed
			// mint, a hand-damaged field): refused, never open.
			return false
		}
		sum := sha256.Sum256([]byte(c.Value))
		if subtle.ConstantTimeCompare(sum[:], want) != 1 {
			return false
		}
	}
	return true
}

// SetAccessToken arms R74 access control; call before Start. required
// with an empty or malformed hash refuses every session (the mint
// failed — the operator sees the boot log), never falls open.
func (s *Server) SetAccessToken(required bool, sha256Hex string) {
	s.authRequired = required
	s.authTokenHash = sha256Hex
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.wsAuthorized(r) {
		log.Printf("WS refused: origin %q host %q failed auth", r.Header.Get("Origin"), r.Host)
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Origin is verified above, strictly (the library's
		// OriginPatterns matched against host:port and accepted empty
		// Origins — dead config, H2). Skip its weaker check.
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("WS accept error: %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// THE DEFAULT READ LIMIT IS 32 KiB AND SPEECH IS BIGGER THAN THAT.
	// A voice frame carries a whole utterance (voice_ws.go), which at
	// 48 kHz mono s16 is about 96 KB per second — so on the default the
	// connection would not reject the frame, it would DIE, and the
	// operator would see the microphone kill the page's socket with no
	// explanation. Raised here to the same ceiling voice_ws.go enforces,
	// so an oversized frame is refused with a reason instead.
	conn.SetReadLimit(maxVoiceFrameBytes)

	ctx := r.Context()

	s.wsMu.Lock()
	s.wsConns[conn] = &wsClient{}
	s.wsMu.Unlock()
	defer func() {
		s.wsMu.Lock()
		delete(s.wsConns, conn)
		s.wsMu.Unlock()
	}()

	s.sessionMu.Lock()
	s.sessionConns++
	s.sessionMu.Unlock()
	defer func() {
		s.sessionMu.Lock()
		s.sessionConns--
		s.sessionMu.Unlock()
	}()

	// On connect, deliver undelivered outbox messages
	h := s.currentHandler()
	if h.GetOutbox != nil {
		items, err := h.GetOutbox()
		if err == nil && len(items) > 0 {
			msg := ServerMessage{Type: "outbox", Outbox: items}
			// Mark only on a landed write — a connect that dies mid-
			// delivery must leave the items undelivered for the next
			// connection (same rule as the pump's marking).
			if s.sendMsg(ctx, conn, msg) == nil && h.MarkDelivered != nil {
				for _, item := range items {
					h.MarkDelivered(item.ID)
				}
			}
		}
	}

	// Send initial status
	s.sendStatus(ctx, conn, h)
	// Replay recent conversation: a page refresh must not erase the
	// relationship — the transcript lives in the store, not the browser.
	if h.RecentTurns != nil {
		if turns, err := h.RecentTurns(); err == nil && len(turns) > 0 {
			s.sendMsg(ctx, conn, ServerMessage{Type: "history", History: turns})
		}
	}

	// A page that reloads mid-turn must land in the honest state, not a
	// frozen one (reported live, 2026-08-24: reload stopped the thinking
	// dots and the orb, and the page never caught up with the turn). The
	// turn is still running, so the fresh page gets the same frame the
	// turn opened with — every subsequent frame (tool events, the reply)
	// arrives by broadcast from handleChat, not just to the connection
	// that spoke. TurnActive is nil before birth: an unborn dashboard
	// has no turns to be honest about.
	if h.TurnActive != nil && h.TurnActive() {
		s.sendMsg(ctx, conn, ServerMessage{Type: "response", Message: "", Stream: true})
	}

	// LIVE OUTBOX PUSH: see runOutboxPump — store writes signal the pump
	// (wired by the app), the pump fans to live connections. Connect-time
	// delivery above remains the nobody-was-connected fallback.

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		s.sessionMu.Lock()
		s.lastActivity = time.Now()
		s.sessionMu.Unlock()

		// AUDIO IS BINARY AND EVERYTHING ELSE IS JSON. The frame type
		// is the whole discriminator: there is no envelope to parse, no
		// type field to trust, and no way for one to be mistaken for
		// the other. A JSON control plane and a binary data plane on
		// ONE socket — which is what an earlier design built a second
		// transport to achieve, and this is what it costs.
		if typ == websocket.MessageBinary {
			s.handleVoiceFrame(ctx, conn, data)
			continue
		}

		// THE 12 MiB READ LIMIT IS FOR VOICE, AND ONLY VOICE GETS IT.
		// The library has one limit per connection, so raising it for
		// utterances silently raised it for JSON too — a regression
		// the voice landing shipped: before it, text frames were
		// bounded at the library's 32 KiB default. No control message
		// is within two orders of magnitude of this bound; one that
		// reaches it is not chat, and parsing it would hand whoever
		// sent it a 12 MiB allocation per frame.
		if len(data) > maxTextFrameBytes {
			s.sendError(ctx, conn, "message too large")
			continue
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.sendError(ctx, conn, "invalid message format")
			continue
		}

		switch msg.Type {
		case "chat":
			// Re-resolve per message: a genesis on this same connection swaps
			// FIRSTBOOT→LIVE, and every subsequent message must hit the live
			// handler — capturing once at connect time froze the old mode.
			h := s.currentHandler()

			// STEER FIRST, AND CLAIM IN THE SAME BREATH. If a turn is
			// already running these words belong INSIDE it — delivered at
			// its next tool-call boundary, without interrupting the work.
			// If none is, the gate is taken here and now, so the next
			// message read (this loop resumes immediately below) sees a
			// turn to steer rather than an empty gate to race for.
			admit := h.AdmitChat
			if admit == nil {
				admit = h.Steer // FIRSTBOOT: no gate, no claim to make
			}
			if admit != nil {
				delivered, err := admit(msg.Message)
				switch {
				case errors.Is(err, ErrBusyInternal):
					// A facility pass holds the gate. Steering into it
					// would swallow the words — they are only read at a
					// conversation turn's tool boundaries — so the
					// message WAITS, visibly, and opens its own turn the
					// moment the pass ends. The waiter blocks on the
					// gate, which hands the token to waiting people
					// before the next rhythm tick can take it: operators
					// outrank metabolism.
					s.broadcast(ServerMessage{Type: "response", Role: "system", Done: true,
						Message: "An internal pass (memory metabolism) holds the turn — your message is queued and will run the moment it ends."})
					qtext := msg.Message
					turnCtx := s.serverTurnCtx()
					go func() {
						actx, acancel := context.WithTimeout(turnCtx, queuedTurnMaxWait)
						defer acancel()
						if h.AcquireTurn == nil {
							s.broadcast(ServerMessage{Type: "error", Message: "identity not ready for queued turns"})
							return
						}
						if err := h.AcquireTurn(actx); err != nil {
							s.broadcast(ServerMessage{Type: "error", Message: fmt.Sprintf("the internal pass did not yield in time; your message was NOT delivered — please send it again (%v)", err)})
							return
						}
						s.handleChat(turnCtx, conn, qtext, h)
						s.sendStatus(ctx, conn, h)
						log.Printf("dashboard: queued turn finished (%d chars in)", len(qtext))
					}()
					continue
				case err != nil:
					s.sendError(ctx, conn, err.Error())
					continue
				case delivered:
					s.sendMsg(ctx, conn, ServerMessage{Type: "steered", Message: msg.Message})
					// Every open screen, not just the one that spoke: two
					// tabs on one identity must not disagree about what
					// the identity has yet to hear.
					s.BroadcastSteering()
					continue
				}
			}

			// No turn running: open one, and DO NOT BLOCK THIS LOOP doing it.
			// The loop must return to ReadMessage or the next thing the
			// operator says is not queued behind the turn, it is unread —
			// which is the whole defect. Owner: this connection; exit: logged.
			// LIFETIME: the SERVER's context, not this socket's. A reload
			// drops the socket; the turn is the identity's work and must
			// survive it (responses are broadcast for exactly this reason).
			// Cancel paths that remain: the operator's explicit cancel
			// (CancelTurn), and server shutdown.
			text := msg.Message
			turnCtx := s.serverTurnCtx()
			go func() {
				s.handleChat(turnCtx, conn, text, h)
				s.sendStatus(ctx, conn, h)
				log.Printf("dashboard: turn finished (%d chars in)", len(text))
			}()

		case "cancel":
			// The other half of reach. Steering adds information; this ends
			// the work. Both are needed: the expensive failure is not a
			// stale identity, it is a turn spending its whole budget going
			// somewhere the operator can already see is wrong.
			h := s.currentHandler()
			if h.CancelTurn != nil && h.CancelTurn() {
				s.sendMsg(ctx, conn, ServerMessage{Type: "cancelled"})
			} else {
				s.sendError(ctx, conn, "no turn is running to cancel")
			}

		case "query":
			h := s.currentHandler()
			switch msg.Query {
			case "status":
				s.sendStatus(ctx, conn, h)
			case "sections":
				// Frame furniture (R66 UP2), answered from server state,
				// not the swappable handler: the section registry is
				// app-lifetime and mode-independent (FIRSTBOOT included —
				// an unborn dashboard is still the frame).
				s.sendMsg(ctx, conn, s.sectionsMessage())
			case "ui_layout":
				s.sendMsg(ctx, conn, s.layoutMessage())
			case "ui_theme":
				s.sendMsg(ctx, conn, s.themeMessage())
			case "ui.overlay":
				// W2: the overlay readback owed to the human. Frame
				// furniture, answered from server state like sections/
				// layout/theme — mode-independent, no handler needed.
				s.sendMsg(ctx, conn, s.overlayMessage())
			case "steering":
				// Answerable as well as broadcast, so a client that
				// reconnects mid-turn learns what is still waiting
				// instead of rendering an empty queue over a full one.
				s.sendMsg(ctx, conn, s.steeringMessage(h))
			case "outbox":
				s.sendOutbox(ctx, conn, h)
			case "projects":
				s.sendProjects(ctx, conn, h, msg.RequestID)
			case "workspace":
				s.sendWorkspace(ctx, conn, h, msg.Name)
			case "sandbox":
				s.sendSandbox(ctx, conn, h)
			case "work":
				if h.GetWork == nil {
					s.sendError(ctx, conn, "not available")
					continue
				}
				if w, err := h.GetWork(); err == nil {
					s.sendMsg(ctx, conn, ServerMessage{Type: "work", Work: w})
				} else {
					s.sendError(ctx, conn, err.Error())
				}
			case "tools":
				if h.GetTools == nil {
					s.sendError(ctx, conn, "not available")
					continue
				}
				tools, err := h.GetTools()
				if err != nil {
					s.sendError(ctx, conn, err.Error())
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{Type: "tools", Tools: tools})
			case "identity":
				if h.GetIdentity == nil {
					s.sendError(ctx, conn, "not available")
					continue
				}
				state, err := h.GetIdentity()
				if err != nil {
					s.sendError(ctx, conn, err.Error())
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{Type: "identity", Identity: state})
			case "config":
				if h.GetConfig == nil {
					s.sendError(ctx, conn, "not available")
					continue
				}
				state, err := h.GetConfig()
				if err != nil {
					s.sendError(ctx, conn, err.Error())
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{Type: "config", Config: state})
			case "providers":
				if h.GetProviders == nil {
					s.sendMsg(ctx, conn, ServerMessage{Type: "providers", Providers: nil})
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{Type: "providers", Providers: h.GetProviders()})
			case "discover":
				if h.DiscoverModels == nil {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "error", Message: "not available", Provider: msg.Provider})
					continue
				}
				models, err := h.DiscoverModels(msg.Provider, msg.APIKey)
				if err != nil {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "error", Message: err.Error(), Provider: msg.Provider})
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "models", Provider: msg.Provider, ModelList: models})
			case "continuity":
				if h.GetContinuity == nil {
					s.sendError(ctx, conn, "not available")
					continue
				}
				cont, err := h.GetContinuity()
				if err != nil {
					s.sendError(ctx, conn, err.Error())
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{Type: "continuity", Continuity: cont})
			case "logs":
				// List (Name empty) or tail (Name set); nil handlers mean
				// logging is off — an empty list, not an error, so the
				// viewer renders the disabled state honestly.
				if h.ListLogs == nil {
					s.sendMsg(ctx, conn, ServerMessage{Type: "logs", LogsList: nil})
					continue
				}
				if msg.Name != "" {
					tail, err := h.TailLogs(msg.Name)
					if err != nil {
						s.sendError(ctx, conn, err.Error())
						continue
					}
					s.sendMsg(ctx, conn, ServerMessage{Type: "logs", LogsTail: tail})
					continue
				}
				files, err := h.ListLogs()
				if err != nil {
					s.sendError(ctx, conn, err.Error())
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{Type: "logs", LogsList: files})
			default:
				s.sendError(ctx, conn, "unknown query: "+msg.Query)
			}

		case "sandbox_set":
			h := s.currentHandler()
			if h.SetSandbox == nil {
				s.sendError(ctx, conn, "not available")
				continue
			}
			if err := h.SetSandbox(msg.Roots); err != nil {
				s.sendError(ctx, conn, err.Error())
				continue
			}
			s.sendSandbox(ctx, conn, h)

		case "project":
			h := s.currentHandler()
			// Success and refusal both echo the request id, so the page
			// can tell ITS create from a broadcast that happened to land
			// first, and disarm its pending state on the matching answer
			// alone (D72). The id already existed on every request; the
			// echo is the whole fix — no acknowledgement subsystem.
			if h.ProjectAct == nil || msg.Project == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
				continue
			}
			p := msg.Project
			if err := h.ProjectAct(p.Action, p.ID, p.Name, p.Description, p.Focus); err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				continue
			}
			s.sendProjects(ctx, conn, h, msg.RequestID)
			// §8.11: the answer above reaches the actor; the broadcast
			// below reaches every OTHER live window. Without it, a second
			// dashboard stayed stale until its own next query — the exact
			// gap this fan-out closes. Skips the actor so it never sees
			// the same payload twice (echo + broadcast).
			s.broadcastProjects(ctx, h, conn)

		case "config_set":
			h := s.currentHandler() // stale-handler bug (#3): birth swaps FIRSTBOOT->LIVE on this same socket
			// Top-level action (not a query): whitelisted changes; the
			// handler validates, persists, applies live where possible.
			if h.SetConfig == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
				continue
			}
			if msg.Config == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "config_set requires config")
				continue
			}
			// A substrate change runs REAL INFERENCE to prove the
			// candidate before adopting it, so it can take the whole
			// provider timeout — 120s by default, more with retries.
			// This read loop handles one message at a time, so holding
			// that inline froze the entire socket: no status, no chat,
			// no second attempt, for minutes. Answering from a goroutine
			// is safe here because writes are per-client mutexed
			// (sendMsg) and the reply is requestID-correlated, which is
			// exactly how the client already matches it.
			if changesSubstrate(msg.Config) {
				reqID, want := msg.RequestID, msg.Config
				go func() {
					state, err := h.SetConfig(want)
					if err != nil {
						s.sendErrorFor(ctx, conn, reqID, err.Error())
						return
					}
					s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
				}()
				continue
			}
			state, err := h.SetConfig(msg.Config)
			if err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				continue
			}
			s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "config", Config: state})

		case "tool_toggle":
			h := s.currentHandler()
			if h.SetToolFunc == nil {
				s.sendError(ctx, conn, "not available")
				continue
			}
			if msg.Tool == "" {
				s.sendError(ctx, conn, "tool_toggle requires tool")
				continue
			}
			if err := h.SetToolFunc(msg.Tool, msg.Enabled); err != nil {
				s.sendError(ctx, conn, err.Error())
				continue
			}
			tools, _ := h.GetTools()
			s.sendMsg(ctx, conn, ServerMessage{Type: "tools", Tools: tools})

		case "genesis":
			h := s.currentHandler() // same rule (#3)
			if msg.Genesis == nil {
				s.sendError(ctx, conn, "genesis request is empty")
				continue
			}
			if h.HandleGenesis == nil {
				s.sendError(ctx, conn, "genesis not configured")
				continue
			}
			response, err := h.HandleGenesis(ctx, msg.Genesis)
			if err != nil {
				s.sendError(ctx, conn, fmt.Sprintf("genesis failed: %v", err))
				continue
			}
			// Status FIRST, from the handler birth just swapped in: it
			// carries the identity's real name, so the greeting renders
			// under that name instead of the firstboot placeholder, and
			// the birth form is already gone when the words land. The
			// operator no longer has to refresh to see the birth begin.
			s.sendStatus(ctx, conn, s.currentHandler())
			s.sendMsg(ctx, conn, ServerMessage{Type: "response", Message: response, Role: "identity", Done: true})

		case "provider_set", "provider_delete":
			h := s.currentHandler()
			var err error
			switch {
			case msg.Type == "provider_set" && h.SetProvider != nil && msg.Entry != nil:
				err = h.SetProvider(*msg.Entry)
			case msg.Type == "provider_delete" && h.DeleteProvider != nil:
				err = h.DeleteProvider(msg.Provider)
			default:
				err = fmt.Errorf("provider editing not available")
			}
			if err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("providers: %v", err))
				continue
			}
			if h.GetProviders != nil {
				s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "providers", Providers: h.GetProviders()})
			}
			if h.GetConfig != nil {
				state, err := h.GetConfig()
				if err != nil {
					s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("config after provider change: %v", err))
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "config", Config: state})
			}

		default:
			s.sendError(ctx, conn, "unknown message type: "+msg.Type)
		}
	}
}

func (s *Server) handleChat(ctx context.Context, conn *websocket.Conn, message string, h *WSHandler) {
	if h.HandleMessage == nil {
		s.sendError(ctx, conn, "identity not ready")
		return
	}

	speaker := h.Speaker
	if speaker == "" {
		speaker = "system" // no claim of identity without an explicit one
	}
	if speaker == "identity" {
		// Only a mind thinks. The substrate answering with a pointer to
		// the birth form used to show thinking dots first, so the unborn
		// identity appeared to deliberate before the substrate spoke in
		// its bubble. Broadcast, not owner-only: every open screen and
		// any page reloaded mid-turn must land in the thinking state
		// (live report 2026-08-24 - reload froze the dots and the orb).
		s.broadcast(ServerMessage{Type: "response", Message: "", Stream: true})
	}

	var response string
	var err error
	if h.ObserveChat != nil {
		// Tool calls stream to the operator as they happen — the identity's
		// work is visible in the dashboard, not only in the server log.
		// Broadcast: a reloaded page and every other screen see the
		// work continue - the feed is turn state, not owner mail.
		emit := func(kind, name, args string) {
			s.broadcast(ServerMessage{Type: "event", Kind: kind, Name: name, Args: args})
		}
		response, err = h.ObserveChat(ctx, message, emit)
	} else {
		response, err = h.HandleMessage(ctx, message)
	}
	if err != nil {
		s.broadcast(ServerMessage{Type: "error", Message: fmt.Sprintf("identity error: %v", err)})
		return
	}

	s.broadcast(ServerMessage{Type: "response", Message: response, Role: speaker, Done: true})
}

func (s *Server) sendStatus(ctx context.Context, conn *websocket.Conn, h *WSHandler) {
	if h.GetStats == nil {
		return
	}

	stats, err := h.GetStats()
	if err != nil {
		return
	}
	stats.Name = h.IdentityName
	stats.Uptime = time.Since(s.startedAt).Round(time.Second).String()
	// Whether to offer a microphone at all. Set here rather than by the
	// stats source because it is a fact about THIS server's wiring: a
	// handler with no HearUtterance has no microphone whatever the
	// configuration says.
	stats.Voice = h.HearUtterance != nil && h.VoiceConfigured != nil && h.VoiceConfigured()
	if stats.Voice {
		stats.VoiceMaxFrameBytes = maxVoiceFrameBytes
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "status", Stats: stats})
}

func (s *Server) steeringMessage(h *WSHandler) ServerMessage {
	if h == nil || h.PendingSteers == nil {
		return ServerMessage{Type: "steering"}
	}
	return ServerMessage{Type: "steering", Pending: h.PendingSteers()}
}

// BroadcastResponse pushes a completed turn's answer to every screen.
// The leftover-steer flush in internal/app owns turns no socket loop
// started, and its answers must land like any other turn's.
func (s *Server) BroadcastResponse(role, message string) {
	s.broadcast(ServerMessage{Type: "response", Message: message, Role: role, Done: true})
}

// BroadcastSteering pushes the queue to every open screen. Called when
// words are accepted and again when the loop drains them, because the
// operator needs to see the queue EMPTY as much as they need to see it
// fill: that is the moment the identity actually heard them.
func (s *Server) BroadcastSteering() { s.broadcast(s.steeringMessage(s.currentHandler())) }

func (s *Server) sendOutbox(ctx context.Context, conn *websocket.Conn, h *WSHandler) {
	if h.GetOutbox == nil {
		return
	}

	items, err := h.GetOutbox()
	if err != nil {
		return
	}

	if err := s.sendMsg(ctx, conn, ServerMessage{Type: "outbox", Outbox: items}); err != nil {
		// NOT delivered, NOT marked: a closing or stalled socket must
		// not void unseen messages (D43, Sev 2026-08-26). The connect
		// and pump paths already write before they mark; the explicit
		// query follows the same rule. The items stay queued for the
		// next pass.
		return
	}

	if h.MarkDelivered != nil {
		for _, item := range items {
			h.MarkDelivered(item.ID)
		}
	}
}

func (s *Server) sendSandbox(ctx context.Context, conn *websocket.Conn, h *WSHandler) {
	if h.GetSandbox == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	sb, err := h.GetSandbox()
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "sandbox", Sandbox: sb})
}

// sendWorkspace answers the "workspace" query. Name carries the
// project ID (the "logs" precedent: empty lists, set selects).
func (s *Server) sendWorkspace(ctx context.Context, conn *websocket.Conn, h *WSHandler, id string) {
	if h.GetWorkspace == nil {
		s.sendError(ctx, conn, "not available")
		return
	}
	if id == "" {
		s.sendError(ctx, conn, "workspace query requires a project id")
		return
	}
	ws, err := h.GetWorkspace(id)
	if err != nil {
		s.sendError(ctx, conn, err.Error())
		return
	}
	if ws == nil {
		// Unknown ID is a named error, never an inert empty
		// render — the operator must learn the id is stale.
		s.sendError(ctx, conn, "project not found: "+id)
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{Type: "workspace", Workspace: ws})
}

func (s *Server) sendProjects(ctx context.Context, conn *websocket.Conn, h *WSHandler, reqID string) {
	if h.GetProjects == nil {
		s.sendErrorFor(ctx, conn, reqID, "not available")
		return
	}
	ps, err := h.GetProjects()
	if err != nil {
		s.sendErrorFor(ctx, conn, reqID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "projects", Projects: ps})
}

// broadcastProjects fans a fresh projects payload to every live
// connection except the actor's (its request-id echo already carries
// the same payload — no double send), no request id on the wire — a
// BROADCAST, distinguishable from an answer by the absent id. The
// §8.11 fix: the server used to answer project actions to the acting
// connection alone, so a dashboard open in two windows showed a stale
// dock in the one that didn't act. The actor still gets its request-id
// echo from the handler above; this pass is for everyone else. One
// fetch, one payload, fan-out under wsMu — the PushTransient/outbox-
// pump shape, minus the store write.
func (s *Server) broadcastProjects(ctx context.Context, h *WSHandler, skip *websocket.Conn) {
	if h.GetProjects == nil {
		return
	}
	ps, err := h.GetProjects()
	if err != nil {
		return
	}
	msg := ServerMessage{Type: "projects", Projects: ps}
	s.wsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for c := range s.wsConns {
		if c != skip {
			conns = append(conns, c)
		}
	}
	s.wsMu.Unlock()
	for _, c := range conns {
		s.sendMsg(ctx, c, msg)
	}
}

func (s *Server) sendError(ctx context.Context, conn *websocket.Conn, msg string) {
	s.sendMsg(ctx, conn, ServerMessage{Type: "error", Message: msg})
}

func (s *Server) sendErrorFor(ctx context.Context, conn *websocket.Conn, requestID, msg string) {
	s.sendMsg(ctx, conn, ServerMessage{RequestID: requestID, Type: "error", Message: msg})
}

// wsClient carries per-connection write state. The lock serializes the
// read-loop replies and the outbox pump onto ONE connection — and only
// that one. The global writeMu this replaces was the wedge (deferred
// bug-hunt item, fixed 2026-08-19): one stalled socket serialized every
// client's writes, and a connection's own read-loop writes carry that
// connection's lifetime ctx, so the stall was unbounded.
type wsClient struct {
	mu sync.Mutex
}

// writeWait bounds every frame write regardless of caller ctx. 2s is
// the bound the 2026-08-18 Method pass already chose for the SAFE wake
// path; now it is structural for all writes instead of one caller's
// discipline.
const writeWait = 2 * time.Second

// sendMsg writes one frame; the error return lets delivery-tracking
// callers (the outbox pump, connect-time delivery) mark an item
// delivered only when a write actually landed — "a message shown twice
// beats a message voided" applies to write FAILURES exactly as it does
// to zero connections. Fire-and-forget callers ignore the return.
func (s *Server) sendMsg(ctx context.Context, conn *websocket.Conn, msg ServerMessage) error {
	data, _ := json.Marshal(msg)
	s.wsMu.Lock()
	cl := s.wsConns[conn]
	s.wsMu.Unlock()
	if cl == nil {
		return errors.New("connection already dropped") // late callers on a dead client are no-ops
	}
	wctx, cancel := context.WithTimeout(ctx, writeWait)
	defer cancel()
	cl.mu.Lock()
	err := conn.Write(wctx, websocket.MessageText, data)
	cl.mu.Unlock()
	if err != nil {
		// A socket that cannot take a bounded write is dead. Close it so
		// its read loop unblocks and the connection leaves the map,
		// instead of eating the write penalty on every future broadcast.
		log.Printf("WS write failed (%v) — dropping client", err)
		s.dropConn(conn)
	}
	return err
}

// dropConn removes a connection and closes it exactly once. Safe against
// the read loop's own deferred delete (map deletes are idempotent; the
// close races are absorbed by the presence check).
func (s *Server) dropConn(conn *websocket.Conn) {
	s.wsMu.Lock()
	_, present := s.wsConns[conn]
	delete(s.wsConns, conn)
	s.wsMu.Unlock()
	if present {
		// CloseNow, not Close (D51, Sev 2026-08-26): Close performs the
		// closing handshake and WAITS for the peer — and a connection
		// being dropped for a stalled write is precisely one that will
		// not answer. The graceful path burned seconds per dead client
		// inside broadcast loops; CloseNow drops the TCP conn and the
		// read loop errors out. Same reasoning as Shutdown's.
		conn.CloseNow()
	}
}

// changesSubstrate reports a config_set that swaps the model provider or
// model, the only whitelisted change that performs a network round trip
// before it can answer. Everything else on this path is a file write and
// stays inline, where its ordering against other messages is obvious.
func changesSubstrate(cfg map[string]interface{}) bool {
	if cfg == nil {
		return false
	}
	_, provider := cfg["llm.provider"]
	_, model := cfg["llm.model"]
	return provider || model
}
