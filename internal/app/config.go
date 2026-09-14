// .
// .
// .
package app

import (
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"path/filepath"

	configdir "github.com/aiii-dot-id/aii-os/config"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

type IdentityConfig struct {
	LedgerPath string `json:"ledger_path"`
	DBPath     string `json:"db_path"`
	KeyPath    string `json:"key_path"`
}

// .
// .
// .
// .
// .
// .
// .
type LLMConfig struct {
	// .
	// .
	Provider string `json:"provider"`
	// .
	// .
	Model string `json:"model"`
	// .
	// .
	APIKeyEnv      string `json:"api_key_env"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Stream         *bool  `json:"stream,omitempty"`
	Retries        int    `json:"retries"`
	RetryBackoffMS int    `json:"retry_backoff_ms"`
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
type SpeechConfig struct {
	STT STTConfig `json:"stt"`
}

// .
type STTConfig struct {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	Provider string `json:"provider"`
	// .
	Model string `json:"model"`
	// .
	Language string `json:"language"`
	// .
	APIKeyEnv string `json:"api_key_env"`
	// .
	TimeoutSeconds int `json:"timeout_seconds"`
}

type DashboardConfig struct {
	// .
	// .
	// .
	// .
	// .
	Host string `json:"host"`
	Port int    `json:"port"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	OperatorASCII *bool `json:"operator_ascii,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	RequireToken bool `json:"require_token"`
	// .
	// .
	// .
	// .
	// .
	AuthTokenSHA256 string `json:"auth_token_sha256,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	TLS bool `json:"tls"`
	// .
	// .
	// .
	// .
	Origin string `json:"origin,omitempty"`
}

// .
// .
// .
type ProjectsConfig struct {
	Root string `json:"root"`
}

type ToolsConfig struct {
	CWD string `json:"cwd"`
	// .
	// .
	// .
	Disabled []string `json:"disabled,omitempty"`
	// .
	// .
	// .
	ExtraRoots []string `json:"extra_roots,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	LocalHosts []string `json:"local_hosts,omitempty"`
	// .
	// .
	ShellTimeoutSeconds    int `json:"shell_timeout_seconds"`
	WebFetchTimeoutSeconds int `json:"webfetch_timeout_seconds"`
}

// .
// .
type PluginsConfig struct {
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
	Autoload string `json:"autoload"`

	// .
	// .
	// .
	// .
	// .
	CertifierRoot string `json:"certifier_root,omitempty"`
	ReviewerRoot  string `json:"reviewer_root,omitempty"`
	PlatformRoot  string `json:"platform_root,omitempty"`

	// .
	// .
	// .
	// .
	CatalogDir string `json:"catalog_dir,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	CatalogURL string `json:"catalog_url,omitempty"`

	// .
	// .
	// .
	// .
	// .
	Grants map[string]broker.Grant `json:"grants,omitempty"`

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
	AuthProfiles map[string]broker.AuthProfile `json:"auth_profiles,omitempty"`

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	WorkerBinary string `json:"worker_binary,omitempty"`

	// .
	// .
	// .
	// .
	// .
	Runtime PluginRuntimeConfig `json:"runtime"`

	// .
	// .
	// .
	// .
	// .
	// .
	Resources map[string]PluginResources `json:"resources,omitempty"`

	// .
	// .
	// .
	// .
	// .
	Settings map[string]map[string]interface{} `json:"settings,omitempty"`

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	DevSection *DevSectionConfig `json:"dev_section,omitempty"`
}

// .
// .
// .
type DevSectionConfig struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// .
type PluginResources struct {
	MemoryMaxBytes uint64 `json:"memory_max_bytes,omitempty"`
	// .
	// .
	StreamMaxBytes int `json:"stream_max_bytes,omitempty"`
	// .
	// .
	FilesMaxBytes int `json:"files_max_bytes,omitempty"`
}

type PromptConfig struct {
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
	MaxTokens          int `json:"max_tokens"`
	MaxToolResultChars int `json:"max_tool_result_chars,omitempty"`
	// .
	// .
	PulseIntervalSeconds int `json:"pulse_interval_seconds,omitempty"`
	RecentTurns          int `json:"recent_turns"`
}

// .
// .
// .
// .
type AgencyConfig struct {
	MaxToolRounds        int `json:"max_tool_rounds"`
	MaxSubagentDepth     int `json:"max_subagent_depth"`
	MaxParallelSubagents int `json:"max_parallel_subagents"`
	SubagentWallSeconds  int `json:"subagent_wall_seconds"`
	// .
	// .
	// .
	// .
	// .
	// .
	SubagentWallSecondsLocal int `json:"subagent_wall_seconds_local"`
	SubagentMaxMints         int `json:"subagent_max_mints"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	SubagentMaxToolRounds int `json:"subagent_max_tool_rounds"`
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
	SubagentMaxToolCalls int `json:"subagent_max_tool_calls"`
	// .
	// .
	// .
	// .
	// .
	// .
	SubagentMaxLegs int `json:"subagent_max_legs"`
	// .
	// .
	// .
	SubagentContinuation *bool `json:"subagent_continuation,omitempty"`
	// .
	// .
	// .
	// .
	// .
	SpawnQueue    *bool `json:"spawn_queue,omitempty"`
	RhythmSeconds int   `json:"rhythm_seconds"`
	// .
	// .
	// .
	// .
	// .
	QueueWorkers int `json:"queue_workers"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	HarvestWake *bool `json:"harvest_wake,omitempty"`
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
	// .
	// .
	// .
	// .
	HeuristicNudges *bool `json:"heuristic_nudges,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	BreadthNudge *bool `json:"breadth_nudge,omitempty"`
	// .
	// .
	// .
	// .
	// .
	PlanNudge *bool `json:"plan_nudge,omitempty"`
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
	PlanningBrief *bool `json:"planning_brief,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	YieldAnswer *bool `json:"yield_answer,omitempty"`
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
	TurnContinuation *bool `json:"turn_continuation,omitempty"`
	// .
	// .
	// .
	Roles map[string]RoleRoute `json:"roles,omitempty"`
	// .
	// .
	PreferLocalForRoles bool `json:"prefer_local_for_roles"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	TurnTokenBudget int `json:"turn_token_budget"`
}

// .
type RoleRoute struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type WitnessConfig struct {
	URL                string `json:"url"`
	IntervalEvents     int    `json:"interval_events"`
	PlatformPubkeyPath string `json:"platform_pubkey_path"`
	TLSSPKISHA256      string `json:"tls_spki_sha256"`
}

// .
// .
// .
// .
// .
// .
type CertificateConfig struct {
	ServerURL     string `json:"server_url,omitempty"`
	TLSSPKISHA256 string `json:"tls_spki_sha256,omitempty"`
	ACMEDirectory string `json:"acme_directory,omitempty"`
	Contact       string `json:"contact,omitempty"`
	// .
	// .
	// .
	// .
	// .
	RouteMode string `json:"route_mode,omitempty"`
	// .
	// .
	// .
	// .
	RelayEndpoint string `json:"relay_endpoint,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	DirectAddresses *[]string `json:"direct_addresses,omitempty"`
}

const (
	// .
	// .
	// .
	// .
	// .
	defaultCertificateServerURL = "https://certificate.aiii.id"
	defaultACMEDirectory        = "https://acme-v02.api.letsencrypt.org/directory"
)

func (c CertificateConfig) serverURL() string {
	switch c.ServerURL {
	case "":
		return defaultCertificateServerURL
	case "none":
		// .
		// .
		return ""
	}
	return c.ServerURL
}

func (c CertificateConfig) acmeDirectory() string {
	if c.ACMEDirectory == "" {
		return defaultACMEDirectory
	}
	return c.ACMEDirectory
}

type GenesisConfig struct {
	ServerURL    string `json:"server_url"`
	FirewallURL  string `json:"firewall_url"`
	BootstrapURL string `json:"bootstrap_url"`
}

// .
// .
// .
// .
// .
type UpdatesConfig struct {
	Automatic bool `json:"automatic"`
	// .
	// .
	// .
	// .
	// .
	Repo string `json:"repo"`
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
type LogsConfig struct {
	Dir          string `json:"dir"`
	MaxBackups   int    `json:"max_backups"`
	CompressDays int    `json:"compress_days"`

	// .
	// .
	// .
	// .
	// .
	// .
	present bool `json:"-"`
}

// .
func (l *LogsConfig) UnmarshalJSON(data []byte) error {
	type plain LogsConfig
	p := plain{}
	if string(data) == "null" {
		*l = (LogsConfig)(p)
		return nil
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	*l = (LogsConfig)(p)
	l.present = true
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
type Contact struct {
	Name    string `json:"name"`
	Channel string `json:"channel"`
	Address string `json:"address"`
	// .
	// .
	// .
	Wake bool `json:"wake"`
}

// .
// .
// .
type MaintenanceConfig struct {
	// .
	// .
	Enabled *bool `json:"enabled,omitempty"`
	// .
	BackupKeep int `json:"backup_keep,omitempty"`
}

type Config struct {
	Identity    IdentityConfig    `json:"identity"`
	LLM         LLMConfig         `json:"llm"`
	Dashboard   DashboardConfig   `json:"dashboard"`
	Speech      SpeechConfig      `json:"speech"`
	Tools       ToolsConfig       `json:"tools"`
	Plugins     PluginsConfig     `json:"plugins"`
	Projects    ProjectsConfig    `json:"projects"`
	Prompt      PromptConfig      `json:"prompt"`
	Agency      AgencyConfig      `json:"agency"`
	Timezone    string            `json:"timezone"`
	Witness     WitnessConfig     `json:"witness_server"`
	Certificate CertificateConfig `json:"certificate"`
	Genesis     GenesisConfig     `json:"genesis"`
	Updates     UpdatesConfig     `json:"updates"`
	Logs        LogsConfig        `json:"logs"`
	Maintenance MaintenanceConfig `json:"maintenance"`
	Memory      MemoryConfig      `json:"memory"`
	Contacts    []Contact         `json:"contacts"`

	// .
	// .
	SourcePath string `json:"-"`
}

func defaultConfig() *Config {
	cfg := &Config{}
	if err := json.Unmarshal(configdir.Config, cfg); err != nil {
		panic("embedded config.json is invalid: " + err.Error())
	}
	applyDefaults(cfg)
	return cfg
}

// .
type PluginRuntimeConfig struct {
	MaxInstalledBytes  int64 `json:"max_installed_bytes"`
	MaxFiles           int   `json:"max_files"`
	MaxFileBytes       int64 `json:"max_file_bytes"`
	MaxCompressedBytes int64 `json:"max_compressed_bytes"`
	MaxDepth           int   `json:"max_depth"`
	RootsKept          int   `json:"roots_kept"`
}

// .
func (c PluginRuntimeConfig) TreeLimits() packagefmt.TreeLimits {
	return packagefmt.TreeLimits{
		MaxInstalledBytes: c.MaxInstalledBytes, MaxFiles: c.MaxFiles, MaxFileBytes: c.MaxFileBytes,
		MaxCompressedBytes: c.MaxCompressedBytes, MaxDepth: c.MaxDepth,
	}
}

// .
// .
// .
type MemoryConfig struct {
	Salience memory.SalienceWeights `json:"salience"`
}

func applyDefaults(cfg *Config) {
	if cfg.Memory.Salience.Version == "" {
		cfg.Memory.Salience = memory.DefaultSalience
	}
	// .
	d := packagefmt.DefaultTreeLimits
	r := &cfg.Plugins.Runtime
	if r.MaxInstalledBytes <= 0 {
		r.MaxInstalledBytes = d.MaxInstalledBytes
	}
	if r.MaxFiles <= 0 {
		r.MaxFiles = d.MaxFiles
	}
	if r.MaxFileBytes <= 0 {
		r.MaxFileBytes = d.MaxFileBytes
	}
	if r.MaxCompressedBytes <= 0 {
		r.MaxCompressedBytes = d.MaxCompressedBytes
	}
	if r.MaxDepth <= 0 {
		r.MaxDepth = d.MaxDepth
	}
	if r.RootsKept <= 0 {
		r.RootsKept = 2
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if cfg.Identity.LedgerPath == "" {
		cfg.Identity.LedgerPath = filepath.Join("data", "ledger.jsonl")
	}
	if cfg.Identity.DBPath == "" {
		cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	}
	if cfg.Identity.KeyPath == "" {
		cfg.Identity.KeyPath = filepath.Join("data", "identity.sec")
	}
	// .
	// .
	// .
	// .
	if cfg.LLM.APIKeyEnv == "" {
		cfg.LLM.APIKeyEnv = "OPENAI_API_KEY"
	}
	// .
	// .
	// .
	// .
	if !cfg.Logs.present && cfg.Logs.Dir == "" {
		cfg.Logs.Dir = "log"
	}
	if cfg.LLM.TimeoutSeconds == 0 {
		cfg.LLM.TimeoutSeconds = 120
	}
	if cfg.Prompt.RecentTurns == 0 {
		cfg.Prompt.RecentTurns = 20
	}
	if cfg.Tools.ShellTimeoutSeconds == 0 {
		cfg.Tools.ShellTimeoutSeconds = 120
	}
	if cfg.Tools.WebFetchTimeoutSeconds == 0 {
		cfg.Tools.WebFetchTimeoutSeconds = 30
	}
	if cfg.Agency.MaxToolRounds == 0 {
		// .
		// .
		// .
		// .
		// .
		cfg.Agency.MaxToolRounds = 30
	}
	if cfg.Agency.TurnTokenBudget == 0 {
		cfg.Agency.TurnTokenBudget = 600_000
	}
	if cfg.Agency.SubagentMaxToolRounds == 0 {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		cfg.Agency.SubagentMaxToolRounds = 30
	}
	// .
	// .
	// .
	if cfg.Agency.SubagentMaxToolRounds > cfg.Agency.MaxToolRounds {
		cfg.Agency.SubagentMaxToolRounds = cfg.Agency.MaxToolRounds
	}
	if cfg.Agency.SubagentMaxToolCalls == 0 {
		cfg.Agency.SubagentMaxToolCalls = 64
	}
	if cfg.Agency.SubagentMaxLegs == 0 {
		cfg.Agency.SubagentMaxLegs = 4
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
	if cfg.Agency.MaxSubagentDepth == 0 {
		cfg.Agency.MaxSubagentDepth = 3
	}
	if cfg.Agency.QueueWorkers == 0 {
		// .
		// .
		cfg.Agency.QueueWorkers = -1
	}
	if cfg.Agency.MaxParallelSubagents == 0 {
		cfg.Agency.MaxParallelSubagents = 3
	}
	if cfg.Agency.QueueWorkers == -1 {
		cfg.Agency.QueueWorkers = cfg.Agency.MaxParallelSubagents
	}
	if cfg.Agency.SubagentWallSeconds == 0 {
		// .
		// .
		// .
		cfg.Agency.SubagentWallSeconds = 1200
	}
	if cfg.Agency.SubagentWallSecondsLocal == 0 {
		cfg.Agency.SubagentWallSecondsLocal = 1800
	}
	if cfg.Agency.SubagentMaxMints == 0 {
		cfg.Agency.SubagentMaxMints = 20
	}
	if cfg.Agency.RhythmSeconds == 0 {
		cfg.Agency.RhythmSeconds = 600
	}

	// .
	// .
	// .
	if cfg.Tools.CWD == "" {
		cfg.Tools.CWD = "."
	}
	if cfg.Plugins.Autoload == "" {
		cfg.Plugins.Autoload = "T1"
	}
	if cfg.Dashboard.Port == 0 {
		cfg.Dashboard.Port = 8080
	}
	if cfg.Dashboard.Host == "" {
		cfg.Dashboard.Host = "127.0.0.1"
	}
	// .
	// .
	// .
	// .
	if !dashboard.IsLoopback(cfg.Dashboard.Host) && !cfg.Dashboard.TLS {
		cfg.Dashboard.TLS = true
	}
}
