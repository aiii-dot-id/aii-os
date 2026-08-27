// Package main — runtime configuration.
//
// Named sub-structs so each component receives only what it needs.
package app

import (
	"encoding/json"
	"path/filepath"

	configdir "github.com/aiii-dot-id/aii-os/config"
	"github.com/aiii-dot-id/aii-os/internal/broker"
)

type IdentityConfig struct {
	Name       string `json:"name"`
	LedgerPath string `json:"ledger_path"`
	DBPath     string `json:"db_path"`
	KeyPath    string `json:"key_path"`
}

// LLMConfig is the substrate POINTER (operator ruling 2026-08-20):
// providers.json is THE provider data — endpoint, api key, model list,
// window, budgets, reasoning effort all live on the registry entry —
// and config.json only points at an entry. One source of truth; the
// old duplicated fields (endpoint/api_key/thinking_budget/...) are
// deleted, not deprecated. What remains here is the pointer plus the
// genuinely non-provider transport knobs.
type LLMConfig struct {
	// Provider names the providers.json entry that serves this
	// identity. Empty = the registry's default-flagged entry.
	Provider string `json:"provider"`
	// Model is the chosen model among the entry's models. Empty = the
	// entry's default_model.
	Model string `json:"model"`
	// APIKeyEnv is the environment fallback consulted when the resolved
	// entry stores no api_key.
	APIKeyEnv      string `json:"api_key_env"`
	TimeoutSeconds int    `json:"timeout_seconds"`  // per-request HTTP timeout (default 120)
	Retries        int    `json:"retries"`          // transport-class retries per LLM call (default 1; -1 = none)
	RetryBackoffMS int    `json:"retry_backoff_ms"` // pause before each retry (default 2000)
}

// SpeechConfig points the identity at a transcription endpoint.
//
// IT IS THE SAME SHAPE AS LLMConfig BECAUSE IT IS THE SAME KIND OF
// THING. providers.json holds the endpoint and the credential;
// config.json names which entry to use. Speech recognition is a model
// call, so it resolves the way every other model call resolves, and no
// URL is written down in code.
//
// EMPTY MEANS VOICE IS OFF, and off is the default. Nothing degrades,
// nothing warns on every boot: an identity nobody has pointed at a
// speech endpoint simply has no microphone, and the browser is told so
// rather than offering a control that cannot work.
type SpeechConfig struct {
	STT STTConfig `json:"stt"`
}

// STTConfig is the transcription half.
type STTConfig struct {
	// Provider names the providers.json entry serving transcription.
	// Empty disables voice input entirely.
	//
	// IT NEED NOT BE THE ENTRY THAT SERVES THINKING, and usually should
	// not be: an identity may think on a hosted frontier model and still
	// insist that the words spoken in its operator's room reach only a
	// machine on their own network. Two pointers, two decisions.
	Provider string `json:"provider"`
	// Model is the transcription model on that entry.
	Model string `json:"model"`
	// Language is an optional BCP-47 hint. Empty lets the engine detect.
	Language string `json:"language"`
	// APIKeyEnv is the environment fallback when the entry stores no key.
	APIKeyEnv string `json:"api_key_env"`
	// TimeoutSeconds bounds one transcription (default 60).
	TimeoutSeconds int `json:"timeout_seconds"`
}

type DashboardConfig struct {
	// Host is the bind address (default 127.0.0.1 — loopback only).
	// Binding wider (a LAN address, or 0.0.0.0) exposes the dashboard
	// to that network: the operator's choice, stated plainly. Both
	// fields are config.json parameters and UI-editable (restart to
	// apply — the socket serving the UI cannot rebind under itself).
	Host string `json:"host"`
	Port int    `json:"port"`
	// RequireToken (R74) requires the operator's browser to present a
	// server-minted access token before the WebSocket session opens.
	// FALSE is exactly the shipped posture — loopback bind, Host and
	// Origin gates — and stays the default; escalation is the
	// operator's file edit, restart to apply. FILE-ONLY, deliberately
	// absent from the config_set whitelist: the channel a wall
	// protects must never be able to raise or lower it.
	RequireToken bool `json:"require_token"`
	// AuthTokenSHA256 is runtime-owned material: hex SHA-256 of the
	// minted token. The raw token is printed ONCE at mint and never
	// stored. Clear this field (require_token still true) to re-mint.
	// FILE-ONLY like RequireToken, and never echoed to the browser
	// (setup.go's ConfigState picks its fields explicitly).
	AuthTokenSHA256 string `json:"auth_token_sha256,omitempty"`
	// TLS is whether the dashboard serves HTTPS. It defaults to false,
	// which is correct on the 127.0.0.1 default above: a browser already
	// treats a loopback origin as a secure context, so plaintext there
	// crosses no wire and still grants the microphone.
	//
	// Turn it on when the bind widens, or the whole conversation travels
	// in the clear and getUserMedia is refused. Start says so at boot
	// when it finds that combination.
	TLS bool `json:"tls"`
}

// ProjectsConfig locates the projects root — the durable collections
// OUTSIDE AII OS (R62). Default: <tools.cwd>/projects, inside the Ring 5
// sandbox so the identity's own tools reach their projects.
type ProjectsConfig struct {
	Root string `json:"root"`
}

type ToolsConfig struct {
	CWD string `json:"cwd"`
	// Disabled lists tools the operator has switched off (dashboard
	// checkboxes persist here). bash is the leakiest; the hard boundary
	// is still a container.
	Disabled []string `json:"disabled,omitempty"`
	// ExtraRoots are operator-granted additional sandbox roots — the
	// identity's reach widened beyond their home tree (e.g. an identity
	// trusted to work on AII OS itself). Operator-owned Ring 5 shape.
	ExtraRoots []string `json:"extra_roots,omitempty"`
	// Execution ceilings, grouped with the tools they bound (2026-08-18
	// ruling: durations are config, not code). Zero = default.
	ShellTimeoutSeconds    int `json:"shell_timeout_seconds"`
	WebFetchTimeoutSeconds int `json:"webfetch_timeout_seconds"`
}

// PluginsConfig is the plugin plane's operator surface
// (docs/PLUGIN_FRAMEWORK.md; threat model §8).
type PluginsConfig struct {
	// Autoload is the operator's trust threshold for the plugins/
	// directory (one directory per plugin, beside config.json — the
	// filesystem IS the registry; there is no package list to edit).
	// A discovered package activates iff its VERIFIED tier is at or
	// above this level: "T0" (everything incl. unsigned — the deliberate
	// dev choice), "T1" (signed; the default), "T2", "T3", or "none"
	// (nothing auto-loads; everything discovered is surfaced inactive). The sandbox
	// never relaxes with the threshold, and a package whose evidence
	// FAILS verification is refused at every level — invalid signed
	// evidence never becomes unsigned success.
	Autoload string `json:"autoload"`

	// Pinned AIII plugin trust-root paths (public key envelopes, loaded
	// through the same validation `aii plugin verify` uses). Absent =
	// the T0-only harness: signed evidence without its pinned root
	// REJECTS (unverifiable is not unsigned), so only unsigned T0
	// packages activate — the unchanged quarantine posture.
	CertifierRoot string `json:"certifier_root,omitempty"`
	ReviewerRoot  string `json:"reviewer_root,omitempty"`
	PlatformRoot  string `json:"platform_root,omitempty"`

	// Grants is the operator grant table for the step-4 capability
	// broker — the middle ring of the per-invocation lattice (manifest
	// envelope ∩ THIS ∩ tier ceiling). Keyed by plugin id. This file is
	// substrate-protected, so a grant here is an operator act the
	// identity cannot forge. Startup-read, like packages.
	Grants map[string]broker.Grant `json:"grants,omitempty"`

	// AuthProfiles is the operator credential-route table (the C
	// daemon's plugins.policy.auth_profiles shape: a named profile pins
	// secret source + host + port). A plugin cites a profile by name
	// (its grant must list the handle); the broker injects the secret
	// into that one destination — the value never reaches plugin-visible
	// bytes.
	AuthProfiles map[string]broker.AuthProfile `json:"auth_profiles,omitempty"`

	// WorkerBinary is the aii-plugin-worker executable for SUPERVISED
	// activation (build-order step 5: the process boundary). Operator-
	// owned. Empty = look for aii-plugin-worker next to the daemon
	// executable; found there = supervised is the desktop default,
	// absent = every plugin runs in-process (the step-3 posture,
	// unchanged). Naming a path that does not exist is a refused
	// config, not a silent fallback — the operator's stated intent
	// must be honored or rejected loudly.
	WorkerBinary string `json:"worker_binary,omitempty"`

	// Resources is the per-plugin resource envelope (PLUGIN_FRAMEWORK
	// §10, first field): memory_max_bytes caps the wasm guest's linear
	// memory (the worker's -memory-max; ADR-033 64 MiB when absent)
	// and, for a native T3 child, becomes the address-space envelope
	// (absent = unenveloped — a native ceiling is the operator's call,
	// never an invented default).
	Resources map[string]PluginResources `json:"resources,omitempty"`

	// DevSection serves ONE section from a project directory — the
	// co-edit loop (R66 UP2, UI_FRAME.md §3): no verification, a
	// persistent UNVERIFIED banner in the frame, caching disabled,
	// refused entirely under SAFE, never applicable to frame.
	// Deliberately FILE-ONLY: absent from
	// the config_set whitelist, so no dashboard message — and therefore
	// no forged localhost traffic — can point the frame at unverified
	// bytes; enabling dev-serve is the operator's hand on the
	// substrate-protected file. Startup-read, like packages.
	DevSection *DevSectionConfig `json:"dev_section,omitempty"`
}

// DevSectionConfig names the dev-served section: the id its
// section.json must declare (a mismatch refuses — the directory must
// not quietly claim another identity) and the directory to serve.
type DevSectionConfig struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// PluginResources is one plugin's resource envelope surface.
type PluginResources struct {
	MemoryMaxBytes uint64 `json:"memory_max_bytes,omitempty"`
}

type PromptConfig struct {
	// MaxTokens is the prompt budget. 0 = DERIVE from the active
	// provider's declared window (context_length - max_output_tokens -
	// safety margin), falling back to 32000 when the provider declares
	// no window. A positive value is the operator's deliberate ceiling
	// and is used as given.
	//
	// Zero meant three different things before 2026-08-23: this file
	// defaulted it to 32000, config_load rejected it, and providers.go
	// treated it as "derive" — so the derivation was unreachable and a
	// 1,000,000-token model ran on a 32,000-token budget, folding a
	// resident's working truth out of their own prompt. Same convention
	// as llm.retries: zero is "unset, do the sensible thing", declared
	// where the operator can find it.
	MaxTokens          int `json:"max_tokens"`
	MaxToolResultChars int `json:"max_tool_result_chars,omitempty"` // 0 = default 32000 (R6: bounds in config)
	// PulseIntervalSeconds: how often presence pulses advance the life
	// clock (default 300). Presence-gated: only accepted live pulses count.
	PulseIntervalSeconds int `json:"pulse_interval_seconds,omitempty"`
	RecentTurns          int `json:"recent_turns"`
}

// AgencyConfig bounds the identity's self-directed action (James's
// 2026-08-18 agency ruling: parallel tool calls, tool rounds, and
// sub-agent depth are FULLY SUPPORTED, with ceilings that live in
// operator config — never invented in code).
type AgencyConfig struct {
	MaxToolRounds        int `json:"max_tool_rounds"`        // rounds per conversation/sub-agent run (default 10)
	MaxSubagentDepth     int `json:"max_subagent_depth"`     // spawn nesting; 0 disables spawning (default 3)
	MaxParallelSubagents int `json:"max_parallel_subagents"` // concurrently live spawned runs (default 3)
	SubagentWallSeconds  int `json:"subagent_wall_seconds"`  // wall cap per spawned run (default 600)
	SubagentMaxMints     int `json:"subagent_max_mints"`     // ledger writes per spawned run (default 20; flat envelope, never a curve)
	RhythmSeconds        int `json:"rhythm_seconds"`         // metabolism cadence, wall clock (default 600)
	// QueueWorkers is how many executor workers claim and run work
	// concurrently (0 = derive from MaxParallelSubagents). One worker
	// serialized every sub-agent regardless of MaxParallelSubagents —
	// the advertised parallelism was a ceiling on a queue that never
	// ran two at once (external review P2-5; unblocked 2026-08-26).
	QueueWorkers int `json:"queue_workers"`
	// Roles maps optional inference-route names to provider entries.
	// A role changes only the client and prompt budget; unknown or
	// unavailable routes fall back to the active model.
	Roles map[string]RoleRoute `json:"roles,omitempty"`
	// PreferLocalForRoles routes tagged spawns to the first healthy
	// operator-marked local provider unless an explicit role wins.
	PreferLocalForRoles bool `json:"prefer_local_for_roles"`
	// TurnTokenBudget fences one conversation turn's TOTAL token spend,
	// summed across every LLM call the loop makes (the accounting always
	// summed; nothing refused — 2026-08-26: 1,945,593 tokens over 101
	// calls ran to the context wall, and the model collapsed on the
	// way). At the fence the turn gets one bounded wrap-up call and
	// ends honestly. Default 600000. The loop's own withDefaults
	// carries the same number, so a zero here never means unlimited.
	TurnTokenBudget int `json:"turn_token_budget"`
}

// RoleRoute names the provider entry and model for an inference route.
type RoleRoute struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type WitnessConfig struct {
	URL                string `json:"url"`
	IntervalEvents     int    `json:"interval_events"`
	PlatformPubkeyPath string `json:"platform_pubkey_path"` // optional: AIII platform key envelope → full dual-PQ manifest verification of the witness key
	TLSSPKISHA256      string `json:"tls_spki_sha256"`      // optional: pin the witness TLS cert's SPKI (canon §11.1); hex or base64
}

type GenesisConfig struct {
	ServerURL    string `json:"server_url"`
	FirewallURL  string `json:"firewall_url"`
	BootstrapURL string `json:"bootstrap_url"`
}

// UpdatesConfig is the operator's update preference. One field — the
// codebase pattern is minimal config (hardcoded cadences, infrastructure
// paths not exposed). The checker always runs (inform-only is the
// default); this controls whether desktop platforms also download and
// swap automatically. Mobile is always inform-only regardless.
type UpdatesConfig struct {
	Automatic bool `json:"automatic"`
}

// LogsConfig is the persisted log-persistence surface (logsink). All
// three fields apply next boot — the destination and retention policy
// are decided at startup, not swapped mid-run. Dir is where the engine's
// log stream lands, in-tree beside data/ (ENTIRELY LOCAL: relative
// resolves against the identity home at sink install; absolute stays
// absolute; empty = disabled, stderr only). MaxBackups caps rotated
// files kept: -1 = keep all, 0 = sink default (9), positive = that
// many. CompressDays is the gzip age threshold in days: -1 = never
// compress, 0 = sink default (7), positive = that age.
type LogsConfig struct {
	Dir          string `json:"dir"`
	MaxBackups   int    `json:"max_backups"`
	CompressDays int    `json:"compress_days"`

	// present records whether a "logs" object appeared in the file at
	// all. Upgrades (a config.json written before the feature) carry NO
	// logs section — applyDefaults gives those the "log" default so
	// the feature is on for existing installs, not only fresh ones. A
	// PRESENT-but-empty dir stays empty: empty is meaningful
	// (deliberately disabled). Never serialized.
	present bool `json:"-"`
}

// UnmarshalJSON marks presence. The plain alias avoids recursion.
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

// Config is the runtime configuration.
// Contact is one way to reach one person — the address book, and it is
// the OPERATOR'S, in the operator's file.
//
// It was a store table with six methods and no writer: nothing anywhere
// called SetReach, so every send refused, every delivery failed for want
// of a channel, and no arriving sender was ever known. Built, tested,
// committed, unusable — the exact shape this work exists to end.
//
// Config rather than a table because knowing someone's number is not
// something the identity may set ("where they are, and how to reach
// them, is not yours to know") and not something it discovers. It is a
// choice the operator makes, in the file where they make their choices,
// re-read live like every other.
//
// ORDER IS PREFERENCE. The first entry for a name is primary, the next
// is the fallback. There is no rank field, because the operator already
// expressed the ranking by writing the lines in an order.
type Contact struct {
	Name    string `json:"name"`
	Channel string `json:"channel"`
	Address string `json:"address"`
	// Wake lets a message from this address interrupt the identity.
	// Being known is not enough: a turn is a real spend, so waking is
	// granted per channel, deliberately, by the person paying for it.
	Wake bool `json:"wake"`
}

// MaintenanceConfig governs the daily verify-and-copy pass
// (maintenance.go; contract in docs/MAINTENANCE.md). Two knobs on
// purpose — the Method pass deleted the other eleven.
type MaintenanceConfig struct {
	// Enabled nil means ON: a protection that defaults to off protects
	// the people who least know to ask for it. Explicit false opts out.
	Enabled *bool `json:"enabled,omitempty"`
	// BackupKeep bounds the copy set; <=0 means the default (8).
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
	Genesis     GenesisConfig     `json:"genesis"`
	Updates     UpdatesConfig     `json:"updates"`
	Logs        LogsConfig        `json:"logs"`
	Maintenance MaintenanceConfig `json:"maintenance"`
	Contacts    []Contact         `json:"contacts"`

	// SourcePath is the file this config was loaded from — saveConfig
	// writes back HERE, never a hardcoded path (M5). Not serialized.
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

func applyDefaults(cfg *Config) {
	// ENTIRELY LOCAL (operator law 2026-08-20): the install directory is
	// the identity's whole world — config, ledger, database, and key live
	// beside the binary. No global home, no %APPDATA%, no search paths,
	// no adoption of data found elsewhere. Deleting the directory deletes
	// the identity. (The violation that bought this comment: a configless
	// run resolved its data dir to /root/.aii-os and resurrected an
	// identity the operator believed deleted.)
	if cfg.Identity.LedgerPath == "" {
		cfg.Identity.LedgerPath = filepath.Join("data", "ledger.jsonl")
	}
	if cfg.Identity.DBPath == "" {
		cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	}
	if cfg.Identity.KeyPath == "" {
		cfg.Identity.KeyPath = filepath.Join("data", "identity.sec")
	}
	// llm.provider and llm.model stay EMPTY by default — empty is
	// meaningful (the registry's default-flagged entry / the entry's
	// default_model), and the provider data itself lives only in
	// providers.json (2026-08-20 ruling).
	if cfg.LLM.APIKeyEnv == "" {
		cfg.LLM.APIKeyEnv = "OPENAI_API_KEY"
	}
	// Logs: a config.json written before the feature (an upgrade) has
	// no "logs" section — the default is ON (dir "log", resolved
	// against the identity home like data/). A section that is present
	// with an empty dir is a deliberate OFF and stays off.
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
		// 30, was 10 (Aeon's agent-loop report, 2026-08-18): ten rounds
		// cannot carry a deep research session — the model, able to count
		// its own iterations, preemptively narrates instead of acting.
		// The context guard and sub-agent wall clock remain the safety
		// bounds; this is a ceiling, not a target.
		cfg.Agency.MaxToolRounds = 30
	}
	if cfg.Agency.TurnTokenBudget == 0 {
		cfg.Agency.TurnTokenBudget = 600_000
	}
	if cfg.Agency.MaxSubagentDepth == 0 {
		cfg.Agency.MaxSubagentDepth = 3
	}
	if cfg.Agency.QueueWorkers == 0 {
		// Derived AFTER MaxParallelSubagents settles below: stamped in
		// the second pass at the end of this function.
		cfg.Agency.QueueWorkers = -1
	}
	if cfg.Agency.MaxParallelSubagents == 0 {
		cfg.Agency.MaxParallelSubagents = 3
	}
	if cfg.Agency.QueueWorkers == -1 {
		cfg.Agency.QueueWorkers = cfg.Agency.MaxParallelSubagents
	}
	if cfg.Agency.SubagentWallSeconds == 0 {
		cfg.Agency.SubagentWallSeconds = 600
	}
	if cfg.Agency.SubagentMaxMints == 0 {
		cfg.Agency.SubagentMaxMints = 20
	}
	if cfg.Agency.RhythmSeconds == 0 {
		cfg.Agency.RhythmSeconds = 600
	}

	// The visible-output floor (thinking vs max_output) now clamps in
	// resolveLLM — the budgets are provider-entry data, so the trap is
	// disarmed where the values are resolved, not here.
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
}
