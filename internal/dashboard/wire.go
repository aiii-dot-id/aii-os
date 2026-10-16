package dashboard

import (
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

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
	ID            string `json:"id"`
	Content       string `json:"content"`
	Category      string `json:"category"`
	CreatedAt     string `json:"created_at"`
	Provenance    string `json:"provenance,omitempty"`
	Unmetabolized bool   `json:"unmetabolized,omitempty"`
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

	Meaning *MeaningState `json:"meaning,omitempty"`
}

type MeaningState struct {
	Source      string `json:"source,omitempty"`
	Plugin      string `json:"plugin,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
	Why         string `json:"why,omitempty"`

	Choice        string   `json:"choice,omitempty"`
	Plugins       []string `json:"plugins,omitempty"`
	ProviderModel bool     `json:"provider_model,omitempty"`

	Question string `json:"question,omitempty"`

	Waiting bool `json:"waiting,omitempty"`

	SetAside    int    `json:"set_aside,omitempty"`
	SetAsideWhy string `json:"set_aside_why,omitempty"`

	Basis string  `json:"basis,omitempty"`
	Floor float64 `json:"floor,omitempty"`

	Coverage []MeaningCoverage `json:"coverage,omitempty"`

	LastPass *MeaningPass `json:"last_pass,omitempty"`
}

type MeaningCoverage struct {
	Store    string `json:"store"`
	Rows     int64  `json:"rows"`
	Embedded int64  `json:"embedded"`

	Beyond         int64 `json:"beyond,omitempty"`
	BeyondEmbedded int64 `json:"beyond_embedded,omitempty"`
}

type MeaningPass struct {
	At       string `json:"at"`
	Embedded int    `json:"embedded"`
	Stopped  string `json:"stopped,omitempty"`
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

	ImagesShown string `json:"images_shown,omitempty"`

	ImageMaxBytes  int64 `json:"image_max_bytes,omitempty"`
	ImagesMaxBytes int64 `json:"images_max_bytes,omitempty"`
	ImageMaxSide   int   `json:"image_max_side,omitempty"`
	ImagesMaxCount int   `json:"images_max_count,omitempty"`

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

	Ring3MaxChars             int `json:"ring3_max_chars,omitempty"`
	SurfacingMaxChars         int `json:"surfacing_max_chars,omitempty"`
	SelfModelMaxTokens        int `json:"self_model_max_tokens,omitempty"`
	DreamConversationMaxChars int `json:"dream_conversation_max_chars,omitempty"`
	TensionsMaxChars          int `json:"tensions_max_chars,omitempty"`
	BriefMaxChars             int `json:"brief_max_chars,omitempty"`

	AlarmFirings int `json:"alarm_firings,omitempty"`

	ExcerptChars           int `json:"excerpt_chars,omitempty"`
	Ring3RecentExperiences int `json:"ring3_recent_experiences,omitempty"`
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

	HasKey bool `json:"has_key,omitempty"`

	Chat bool `json:"chat"`

	Local bool `json:"local,omitempty"`

	EmbeddingsModel string `json:"embeddings_model,omitempty"`

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

	CanSignIn bool `json:"can_sign_in,omitempty"`

	SignInUnmet string      `json:"sign_in_unmet,omitempty"`
	SignIn      *SignInView `json:"signin,omitempty"`
}

type ProviderEdit struct {
	ProviderInfo

	APIKey string `json:"api_key,omitempty"`

	ClearKey bool `json:"clear_key,omitempty"`

	SetEmbeddingsModel bool `json:"-"`
}

func (e *ProviderEdit) UnmarshalJSON(data []byte) error {
	var wire struct {
		ProviderInfo
		APIKey          string  `json:"api_key"`
		HasKey          *bool   `json:"has_key"`
		EmbeddingsModel *string `json:"embeddings_model"`
		ClearKey        bool    `json:"clear_key"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*e = ProviderEdit{ProviderInfo: wire.ProviderInfo, APIKey: wire.APIKey,
		ClearKey: wire.ClearKey || (wire.HasKey != nil && !*wire.HasKey)}
	if wire.EmbeddingsModel != nil {
		e.EmbeddingsModel, e.SetEmbeddingsModel = strings.TrimSpace(*wire.EmbeddingsModel), true
	}
	return nil
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

	Machine *MachineView `json:"machine,omitempty"`
}

type MachineView struct {
	State    string   `json:"state"`
	Build    string   `json:"build,omitempty"`
	Linked   int      `json:"linked,omitempty"`
	Update   string   `json:"update,omitempty"`
	CanForce bool     `json:"can_force,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Remedy   string   `json:"remedy,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Failing  bool     `json:"failing,omitempty"`

	Warning string `json:"warning,omitempty"`

	Store string `json:"store,omitempty"`
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

	Borrowed string `json:"borrowed,omitempty"`
}

const (
	SignInPending   = "pending"
	SignInConnected = "connected"
	SignInExpired   = "expired"
	SignInFailed    = "failed"
	SignInCancelled = "cancelled"
)

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

	Selection *PluginSelectionView `json:"selection,omitempty"`

	Checks *PluginChecksView `json:"checks,omitempty"`
}

type PluginChecksView struct {
	State    string `json:"state"`
	Variant  string `json:"variant,omitempty"`
	At       string `json:"at,omitempty"`
	Check    string `json:"check,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	Words    string `json:"words,omitempty"`
	Timeouts int    `json:"timeouts,omitempty"`
}

type PluginSelectionView struct {
	Variant  string                `json:"variant"`
	Kept     bool                  `json:"kept,omitempty"`
	Excluded []PluginExclusionView `json:"excluded,omitempty"`

	Better string `json:"better,omitempty"`

	Sets []PluginSetView `json:"sets,omitempty"`

	Needs string `json:"needs,omitempty"`
}

type PluginSetView struct {
	Variant   string             `json:"variant"`
	Chosen    bool               `json:"chosen,omitempty"`
	Why       string             `json:"why,omitempty"`
	Storage   string             `json:"storage,omitempty"`
	Bandwidth string             `json:"bandwidth,omitempty"`
	Resources string             `json:"resources,omitempty"`
	Reasons   []PluginReasonView `json:"reasons,omitempty"`

	Arriving bool `json:"arriving,omitempty"`
}

type PluginReasonView struct {
	Words  string `json:"words"`
	Remedy string `json:"remedy,omitempty"`
}

type PluginExclusionView struct {
	Variant string   `json:"variant"`
	Reasons []string `json:"reasons"`
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

	SessionSettings map[string]map[string]any `json:"session_settings,omitempty"`

	Acts []PluginActView `json:"acts,omitempty"`

	Grants PluginGrantsView `json:"grants"`

	Accelerator *AcceleratorView `json:"accelerator,omitempty"`
	Readiness   *ReadinessView   `json:"readiness,omitempty"`

	Startup *StartupView `json:"startup,omitempty"`

	Lifecycle *PluginLifecycleView `json:"lifecycle,omitempty"`

	Models []ModelView `json:"models,omitempty"`

	Probes *PluginProbesView `json:"probes,omitempty"`
}

type PluginProbesView struct {
	State string `json:"state"`
	At    string `json:"at,omitempty"`
	Words string `json:"words,omitempty"`
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

	Differs string `json:"differs,omitempty"`
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

	Needs string `json:"needs,omitempty"`

	Requires string `json:"requires,omitempty"`
}

const (
	SettingModelsReady    = "ready"
	SettingModelsFetching = "fetching"
	SettingModelsWaiting  = "waiting"
	SettingModelsStopped  = "stopped"
	SettingModelsAbsent   = "absent"
)

type SettingModelsView struct {
	State        string `json:"state"`
	FilesPresent int    `json:"files_present"`
	FilesTotal   int    `json:"files_total"`
	BytesPresent int64  `json:"bytes_present"`
	BytesTotal   int64  `json:"bytes_total"`
	LastError    string `json:"last_error,omitempty"`
	RetryAt      string `json:"retry_at,omitempty"`
	Remedy       string `json:"remedy,omitempty"`
}

type PluginSettingView struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
	Value       any    `json:"value,omitempty"`
	Effective   any    `json:"effective,omitempty"`
	Invalid     string `json:"invalid,omitempty"`

	Models *SettingModelsView `json:"models,omitempty"`

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
	ID        string           `json:"id"`
	Operation string           `json:"operation"`
	Summary   string           `json:"summary,omitempty"`
	Effects   string           `json:"effects,omitempty"`
	Args      map[string]any   `json:"args"`
	Finals    []PluginActFinal `json:"finals,omitempty"`
	Session   string           `json:"session,omitempty"`
	Proposed  string           `json:"proposed"`
	Expires   string           `json:"expires"`
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
	ID        string `json:"id"`
	Version   string `json:"version"`
	Phase     string `json:"phase"`
	Since     string `json:"since,omitempty"`
	Summary   string `json:"summary"`
	Attempt   int    `json:"attempt,omitempty"`
	LastError string `json:"last_error,omitempty"`

	Remedy          string      `json:"remedy,omitempty"`
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

	Selection *PluginSelectionView `json:"selection,omitempty"`
}

type DashboardState struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	TLS          bool   `json:"tls"`
	Origin       string `json:"origin,omitempty"`
	RequireToken bool   `json:"require_token"`

	TokenShort bool `json:"token_short,omitempty"`

	AccessCodeSeconds int `json:"access_code_seconds"`

	Settings []SettingRead `json:"settings,omitempty"`
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

	ServiceDeclined bool `json:"service_declined,omitempty"`

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

	DirectOrigin string   `json:"direct_origin,omitempty"`
	MovesWith    []string `json:"moves_with,omitempty"`

	DirectAddresses  *[]string `json:"direct_addresses,omitempty"`
	MachineAddresses []string  `json:"machine_addresses,omitempty"`
}

type RelayChoice struct {
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Origin    string `json:"origin,omitempty"`
	Suggested bool   `json:"suggested,omitempty"`
}

type UpdatesConfigState struct {
	Automatic bool `json:"automatic"`
}

type AgencyConfigState struct {
	PreferLocalForRoles bool `json:"prefer_local_for_roles"`

	RhythmSeconds   int `json:"rhythm_seconds,omitempty"`
	OutcomeWindow   int `json:"outcome_window,omitempty"`
	TurnTokenBudget int `json:"turn_token_budget,omitempty"`

	ToolSchemaRefusals int `json:"tool_schema_refusals,omitempty"`

	TurnCheckpointCalls       int `json:"turn_checkpoint_calls,omitempty"`
	TurnCheckpointGraceRounds int `json:"turn_checkpoint_grace_rounds,omitempty"`
	MaxTurnContinuations      int `json:"max_turn_continuations,omitempty"`

	TurnCheckpointSlack int `json:"turn_checkpoint_slack,omitempty"`

	ContinuationWallSeconds int `json:"continuation_wall_seconds,omitempty"`
	AlarmWallSeconds        int `json:"alarm_wall_seconds,omitempty"`
	HarvestWallSeconds      int `json:"harvest_wall_seconds,omitempty"`
}

type CognitionConfigState struct {
	ConsolidateBatch           int    `json:"consolidate_batch,omitempty"`
	ConsolidateMaxOps          int    `json:"consolidate_max_ops,omitempty"`
	ConsolidateSpacingSeconds  int    `json:"consolidate_spacing_seconds,omitempty"`
	MorningBriefAt             string `json:"morning_brief_at,omitempty"`
	SelfModelEveryMinutes      int    `json:"self_model_every_minutes,omitempty"`
	IdentityReviewEveryMinutes int    `json:"identity_review_every_minutes,omitempty"`
	SelfModelEvidenceRows      int    `json:"self_model_evidence_rows,omitempty"`
	SelfModelSpokenRows        int    `json:"self_model_spoken_rows,omitempty"`

	MorningBriefDefault string `json:"morning_brief_default"`

	PulseSeconds int                     `json:"pulse_seconds"`
	Settings     []CognitionSettingState `json:"settings"`

	InputBudget       int                       `json:"input_budget,omitempty"`
	InputBudgetSource string                    `json:"input_budget_source,omitempty"`
	Conditions        []CognitionConditionState `json:"conditions"`
}

type CognitionSettingState struct {
	Key           string `json:"key"`
	Min           int    `json:"min"`
	Max           int    `json:"max"`
	Default       int    `json:"default"`
	Unit          string `json:"unit"`
	ZeroIsDefault bool   `json:"zero_is_default"`
	Held          string `json:"held,omitempty"`
}

type CognitionConditionState struct {
	Facility string `json:"facility"`
	Kind     string `json:"kind"`
	Said     string `json:"said"`
	At       string `json:"at"`
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

	Package *UpdatePackage `json:"package,omitempty"`
}

type UpdatePackage struct {
	Version string `json:"version"`
	Path    string `json:"path,omitempty"`
	Refusal string `json:"refusal,omitempty"`
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

	Cognition       CognitionConfigState `json:"cognition"`
	Updates         UpdatesConfigState   `json:"updates"`
	Logs            LogsConfigState      `json:"logs"`
	Timezone        string               `json:"timezone"`
	RestartRequired []string             `json:"restart_required"`
	PublicName      PublicNameState      `json:"public_name"`

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

	DegradedWhy string `json:"degraded_why,omitempty"`

	ReviewAt     string `json:"review_at,omitempty"`
	ReviewStatus string `json:"review_status,omitempty"`
	ReviewIssues int    `json:"review_issues,omitempty"`

	ReviewLines []string `json:"review_lines,omitempty"`
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

	UninstallOffered bool `json:"uninstall_offered,omitempty"`

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

	VoiceSpeaker string `json:"voice_speaker,omitempty"`

	Speakers *SpeakerPolicyState `json:"speakers,omitempty"`

	Expert bool `json:"expert"`

	HostedOnPhone bool `json:"hosted_on_phone,omitempty"`

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
	Off        bool     `json:"off"`

	HeldWhy string `json:"held_why,omitempty"`
	Held    string `json:"held,omitempty"`

	NotKept []string `json:"not_kept,omitempty"`
}

type ProjectState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"`
	Focus       string `json:"focus,omitempty"`
	Dir         string `json:"dir"`
	Active      bool   `json:"active"`

	Attributes map[string]any `json:"attributes,omitempty"`

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
	Action      string          `json:"action"`
	ID          string          `json:"id,omitempty"`
	Name        string          `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Focus       *string         `json:"focus,omitempty"`
	Attributes  *map[string]any `json:"attributes,omitempty"`

	Parent   *string          `json:"parent,omitempty"`
	Contract *ProjectContract `json:"contract,omitempty"`
}

type AskView struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	From      string         `json:"from"`
	Text      string         `json:"text"`
	Choices   []string       `json:"choices,omitempty"`
	Connector string         `json:"connector,omitempty"`
	Session   string         `json:"session,omitempty"`
	Plugin    string         `json:"plugin,omitempty"`
	Operation string         `json:"operation,omitempty"`
	Effects   string         `json:"effects,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	Proposed  string         `json:"proposed"`
	Expires   string         `json:"expires,omitempty"`
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
	RequestOptions    *conversation.RequestOptions `json:"request_options,omitempty"`
	InteractionWindow *interaction.WindowQuery     `json:"interaction_window,omitempty"`

	AccessCode string `json:"access_code,omitempty"`

	InteractionQuery *interaction.Query `json:"interaction_query,omitempty"`
	MailRepair       *MailRepairRequest `json:"mail_repair,omitempty"`
	Messages         *MessagesRequest   `json:"messages,omitempty"`
	RequestID        string             `json:"request_id,omitempty"`
	Type             string             `json:"type"`
	Message          string             `json:"message"`
	Attachments      []string           `json:"attachments,omitempty"`
	Query            string             `json:"query"`
	Genesis          *GenesisRequest    `json:"genesis"`
	Tool             string             `json:"tool"`
	Config           map[string]any     `json:"config"`
	Enabled          bool               `json:"enabled"`
	Project          *ProjectRequest    `json:"project,omitempty"`
	Plugin           *PluginAction      `json:"plugin,omitempty"`
	Ask              *AskAnswer         `json:"ask,omitempty"`
	Profile          string             `json:"profile,omitempty"`
	PluginKey        *PluginKeyRequest  `json:"plugin_key,omitempty"`
	ProfileEdit      *AuthProfileEdit   `json:"profile_edit,omitempty"`
	Grade            *GradeRequest      `json:"grade,omitempty"`
	Roots            []string           `json:"roots,omitempty"`
	SandboxOff       *bool              `json:"sandbox_off,omitempty"`
	Provider         string             `json:"provider,omitempty"`
	APIKey           string             `json:"api_key,omitempty"`
	BaseURL          string             `json:"base_url,omitempty"`
	Direction        string             `json:"direction,omitempty"`
	Search           string             `json:"search,omitempty"`
	Language         string             `json:"language,omitempty"`
	Name             string             `json:"name,omitempty"`
	Q                string             `json:"q,omitempty"`
	Entry            *ProviderEdit      `json:"entry,omitempty"`
	Position         *int               `json:"position,omitempty"`
	EntrySHA256      string             `json:"entry_sha256,omitempty"`
	Input            string             `json:"input,omitempty"`
	Effort           string             `json:"effort,omitempty"`
	Voice            *VoiceRequest      `json:"voice,omitempty"`
	Backups          *ContinuityRequest `json:"backups,omitempty"`
	RestoreNew       *RestoreNewRequest `json:"restore_new,omitempty"`
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
	StructuredOutput  json.RawMessage          `json:"structured_output,omitempty"`
	RequestErrorCode  string                   `json:"request_error_code,omitempty"`
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

	NeedsKey bool `json:"needs_key,omitempty"`

	AccessCode *AccessCodeState `json:"access_code,omitempty"`

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
