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
	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/coder/websocket"
)

//go:embed static/*
var staticFS embed.FS

const AccessTokenMaxBytes = 4096

const legacyDashboardCookieName = "aii_token"

type Server struct {
	wsMu    sync.Mutex
	wsConns map[*websocket.Conn]*wsClient

	interactionSignal chan struct{}
	interactionDone   chan struct{}
	outboxSignal      chan struct{}
	pumpCancel        context.CancelFunc
	pumpDone          chan struct{}

	turnCtx       context.Context
	turnCtxCancel context.CancelFunc

	jobsMu       sync.Mutex
	jobsWG       sync.WaitGroup
	jobsStopping bool

	auth atomic.Pointer[accessPolicy]

	wsAdmitHook func()
	sweepEvery  time.Duration

	gate *quiesce.Gate

	sessionMu    sync.Mutex
	sessionConns int
	lastActivity time.Time
	sessionGrace time.Duration

	lastOperatorAct time.Time

	voiceMu   sync.Mutex
	voiceBusy bool

	port   int
	addr   string
	server *http.Server

	tlsDir      string
	tlsMaterial *TLSMaterial
	startedAt   time.Time
	handler     *WSHandler
	mu          sync.RWMutex

	loopbackAddr string
	loopServer   *http.Server
	hostsMu      sync.RWMutex
	allowedHosts map[string]bool
	host         string
	boundAddr    string
	tls          bool
	anyHostPort  string
	webhook      func(w http.ResponseWriter, r *http.Request, pluginID, hookPath string)

	origin string

	publicName string
	publicCert func() *tls.Certificate

	secMu        sync.RWMutex
	secReg       *sections.Registry
	layoutSource func() []byte
	themeSource  func() []byte

	overlayDir string

	buildStamp string

	overlayReported map[string]bool

	overlayEvents []OverlayEvent
}

type OverlayEvent struct {
	Path      string `json:"path"`
	Outcome   string `json:"outcome"`
	DecidedAt string `json:"decided_at"`
}

type HistoryTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`

	VoiceRef string `json:"voice_ref,omitempty"`
	Note     string `json:"note,omitempty"`
}

type ToolState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

type BeliefItem struct {
	ID            string  `json:"id"`
	Statement     string  `json:"statement"`
	Ring          int     `json:"ring"`
	Status        string  `json:"status"`
	EvidenceCount int     `json:"evidence_count"`
	Confidence    float64 `json:"confidence"`
}

type IntentionItem struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	State     string `json:"state"`
}

type ExperienceItem struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	CreatedAt  string `json:"created_at"`
	Provenance string `json:"provenance,omitempty"`
}

type IdentityState struct {
	Beliefs       []BeliefItem     `json:"beliefs"`
	Intentions    []IntentionItem  `json:"intentions"`
	Experiences   []ExperienceItem `json:"experiences"`
	Synthesis     string           `json:"synthesis"`
	Brief         string           `json:"brief"`
	Charter       string           `json:"charter"`
	Approval      *CharterApproval `json:"approval,omitempty"`
	TrustLevel    string           `json:"trust_level"`
	AutonomyLevel string           `json:"autonomy_level"`
	PrivateCount  int              `json:"private_count"`
}

type CharterApproval struct {
	State   string `json:"state"`
	Excerpt string `json:"excerpt,omitempty"`
	Chars   int    `json:"chars,omitempty"`
	Turn    uint64 `json:"turn,omitempty"`
	SaidAt  string `json:"said_at,omitempty"`
	Record  uint64 `json:"record,omitempty"`
	Error   string `json:"error,omitempty"`
}

type LLMConfigState struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ResolvedProvider string `json:"resolved_provider"`
	ResolvedModel    string `json:"resolved_model"`
	TimeoutSeconds   int    `json:"timeout_seconds"`

	ProbeTimeoutSeconds int    `json:"probe_timeout_seconds"`
	Endpoint            string `json:"endpoint"`
	APIKeyMasked        string `json:"api_key_masked"`
	ThinkingBudget      int    `json:"thinking_budget"`
	ContextLength       int    `json:"context_length"`

	PromptBudget       int    `json:"prompt_budget"`
	PromptBudgetSource string `json:"prompt_budget_source"`
	ReasoningEffort    string `json:"reasoning_effort"`

	EffortPlan string `json:"effort_plan"`

	EffortModel string `json:"effort_model,omitempty"`

	ThinkingApplies bool `json:"thinking_applies"`
	MaxOutputTokens int  `json:"max_output_tokens"`

	EffortChoice *EffortChoice `json:"effort_choice,omitempty"`
	Error        string        `json:"error,omitempty"`
}

type EffortChoice struct {
	Levels  []string `json:"levels"`
	Checked []string `json:"checked,omitempty"`
	InForce string   `json:"in_force"`
}

type WitnessConfigState struct {
	URL                string `json:"url"`
	IntervalEvents     int    `json:"interval_events"`
	PlatformPubkeyPath string `json:"platform_pubkey_path"`
	TLSSPKISHA256      string `json:"tls_spki_sha256"`
}

type GenesisConfigState struct {
	ServerURL    string `json:"server_url"`
	FirewallURL  string `json:"firewall_url"`
	BootstrapURL string `json:"bootstrap_url"`
}

type PromptConfigState struct {
	MaxTokens          int `json:"max_tokens"`
	RecentTurns        int `json:"recent_turns"`
	MaxToolResultChars int `json:"max_tool_result_chars"`
}

type ProviderDirectory struct {
	Providers                []ProviderInfo
	Broken                   []BrokenProviderInfo
	SkipSignInWithValidToken bool
}

type BrokenProviderInfo struct {
	Position int    `json:"position"`
	SHA256   string `json:"sha256"`
	Name     string `json:"name,omitempty"`
	Reason   string `json:"reason"`

	Repair string `json:"repair,omitempty"`
}

type ProviderInfo struct {
	Name     string `json:"name"`
	APIType  string `json:"api_type,omitempty"`
	Endpoint string `json:"endpoint"`

	APIKey string `json:"api_key,omitempty"`
	HasKey bool   `json:"has_key,omitempty"`

	Chat bool `json:"chat"`

	Local bool `json:"local,omitempty"`

	Speech          *ProviderSpeech `json:"speech,omitempty"`
	Credential      string          `json:"credential,omitempty"`
	CredentialInfo  *CredentialInfo `json:"credential_info,omitempty"`
	StatusReason    string          `json:"status_reason,omitempty"`
	Preselect       bool            `json:"preselect,omitempty"`
	PreselectWhy    string          `json:"preselect_why,omitempty"`
	APIKeyEnv       string          `json:"api_key_env,omitempty"`
	DefaultModel    string          `json:"default_model,omitempty"`
	ContextLength   int             `json:"context_length,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`

	EffortLevels []string `json:"effort_levels,omitempty"`

	EffortModel  string `json:"effort_model,omitempty"`
	SummaryField string `json:"summary_field,omitempty"`

	SummaryLevels   []string `json:"summary_levels,omitempty"`
	ThinkingBudget  int      `json:"thinking_budget,omitempty"`
	ThinkingMode    string   `json:"thinking_mode,omitempty"`
	ThinkingDisplay string   `json:"thinking_display,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`

	Extra             map[string]any   `json:"extra,omitempty"`
	Cache             *llm.CachePolicy `json:"cache,omitempty"`
	CacheModes        []string         `json:"cache_modes,omitempty"`
	CacheTTLs         []string         `json:"cache_ttls,omitempty"`
	CacheDiagnostics  bool             `json:"cache_diagnostics,omitempty"`
	CacheKeySupported bool             `json:"cache_key_supported,omitempty"`
	Status            string           `json:"status,omitempty"`
	SubscribeURL      string           `json:"subscribe_url,omitempty"`
	Default           bool             `json:"default,omitempty"`
	Models            []string         `json:"models,omitempty"`
	ConfiguredModels  []string         `json:"configured_models,omitempty"`

	CanSignIn bool        `json:"can_sign_in,omitempty"`
	SignIn    *SignInView `json:"signin,omitempty"`
}

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

type PluginsState struct {
	Autoload  string              `json:"autoload"`
	Skips     []PluginSkipView    `json:"skips,omitempty"`
	Pending   []PluginPendingView `json:"pending,omitempty"`
	Installed []PluginView        `json:"installed,omitempty"`
	Catalog   []CatalogEntryView  `json:"catalog,omitempty"`

	CatalogURL       string `json:"catalog_url"`
	CatalogDir       string `json:"catalog_dir,omitempty"`
	CatalogFetchedAt string `json:"catalog_fetched_at,omitempty"`
	CatalogError     string `json:"catalog_error,omitempty"`
	CatalogUpdates   int    `json:"catalog_updates,omitempty"`

	Runtime *RuntimeLimitsView `json:"runtime,omitempty"`

	AuthProfiles []AuthProfileView `json:"auth_profiles,omitempty"`
	Providers    []ProviderView    `json:"providers,omitempty"`
}

type AuthProfileView struct {
	Name     string `json:"name"`
	Scheme   string `json:"scheme"`
	Provider string `json:"provider,omitempty"`
	ClientID string `json:"client_id,omitempty"`

	HasClientSecret bool     `json:"has_client_secret,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
	Hosts           []string `json:"hosts,omitempty"`

	Host         string `json:"host,omitempty"`
	Port         int    `json:"port,omitempty"`
	SecretSource string `json:"secret_source,omitempty"`

	State     string   `json:"state"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	Handles   []string `json:"handles,omitempty"`

	CanDevice   bool            `json:"can_device,omitempty"`
	Device      *DeviceCodeView `json:"device,omitempty"`
	Custom      bool            `json:"custom,omitempty"`
	SignIn      *SignInView     `json:"signin,omitempty"`
	RedirectURI string          `json:"redirect_uri,omitempty"`

	Revocation string `json:"revocation,omitempty"`
}

type SignInView struct {
	Status string          `json:"status"`
	URL    string          `json:"url,omitempty"`
	Device *DeviceCodeView `json:"device,omitempty"`
	Manual bool            `json:"manual,omitempty"`
}

type DeviceCodeView struct {
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	Expires                 string `json:"expires"`
}

type ProviderView struct {
	Name        string        `json:"name"`
	SignIn      string        `json:"sign_in,omitempty"`
	RedirectURI string        `json:"redirect_uri,omitempty"`
	Services    []ServiceView `json:"services"`
	Hosts       []string      `json:"hosts"`
	Device      bool          `json:"device"`

	DeviceExcludes []string `json:"device_excludes,omitempty"`
}

type ServiceView struct {
	Name   string   `json:"name"`
	Read   []string `json:"read"`
	Modify []string `json:"modify"`
}

type AuthProfileEdit struct {
	RedirectURI  string            `json:"redirect_uri,omitempty"`
	Name         string            `json:"name"`
	Provider     string            `json:"provider"`
	ClientID     string            `json:"client_id"`
	ClientSecret string            `json:"client_secret,omitempty"`
	Services     map[string]string `json:"services,omitempty"`
	Scopes       []string          `json:"scopes,omitempty"`
	Hosts        []string          `json:"hosts,omitempty"`
	AuthorizeURL string            `json:"authorize_url,omitempty"`
	TokenURL     string            `json:"token_url,omitempty"`
	DeviceURL    string            `json:"device_url,omitempty"`
	RevokeURL    string            `json:"revoke_url,omitempty"`
}

type RuntimeLimitsView struct {
	MaxInstalledBytes  int64 `json:"max_installed_bytes"`
	MaxFiles           int   `json:"max_files"`
	MaxFileBytes       int64 `json:"max_file_bytes"`
	MaxCompressedBytes int64 `json:"max_compressed_bytes"`
	MaxDepth           int   `json:"max_depth"`
	RootsKept          int   `json:"roots_kept"`
	MaxStartupMS       int64 `json:"max_startup_ms"`
}

type StartupView struct {
	EffectiveMS int64  `json:"effective_ms"`
	RequestedMS int64  `json:"requested_ms"`
	CeilingMS   int64  `json:"ceiling_ms"`
	Source      string `json:"source"`
	Capped      bool   `json:"capped,omitempty"`
}

type PluginLifecycleView struct {
	State       string                 `json:"state"`
	Since       string                 `json:"since,omitempty"`
	Admission   string                 `json:"admission,omitempty"`
	Residue     []string               `json:"residue,omitempty"`
	RetryAt     string                 `json:"retry_at,omitempty"`
	Activations []PluginActivationView `json:"activations,omitempty"`
	Refusal     *PluginRefusalView     `json:"refusal,omitempty"`

	Held string `json:"held,omitempty"`
}

type PluginActivationView struct {
	Gen     uint64           `json:"gen"`
	Role    string           `json:"role"`
	Version string           `json:"version,omitempty"`
	Since   string           `json:"since,omitempty"`
	Timings map[string]int64 `json:"timings_ms,omitempty"`
}

type PluginRefusalView struct {
	Stage    string `json:"stage"`
	Class    string `json:"class"`
	Cause    string `json:"cause"`
	Remedy   string `json:"remedy,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type PluginView struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Tier    string `json:"tier"`
	Mode    string `json:"mode"`
	Variant string `json:"variant"`

	Publisher   string `json:"publisher,omitempty"`
	PublisherID string `json:"publisher_id,omitempty"`

	Title        string              `json:"title,omitempty"`
	Description  string              `json:"description,omitempty"`
	Family       string              `json:"family,omitempty"`
	Interfaces   []string            `json:"interfaces,omitempty"`
	Capabilities []string            `json:"capabilities,omitempty"`
	Runtime      string              `json:"runtime,omitempty"`
	PackageHash  string              `json:"package_hash,omitempty"`
	Tools        []string            `json:"tools,omitempty"`
	Settings     []PluginSettingView `json:"settings,omitempty"`

	Applies string `json:"applies,omitempty"`

	SessionSettings map[string]map[string]interface{} `json:"session_settings,omitempty"`

	Acts []PluginActView `json:"acts,omitempty"`

	Grants PluginGrantsView `json:"grants"`

	Accelerator *AcceleratorView `json:"accelerator,omitempty"`
	Readiness   *ReadinessView   `json:"readiness,omitempty"`

	Startup *StartupView `json:"startup,omitempty"`

	Lifecycle *PluginLifecycleView `json:"lifecycle,omitempty"`

	Models []ModelView `json:"models,omitempty"`
}

type PluginGrantsView struct {
	Interactions bool     `json:"interactions"`
	Listed       bool     `json:"listed"`
	KV           bool     `json:"kv"`
	Memory       bool     `json:"memory"`
	Voice        bool     `json:"voice"`
	Embeddings   bool     `json:"embeddings"`
	Tools        bool     `json:"tools"`
	Files        bool     `json:"files"`
	Hosts        []string `json:"hosts,omitempty"`
	Handles      []string `json:"handles,omitempty"`

	Local                []string `json:"local,omitempty"`
	PlaintextCredentials bool     `json:"plaintext_credentials,omitempty"`

	AutoConfirm []string `json:"auto_confirm,omitempty"`
	ReadOnly    bool     `json:"read_only"`
}

type ModelView struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Present bool   `json:"present"`
	Partial int64  `json:"partial,omitempty"`
}

type AcceleratorView struct {
	OS               string   `json:"os"`
	Arch             string   `json:"arch"`
	Backend          string   `json:"backend"`
	Operators        []string `json:"operators,omitempty"`
	RuntimeLibraries []string `json:"runtime_libraries,omitempty"`
	Precision        string   `json:"precision"`
	Models           []string `json:"models"`
	MemoryBytes      int64    `json:"memory_bytes"`
	SessionLimit     int      `json:"session_limit"`
	Fallback         string   `json:"fallback"`
}

type ReadinessView struct {
	ModelsLoaded int    `json:"models_loaded"`
	Accelerator  string `json:"accelerator"`
	ProbeMS      int    `json:"probe_ms"`
}

type CatalogEntryView struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Tier        string `json:"tier"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	License     string `json:"license,omitempty"`

	Title            string   `json:"title,omitempty"`
	Category         string   `json:"category,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	Updated          string   `json:"updated,omitempty"`
	Size             int64    `json:"size,omitempty"`
	Available        bool     `json:"available"`
	Installed        bool     `json:"installed"`
	InstalledVersion string   `json:"installed_version,omitempty"`
	InstalledTier    string   `json:"installed_tier,omitempty"`
	UpdateAvailable  bool     `json:"update_available,omitempty"`

	Pending     string `json:"pending,omitempty"`
	PendingText string `json:"pending_text,omitempty"`

	Requires string `json:"requires,omitempty"`
}

type PluginSettingView struct {
	Key         string      `json:"key"`
	Type        string      `json:"type"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Default     interface{} `json:"default,omitempty"`
	Value       interface{} `json:"value,omitempty"`
	Effective   interface{} `json:"effective,omitempty"`
	Invalid     string      `json:"invalid,omitempty"`

	Undeclared bool              `json:"undeclared,omitempty"`
	Values     []string          `json:"values,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Required   bool              `json:"required,omitempty"`
	Minimum    *float64          `json:"minimum,omitempty"`
	Maximum    *float64          `json:"maximum,omitempty"`
	Handles    []string          `json:"handles,omitempty"`

	Scope string `json:"scope,omitempty"`

	OAuth *SettingOAuthHintView `json:"oauth,omitempty"`

	KeyKept bool   `json:"key_kept,omitempty"`
	KeyHost string `json:"key_host,omitempty"`
	KeyNot  string `json:"key_not,omitempty"`

	ChoicesFrom bool `json:"choices_from,omitempty"`

	Account *SettingAccountView `json:"account,omitempty"`
}

type SettingAccountView struct {
	Hosts   []string       `json:"hosts,omitempty"`
	Not     string         `json:"not,omitempty"`
	Granted bool           `json:"granted,omitempty"`
	Offers  []AccountOffer `json:"offers,omitempty"`
}

type AccountOffer struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Not   string `json:"not,omitempty"`
}

type SettingChoice struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

type SettingChoicesReply struct {
	Plugin  string          `json:"plugin"`
	Key     string          `json:"key"`
	Choices []SettingChoice `json:"choices,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type SettingOAuthHintView struct {
	Provider string   `json:"provider"`
	Services []string `json:"services,omitempty"`
}

type PluginActView struct {
	ID        string                 `json:"id"`
	Operation string                 `json:"operation"`
	Summary   string                 `json:"summary,omitempty"`
	Effects   string                 `json:"effects,omitempty"`
	Args      map[string]interface{} `json:"args"`
	Finals    []PluginActFinal       `json:"finals,omitempty"`
	Session   string                 `json:"session,omitempty"`
	Proposed  string                 `json:"proposed"`
	Expires   string                 `json:"expires"`
}

type PluginActFinal struct {
	Sequence int64  `json:"sequence"`
	Text     string `json:"text"`
	Heard    bool   `json:"heard"`
}

type PluginSkipView struct {
	Kind    string `json:"kind,omitempty"`
	Dir     string `json:"dir"`
	Package string `json:"package,omitempty"`
	ID      string `json:"id"`
	Tier    string `json:"tier"`
	Reason  string `json:"reason"`
}

type PluginPendingView struct {
	ID              string      `json:"id"`
	Version         string      `json:"version"`
	Phase           string      `json:"phase"`
	Since           string      `json:"since,omitempty"`
	Summary         string      `json:"summary"`
	Attempt         int         `json:"attempt,omitempty"`
	LastError       string      `json:"last_error,omitempty"`
	RetryAt         string      `json:"retry_at,omitempty"`
	BytesPresent    int64       `json:"bytes_present"`
	BytesTotal      int64       `json:"bytes_total"`
	FilesPresent    int         `json:"files_present"`
	FilesTotal      int         `json:"files_total"`
	RuntimeDeclared bool        `json:"runtime_declared,omitempty"`
	RuntimePresent  bool        `json:"runtime_present,omitempty"`
	RuntimeBytes    int64       `json:"runtime_bytes,omitempty"`
	Models          []ModelView `json:"models,omitempty"`

	Refusal *PluginRefusalView `json:"refusal,omitempty"`
	Residue []string           `json:"residue,omitempty"`

	Lifecycle *PluginLifecycleView `json:"lifecycle,omitempty"`
	CleanupAt string               `json:"cleanup_at,omitempty"`
}

type DashboardState struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	TLS          bool   `json:"tls"`
	Origin       string `json:"origin,omitempty"`
	RequireToken bool   `json:"require_token"`
}

type PublicNameState struct {
	Status    string  `json:"status"`
	Name      string  `json:"name,omitempty"`
	Origin    string  `json:"origin,omitempty"`
	NotAfter  string  `json:"not_after,omitempty"`
	RenewAt   string  `json:"renew_at,omitempty"`
	DaysLeft  float64 `json:"days_left,omitempty"`
	LastError string  `json:"last_error,omitempty"`
	TLS       bool    `json:"tls"`
	CanClaim  bool    `json:"can_claim"`

	Address    string `json:"address,omitempty"`
	ResolvesTo string `json:"resolves_to,omitempty"`

	Zone        string `json:"zone,omitempty"`
	ServiceZone string `json:"service_zone,omitempty"`
	CanMove     bool   `json:"can_move"`

	Relay          string `json:"relay,omitempty"`
	RelayConnected bool   `json:"relay_connected,omitempty"`
	RelayError     string `json:"relay_error,omitempty"`

	RouteMode     string         `json:"route_mode,omitempty"`
	RelayEndpoint string         `json:"relay_endpoint,omitempty"`
	Relays        *[]RelayChoice `json:"relays,omitempty"`
}

type RelayChoice struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type UpdatesConfigState struct {
	Automatic bool `json:"automatic"`
}

type AgencyConfigState struct {
	PreferLocalForRoles bool `json:"prefer_local_for_roles"`

	HeuristicNudges bool `json:"heuristic_nudges"`
}

type LogsConfigState struct {
	Dir          string `json:"dir"`
	MaxBackups   int    `json:"max_backups"`
	CompressDays int    `json:"compress_days"`
}

type UpdateState struct {
	CurrentVersion   string `json:"current_version"`
	AvailableVersion string `json:"available_version,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	NeedsRestart     bool   `json:"needs_restart,omitempty"`
	Error            string `json:"error,omitempty"`
	CheckedAt        string `json:"checked_at,omitempty"`
	Checking         bool   `json:"checking,omitempty"`
	Enabled          bool   `json:"enabled"`
	Automatic        bool   `json:"automatic"`
	StageRefusal     string `json:"stage_refusal,omitempty"`
	ReleaseURL       string `json:"release_url,omitempty"`
}

type MaintenanceConfigState struct {
	Enabled    bool `json:"enabled"`
	BackupKeep int  `json:"backup_keep"`
}

type ConfigState struct {
	Maintenance MaintenanceConfigState `json:"maintenance"`

	SecretsOK bool           `json:"secrets_ok"`
	Database  DatabaseState  `json:"database"`
	LLM       LLMConfigState `json:"llm"`

	Speech    SpeechConfigState `json:"speech"`
	Dashboard DashboardState    `json:"dashboard"`
	Plugins   PluginsState      `json:"plugins"`

	CredentialKinds []string           `json:"credential_kinds,omitempty"`
	Witness         WitnessConfigState `json:"witness"`
	Genesis         GenesisConfigState `json:"genesis"`
	Prompt          PromptConfigState  `json:"prompt"`
	Agency          AgencyConfigState  `json:"agency"`
	Updates         UpdatesConfigState `json:"updates"`
	Logs            LogsConfigState    `json:"logs"`
	Timezone        string             `json:"timezone"`
	RestartRequired []string           `json:"restart_required"`
	PublicName      PublicNameState    `json:"public_name"`

	Contacts []ContactState `json:"contacts"`
}

type ContactState struct {
	Name     string `json:"name"`
	Channel  string `json:"channel"`
	Address  string `json:"address"`
	Wake     bool   `json:"wake"`
	Operator bool   `json:"operator"`
}

type ContinuityState struct {
	LedgerSeq   uint64 `json:"ledger_seq"`
	AnchoredSeq int64  `json:"anchored_seq"`
	WitnessedAt string `json:"witnessed_at"`
	Unanchored  int64  `json:"unanchored"`
	WitnessURL  string `json:"witness_url"`
	LifeTicks   int64  `json:"lifetime_ticks"`

	Mode          string `json:"mode"`
	SafeReason    string `json:"safe_reason"`
	DegradedSince string `json:"degraded_since"`

	ReviewAt     string `json:"review_at,omitempty"`
	ReviewStatus string `json:"review_status,omitempty"`
	ReviewIssues int    `json:"review_issues,omitempty"`
}

type WSHandler struct {
	Interactions   interaction.Reader
	DatabaseExport func(context.Context) (io.ReadCloser, int64, error)

	Background func(work func()) bool

	Speaker  string
	GetStats func() (*StatsResponse, error)

	PublicNameClaim func() (PublicNameState, error)
	PublicNameRetry func() (PublicNameState, error)
	PublicNameMove  func() (PublicNameState, error)
	PublicNameState func() PublicNameState
	HandleMessage   func(ctx context.Context, msg string) (string, error)
	GetOutbox       func() ([]OutboxItem, error)
	HeldMail        func(context.Context, string) (MailRepairState, error)
	Messages        func(context.Context, MessagesRequest) (MessagesState, error)
	RepairMail      func(context.Context, MailRepairRequest) error
	InspectMail     func(context.Context, string) (MailRepairState, error)
	MarkDelivered   func(id string) error

	PagesClosed   func()
	HandleGenesis func(ctx context.Context, req *GenesisRequest) (string, error)

	RecentTurns func() ([]HistoryTurn, error)
	GetTools    func() ([]ToolState, error)
	SetToolFunc func(name string, enabled bool) error
	ObserveChat func(ctx context.Context, msg string, emit func(kind, name, args string)) (string, error)

	TurnActive func() bool
	Steer      func(text string) (bool, error)

	AdmitChat func(ctx context.Context, text string) (bool, error)

	ReleaseTurn func()

	GetAsks   func() []AskView
	AnswerAsk func(AskAnswer) (string, error)

	GradeResult func(GradeRequest) (uint64, error)

	AcquireTurn func(ctx context.Context) error
	CancelTurn  func() bool

	PendingSteers func() []string

	HearUtterance   func(ctx context.Context, pcm []byte, sampleRate, channels int, answer bool) error
	VoiceConfigured func() bool

	VoiceStatus func() (state, reason, source string)

	VoiceMode func() (listen, speak string, revision uint64)

	AudioPlane       func() *audio.Plane
	VoiceEngine      func() bool
	VoiceSessionOpen func(ctx context.Context, inputID, outputID, mode string, processing *CaptureProcessing) (VoiceSession, error)

	GetIdentity   func() (*IdentityState, error)
	GetContinuity func() (*ContinuityState, error)

	Recall func(query string) (string, error)

	GetProviders func() ProviderDirectory

	SetProvider    func(ProviderInfo) error
	SetEffort      func(level string) error
	DeleteProvider func(name string) error

	RepairProvider       func(position int, sha256 string) error
	RemoveBrokenProvider func(position int, sha256 string) error

	SetSpeechService func(name, apiKey, baseURL string) error

	SpeechLists func(provider, direction, search, language, apiKey string) (SpeechLists, error)

	SpeakMint func(say SpeakText) (string, error)

	SpeakPlay func(ctx context.Context, id string, w io.Writer) error

	SpeakAhead func(text string) string

	ReplyVoice func() string

	SpeakerPolicy func() *SpeakerPolicyState

	DashboardToken func() string

	RotateDashboardToken func() error

	RequireDashboardToken func() error

	SignInProvider      func(name string) (string, error)
	CompleteSignIn      func(name, input string) error
	OAuthCallback       func(code, state, authorityError string) error
	CancelSignIn        func(name string) error
	CancelProfileSignIn func(name string) error

	SignInProfile         func(name string) (string, error)
	CompleteProfileSignIn func(name, input string) error
	DeviceSignInProfile   func(name string) (*DeviceCodeView, error)
	DisconnectProfile     func(name string) error
	SetAuthProfile        func(edit AuthProfileEdit) error
	DeleteAuthProfile     func(name string) error

	SetPluginKey   func(plugin, key, secret string) error
	ClearPluginKey func(plugin, key string) error

	SettingChoices func(plugin, key string) ([]SettingChoice, error)

	UpdateCheck func() (*UpdateState, error)

	Continuity *ContinuityHooks

	RestoreNew *RestoreNewHooks

	Restart func() error

	DiscoverModels func(provider, baseURL, apiKey string) ([]string, error)

	GetWork func() (*WorkState, error)

	GetSandbox func() (*SandboxState, error)
	SetSandbox func(roots []string) error

	GetProjects func() ([]ProjectState, error)
	ProjectAct  func(ProjectRequest) error

	PluginAct func(PluginAction) error

	CatalogRefresh func() error

	GetWorkspace func(id string) (*WorkspaceState, error)

	GetProjectRoot func(id string) (string, bool)

	GetConfig func() (*ConfigState, error)
	SetConfig func(changes map[string]interface{}) (*ConfigState, error)

	ListLogs func() ([]LogFileState, error)
	TailLogs func(name string) (*LogTailState, error)
}

type StatsResponse struct {
	Name            string `json:"name"`
	BeliefCount     int    `json:"belief_count"`
	ReflectionCount int    `json:"reflection_count"`
	ExperienceCount int    `json:"experience_count"`
	IntentionCount  int    `json:"intention_count"`
	LedgerSeq       uint64 `json:"ledger_seq"`
	LifetimeTicks   int64  `json:"lifetime_ticks"`
	Uptime          string `json:"uptime"`

	ForegroundHolds []string `json:"foreground_holds,omitempty"`

	MalformedCalls  uint64 `json:"malformed_calls"`
	SuspiciousPaths uint64 `json:"suspicious_paths"`

	DuplicateArgKeys uint64 `json:"duplicate_arg_keys"`

	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`

	VoiceMaxFrameBytes int          `json:"voice_max_frame_bytes,omitempty"`
	Update             *UpdateState `json:"update,omitempty"`
	PluginUpdates      int          `json:"plugin_updates,omitempty"`

	CredentialWarning string `json:"credential_warning,omitempty"`

	LastTurn string `json:"last_turn,omitempty"`

	Voice bool `json:"voice"`

	VoiceEngine bool `json:"voice_engine"`

	VoiceState  string `json:"voice_state"`
	VoiceReason string `json:"voice_reason,omitempty"`

	VoiceSource string `json:"voice_source,omitempty"`

	VoiceListen       string `json:"voice_listen,omitempty"`
	VoiceSpeak        string `json:"voice_speak,omitempty"`
	VoiceModeRevision uint64 `json:"voice_mode_revision,omitempty"`

	ReplyVoice string `json:"reply_voice,omitempty"`

	Speakers *SpeakerPolicyState `json:"speakers,omitempty"`

	Expert bool `json:"expert"`

	Notices *NoticeRoute `json:"notices,omitempty"`
}

type OutboxItem struct {
	ID      string `json:"id"`
	To      string `json:"to"`
	Content string `json:"content"`
}

type WorkSessionItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
	Project     string `json:"project,omitempty"`

	Grade *GradeView `json:"grade,omitempty"`
}

type GradeView struct {
	Grade   string `json:"grade"`
	Comment string `json:"comment,omitempty"`
	Turn    uint64 `json:"turn"`
}

type GradeRequest struct {
	Session string `json:"session"`
	Grade   string `json:"grade"`
	Comment string `json:"comment,omitempty"`
	Item    string `json:"item,omitempty"`
}

type WorkState struct {
	Live      []WorkSessionItem `json:"live"`
	Queued    int               `json:"queued"`
	Delivered []WorkSessionItem `json:"delivered"`
}

type WorkspaceState struct {
	Project ProjectState      `json:"project"`
	Files   []WorkspaceFile   `json:"files,omitempty"`
	Work    []WorkSessionItem `json:"work,omitempty"`

	FilesTotal  int  `json:"files_total,omitempty"`
	FilesCapped bool `json:"files_capped,omitempty"`

	WorkCapped bool `json:"work_capped,omitempty"`
}

type WorkspaceFile struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

type SandboxState struct {
	Root       string   `json:"root"`
	ExtraRoots []string `json:"extra_roots"`
}

type ProjectState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"`
	Focus       string `json:"focus,omitempty"`
	Dir         string `json:"dir"`
	Active      bool   `json:"active"`

	Attributes map[string]interface{} `json:"attributes,omitempty"`

	Contract *ProjectContract `json:"contract,omitempty"`
	Parent   string           `json:"parent,omitempty"`

	Progress *ContractProgress `json:"progress,omitempty"`
}

type ProjectContract struct {
	Outcome     string   `json:"outcome,omitempty"`
	Acceptance  []string `json:"acceptance,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
}

type ProjectRequest struct {
	Action      string                  `json:"action"`
	ID          string                  `json:"id,omitempty"`
	Name        string                  `json:"name,omitempty"`
	Description *string                 `json:"description,omitempty"`
	Focus       *string                 `json:"focus,omitempty"`
	Attributes  *map[string]interface{} `json:"attributes,omitempty"`

	Parent   *string          `json:"parent,omitempty"`
	Contract *ProjectContract `json:"contract,omitempty"`
}

type AskView struct {
	ID        string                 `json:"id"`
	Kind      string                 `json:"kind"`
	From      string                 `json:"from"`
	Text      string                 `json:"text"`
	Choices   []string               `json:"choices,omitempty"`
	Connector string                 `json:"connector,omitempty"`
	Session   string                 `json:"session,omitempty"`
	Plugin    string                 `json:"plugin,omitempty"`
	Operation string                 `json:"operation,omitempty"`
	Effects   string                 `json:"effects,omitempty"`
	Args      map[string]interface{} `json:"args,omitempty"`
	Proposed  string                 `json:"proposed"`
	Expires   string                 `json:"expires,omitempty"`
}

type AskAnswer struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
	Choice string `json:"choice,omitempty"`
	Text   string `json:"text,omitempty"`
	Scope  string `json:"scope,omitempty"`
}

type PluginAction struct {
	Action string `json:"action"`
	ID     string `json:"id"`

	Act string `json:"act,omitempty"`
}

type PluginKeyRequest struct {
	Plugin string `json:"plugin"`
	Key    string `json:"key"`
	Secret string `json:"secret,omitempty"`
}

type ClientMessage struct {
	InteractionWindow *interaction.WindowQuery `json:"interaction_window,omitempty"`

	InteractionQuery *interaction.Query     `json:"interaction_query,omitempty"`
	MailRepair       *MailRepairRequest     `json:"mail_repair,omitempty"`
	Messages         *MessagesRequest       `json:"messages,omitempty"`
	RequestID        string                 `json:"request_id,omitempty"`
	Type             string                 `json:"type"`
	Message          string                 `json:"message"`
	Query            string                 `json:"query"`
	Genesis          *GenesisRequest        `json:"genesis"`
	Tool             string                 `json:"tool"`
	Config           map[string]interface{} `json:"config"`
	Enabled          bool                   `json:"enabled"`
	Project          *ProjectRequest        `json:"project,omitempty"`
	Plugin           *PluginAction          `json:"plugin,omitempty"`
	Ask              *AskAnswer             `json:"ask,omitempty"`
	Profile          string                 `json:"profile,omitempty"`
	PluginKey        *PluginKeyRequest      `json:"plugin_key,omitempty"`
	ProfileEdit      *AuthProfileEdit       `json:"profile_edit,omitempty"`
	Grade            *GradeRequest          `json:"grade,omitempty"`
	Roots            []string               `json:"roots,omitempty"`
	Provider         string                 `json:"provider,omitempty"`
	APIKey           string                 `json:"api_key,omitempty"`
	BaseURL          string                 `json:"base_url,omitempty"`
	Direction        string                 `json:"direction,omitempty"`
	Search           string                 `json:"search,omitempty"`
	Language         string                 `json:"language,omitempty"`
	Name             string                 `json:"name,omitempty"`
	Q                string                 `json:"q,omitempty"`
	Entry            *ProviderInfo          `json:"entry,omitempty"`
	Position         *int                   `json:"position,omitempty"`
	EntrySHA256      string                 `json:"entry_sha256,omitempty"`
	Input            string                 `json:"input,omitempty"`
	Effort           string                 `json:"effort,omitempty"`
	Voice            *VoiceRequest          `json:"voice,omitempty"`
	Backups          *ContinuityRequest     `json:"backups,omitempty"`
	RestoreNew       *RestoreNewRequest     `json:"restore_new,omitempty"`
}

type GenesisRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	Endpoint string `json:"endpoint"`
}

type SectionState struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Slot     string   `json:"slot"`
	Commands []string `json:"commands"`
	Topics   []string `json:"topics"`
	Entry    string   `json:"entry"`

	Dev bool `json:"dev,omitempty"`
}

type ServerMessage struct {
	InteractionWindow *interaction.WindowState `json:"interaction_window,omitempty"`

	ChatRecording     *ChatRecording    `json:"chat_recording,omitempty"`
	InteractionPage   *interaction.Page `json:"interaction_page,omitempty"`
	InteractionReason string            `json:"interaction_reason,omitempty"`
	MailRepair        *MailRepairState  `json:"mail_repair,omitempty"`
	Messages          *MessagesState    `json:"messages,omitempty"`
	PublicName        *PublicNameState  `json:"public_name,omitempty"`
	RequestID         string            `json:"request_id,omitempty"`
	Type              string            `json:"type"`
	TurnActive        bool              `json:"turn_active,omitempty"`
	Message           string            `json:"message,omitempty"`
	Stats             *StatsResponse    `json:"stats,omitempty"`
	Outbox            []OutboxItem      `json:"outbox,omitempty"`
	Asks              []AskView         `json:"asks,omitempty"`
	Device            *DeviceCodeView   `json:"device,omitempty"`
	Projects          []ProjectState    `json:"projects,omitempty"`
	Sandbox           *SandboxState     `json:"sandbox,omitempty"`
	Work              *WorkState        `json:"work,omitempty"`
	Workspace         *WorkspaceState   `json:"workspace,omitempty"`
	History           []HistoryTurn     `json:"history,omitempty"`
	Tools             []ToolState       `json:"tools,omitempty"`
	Identity          *IdentityState    `json:"identity,omitempty"`
	Query             string            `json:"query,omitempty"`
	Continuity        *ContinuityState  `json:"continuity,omitempty"`
	Config            *ConfigState      `json:"config,omitempty"`

	SettingChoices *SettingChoicesReply `json:"setting_choices,omitempty"`
	Providers      []ProviderInfo       `json:"providers,omitempty"`

	BrokenProviders          []BrokenProviderInfo `json:"broken_providers,omitempty"`
	SkipSignInWithValidToken *bool                `json:"skip_signin_with_valid_token,omitempty"`
	SignInURL                string               `json:"signin_url,omitempty"`
	Backups                  *ContinuityReply     `json:"backups,omitempty"`
	RestoreNew               *RestoreNewReply     `json:"restore_new,omitempty"`
	Update                   *UpdateState         `json:"update,omitempty"`
	Provider                 string               `json:"provider,omitempty"`
	ModelList                []string             `json:"model_list,omitempty"`
	SpeechLists              *SpeechLists         `json:"speech_lists,omitempty"`

	DashboardToken string `json:"dashboard_token,omitempty"`

	Pending []string `json:"pending,omitempty"`
	Kind    string   `json:"kind,omitempty"`
	Name    string   `json:"name,omitempty"`
	Args    string   `json:"args,omitempty"`

	Role   string `json:"role,omitempty"`
	Stream bool   `json:"stream,omitempty"`
	Done   bool   `json:"done,omitempty"`

	Sections []SectionState  `json:"sections,omitempty"`
	Layout   json.RawMessage `json:"layout,omitempty"`
	Theme    json.RawMessage `json:"theme,omitempty"`

	Overlays []OverlayEvent `json:"overlays,omitempty"`

	Token uint64   `json:"token,omitempty"`
	Paths []string `json:"paths,omitempty"`

	LogsList []LogFileState `json:"logs_list,omitempty"`
	LogsTail *LogTailState  `json:"logs_tail,omitempty"`

	VoiceSession *VoiceSessionState `json:"voice_session,omitempty"`
	VoiceEvent   *VoiceEvent        `json:"voice_event,omitempty"`
	VoiceReply   *VoiceReplyRef     `json:"voice_reply,omitempty"`
	VoiceHush    *VoiceHush         `json:"voice_hush,omitempty"`
}

type LogFileState struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	ModAt string `json:"mod_at"`
}

type LogTailState struct {
	Name  string   `json:"name"`
	Lines []string `json:"lines"`
}

func SchemeFor(tls bool) string {
	if tls {
		return "https"
	}
	return "http"
}

func (s *Server) Scheme() string { return SchemeFor(s.tls) }

func (s *Server) Origin() string {
	s.hostsMu.RLock()
	origin := s.origin
	s.hostsMu.RUnlock()
	if origin != "" {
		return origin
	}
	addr := s.boundAddr
	if addr == "" {
		addr = s.addr
	}

	if host, port, err := net.SplitHostPort(addr); err == nil && isWildcard(host) {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	return s.Scheme() + "://" + addr
}

func (s *Server) LocalURL() string {
	if s.loopbackAddr != "" {
		return "http://" + s.loopbackAddr
	}
	host, port, err := net.SplitHostPort(s.boundAddr)
	if err != nil {
		return ""
	}
	if isWildcard(host) {
		ip := net.ParseIP(host)
		if ip != nil && ip.To4() == nil {
			host = "::1"
		} else {
			host = "127.0.0.1"
		}
	}
	if IsLoopback(host) {
		return s.Scheme() + "://" + net.JoinHostPort(host, port)
	}
	return ""
}

func (s *Server) AddressURL() string {
	host, _, err := net.SplitHostPort(s.boundAddr)
	if err != nil || IsLoopback(host) || isWildcard(host) {
		return ""
	}
	return s.Scheme() + "://" + s.boundAddr
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func isWildcard(host string) bool {
	if host == "0.0.0.0" || host == "::" || host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func LoopbackURL(tls bool, port int) string {
	return fmt.Sprintf("%s://127.0.0.1:%d/", SchemeFor(tls), port)
}

func IsLoopback(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost", "":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func New(host string, port int, handler *WSHandler) *Server {

	s := &Server{
		handler:           handler,
		port:              port,
		wsConns:           make(map[*websocket.Conn]*wsClient),
		outboxSignal:      make(chan struct{}, 1),
		interactionSignal: make(chan struct{}, 1), interactionDone: make(chan struct{}),
		pumpDone:     make(chan struct{}),
		sessionGrace: 90 * time.Second,
		sweepEvery:   60 * time.Second,
		startedAt:    time.Now(),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			p = "/index.html"
		}
		var ctype string
		shippedOnly := false
		switch path.Ext(p) {
		case ".html":
			ctype = "text/html; charset=utf-8"
		case ".js":
			ctype = "text/javascript; charset=utf-8"
		case ".css":
			ctype = "text/css; charset=utf-8"
		case ".png":

			ctype = "image/png"
			shippedOnly = true
		default:
			http.NotFound(w, r)
			return
		}

		var data []byte
		ok := false
		if !shippedOnly {
			data, ok = s.overlayAsset(p)
		}
		if !ok {

			var err error
			data, err = staticFS.ReadFile("static" + p)
			if err != nil {
				if p == "/index.html" {

					http.Error(w, "cannot read index.html", http.StatusInternalServerError)
					return
				}
				http.NotFound(w, r)
				return
			}
		}

		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Security-Policy", uiCSP)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		referrerPolicy := "no-referrer"
		if p == "/index.html" {

			referrerPolicy = "same-origin"
		}
		w.Header().Set("Referrer-Policy", referrerPolicy)

		w.Header().Set("Cache-Control", "no-cache")

		w.Write(data)
	})

	mux.HandleFunc("GET /sections/{id}/{path...}", s.handleSectionFile)

	mux.HandleFunc("GET /p/{id}/{path...}", s.handleProjectFile)

	mux.HandleFunc("POST /restore/upload", s.handleRestoreUpload)
	mux.HandleFunc("POST /speech/say", s.handleSpeechSay)

	mux.HandleFunc("POST /database/export", s.handleDatabaseExport)
	mux.HandleFunc("GET /speech/say/{id}", s.handleSpeechPlay)

	mux.HandleFunc("GET /auth/token", s.handleAccessToken)
	mux.HandleFunc("POST /auth/token", s.handleAccessToken)
	mux.HandleFunc("POST /auth/rotate", s.handleRotateAccessToken)
	mux.HandleFunc("POST /auth/require", s.handleRequireAccessToken)

	mux.HandleFunc("GET /interaction/detail", s.serveInteractionDetail)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /oauth/callback", s.handleOAuthCallback)

	mux.HandleFunc("POST /hooks/{plugin}/{path...}", s.handleWebhook)
	mux.HandleFunc("GET /hooks/{plugin}/{path...}", s.handleWebhook)

	if host == "" {
		host = "127.0.0.1"
	}
	s.host = host
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", s.port))
	s.server = &http.Server{
		Addr:    addr,
		Handler: s.hostGate(mux),

		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 16,
	}
	s.addr = addr

	return s
}

func (s *Server) hostAllowed(hostPort string) bool {
	s.hostsMu.RLock()
	defer s.hostsMu.RUnlock()
	if s.allowedHosts[hostPort] {
		return true
	}
	if s.anyHostPort == "" {
		return false
	}
	_, p, err := net.SplitHostPort(hostPort)
	return err == nil && p == s.anyHostPort
}

func (s *Server) SetWebhookHandler(fn func(w http.ResponseWriter, r *http.Request, pluginID, hookPath string)) {
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	s.webhook = fn
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	s.hostsMu.RLock()
	fn := s.webhook
	s.hostsMu.RUnlock()
	if fn == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	fn(w, r, r.PathValue("plugin"), r.PathValue("path"))
}

func (s *Server) AllowHost(hostPort string) {
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	if s.allowedHosts == nil {
		s.allowedHosts = map[string]bool{}
	}
	s.allowedHosts[hostPort] = true
}

func (s *Server) TLSMaterial() *TLSMaterial { return s.tlsMaterial }

func (s *Server) SetOrigin(origin string) {
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	s.origin = strings.TrimSuffix(origin, "/")
}

func (s *Server) SetPublicCertificate(name string, source func() *tls.Certificate) {
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	s.publicName = strings.ToLower(name)
	s.publicCert = source
}

func (s *Server) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.hostsMu.RLock()
	name, source := s.publicName, s.publicCert
	s.hostsMu.RUnlock()
	if name == "" || source == nil || !strings.EqualFold(hello.ServerName, name) {
		return nil, nil
	}
	return source(), nil
}

func (s *Server) hostGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			logsink.Warn("dashboard.refusal", "refused request with foreign Host %q", r.Host)
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if scrubTokenQuery(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func scrubTokenQuery(w http.ResponseWriter, r *http.Request) bool {

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	q := r.URL.Query()
	if _, present := q["token"]; !present {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	q.Del("token")
	clean := *r.URL
	clean.RawQuery = q.Encode()
	http.Redirect(w, r, clean.RequestURI(), http.StatusSeeOther)
	return true
}

func (s *Server) handleAccessToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.accessRequired() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if s.tokenAuthorized(r) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if s.upgradeAccessCookie(w, r) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "dashboard access token required", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, AccessTokenMaxBytes+1))
	if err != nil || len(body) > AccessTokenMaxBytes || !s.validAccessToken(strings.TrimSpace(string(body))) {

		logsink.Warn("dashboard.refusal", "access token refused from %s", clientHost(r))
		time.Sleep(refusedLoginDelay)
		http.Error(w, "dashboard access token refused", http.StatusUnauthorized)
		return
	}
	s.setAccessCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRotateAccessToken(w http.ResponseWriter, r *http.Request) {
	s.handleTokenAct(w, r, false)
}

func (s *Server) handleRequireAccessToken(w http.ResponseWriter, r *http.Request) {
	s.handleTokenAct(w, r, true)
}

func (s *Server) handleTokenAct(w http.ResponseWriter, r *http.Request, require bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	done, where := "rotated", "rotate it there"
	if require {
		done, where = "turned on", "turn it on there"
	}
	switch {
	case require && s.accessRequired():
		http.Error(w, "this dashboard already asks for an access token — there is nothing to turn on; Rotate access token replaces it", http.StatusConflict)
		return
	case !require && !s.AccessTokenRequired():
		http.Error(w, "this dashboard is not asking for an access token, so there is none to rotate", http.StatusConflict)
		return
	case !require && !s.tokenAuthorized(r):
		http.Error(w, "dashboard access token required", http.StatusUnauthorized)
		return
	}

	if !sameSiteAs(r.Header.Get("Origin"), r.Host) {
		http.Error(w, "request came from another site", http.StatusForbidden)
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		http.Error(w, "request came from another site", http.StatusForbidden)
		return
	}
	if !privateConnection(r) {
		http.Error(w, "This connection is not private, so the access token was not "+done+": its cookie would cross the network in the clear. Open this page on the machine itself, or serve the dashboard over HTTPS (Settings → Dashboard), and "+where+".", http.StatusForbidden)
		return
	}
	var act func() error
	if h := s.currentHandler(); h != nil {
		act = h.RotateDashboardToken
		if require {
			act = h.RequireDashboardToken
		}
	}
	if act == nil {
		http.Error(w, "the access token cannot be "+done+" here", http.StatusNotFound)
		return
	}
	before := s.accessHash()
	err := act()

	if s.accessRequired() && s.accessHash() != before {
		s.setAccessCookie(w, r)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

const refusedLoginDelay = 400 * time.Millisecond

func clientHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) upgradeAccessCookie(w http.ResponseWriter, r *http.Request) bool {
	if s.accessHash() == "" {
		return false
	}
	if current, err := r.Cookie(dashboardCookieName(r)); err == nil && s.validAccessToken(current.Value) {
		s.setAccessCookie(w, r)
		return true
	}
	legacy, err := r.Cookie(legacyDashboardCookieName)
	if err != nil || !s.validAccessToken(legacy.Value) {
		return false
	}
	s.setAccessCookie(w, r)
	http.SetCookie(w, &http.Cookie{
		Name:     legacyDashboardCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
	return true
}

func (s *Server) setAccessCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     dashboardCookieName(r),
		Value:    s.accessHash(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,

		Secure: r.TLS != nil,
		MaxAge: 365 * 24 * 60 * 60,
	})
}

func dashboardCookieName(r *http.Request) string {
	_, port, err := net.SplitHostPort(r.Host)
	if err != nil || port == "" {
		if r.TLS != nil {
			port = "443"
		} else {
			port = "80"
		}
	}
	return "aii_token_" + port
}

func (s *Server) validAccessToken(offered string) bool {
	want, err := hex.DecodeString(s.accessHash())
	if err != nil || len(want) != sha256.Size {
		return false
	}
	sum := sha256.Sum256([]byte(offered))
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}

func listenDashboard(addr string, configuredPort int, listen func(string, string) (net.Listener, error)) (net.Listener, net.Listener, error) {
	const attempts = 16
	var collision error
	for attempt := 0; attempt < attempts; attempt++ {
		ln, err := listen("tcp", addr)
		if err != nil {
			return nil, nil, err
		}
		actualHost, actualPort, err := net.SplitHostPort(ln.Addr().String())
		if err != nil {
			ln.Close()
			return nil, nil, err
		}
		if IsLoopback(actualHost) || isWildcard(actualHost) {
			return ln, nil, nil
		}
		loopback, err := listen("tcp", "127.0.0.1:"+actualPort)
		if err == nil {
			return ln, loopback, nil
		}
		ln.Close()
		if configuredPort != 0 {
			return nil, nil, fmt.Errorf("the loopback companion 127.0.0.1:%s of the %s bind could not be bound: %w", actualPort, addr, err)
		}
		collision = err
	}
	return nil, nil, fmt.Errorf("could not reserve one ephemeral port for both network and loopback listeners: %w", collision)
}

func (s *Server) Start(tlsDir string) (string, error) {
	s.tlsDir = tlsDir
	ln, pairedLoopback, err := listenDashboard(s.addr, s.port, net.Listen)
	if err != nil {
		return "", fmt.Errorf("listen failed: %w", err)
	}

	actualAddr := ln.Addr().String()
	s.boundAddr = actualAddr

	ln = newLimitListener(ln, maxConcurrentConns)

	actualHost, actualPort, err := net.SplitHostPort(actualAddr)
	if err != nil {
		ln.Close()
		if pairedLoopback != nil {
			pairedLoopback.Close()
		}
		return "", fmt.Errorf("split listen addr %q: %w", actualAddr, err)
	}

	s.hostsMu.Lock()
	if s.allowedHosts == nil {
		s.allowedHosts = map[string]bool{}
	}
	s.allowedHosts[actualAddr] = true
	s.allowedHosts[net.JoinHostPort(s.host, actualPort)] = true
	if s.host == "127.0.0.1" || s.host == "::1" || s.host == "localhost" {
		s.allowedHosts["127.0.0.1:"+actualPort] = true
		s.allowedHosts["localhost:"+actualPort] = true
	}
	if isWildcard(actualHost) {
		s.anyHostPort = actualPort
	}
	s.hostsMu.Unlock()
	if isWildcard(actualHost) {
		logsink.Warn("dashboard.decision", "bound to %s — reachable from the whole network; the Host gate matches the port only", actualAddr)
	}

	var loopbacks []net.Listener
	if !IsLoopback(actualHost) && !isWildcard(actualHost) {
		for _, la := range []string{"127.0.0.1:" + actualPort, "[::1]:" + actualPort} {
			lln := pairedLoopback
			var lerr error
			if lln == nil || lln.Addr().String() != la {
				lln, lerr = net.Listen("tcp", la)
			}
			if lerr != nil {
				logsink.Warn("dashboard.error", "loopback %s not served (%v)", la, lerr)
				continue
			}
			loopbacks = append(loopbacks, newLimitListener(lln, maxConcurrentConns))
			s.hostsMu.Lock()
			s.allowedHosts[la] = true
			if s.loopbackAddr == "" {
				s.loopbackAddr = la
				s.allowedHosts["localhost:"+actualPort] = true
			}
			s.hostsMu.Unlock()
		}
	}
	serveLoopbacks := func() {
		if len(loopbacks) == 0 {
			return
		}

		s.loopServer = &http.Server{
			Handler:           s.server.Handler,
			ReadHeaderTimeout: s.server.ReadHeaderTimeout,
			IdleTimeout:       s.server.IdleTimeout,
			MaxHeaderBytes:    s.server.MaxHeaderBytes,
		}
		for _, lln := range loopbacks {
			go func(l net.Listener) {
				if err := s.loopServer.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logsink.Error("dashboard.error", "dashboard loopback server error: %v", err)
				}
			}(lln)
		}
	}

	if s.tlsDir == "" {
		s.tls = false
		if !IsLoopback(actualHost) {
			logsink.Warn("dashboard.decision", "serving PLAIN HTTP on %s — every word between this identity and its operator crosses the network in the clear, and the browser will refuse the microphone. Settings → Dashboard → HTTPS.", actualAddr)
		}
		s.startTurnLifecycles()
		serveLoopbacks()
		go func() {
			if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logsink.Error("dashboard.error", "dashboard server error: %v", err)
			}
		}()
		return actualAddr, nil
	}
	s.tls = true
	mat, terr := EnsureTLS(s.tlsDir, s.host)
	if terr != nil {
		ln.Close()
		for _, lln := range loopbacks {
			lln.Close()
		}
		return "", fmt.Errorf("dashboard TLS: %w", terr)
	}

	s.tlsMaterial = mat

	pair, perr := tls.LoadX509KeyPair(mat.LeafCert, mat.LeafKey)
	if perr != nil {
		ln.Close()
		for _, lln := range loopbacks {
			lln.Close()
		}
		return "", fmt.Errorf("dashboard TLS: certificate and key are not usable together (%s, %s): %w", mat.LeafCert, mat.LeafKey, perr)
	}

	s.server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{pair}, GetCertificate: s.getCertificate}
	s.startTurnLifecycles()
	serveLoopbacks()

	ln = newSniffListener(ln, s.bounceTarget)
	go func() {

		if err := s.server.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logsink.Error("dashboard.error", "dashboard server error: %v", err)
		}
	}()

	return actualAddr, nil
}

func (s *Server) startTurnLifecycles() {
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	turnCtx, turnCancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.pumpCancel = pumpCancel
	s.turnCtx = turnCtx
	s.turnCtxCancel = turnCancel
	s.mu.Unlock()
	go s.runOutboxPump(pumpCtx)
	go s.runInteractionView(pumpCtx)
}

func (s *Server) serverTurnCtx() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.turnCtx != nil {
		return s.turnCtx
	}
	return context.Background()
}

func (s *Server) PokeOutbox() {
	select {
	case s.outboxSignal <- struct{}{}:
	default:
	}
}

func (s *Server) SetQuiesceGate(g *quiesce.Gate) { s.gate = g }

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

			continue
		}
		delivered := false
		for _, c := range conns {
			if s.sendMsg(ctx, c, msg) == nil {
				delivered = true
			}
		}

		if delivered && h.MarkDelivered != nil {
			for _, item := range items {
				h.MarkDelivered(item.ID)
			}
		}
	}
}

func (s *Server) PushTransient(id, content string) int {
	return s.PushTransientItems([]OutboxItem{{ID: id, To: "operator", Content: content}})
}

func (s *Server) PushTransientItems(items []OutboxItem) int {
	msg := ServerMessage{Type: "outbox", Outbox: items}
	s.wsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for c := range s.wsConns {
		conns = append(conns, c)
	}
	s.wsMu.Unlock()
	for _, c := range conns {

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		s.sendMsg(ctx, c, msg)
		cancel()
	}
	return len(conns)
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.jobsMu.Lock()
	s.jobsStopping = true
	s.jobsMu.Unlock()
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
	if pumpCancel != nil {
		select {
		case <-s.interactionDone:
		case <-ctx.Done():
		}
	}

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

		c.CloseNow()
	}

	if s.loopServer != nil {
		_ = s.loopServer.Shutdown(ctx)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.server.Shutdown(ctx) }()
	var shutdownErr error
	select {
	case err := <-errCh:
		shutdownErr = err
	case <-ctx.Done():

		_ = s.server.Close()
		return ctx.Err()
	}
	turnsDone := make(chan struct{})
	go func() {
		s.jobsWG.Wait()
		close(turnsDone)
	}()
	select {
	case <-turnsDone:
		return shutdownErr
	case <-ctx.Done():
		return errors.Join(shutdownErr, ctx.Err())
	}
}

func (s *Server) SwapHandler(h *WSHandler) {
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
	if h != nil && h.Interactions != nil {
		var retired []*websocket.Conn
		s.wsMu.Lock()
		for c, cl := range s.wsConns {
			if cl.interactionVersion != "1" {
				retired = append(retired, c)
			}
		}
		s.wsMu.Unlock()
		for _, c := range retired {
			c.CloseNow()
		}
	}
	s.PokeInteractions()
}

func (s *Server) currentHandler() *WSHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handler
}

func (s *Server) background(h *WSHandler, work func()) bool {
	if h.Background != nil {
		return h.Background(work)
	}
	return s.runJob(work)
}

func (s *Server) runJob(work func()) bool {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if s.jobsStopping {
		return false
	}
	s.jobsWG.Add(1)
	go func() {
		defer s.jobsWG.Done()
		work()
	}()
	return true
}

func (s *Server) noteOperatorAct() {
	s.sessionMu.Lock()
	s.lastOperatorAct = time.Now()
	s.sessionMu.Unlock()
}

func (s *Server) OperatorActiveWithin(d time.Duration) bool {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	return !s.lastOperatorAct.IsZero() && time.Since(s.lastOperatorAct) <= d
}

func isOperatorAct(msgType string) bool {
	switch msgType {
	case "query", "probe", "":
		return false
	}
	return true
}

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

func (s *Server) PageOpen() bool {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return len(s.wsConns) > 0
}

func (s *Server) forgetConn(conn *websocket.Conn) bool {
	s.wsMu.Lock()
	_, present := s.wsConns[conn]
	delete(s.wsConns, conn)
	last := present && len(s.wsConns) == 0
	s.wsMu.Unlock()
	if h := s.currentHandler(); last && h != nil && h.PagesClosed != nil {
		h.PagesClosed()
	}
	return present
}

func (s *Server) wsAuthorized(r *http.Request) bool {
	return s.wsAuthorizedUnder(s.auth.Load(), r)
}

func (s *Server) wsAuthorizedUnder(p *accessPolicy, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != requestScheme(r) || !s.hostAllowed(u.Host) {
		return false
	}
	return tokenAuthorizedUnder(p, r)
}

func (s *Server) tokenAuthorized(r *http.Request) bool {
	return tokenAuthorizedUnder(s.auth.Load(), r)
}

func tokenAuthorizedUnder(p *accessPolicy, r *http.Request) bool {
	required, hash := false, ""
	if p != nil {
		required, hash = p.required, p.hash
	}
	if !required {
		return true
	}
	if hash == "" {
		return false
	}
	c, cerr := r.Cookie(dashboardCookieName(r))
	if cerr != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(hash)) == 1
}

type accessPolicy struct {
	required bool
	hash     string
}

func (s *Server) accessPolicy() (required bool, hash string) {
	p := s.auth.Load()
	if p == nil {
		return false, ""
	}
	return p.required, p.hash
}

func (s *Server) accessRequired() bool { required, _ := s.accessPolicy(); return required }
func (s *Server) accessHash() string   { _, hash := s.accessPolicy(); return hash }

func (s *Server) SetAccessToken(required bool, token string) {
	p := &accessPolicy{required: required}
	if token != "" {
		sum := sha256.Sum256([]byte(token))
		p.hash = hex.EncodeToString(sum[:])
	}

	if old := s.auth.Swap(p); cutsOff(old, p) {
		if n := s.closeAdmitted(); n > 0 {
			logsink.Info("dashboard.decision", "access policy changed — %d open session(s) closed; each signs in again", n)
		}
	}
}

func cutsOff(before, after *accessPolicy) bool {
	if before == nil {
		before = &accessPolicy{}
	}
	if after == nil {
		after = &accessPolicy{}
	}
	return after.required && (!before.required || before.hash != after.hash)
}

func (s *Server) closeAdmitted() int {
	s.wsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for c := range s.wsConns {
		conns = append(conns, c)
	}
	s.wsMu.Unlock()
	for _, c := range conns {
		c.CloseNow()
	}
	return len(conns)
}

func (s *Server) AccessTokenRequired() bool {
	required, hash := s.accessPolicy()
	return required && hash != ""
}

func (s *Server) BindHost() string { return s.host }

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	admitted := s.auth.Load()
	if !s.wsAuthorizedUnder(admitted, r) {
		logsink.Warn("dashboard.refusal", "refused: origin %q host %q failed auth", r.Header.Get("Origin"), r.Host)
		http.Error(w, "unauthorized", http.StatusForbidden)
		return
	}
	if h := s.currentHandler(); h != nil && h.Interactions != nil && r.URL.Query().Get("interaction_version") != "1" {
		http.Error(w, "dashboard protocol changed; reload the page", http.StatusUpgradeRequired)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{

		InsecureSkipVerify: true,
	})
	if err != nil {
		logsink.Warn("dashboard.error", "accept error: %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	conn.SetReadLimit(maxVoiceFrameBytes)

	ctx := r.Context()

	if s.wsAdmitHook != nil {
		s.wsAdmitHook()
	}

	s.wsMu.Lock()
	if cutsOff(admitted, s.auth.Load()) {
		s.wsMu.Unlock()
		logsink.Info("dashboard.refusal", "refused: the access policy changed while the session was being admitted")
		conn.CloseNow()
		return
	}
	cl := &wsClient{interactionVersion: r.URL.Query().Get("interaction_version"), interactionRequests: make(chan ClientMessage, 1), addr: r.RemoteAddr, agent: r.UserAgent(), secretsOK: privateConnection(r)}
	s.wsConns[conn] = cl
	s.wsMu.Unlock()
	defer func() {

		s.dropVoiceSession(cl)
		s.forgetConn(conn)
	}()

	s.sessionMu.Lock()
	s.sessionConns++
	s.sessionMu.Unlock()
	defer func() {
		s.sessionMu.Lock()
		s.sessionConns--
		s.sessionMu.Unlock()
	}()

	h := s.currentHandler()
	if h.Interactions != nil {
		s.sendMsg(ctx, conn, ServerMessage{Type: "interaction_changed"})
	}
	if h.GetOutbox != nil {
		items, err := h.GetOutbox()
		if err == nil && len(items) > 0 {
			msg := ServerMessage{Type: "outbox", Outbox: items}

			if s.sendMsg(ctx, conn, msg) == nil && h.MarkDelivered != nil {
				for _, item := range items {
					h.MarkDelivered(item.ID)
				}
			}
		}
	}

	s.sendStatus(ctx, conn, h)

	if h.Interactions == nil && h.RecentTurns != nil {
		if turns, err := h.RecentTurns(); err == nil && len(turns) > 0 {
			s.sendMsg(ctx, conn, ServerMessage{Type: "history", History: turns})
		}
	}

	if h.TurnActive != nil && h.TurnActive() {
		s.sendMsg(ctx, conn, ServerMessage{Type: "response", Message: "", Stream: true})
	}

	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			break
		}
		s.sessionMu.Lock()
		s.lastActivity = time.Now()
		s.sessionMu.Unlock()

		if typ == websocket.MessageBinary {
			if len(data) > 0 && data[0] == voiceStreamVersion {
				s.handleVoiceStream(ctx, conn, data)
			} else {
				s.handleVoiceFrame(ctx, conn, data)
			}
			continue
		}

		if len(data) > maxTextFrameBytes {
			s.sendError(ctx, conn, "message too large")
			continue
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			s.sendError(ctx, conn, "invalid message format")
			continue
		}

		if msg.Type == "query" && msg.Query == "interaction" {
			var envelope map[string]json.RawMessage
			if err := interaction.Strict(data, &envelope); err != nil {
				s.interactionError(ctx, conn, msg.RequestID, err)
				continue
			}
			var query interaction.Query
			if err := interaction.Strict(envelope["interaction_query"], &query); err != nil {
				s.interactionError(ctx, conn, msg.RequestID, err)
				continue
			}
			msg.InteractionQuery = &query
		}
		if isOperatorAct(msg.Type) {
			s.noteOperatorAct()
		}
		switch msg.Type {
		case "voice_session":
			s.handleVoiceSession(ctx, conn, msg.Voice)
			continue
		case "presence":

			continue
		case "probe":

			active := false
			if h := s.currentHandler(); h != nil && h.TurnActive != nil {
				active = h.TurnActive()
			}
			s.sendMsg(ctx, conn, ServerMessage{Type: "probe", RequestID: msg.RequestID, TurnActive: active})
			continue
		case "chat":

			h := s.currentHandler()
			s.runOperatorMessage(ctx, conn, h, msg.Message, msg.RequestID)

		case "ask":

			h := s.currentHandler()
			if msg.Ask == nil || h == nil || h.AnswerAsk == nil {
				s.sendError(ctx, conn, "ask: nothing to answer")
				continue
			}
			text, err := h.AnswerAsk(*msg.Ask)
			if err != nil {
				s.sendError(ctx, conn, err.Error())
				continue
			}
			s.BroadcastAsks()
			if text != "" {
				s.runOperatorMessage(ctx, conn, h, text)
			}

		case "cancel":

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
				if status, ok := s.statusMessage(h); ok {
					status.RequestID = msg.RequestID
					s.sendMsg(ctx, conn, status)
				}
			case "interaction":
				s.sendInteractionQuery(ctx, conn, h, msg)
			case "history":
				if h != nil && h.RecentTurns != nil {
					if turns, err := h.RecentTurns(); err == nil && len(turns) > 0 {
						s.sendMsg(ctx, conn, ServerMessage{Type: "history", RequestID: msg.RequestID, History: turns})
					} else if err != nil {
						s.sendMsg(ctx, conn, ServerMessage{Type: "error", RequestID: msg.RequestID, Message: "recent conversation unavailable: " + err.Error()})
					}
				}
			case "sections":

				s.sendMsg(ctx, conn, s.sectionsMessage())
			case "ui_layout":
				s.sendMsg(ctx, conn, s.layoutMessage())
			case "ui_theme":
				s.sendMsg(ctx, conn, s.themeMessage())
			case "ui.overlay":

				s.sendMsg(ctx, conn, s.overlayMessage())
			case "steering":

				s.sendMsg(ctx, conn, s.steeringMessage(h))
			case "outbox":
				s.sendOutbox(ctx, conn, h)
			case "held_mail":
				s.handleMailRepair(ctx, conn, h, msg)
			case "messages":
				s.handleMessages(ctx, conn, h, msg)
			case "projects":
				s.sendProjects(ctx, conn, h, msg.RequestID)
			case "workspace":
				s.sendWorkspace(ctx, conn, h, msg.Name)
			case "sandbox":
				s.sendSandbox(ctx, conn, h, msg.RequestID)
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
			case "recall":
				if h.Recall == nil {
					s.sendError(ctx, conn, "not available")
					continue
				}
				q := strings.TrimSpace(msg.Q)
				if q == "" {
					s.sendError(ctx, conn, "recall needs a word or phrase")
					continue
				}
				text, err := h.Recall(q)
				if err != nil {
					s.sendError(ctx, conn, err.Error())
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{Type: "recall", Query: q, Message: text, RequestID: msg.RequestID})
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
			case "asks":
				asks := []AskView{}
				if h.GetAsks != nil {
					if got := h.GetAsks(); got != nil {
						asks = got
					}
				}
				s.sendMsg(ctx, conn, ServerMessage{Type: "asks", Asks: asks})
			case "providers":
				if h.GetProviders == nil {
					s.sendMsg(ctx, conn, ServerMessage{Type: "providers", Providers: nil})
					continue
				}
				s.sendMsg(ctx, conn, providersMessage("", h.GetProviders()))
			case "discover":
				if h.DiscoverModels == nil {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "error", Message: "not available", Provider: msg.Provider})
					continue
				}

				reqID, provider, baseURL, apiKey := msg.RequestID, msg.Provider, msg.BaseURL, msg.APIKey
				if !s.background(h, func() {
					models, err := h.DiscoverModels(provider, baseURL, apiKey)
					if err != nil {
						s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "error", Message: err.Error(), Provider: provider})
						return
					}
					s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "models", Provider: provider, ModelList: models})
				}) {
					s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
				}
			case "speech_lists":
				if h.SpeechLists == nil {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "error", Message: "not available", Provider: msg.Provider})
					continue
				}

				reqID, provider, direction, search, language, apiKey := msg.RequestID, msg.Provider, msg.Direction, msg.Search, msg.Language, msg.APIKey
				if !s.background(h, func() {
					lists, err := h.SpeechLists(provider, direction, search, language, apiKey)
					if err != nil {
						s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "error", Message: err.Error(), Provider: provider})
						return
					}
					s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "speech_lists", SpeechLists: &lists})
				}) {
					s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
				}
			case "dashboard_token":

				if h.DashboardToken == nil || !s.AccessTokenRequired() {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "error", Message: "not available"})
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "dashboard_token", DashboardToken: h.DashboardToken()})
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
				s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
				continue
			}
			if err := h.SetSandbox(msg.Roots); err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				continue
			}
			s.sendSandbox(ctx, conn, h, msg.RequestID)

		case "project":
			h := s.currentHandler()

			if h.ProjectAct == nil || msg.Project == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
				continue
			}
			p := msg.Project
			if err := h.ProjectAct(*p); err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				continue
			}
			s.sendProjects(ctx, conn, h, msg.RequestID)

			s.broadcastProjects(ctx, h, conn)

		case "plugin":
			h := s.currentHandler()
			if h.PluginAct == nil || msg.Plugin == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
				continue
			}

			reqID, act := msg.RequestID, *msg.Plugin
			if !s.background(h, func() {
				if err := h.PluginAct(act); err != nil {
					s.sendErrorFor(ctx, conn, reqID, err.Error())
					return
				}
				var state *ConfigState
				if h.GetConfig != nil {
					state, _ = h.GetConfig()
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
			}) {
				s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
			}
			continue

		case "restart":
			h := s.currentHandler()
			if h.Restart == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
				continue
			}
			if err := h.Restart(); err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				continue
			}
			s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "restart"})
			continue

		case "catalog_refresh":
			h := s.currentHandler()
			if h.CatalogRefresh == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
				continue
			}

			reqID := msg.RequestID
			if !s.background(h, func() {
				if err := h.CatalogRefresh(); err != nil {
					s.sendErrorFor(ctx, conn, reqID, err.Error())
					return
				}
				var state *ConfigState
				if h.GetConfig != nil {
					state, _ = h.GetConfig()
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
			}) {
				s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
			}
			continue

		case "config_set":
			h := s.currentHandler()

			if h.SetConfig == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "not available")
				continue
			}
			if msg.Config == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "config_set requires config")
				continue
			}

			if changesRoute(msg.Config) && !cl.secretsOK {
				s.sendErrorFor(ctx, conn, msg.RequestID, notPrivateRoute)
				continue
			}

			if changesSubstrate(msg.Config) || changesSpeech(msg.Config) {
				reqID, want := msg.RequestID, msg.Config
				if !s.background(h, func() {
					state, err := h.SetConfig(want)
					if err != nil {
						s.sendErrorFor(ctx, conn, reqID, err.Error())
						return
					}
					s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
					s.BroadcastStatus()
				}) {
					s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
				}
				continue
			}
			state, err := h.SetConfig(msg.Config)
			if err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				continue
			}
			s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "config", Config: state})

			s.BroadcastStatus()

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
			h := s.currentHandler()
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

			s.sendStatus(ctx, conn, s.currentHandler())
			s.sendMsg(ctx, conn, s.spokenAloud(ServerMessage{Type: "response", Message: response, Role: "identity", Done: true}))

		case "public_name_claim", "public_name_retry", "public_name_move", "public_name_state":
			h := s.currentHandler()
			var st PublicNameState
			var err error
			switch {
			case msg.Type == "public_name_claim" && h.PublicNameClaim != nil:
				st, err = h.PublicNameClaim()
			case msg.Type == "public_name_retry" && h.PublicNameRetry != nil:
				st, err = h.PublicNameRetry()
			case msg.Type == "public_name_move" && h.PublicNameMove != nil:
				st, err = h.PublicNameMove()
			case msg.Type == "public_name_state" && h.PublicNameState != nil:
				st = h.PublicNameState()
			default:
				s.sendErrorFor(ctx, conn, msg.RequestID, "public names are not available")
				continue
			}
			if err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				continue
			}
			s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "public_name", PublicName: &st})

		case "restore_new":
			s.answerRestoreNew(ctx, msg, cl.secretsOK,
				func(rep RestoreNewReply) {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "restore_new", RestoreNew: &rep})
				},
				func(text string) { s.sendErrorFor(ctx, conn, msg.RequestID, text) })

		case "backups":
			s.handleBackups(ctx, conn, msg, cl.secretsOK)

		case "update_check":

			h := s.currentHandler()
			if h.UpdateCheck == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "updates are not available")
				continue
			}
			st, err := h.UpdateCheck()
			if err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("update check: %v", err))
				continue
			}
			s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "update_check", Update: st})
		case "provider_signin_cancel", "profile_signin_cancel":
			h := s.currentHandler()
			if h == nil {
				continue
			}
			cancel, name := h.CancelSignIn, msg.Provider
			if msg.Type == "profile_signin_cancel" {
				cancel, name = h.CancelProfileSignIn, msg.Profile
			}
			if cancel != nil {
				if err := cancel(name); err != nil {
					s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				}
			}
		case "provider_signin":
			h := s.currentHandler()
			if h.SignInProvider == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "sign-in not available")
				continue
			}
			reqID, name := msg.RequestID, msg.Provider
			if !s.background(h, func() {
				u, err := h.SignInProvider(name)
				if err != nil {
					s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("sign-in: %v", err))
					return
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "provider_signin", SignInURL: u})
			}) {
				s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
			}
		case "provider_signin_complete":
			h := s.currentHandler()
			if h.CompleteSignIn == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "sign-in not available")
				continue
			}
			if err := h.CompleteSignIn(msg.Provider, msg.Input); err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("sign-in: %v", err))
				continue
			}
			if h.GetProviders != nil {
				s.sendMsg(ctx, conn, providersMessage(msg.RequestID, h.GetProviders()))
			}
		case "repair_mail", "inspect_mail":
			s.handleMailRepair(ctx, conn, s.currentHandler(), msg)
		case "grade":

			h := s.currentHandler()
			if msg.Grade == nil || h == nil || h.GradeResult == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "grading is not available")
				continue
			}
			if _, err := h.GradeResult(*msg.Grade); err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, err.Error())
				continue
			}
			if h.GetWork != nil {
				if w, werr := h.GetWork(); werr == nil {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "work", Work: w})
				}
			}
			continue

		case "profile_signin":

			h := s.currentHandler()
			if h.SignInProfile == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "profile sign-in not available")
				continue
			}
			reqID, name := msg.RequestID, msg.Profile
			if !s.background(h, func() {
				u, err := h.SignInProfile(name)
				if err != nil {
					s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("connect: %v", err))
					return
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "profile_signin", SignInURL: u})
			}) {
				s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
			}
		case "profile_signin_complete", "profile_device", "profile_disconnect", "auth_profile_set", "auth_profile_delete":

			h := s.currentHandler()
			reqID, kind, name, edit := msg.RequestID, msg.Type, msg.Profile, msg.ProfileEdit
			if !s.background(h, func() {
				var err error
				var device *DeviceCodeView
				switch {
				case kind == "profile_signin_complete" && h.CompleteProfileSignIn != nil:
					err = h.CompleteProfileSignIn(name, msg.Input)
				case kind == "profile_device" && h.DeviceSignInProfile != nil:
					device, err = h.DeviceSignInProfile(name)
				case kind == "profile_disconnect" && h.DisconnectProfile != nil:
					err = h.DisconnectProfile(name)
				case kind == "auth_profile_set" && h.SetAuthProfile != nil && edit != nil:
					err = h.SetAuthProfile(*edit)
				case kind == "auth_profile_delete" && h.DeleteAuthProfile != nil:
					err = h.DeleteAuthProfile(name)
				default:
					err = fmt.Errorf("not available")
				}
				if err != nil {
					s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("%s: %v", strings.ReplaceAll(kind, "_", " "), err))
					return
				}
				if device != nil {
					s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "profile_device", Device: device})
					return
				}
				var state *ConfigState
				if h.GetConfig != nil {
					state, _ = h.GetConfig()
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
			}) {
				s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
			}
			continue
		case "plugin_key_set", "plugin_key_clear":
			s.handlePluginKey(ctx, conn, s.currentHandler(), msg, cl.secretsOK)
			continue
		case "plugin_setting_choices":
			s.handleSettingChoices(ctx, conn, s.currentHandler(), msg)
			continue
		case "effort_set":

			h := s.currentHandler()
			if h.SetEffort == nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, "effort: not available")
				continue
			}
			reqID, level := msg.RequestID, msg.Effort
			if !s.background(h, func() {
				if err := h.SetEffort(level); err != nil {
					s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("effort: %v", err))
					return
				}
				if h.GetProviders != nil {
					s.sendMsg(ctx, conn, providersMessage(reqID, h.GetProviders()))
				}
				if h.GetConfig != nil {
					state, err := h.GetConfig()
					if err != nil {
						s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("config after effort change: %v", err))
						return
					}
					s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
				}

				s.BroadcastConfig()
			}) {
				s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
			}

		case "provider_set", "provider_delete", "provider_repair", "provider_remove_broken", "speech_service":
			h := s.currentHandler()
			var err error
			switch {
			case msg.Type == "provider_set" && h.SetProvider != nil && msg.Entry != nil:
				err = h.SetProvider(*msg.Entry)
			case msg.Type == "provider_delete" && h.DeleteProvider != nil:
				err = h.DeleteProvider(msg.Provider)
			case msg.Type == "provider_repair" && h.RepairProvider != nil && msg.Position != nil:
				err = h.RepairProvider(*msg.Position, msg.EntrySHA256)
			case msg.Type == "provider_remove_broken" && h.RemoveBrokenProvider != nil && msg.Position != nil:
				err = h.RemoveBrokenProvider(*msg.Position, msg.EntrySHA256)
			case msg.Type == "speech_service" && h.SetSpeechService != nil:
				err = h.SetSpeechService(msg.Provider, msg.APIKey, msg.BaseURL)
			default:
				err = fmt.Errorf("provider editing not available")
			}
			if err != nil {
				s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("providers: %v", err))
				continue
			}
			if h.GetProviders != nil {
				s.sendMsg(ctx, conn, providersMessage(msg.RequestID, h.GetProviders()))
			}
			if h.GetConfig != nil {
				state, err := h.GetConfig()
				if err != nil {
					s.sendErrorFor(ctx, conn, msg.RequestID, fmt.Sprintf("config after provider change: %v", err))
					continue
				}
				s.sendMsg(ctx, conn, ServerMessage{RequestID: msg.RequestID, Type: "config", Config: state})
			}

			s.BroadcastStatus()
			s.BroadcastConfig()

		default:
			s.sendError(ctx, conn, "unknown message type: "+msg.Type)
		}
	}
}

func (s *Server) runOperatorMessage(ctx context.Context, conn *websocket.Conn, h *WSHandler, message string, requestIDs ...string) {
	turnCtx := s.serverTurnCtx()
	if len(requestIDs) > 0 && requestIDs[0] != "" {
		id := requestIDs[0]
		turnCtx = withChatRecording(turnCtx, func(r ChatRecording) {
			s.sendMsg(ctx, conn, ServerMessage{Type: "chat_receipt", RequestID: id, ChatRecording: &r})
		})
	}
	ack := func(state string) {
		if len(requestIDs) > 0 && requestIDs[0] != "" {
			s.sendMsg(ctx, conn, ServerMessage{Type: "chat_ack", RequestID: requestIDs[0], Message: state})
		}
	}

	refuse := func(message string) {
		id := ""
		if len(requestIDs) > 0 {
			id = requestIDs[0]
		}
		s.sendErrorFor(ctx, conn, id, message)
	}
	if h == nil {
		ChatNotRecorded(turnCtx, "identity is not ready")
		ack("refused")
		refuse("identity is not ready")
		return
	}

	admit := h.AdmitChat
	if admit == nil && h.Steer != nil {
		admit = func(_ context.Context, text string) (bool, error) { return h.Steer(text) }
	}
	if admit != nil {
		delivered, err := admit(turnCtx, message)
		switch {
		case errors.Is(err, ErrBusyInternal):
			if h.HandleMessage == nil {
				ChatNotRecorded(turnCtx, "identity is not ready for queued turns")
				ack("refused")
				return
			}

			s.broadcast(ServerMessage{Type: "response", Role: "system", Done: true,
				Message: "An internal pass (memory metabolism) holds the turn — your message is queued and will run the moment it ends."})
			qtext := message
			if !s.runJob(func() {
				actx, acancel := context.WithTimeout(turnCtx, queuedTurnMaxWait)
				defer acancel()
				if h.AcquireTurn == nil {
					ChatNotRecorded(turnCtx, "identity not ready for queued turns")
					refuse("identity not ready for queued turns")
					return
				}
				if err := h.AcquireTurn(actx); err != nil {
					ChatNotRecorded(turnCtx, "queued message could not acquire a turn: "+err.Error())
					refuse(fmt.Sprintf("the internal pass did not yield in time; your message was NOT delivered — please send it again (%v)", err))
					return
				}
				s.handleChat(turnCtx, conn, qtext, h)
				s.sendStatus(ctx, conn, h)
				logsink.Info("dashboard.end", "queued turn finished (%d chars in)", len(qtext))
			}) {
				ChatNotRecorded(turnCtx, "identity is stopping; queued message did not start")
				refuse("identity is stopping; your message was NOT delivered")
				ack("refused")
				return
			}
			ack("accepted")
			return
		case err != nil:
			ChatNotRecorded(turnCtx, err.Error())
			refuse(err.Error())
			ack("refused")
			return
		case delivered:
			ack("accepted")
			s.sendMsg(ctx, conn, ServerMessage{Type: "steered", Message: message})

			s.BroadcastSteering()
			return
		}
	}
	if h.HandleMessage == nil {
		if h.AdmitChat != nil && h.ReleaseTurn != nil {
			h.ReleaseTurn()
		}
		ChatNotRecorded(turnCtx, "identity is not ready")
		ack("refused")
		refuse("identity is not ready")
		return
	}

	text := message
	if !s.runJob(func() {
		s.handleChat(turnCtx, conn, text, h)
		s.sendStatus(ctx, conn, h)
		logsink.Info("dashboard.end", "turn finished (%d chars in)", len(text))
	}) {
		if h.AdmitChat != nil && h.ReleaseTurn != nil {
			h.ReleaseTurn()
		}
		ChatNotRecorded(turnCtx, "identity is stopping; message did not start")
		refuse("identity is stopping; your message was NOT delivered")
		ack("refused")
		return
	}
	ack("accepted")
}

func (s *Server) handleChat(ctx context.Context, conn *websocket.Conn, message string, h *WSHandler) {

	defer ChatRecorded(ctx, nil, errors.New("the host did not confirm whether your message was recorded"))
	if h == nil || h.HandleMessage == nil {
		ChatNotRecorded(ctx, "identity not ready")
		s.sendError(ctx, conn, "identity not ready")
		return
	}

	speaker := h.Speaker
	if speaker == "" {
		speaker = "system"
	}
	if speaker == "identity" {

		s.broadcast(ServerMessage{Type: "response", Message: "", Stream: true})
	}

	var response string
	var err error
	if h.ObserveChat != nil {

		emit := func(kind, name, args string) {
			s.broadcast(ServerMessage{Type: "event", Kind: kind, Name: name, Args: args})
		}
		response, err = h.ObserveChat(ctx, message, emit)
	} else {
		response, err = h.HandleMessage(ctx, message)
	}
	if err != nil {

		if errors.Is(err, context.Canceled) {
			return
		}
		s.broadcast(ServerMessage{Type: "error", Message: fmt.Sprintf("identity error: %v", err)})
		return
	}

	s.broadcast(s.spokenAloud(ServerMessage{Type: "response", Message: response, Role: speaker, Done: true}))
}

func (s *Server) sendStatus(ctx context.Context, conn *websocket.Conn, h *WSHandler) {
	if msg, ok := s.statusMessage(h); ok {
		s.sendMsg(ctx, conn, msg)
	}
}

func (s *Server) statusMessage(h *WSHandler) (ServerMessage, bool) {
	if h == nil || h.GetStats == nil {
		return ServerMessage{}, false
	}
	stats, err := h.GetStats()
	if err != nil {
		return ServerMessage{}, false
	}
	stats.Uptime = time.Since(s.startedAt).Round(time.Second).String()

	stats.Voice = h.HearUtterance != nil && h.VoiceConfigured != nil && h.VoiceConfigured()
	if stats.Voice {
		stats.VoiceMaxFrameBytes = maxVoiceFrameBytes
	}
	stats.VoiceEngine = h.VoiceEngine != nil && h.VoiceSessionOpen != nil && h.AudioPlane != nil && h.VoiceEngine()
	if h.SpeakerPolicy != nil {
		stats.Speakers = h.SpeakerPolicy()
	}
	stats.VoiceState = "setup"
	if h.VoiceStatus != nil {
		stats.VoiceState, stats.VoiceReason, stats.VoiceSource = h.VoiceStatus()
	}
	if h.VoiceMode != nil {
		stats.VoiceListen, stats.VoiceSpeak, stats.VoiceModeRevision = h.VoiceMode()
	}

	if h.ReplyVoice != nil && h.SpeakMint != nil {
		stats.ReplyVoice = h.ReplyVoice()
	}
	if (stats.VoiceState == "plugin" && !stats.VoiceEngine) || (stats.VoiceState == "cloud" && h.HearUtterance == nil) {
		stats.VoiceState, stats.VoiceReason, stats.VoiceSource = "setup", "", ""
	}
	return ServerMessage{Type: "status", Stats: stats}, true
}

func (s *Server) BroadcastStatus() {
	if msg, ok := s.statusMessage(s.currentHandler()); ok {
		s.broadcast(msg)
	}
}

func (s *Server) steeringMessage(h *WSHandler) ServerMessage {
	if h == nil || h.PendingSteers == nil {
		return ServerMessage{Type: "steering"}
	}
	return ServerMessage{Type: "steering", Pending: h.PendingSteers()}
}

func (s *Server) BroadcastResponse(role, message string) {
	s.broadcast(s.spokenAloud(ServerMessage{Type: "response", Message: message, Role: role, Done: true}))
}

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

		return
	}

	if h.MarkDelivered != nil {
		for _, item := range items {
			h.MarkDelivered(item.ID)
		}
	}
}

func (s *Server) sendSandbox(ctx context.Context, conn *websocket.Conn, h *WSHandler, requestID string) {
	if h.GetSandbox == nil {
		s.sendErrorFor(ctx, conn, requestID, "not available")
		return
	}
	sb, err := h.GetSandbox()
	if err != nil {
		s.sendErrorFor(ctx, conn, requestID, err.Error())
		return
	}
	s.sendMsg(ctx, conn, ServerMessage{RequestID: requestID, Type: "sandbox", Sandbox: sb})
}

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

		s.sendError(ctx, conn, "project not found: "+id)
		return
	}

	s.wsMu.Lock()
	if cl := s.wsConns[conn]; cl != nil {
		cl.viewing = id
	}
	s.wsMu.Unlock()
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

func (s *Server) BroadcastConfig() {
	h := s.currentHandler()
	if h == nil || h.GetConfig == nil {
		return
	}
	state, err := h.GetConfig()
	if err != nil || state == nil {
		return
	}
	s.broadcast(ServerMessage{Type: "config", Config: state})
}

func (s *Server) BroadcastAsks() {
	h := s.currentHandler()
	if h == nil || h.GetAsks == nil {
		return
	}
	asks := h.GetAsks()
	if asks == nil {
		asks = []AskView{}
	}
	s.broadcast(ServerMessage{Type: "asks", Asks: asks})
}

func (s *Server) BroadcastSystemLine(text string) {
	s.broadcast(ServerMessage{Type: "system_line", Message: text})
}

func providersMessage(requestID string, d ProviderDirectory) ServerMessage {
	skip := d.SkipSignInWithValidToken
	return ServerMessage{RequestID: requestID, Type: "providers", Providers: d.Providers,
		BrokenProviders: d.Broken, SkipSignInWithValidToken: &skip}
}

func (s *Server) BroadcastProviders() {
	h := s.currentHandler()
	if h == nil || h.GetProviders == nil {
		return
	}
	s.broadcast(providersMessage("", h.GetProviders()))
}

func (s *Server) BroadcastProjects() {
	h := s.currentHandler()
	if h == nil || h.GetProjects == nil {
		return
	}
	ps, err := h.GetProjects()
	if err != nil {
		return
	}
	s.broadcast(ServerMessage{Type: "projects", Projects: ps})
}

func (s *Server) BroadcastWork() {
	h := s.currentHandler()
	if h == nil || h.GetWork == nil {
		return
	}
	w, err := h.GetWork()
	if err != nil {
		return
	}
	s.broadcast(ServerMessage{Type: "work", Work: w})
}

func (s *Server) BroadcastEvent(kind, name, args string) {
	s.broadcast(ServerMessage{
		Type: "event", Kind: "tool_call", Name: name,
		Args: args, Role: "system",
	})
}

func (s *Server) PushWorkspace(id string, ws *WorkspaceState) {
	if ws == nil {
		return
	}
	msg := ServerMessage{Type: "workspace", Workspace: ws}
	s.wsMu.Lock()
	conns := make([]*websocket.Conn, 0, len(s.wsConns))
	for c, cl := range s.wsConns {
		if cl != nil && cl.viewing == id {
			conns = append(conns, c)
		}
	}
	s.wsMu.Unlock()
	for _, c := range conns {
		ctx, cancel := context.WithTimeout(context.Background(), writeWait)
		s.sendMsg(ctx, c, msg)
		cancel()
	}
}

func (s *Server) sendError(ctx context.Context, conn *websocket.Conn, msg string) {
	s.sendMsg(ctx, conn, ServerMessage{Type: "error", Message: msg})
}

func (s *Server) sendErrorFor(ctx context.Context, conn *websocket.Conn, requestID, msg string) {
	s.sendMsg(ctx, conn, ServerMessage{RequestID: requestID, Type: "error", Message: msg})
}

type wsClient struct {
	interactionVersion  string
	interactionRead     atomic.Bool
	interactionRequests chan ClientMessage
	mu                  sync.Mutex

	addr, agent string

	secretsOK bool

	vmu   sync.Mutex
	voice *connVoice

	viewing string
}

const writeWait = 2 * time.Second

func (s *Server) handlePluginKey(ctx context.Context, conn *websocket.Conn, h *WSHandler, msg ClientMessage, secretsOK bool) {
	reqID, kind := msg.RequestID, msg.Type
	label := strings.ReplaceAll(kind, "_", " ")
	if refusal := pluginKeyRefusal(msg, secretsOK); refusal != "" {
		s.sendErrorFor(ctx, conn, reqID, label+": "+refusal)
		return
	}
	req := *msg.PluginKey
	if !s.background(h, func() {
		var err error
		switch {
		case kind == "plugin_key_set" && h.SetPluginKey != nil:
			err = h.SetPluginKey(req.Plugin, req.Key, req.Secret)
		case kind == "plugin_key_clear" && h.ClearPluginKey != nil:
			err = h.ClearPluginKey(req.Plugin, req.Key)
		default:
			err = fmt.Errorf("not available")
		}
		if err != nil {
			s.sendErrorFor(ctx, conn, reqID, fmt.Sprintf("%s: %v", label, err))
			return
		}
		var state *ConfigState
		if h.GetConfig != nil {
			state, _ = h.GetConfig()
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "config", Config: state})
	}) {
		s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
	}
}

func (s *Server) handleSettingChoices(ctx context.Context, conn *websocket.Conn, h *WSHandler, msg ClientMessage) {
	reqID, pk := msg.RequestID, msg.PluginKey
	if pk == nil || pk.Plugin == "" || pk.Key == "" {
		s.sendErrorFor(ctx, conn, reqID, "setting choices: name the plugin and the setting")
		return
	}
	req := *pk
	if !s.background(h, func() {
		reply := &SettingChoicesReply{Plugin: req.Plugin, Key: req.Key}
		if h.SettingChoices == nil {
			reply.Error = "not available"
		} else if choices, err := h.SettingChoices(req.Plugin, req.Key); err != nil {
			reply.Error = err.Error()
		} else {
			reply.Choices = choices
			if reply.Choices == nil {
				reply.Choices = []SettingChoice{}
			}
		}
		s.sendMsg(ctx, conn, ServerMessage{RequestID: reqID, Type: "setting_choices", SettingChoices: reply})
	}) {
		s.sendErrorFor(ctx, conn, reqID, "identity is stopping")
	}
}

func pluginKeyRefusal(msg ClientMessage, secretsOK bool) string {
	pk := msg.PluginKey
	switch {
	case pk == nil || pk.Plugin == "" || pk.Key == "":
		return "name the plugin and the setting"
	case msg.Type == "plugin_key_set" && !secretsOK:
		return "this connection is not private, so the key was not taken — open the dashboard on the machine itself, or switch on TLS in Settings → Dashboard"
	}
	return ""
}

func (s *Server) sendMsg(ctx context.Context, conn *websocket.Conn, msg ServerMessage) error {
	s.wsMu.Lock()
	cl := s.wsConns[conn]
	s.wsMu.Unlock()
	if cl == nil {
		return errors.New("connection already dropped")
	}
	if msg.Config != nil {

		stamped := *msg.Config
		stamped.SecretsOK = cl.secretsOK
		msg.Config = &stamped
	}
	data, _ := json.Marshal(msg)
	wctx, cancel := context.WithTimeout(ctx, writeWait)
	defer cancel()
	cl.mu.Lock()
	err := conn.Write(wctx, websocket.MessageText, data)
	cl.mu.Unlock()
	if err != nil {

		logsink.Warn("dashboard.error", "write failed (%v) — dropping client", err)
		s.dropConn(conn)
	}
	return err
}

func (s *Server) dropConn(conn *websocket.Conn) {
	if s.forgetConn(conn) {

		conn.CloseNow()
	}
}

func changesSpeech(cfg map[string]interface{}) bool {
	for key := range cfg {
		if strings.HasPrefix(key, "speech.stt.") || strings.HasPrefix(key, "speech.tts.") {
			return true
		}
	}
	return false
}

func changesRoute(cfg map[string]interface{}) bool {
	_, mode := cfg["certificate.route_mode"]
	_, relay := cfg["certificate.relay_endpoint"]
	return mode || relay
}

const notPrivateRoute = "this connection is not private, so the route was not changed — open the dashboard on the machine itself, or serve it over HTTPS (Settings → Dashboard), and choose the route there"

func changesSubstrate(cfg map[string]interface{}) bool {
	if cfg == nil {
		return false
	}
	_, provider := cfg["llm.provider"]
	_, model := cfg["llm.model"]
	return provider || model
}

func (s *Server) BoundPort() string {
	_, port, err := net.SplitHostPort(s.boundAddr)
	if err != nil {
		return ""
	}
	return port
}

func (s *Server) BoundAddr() string { return s.boundAddr }

func HostAllowedForTest(s *Server, hostPort string) bool { return s.hostAllowed(hostPort) }

func (s *Server) HostAllowedForTest(hostPort string) bool { return s.hostAllowed(hostPort) }

func (s *Server) bounceTarget(local net.Addr) string {
	if local != nil {
		return "https://" + local.String()
	}
	return "https://" + s.boundAddr
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	h := s.currentHandler()
	q := r.URL.Query()
	if h == nil || h.OAuthCallback == nil || len(q["state"]) != 1 || len(q["code"]) > 1 || len(q["error"]) > 1 || (q.Get("code") == "" && q.Get("error") == "") {
		http.Error(w, "No matching sign-in. Return to AII OS and try again.", http.StatusBadRequest)
		return
	}
	if err := h.OAuthCallback(q.Get("code"), q.Get("state"), q.Get("error")); err != nil {
		http.Error(w, "Sign-in did not complete. Return to AII OS and try again.", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, "<!doctype html><title>AII OS</title><p>Signed in. Return to your AII OS tab.</p>")
}
