package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"path/filepath"

	configdir "github.com/aiii-dot-id/aii-os/config"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/memory/salience"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"strconv"
	"strings"
	"time"
)

type IdentityConfig struct {
	LedgerPath string `json:"ledger_path"`
	DBPath     string `json:"db_path"`
	KeyPath    string `json:"key_path"`

	DBFormat          string `json:"db_format,omitempty"`
	DBOptimizeLevels  bool   `json:"db_optimize_levels,omitempty"`
	DBLearnDictionary bool   `json:"db_learn_dictionary,omitempty"`

	StartRetrySeconds int `json:"start_retry_seconds,omitempty"`

	StartRetryLimit *int `json:"start_retry_limit,omitempty"`
}

type LLMConfig struct {
	Provider string `json:"provider"`

	Model string `json:"model"`

	APIKeyEnv      string `json:"api_key_env"`
	TimeoutSeconds int    `json:"timeout_seconds"`

	ProbeTimeoutSeconds int   `json:"probe_timeout_seconds"`
	Stream              *bool `json:"stream,omitempty"`
	Retries             int   `json:"retries"`
	RetryBackoffMS      int   `json:"retry_backoff_ms"`
}

type SpeechConfig struct {
	STT STTConfig `json:"stt"`
	TTS TTSConfig `json:"tts"`

	Speakers SpeakerPolicyConfig `json:"speakers,omitempty"`

	OperatorSpeakers []string `json:"operator_speakers,omitempty"`

	Mode VoiceModeConfig `json:"mode,omitempty"`

	OpenWaitSeconds int `json:"open_wait_seconds,omitempty"`

	HeardHistory HeardHistoryConfig `json:"heard_history,omitempty"`
}

type HeardHistoryConfig struct {
	Capacity int `json:"capacity,omitempty"`

	RetentionSeconds int `json:"retention_seconds,omitempty"`

	TextBytes int `json:"text_bytes,omitempty"`

	PageRows int `json:"page_rows,omitempty"`
}

type heardSetting struct {
	key                string
	unset, least, most int
	field              func(*HeardHistoryConfig) *int
}

var heardSettings = []heardSetting{
	{"speech.heard_history.capacity", 256, 1, 2048, func(h *HeardHistoryConfig) *int { return &h.Capacity }},
	{"speech.heard_history.retention_seconds", 1800, 60, 86400, func(h *HeardHistoryConfig) *int { return &h.RetentionSeconds }},
	{"speech.heard_history.text_bytes", 8192, 256, 16384, func(h *HeardHistoryConfig) *int { return &h.TextBytes }},
	{"speech.heard_history.page_rows", 20, 1, heardReadRowsMost, func(h *HeardHistoryConfig) *int { return &h.PageRows }},
}

func heardSettingFor(key string) (heardSetting, bool) {
	for _, hs := range heardSettings {
		if hs.key == key {
			return hs, true
		}
	}
	return heardSetting{}, false
}

func (hs heardSetting) check(n int) error {
	if n != 0 && (n < hs.least || n > hs.most) {
		return fmt.Errorf("%s: 0 takes the default (%d); otherwise %d to %d", hs.key, hs.unset, hs.least, hs.most)
	}
	return nil
}

func validHeardHistory(h HeardHistoryConfig) error {
	for _, hs := range heardSettings {
		if err := hs.check(*hs.field(&h)); err != nil {
			return err
		}
	}
	return nil
}

func (h HeardHistoryConfig) limits() heardLimits {
	in := func(hs heardSetting) int {
		if n := *hs.field(&h); n != 0 {
			return n
		}
		return hs.unset
	}
	return heardLimits{
		Capacity:  in(heardSettings[0]),
		Retention: time.Duration(in(heardSettings[1])) * time.Second,
		TextBytes: in(heardSettings[2]),
		PageRows:  in(heardSettings[3]),
	}
}

const DefaultVoiceOpenWaitSeconds = 5

const maxVoiceOpenWaitSeconds = 60

func (c SpeechConfig) openWait() time.Duration {
	if c.OpenWaitSeconds > 0 {
		return time.Duration(c.OpenWaitSeconds) * time.Second
	}
	return DefaultVoiceOpenWaitSeconds * time.Second
}

func validVoiceOpenWait(n int) error {
	if n < 0 || n > maxVoiceOpenWaitSeconds {
		return fmt.Errorf("speech.open_wait_seconds: %d is outside 1 to %d (0 or absent takes the default, %d)", n, maxVoiceOpenWaitSeconds, DefaultVoiceOpenWaitSeconds)
	}
	return nil
}

type VoiceModeConfig struct {
	Listen   string `json:"listen,omitempty"`
	Speak    string `json:"speak,omitempty"`
	Revision uint64 `json:"revision,omitempty"`
}

type SpeakerPolicyConfig struct {
	Mode         string   `json:"mode,omitempty"`
	UIDs         []string `json:"uids,omitempty"`
	Unidentified string   `json:"unidentified,omitempty"`
	Revision     uint64   `json:"revision,omitempty"`
}

type STTConfig struct {
	Provider string `json:"provider"`

	Model string `json:"model"`

	Language string `json:"language"`

	APIKeyEnv string `json:"api_key_env"`

	TimeoutSeconds int `json:"timeout_seconds"`

	MonthlyMinutes int `json:"monthly_minutes,omitempty"`
}

type TTSConfig struct {
	Provider string `json:"provider"`

	Model string `json:"model"`
	Voice string `json:"voice"`

	APIKeyEnv string `json:"api_key_env"`

	TimeoutSeconds int `json:"timeout_seconds"`

	MonthlyCharacters int `json:"monthly_characters,omitempty"`
}

type DashboardFilesConfig struct {
	MaxBytes int64 `json:"max_bytes,omitempty"`

	Types []string `json:"types,omitzero"`

	MaxPerMessage int `json:"max_per_message,omitempty"`
}

type DashboardConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`

	OperatorASCII *bool `json:"operator_ascii,omitempty"`

	StopDrainSeconds int `json:"stop_drain_seconds,omitempty"`

	Files DashboardFilesConfig `json:"files,omitzero"`

	Record DashboardRecordConfig `json:"record,omitzero"`

	SentRequests DashboardSentRequestsConfig `json:"sent_requests,omitzero"`

	RequireToken bool `json:"require_token"`

	AccessToken string `json:"access_token,omitempty"`

	ApprovedScripts []dashboard.ApprovedScript `json:"approved_scripts,omitempty"`

	LegacyAuthTokenSHA256 string `json:"auth_token_sha256,omitempty"`

	TLS bool `json:"tls"`

	Origin string `json:"origin,omitempty"`

	SkipSignInWithValidToken *bool `json:"skip_signin_with_valid_token,omitempty"`

	Expert bool `json:"expert,omitempty"`

	NoticesOffDashboard bool `json:"notices_off_dashboard,omitempty"`

	AccessCodeSeconds int `json:"access_code_seconds,omitempty"`

	RefusedPauses int `json:"refused_pauses,omitempty"`

	UnsignedRequestsPerSecond int `json:"unsigned_requests_per_second,omitempty"`
	UnsignedRequestBurst      int `json:"unsigned_request_burst,omitempty"`

	CarriedIdleSeconds int `json:"carried_idle_seconds,omitempty"`

	Channels               *bool                  `json:"channels,omitempty"`
	DirectWaitMS           int                    `json:"direct_wait_ms,omitempty"`
	DirectReadyAllowanceMS int                    `json:"direct_ready_allowance_ms,omitempty"`
	ChannelSessions        int                    `json:"channel_sessions,omitempty"`
	ChannelQueueBytes      int                    `json:"channel_queue_bytes,omitempty"`
	ChannelStallMS         int                    `json:"channel_stall_ms,omitempty"`
	ICEServers             *[]dashboard.ICEServer `json:"ice_servers,omitempty"`
}

func (d DashboardConfig) channelSettings() dashboard.ChannelSettings {
	set := dashboard.ChannelSettings{
		Off: d.Channels != nil && !*d.Channels, WaitMS: d.DirectWaitMS, ReadyAllowanceMS: d.DirectReadyAllowanceMS,
		Sessions: d.ChannelSessions, QueueBytes: d.ChannelQueueBytes, StallMS: d.ChannelStallMS,
	}
	if d.ICEServers != nil {
		set.ICEServers, set.ICEServersNamed = append([]dashboard.ICEServer{}, *d.ICEServers...), true
	}
	return set
}

func (d DashboardConfig) inForce() []dashboard.SettingRead {
	held := func(n, unset, least, most int) int {
		switch {
		case n <= 0:
			return unset
		case n < least:
			return least
		case n > most:
			return most
		}
		return n
	}
	number := func(n int, unit string) string { return strings.TrimSpace(strconv.Itoa(n) + " " + unit) }
	set := d.channelSettings().InForce()
	servers := "what the relay names"
	if d.ICEServers != nil {
		var urls []string
		credentials := 0
		for _, sv := range *d.ICEServers {
			urls = append(urls, sv.URLs...)
			if sv.Credential != "" {
				credentials++
			}
		}
		servers = strconv.Itoa(len(*d.ICEServers)) + " named"
		if len(urls) > 0 {
			servers += ": " + strings.Join(urls, ", ")
		}
		if credentials > 0 {
			servers += " (" + strconv.Itoa(credentials) + " with a credential, read in config.json)"
		}
	}
	secret := func(v string) string {
		if v == "" {
			return "not set"
		}
		return "set (shown by the access token's own control on this card)"
	}
	return dashboard.ReadSettings("dashboard", d, map[string]string{
		"dashboard.access_token":                 secret(d.AccessToken),
		"dashboard.auth_token_sha256":            secret(d.LegacyAuthTokenSHA256),
		"dashboard.skip_signin_with_valid_token": dashboard.OnOff(d.skipSignInWithValidToken()),
		"dashboard.access_code_seconds":          number(accessCodeSeconds(d.AccessCodeSeconds), "seconds"),
		"dashboard.refused_pauses":               number(held(d.RefusedPauses, dashboard.DefaultRefusedPauses, dashboard.MinRefusedPauses, dashboard.MaxRefusedPauses), ""),
		"dashboard.unsigned_requests_per_second": number(held(d.UnsignedRequestsPerSecond, dashboard.DefaultUnsignedRequestsPerSecond, dashboard.MinUnsignedRequestsPerSecond, dashboard.MaxUnsignedRequestsPerSecond), ""),
		"dashboard.unsigned_request_burst":       number(held(d.UnsignedRequestBurst, dashboard.DefaultUnsignedRequestBurst, dashboard.MinUnsignedRequestBurst, dashboard.MaxUnsignedRequestBurst), ""),
		"dashboard.carried_idle_seconds":         number(held(d.CarriedIdleSeconds, dashboard.DefaultCarriedIdleSeconds, dashboard.MinCarriedIdleSeconds, dashboard.MaxCarriedIdleSeconds), "seconds"),
		"dashboard.channels":                     dashboard.OnOff(!set.Off),
		"dashboard.direct_wait_ms":               number(set.WaitMS, "ms"),
		"dashboard.direct_ready_allowance_ms":    number(set.ReadyAllowanceMS, "ms"),
		"dashboard.channel_sessions":             number(set.Sessions, ""),
		"dashboard.channel_queue_bytes":          number(set.QueueBytes, "bytes"),
		"dashboard.channel_stall_ms":             number(set.StallMS, "ms"),
		"dashboard.ice_servers":                  servers,
		"dashboard.record": "page_rows " + strconv.Itoa(held(d.Record.PageRows, dashboard.DefaultRecordPageRows, dashboard.MinRecordPageRows, dashboard.MaxRecordPageRows)) +
			", first_view_bytes " + strconv.Itoa(held(d.Record.FirstViewBytes, dashboard.DefaultRecordFirstViewBytes, dashboard.MinRecordFirstViewBytes, dashboard.MaxRecordFirstViewBytes)),
		"dashboard.sent_requests": func() string {
			kept, most := d.SentRequests.limits()
			return "kept " + strconv.Itoa(kept) + ", max_bytes " + strconv.Itoa(most)
		}(),
	})
}

func (d DashboardConfig) skipSignInWithValidToken() bool {
	return d.SkipSignInWithValidToken == nil || *d.SkipSignInWithValidToken
}

type ProjectsConfig struct {
	Root string `json:"root"`
}

type ToolsConfig struct {
	CWD string `json:"cwd"`

	Disabled []string `json:"disabled,omitempty"`

	ExtraRoots []string `json:"extra_roots,omitempty"`

	SandboxOff bool `json:"sandbox_off,omitempty"`

	LocalHosts []string `json:"local_hosts,omitempty"`

	ShellTimeoutSeconds    int `json:"shell_timeout_seconds"`
	WebFetchTimeoutSeconds int `json:"webfetch_timeout_seconds,omitempty"`
}

type PluginsConfig struct {
	Autoload string `json:"autoload"`

	CertifierRoot string `json:"certifier_root,omitempty"`
	ReviewerRoot  string `json:"reviewer_root,omitempty"`
	PlatformRoot  string `json:"platform_root,omitempty"`

	CatalogDir string `json:"catalog_dir,omitempty"`

	CatalogURL string `json:"catalog_url,omitempty"`

	Grants map[string]broker.Grant `json:"grants,omitempty"`

	AuthProfiles map[string]broker.AuthProfile `json:"auth_profiles,omitempty"`

	WorkerBinary string `json:"worker_binary,omitempty"`

	Runtime PluginRuntimeConfig `json:"runtime"`

	Resources map[string]PluginResources `json:"resources,omitempty"`

	Settings map[string]map[string]any `json:"settings,omitempty"`

	DevSection *DevSectionConfig `json:"dev_section,omitempty"`
}

type DevSectionConfig struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type PluginResources struct {
	MemoryMaxBytes uint64 `json:"memory_max_bytes,omitempty"`

	StartupTimeoutMS *int64 `json:"startup_timeout_ms,omitempty"`

	StreamMaxBytes int `json:"stream_max_bytes,omitempty"`

	FilesMaxBytes int `json:"files_max_bytes,omitempty"`
}

type PromptConfig struct {
	MaxTokens          int `json:"max_tokens"`
	MaxToolResultChars int `json:"max_tool_result_chars,omitempty"`

	PulseIntervalSeconds int `json:"pulse_interval_seconds,omitempty"`

	RecentTurns int `json:"recent_turns"`

	Ring3MaxChars int `json:"ring3_max_chars,omitempty"`

	SurfacingMaxChars int `json:"surfacing_max_chars,omitempty"`

	SelfModelMaxTokens int `json:"self_model_max_tokens,omitempty"`

	DreamConversationMaxChars int `json:"dream_conversation_max_chars,omitempty"`

	TensionsMaxChars int `json:"tensions_max_chars,omitempty"`

	BriefMaxChars int `json:"brief_max_chars,omitempty"`

	AlarmFirings int `json:"alarm_firings,omitempty"`

	ExcerptChars int `json:"excerpt_chars,omitempty"`

	Ring3RecentExperiences int `json:"ring3_recent_experiences,omitempty"`
}

type CognitionConfig struct {
	ConsolidateBatch int `json:"consolidate_batch,omitempty"`

	ConsolidateMaxOps int `json:"consolidate_max_ops,omitempty"`

	ConsolidateSpacingSeconds int `json:"consolidate_spacing_seconds,omitempty"`

	MorningBriefAt string `json:"morning_brief_at,omitempty"`

	SelfModelEveryMinutes      int `json:"self_model_every_minutes,omitempty"`
	IdentityReviewEveryMinutes int `json:"identity_review_every_minutes,omitempty"`

	SelfModelEvidenceRows int `json:"self_model_evidence_rows,omitempty"`
	SelfModelSpokenRows   int `json:"self_model_spoken_rows,omitempty"`
}

type AgencyConfig struct {
	MaxToolRounds        int `json:"max_tool_rounds"`
	MaxSubagentDepth     int `json:"max_subagent_depth"`
	MaxParallelSubagents int `json:"max_parallel_subagents"`
	SubagentWallSeconds  int `json:"subagent_wall_seconds"`

	SubagentWallSecondsLocal int `json:"subagent_wall_seconds_local"`
	SubagentMaxMints         int `json:"subagent_max_mints"`

	SubagentMaxToolRounds int `json:"subagent_max_tool_rounds"`

	SubagentMaxToolCalls int `json:"subagent_max_tool_calls"`

	SubagentMaxLegs int `json:"subagent_max_legs"`

	SubagentContinuation *bool `json:"subagent_continuation,omitempty"`

	SpawnQueue    *bool `json:"spawn_queue,omitempty"`
	RhythmSeconds int   `json:"rhythm_seconds"`

	OutcomeWindow int `json:"outcome_window,omitempty"`

	QueueWorkers int `json:"queue_workers"`

	HarvestWake *bool `json:"harvest_wake,omitempty"`

	YieldAnswer *bool `json:"yield_answer,omitempty"`

	TurnContinuation *bool `json:"turn_continuation,omitempty"`

	Roles map[string]RoleRoute `json:"roles,omitempty"`

	PreferLocalForRoles bool `json:"prefer_local_for_roles"`

	TurnTokenBudget int `json:"turn_token_budget"`

	ToolSchemaRefusals int `json:"tool_schema_refusals,omitempty"`

	TurnCheckpointCalls       int `json:"turn_checkpoint_calls,omitempty"`
	TurnCheckpointGraceRounds int `json:"turn_checkpoint_grace_rounds,omitempty"`
	MaxTurnContinuations      int `json:"max_turn_continuations,omitempty"`

	TurnCheckpointSlack int `json:"turn_checkpoint_slack,omitempty"`

	ContinuationWallSeconds int `json:"continuation_wall_seconds,omitempty"`
	AlarmWallSeconds        int `json:"alarm_wall_seconds,omitempty"`
	HarvestWallSeconds      int `json:"harvest_wall_seconds,omitempty"`
}

func (ag AgencyConfig) checkpoint() conversation.Checkpoint {
	return conversation.Checkpoint{Calls: ag.TurnCheckpointCalls, GraceRounds: ag.TurnCheckpointGraceRounds, Slack: ag.TurnCheckpointSlack}
}

const maxToolSchemaRefusals = 32

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

type GenesisConfig struct {
	ServerURL    string `json:"server_url"`
	FirewallURL  string `json:"firewall_url"`
	BootstrapURL string `json:"bootstrap_url"`

	FetchAttempts  int `json:"fetch_attempts,omitempty"`
	FetchTimeoutMs int `json:"fetch_timeout_ms,omitempty"`
}

const (
	genesisFetchAttemptsMax  = 5
	genesisFetchTimeoutMinMs = 1000
	genesisFetchTimeoutMaxMs = 60000
)

func validGenesisPatience(g GenesisConfig) error {
	if g.FetchAttempts < 0 || g.FetchAttempts > genesisFetchAttemptsMax {
		return fmt.Errorf("genesis.fetch_attempts must be zero (take the default, %d) or between 1 and %d", genesis.DefaultFetchAttempts, genesisFetchAttemptsMax)
	}
	if g.FetchTimeoutMs != 0 && (g.FetchTimeoutMs < genesisFetchTimeoutMinMs || g.FetchTimeoutMs > genesisFetchTimeoutMaxMs) {
		return fmt.Errorf("genesis.fetch_timeout_ms must be zero (take the default, %d) or between %d and %d", genesis.DefaultFetchTimeout.Milliseconds(), genesisFetchTimeoutMinMs, genesisFetchTimeoutMaxMs)
	}
	return nil
}

func (g GenesisConfig) client(life context.Context) *genesis.GenesisClient {
	return genesis.NewClient(g.ServerURL, g.FirewallURL, g.BootstrapURL).
		WithPatience(g.FetchAttempts, time.Duration(g.FetchTimeoutMs)*time.Millisecond).
		WithRefusalNotice(foundingRefusalNotice).
		WithLifetime(life)
}

type UpdatesConfig struct {
	Automatic bool `json:"automatic"`

	Repo string `json:"repo"`

	HandoverWaitSeconds int `json:"handover_wait_seconds,omitempty"`
}

type MachineConfig struct {
	Dir string `json:"dir,omitempty"`
}

type TapSetting struct {
	Until string `json:"until,omitempty"`
}

type LogsConfig struct {
	Dir          string `json:"dir"`
	MaxBackups   int    `json:"max_backups"`
	CompressDays int    `json:"compress_days"`

	MaxDays int `json:"max_days,omitempty"`

	Detail map[string]string `json:"detail,omitempty"`

	Taps map[string]TapSetting `json:"taps,omitempty"`

	Group map[string][]string `json:"group,omitempty"`

	Level string `json:"level,omitempty"`

	present bool `json:"-"`
}

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

type Contact struct {
	Name    string `json:"name"`
	Channel string `json:"channel"`
	Address string `json:"address"`

	Wake bool `json:"wake"`

	Operator bool `json:"operator,omitempty"`
}

type Config struct {
	Identity    IdentityConfig               `json:"identity"`
	LLM         LLMConfig                    `json:"llm"`
	Dashboard   DashboardConfig              `json:"dashboard"`
	Speech      SpeechConfig                 `json:"speech"`
	Tools       ToolsConfig                  `json:"tools"`
	Plugins     PluginsConfig                `json:"plugins"`
	Projects    ProjectsConfig               `json:"projects"`
	Prompt      PromptConfig                 `json:"prompt"`
	Agency      AgencyConfig                 `json:"agency"`
	Cognition   CognitionConfig              `json:"cognition,omitzero"`
	Timezone    string                       `json:"timezone"`
	Witness     WitnessConfig                `json:"witness_server"`
	Certificate publicname.CertificateConfig `json:"certificate"`
	Genesis     GenesisConfig                `json:"genesis"`
	Updates     UpdatesConfig                `json:"updates"`
	Machine     MachineConfig                `json:"machine,omitempty"`
	Logs        LogsConfig                   `json:"logs"`
	Maintenance continuity.MaintenanceConfig `json:"maintenance"`
	Memory      MemoryConfig                 `json:"memory"`
	Contacts    []Contact                    `json:"contacts"`

	contactsPresent bool

	cognitionHeld map[string]string

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

type PluginRuntimeConfig struct {
	MaxInstalledBytes  int64 `json:"max_installed_bytes,omitempty"`
	MaxFiles           int   `json:"max_files"`
	MaxFileBytes       int64 `json:"max_file_bytes,omitempty"`
	MaxCompressedBytes int64 `json:"max_compressed_bytes,omitempty"`
	MaxDepth           int   `json:"max_depth"`
	RootsKept          int   `json:"roots_kept"`

	MaxStartupMS int64 `json:"max_startup_ms,omitempty"`

	AdmissionMemoryReserveBytes int64 `json:"admission_memory_reserve_bytes,omitempty"`

	SlowStorageCallMs          int   `json:"slow_storage_call_ms,omitempty"`
	AdmissionMemoryBudgetBytes int64 `json:"admission_memory_budget_bytes,omitempty"`
	MaxConcurrentStarts        int   `json:"max_concurrent_starts,omitempty"`

	HardwareQueryMs int `json:"hardware_query_ms,omitempty"`
}

var r104ByteDefaults = struct{ installed, file, compressed int64 }{1 << 30, 512 << 20, 512 << 20}

func retiredByteDefault(key string, n int64) bool {
	switch key {
	case "plugins.runtime.max_installed_bytes":
		return n == r104ByteDefaults.installed
	case "plugins.runtime.max_file_bytes":
		return n == r104ByteDefaults.file
	case "plugins.runtime.max_compressed_bytes":
		return n == r104ByteDefaults.compressed
	}
	return false
}

func (c PluginRuntimeConfig) TreeLimits() packagefmt.TreeLimits {
	return packagefmt.TreeLimits{
		MaxInstalledBytes: c.MaxInstalledBytes, MaxFiles: c.MaxFiles, MaxFileBytes: c.MaxFileBytes,
		MaxCompressedBytes: c.MaxCompressedBytes, MaxDepth: c.MaxDepth,
	}
}

type MemoryConfig struct {
	Salience salience.SalienceWeights `json:"salience"`

	MeaningSource string `json:"meaning_source,omitempty"`

	BackfillSeconds int `json:"backfill_seconds,omitempty"`

	BackfillRows int `json:"backfill_rows,omitempty"`

	BackfillLocalSeconds int `json:"backfill_local_seconds,omitempty"`

	EmbedCueSeconds    int `json:"embed_cue_seconds,omitempty"`
	EmbedMemorySeconds int `json:"embed_memory_seconds,omitempty"`

	SourceCheckRetrySeconds int `json:"source_check_retry_seconds,omitempty"`

	ReplyHoldSeconds int `json:"reply_hold_seconds,omitempty"`

	ConversationVectorWindow int `json:"conversation_vector_window,omitempty"`
}

func applyDefaults(cfg *Config) {
	if cfg.Memory.Salience.Version == "" {
		cfg.Memory.Salience = salience.DefaultSalience()
	}

	d := packagefmt.DefaultTreeLimits
	r := &cfg.Plugins.Runtime
	if r.MaxFiles <= 0 {
		r.MaxFiles = d.MaxFiles
	}
	if r.MaxDepth <= 0 {
		r.MaxDepth = d.MaxDepth
	}
	if r.RootsKept <= 0 {
		r.RootsKept = 2
	}
	if r.MaxStartupMS <= 0 {
		r.MaxStartupMS = int64(pluginhost.DefaultStartupCeiling / time.Millisecond)
	}

	if cfg.Identity.LedgerPath == "" {
		cfg.Identity.LedgerPath = filepath.Join("data", "ledger.jsonl")
	}
	if cfg.Identity.DBPath == "" {
		cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	}
	if cfg.Identity.KeyPath == "" {
		cfg.Identity.KeyPath = filepath.Join("data", "identity.sec")
	}

	if cfg.LLM.APIKeyEnv == "" {
		cfg.LLM.APIKeyEnv = "OPENAI_API_KEY"
	}

	if !cfg.Logs.present && cfg.Logs.Dir == "" {
		cfg.Logs.Dir = "log"
	}
	if cfg.LLM.ProbeTimeoutSeconds == 0 {
		cfg.LLM.ProbeTimeoutSeconds = 45
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
	if cfg.Agency.MaxToolRounds == 0 {

		cfg.Agency.MaxToolRounds = 30
	}
	if cfg.Agency.TurnTokenBudget == 0 {
		cfg.Agency.TurnTokenBudget = defaultTurnTokenBudget
	}
	if cfg.Agency.SubagentMaxToolRounds == 0 {

		cfg.Agency.SubagentMaxToolRounds = 30
	}

	if cfg.Agency.SubagentMaxToolCalls == 0 {
		cfg.Agency.SubagentMaxToolCalls = 64
	}
	if cfg.Agency.SubagentMaxLegs == 0 {
		cfg.Agency.SubagentMaxLegs = 4
	}

	if cfg.Agency.MaxSubagentDepth == 0 {
		cfg.Agency.MaxSubagentDepth = 3
	}
	if cfg.Agency.QueueWorkers == 0 {

		cfg.Agency.QueueWorkers = -1
	}
	if cfg.Agency.MaxParallelSubagents == 0 {
		cfg.Agency.MaxParallelSubagents = 3
	}
	if cfg.Agency.QueueWorkers == -1 {
		cfg.Agency.QueueWorkers = cfg.Agency.MaxParallelSubagents
	}
	if cfg.Agency.SubagentWallSeconds == 0 {

		cfg.Agency.SubagentWallSeconds = 1200
	}
	if cfg.Agency.SubagentWallSecondsLocal == 0 {
		cfg.Agency.SubagentWallSecondsLocal = 1800
	}
	if cfg.Agency.SubagentMaxMints == 0 {
		cfg.Agency.SubagentMaxMints = 20
	}
	if cfg.Agency.RhythmSeconds == 0 {
		cfg.Agency.RhythmSeconds = defaultRhythmSeconds
	}
	if cfg.Agency.OutcomeWindow == 0 {
		cfg.Agency.OutcomeWindow = cognitive.DefaultOutcomeWindow
	}

	if cfg.Tools.CWD == "" {
		cfg.Tools.CWD = "."
	}
	if cfg.Plugins.Autoload == "" {
		cfg.Plugins.Autoload = "T1"
	}
	if cfg.Dashboard.Port == 0 {
		cfg.Dashboard.Port = 8080
	}
	applyRuled(cfg)
}

func applyRuled(cfg *Config) (said []string) {

	r := &cfg.Plugins.Runtime
	for _, c := range []struct {
		key string
		v   *int64
	}{{"plugins.runtime.max_installed_bytes", &r.MaxInstalledBytes}, {"plugins.runtime.max_file_bytes", &r.MaxFileBytes}, {"plugins.runtime.max_compressed_bytes", &r.MaxCompressedBytes}} {
		if retiredByteDefault(c.key, *c.v) {
			said = append(said, fmt.Sprintf("%s %d is the former default, which the loader wrote into this file: read as not set, so free disk decides which component set this machine runs; the next save leaves it out", c.key, *c.v))
			*c.v = 0
		}
		if *c.v < 0 {
			*c.v = 0
		}
	}

	if cfg.Agency.SubagentMaxToolRounds > cfg.Agency.MaxToolRounds {
		said = append(said, fmt.Sprintf("agency.subagent_max_tool_rounds %d is above agency.max_tool_rounds %d: a child is held to %d rounds",
			cfg.Agency.SubagentMaxToolRounds, cfg.Agency.MaxToolRounds, cfg.Agency.MaxToolRounds))
		cfg.Agency.SubagentMaxToolRounds = cfg.Agency.MaxToolRounds
	}

	if strings.TrimSpace(cfg.Dashboard.Host) == "" {
		cfg.Dashboard.Host = "127.0.0.1"
	}

	if !dashboard.IsLoopback(cfg.Dashboard.Host) && !cfg.Dashboard.TLS {
		said = append(said, fmt.Sprintf("dashboard.host %s is an address other machines can reach: the dashboard serves TLS there (dashboard.tls was false)", cfg.Dashboard.Host))
		cfg.Dashboard.TLS = true
	}
	return said
}

func agencyOn(v *bool) bool { return v == nil || *v }
