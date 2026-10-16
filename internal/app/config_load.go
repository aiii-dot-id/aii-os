package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func LoadConfig(path string) (*Config, error) {
	cfg, err := ReadConfig(path)
	if !errors.Is(err, fs.ErrNotExist) {
		return cfg, err
	}

	loadedFrom := path
	if path == "" {
		loadedFrom = install.ConfigPathIn("")
	}
	logsink.Info("config.start", "no config found — creating the default, FIRSTBOOT")
	cfg = defaultConfig()
	cfg.SourcePath = loadedFrom
	if _, err := saveConfig(cfg); err != nil {
		return nil, fmt.Errorf("cannot write default config: %w", err)
	}
	return cfg, nil
}

func ReadConfig(path string) (*Config, error) {

	loadedFrom := path
	data, err := atomicfile.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := *defaultConfig()

	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}
	if len(bytes.TrimSpace(data[decoder.InputOffset():])) != 0 {
		return nil, fmt.Errorf("cannot parse config: trailing data")
	}

	agency := namedInSection(data, "agency")
	if !agency["queue_workers"] && agency["max_parallel_subagents"] && cfg.Agency.MaxParallelSubagents > 0 {
		cfg.Agency.QueueWorkers = cfg.Agency.MaxParallelSubagents
	}
	if !agency["subagent_max_tool_rounds"] && cfg.Agency.MaxToolRounds > 0 && cfg.Agency.SubagentMaxToolRounds > cfg.Agency.MaxToolRounds {
		cfg.Agency.SubagentMaxToolRounds = cfg.Agency.MaxToolRounds
	}
	for _, said := range applyRuled(&cfg) {
		logsink.Warn("config.decision", "%s: %s", loadedFrom, said)
	}

	cfg.SourcePath = loadedFrom
	if !store.ValidDatabaseFormat(cfg.Identity.DBFormat) {
		return nil, fmt.Errorf("identity.db_format must be empty, sqlite, or zstd")
	}
	if err := validMeaningChoice(cfg.Memory.MeaningSource); err != nil {
		return nil, fmt.Errorf("%s: %w", meaningSourceKey, err)
	}
	if _, err := cfg.Plugins.Runtime.startupCeiling(); err != nil {
		return nil, fmt.Errorf("plugins.runtime.max_startup_ms: %w", err)
	}
	if cfg.Plugins.Runtime.SlowStorageCallMs < 0 {
		return nil, fmt.Errorf("plugins.runtime.slow_storage_call_ms must be zero (take the default) or positive")
	}
	if err := validHardwareQueryMs(cfg.Plugins.Runtime.HardwareQueryMs); err != nil {
		return nil, err
	}
	if err := validGenesisPatience(cfg.Genesis); err != nil {
		return nil, err
	}
	if err := validStartNumbers(&cfg); err != nil {
		return nil, err
	}
	if cfg.Dashboard.StopDrainSeconds < 0 {
		return nil, fmt.Errorf("dashboard.stop_drain_seconds must be zero (take the default) or positive")
	}

	if cfg.Dashboard.ICEServers != nil {
		if err := dashboard.CheckICEServers(*cfg.Dashboard.ICEServers); err != nil {
			return nil, err
		}
	}
	if cfg.Dashboard.Files.MaxBytes < 0 || cfg.Dashboard.Files.MaxPerMessage < 0 {
		return nil, fmt.Errorf("dashboard.files.max_bytes and dashboard.files.max_per_message must be zero (take the default) or positive")
	}
	for _, ext := range cfg.Dashboard.Files.Types {
		if len(ext) < 2 || ext[0] != '.' || strings.ContainsAny(ext[1:], `./\ `) {
			return nil, fmt.Errorf("dashboard.files.types: %q is not an extension: write each with its dot, as in \".pdf\"", ext)
		}
	}
	if r := cfg.Plugins.Runtime; r.AdmissionMemoryReserveBytes < 0 || r.AdmissionMemoryBudgetBytes < 0 || r.MaxConcurrentStarts < 0 {
		return nil, fmt.Errorf("plugins.runtime: admission_memory_reserve_bytes, admission_memory_budget_bytes and max_concurrent_starts must not be negative")
	}
	for id, resource := range cfg.Plugins.Resources {
		if _, err := resource.startupTimeout(); err != nil {
			return nil, fmt.Errorf("plugins.resources.%s.startup_timeout_ms: %w", id, err)
		}
	}

	if level := cfg.Plugins.Autoload; level != "" {
		if _, _, ok := autoloadTier(level); !ok {
			return nil, fmt.Errorf("plugins.autoload: %q is not a level (none, T0, T1, T2, T3)", level)
		}
	}

	if err := validateVoiceMode(cfg.Speech.Mode); err != nil {
		return nil, err
	}
	if err := validVoiceOpenWait(cfg.Speech.OpenWaitSeconds); err != nil {
		return nil, err
	}

	if err := validateSpeakerPolicy(cfg.Speech.Speakers); err != nil {
		return nil, fmt.Errorf("speech.speakers: %w", err)
	}
	if err := validHeardHistory(cfg.Speech.HeardHistory); err != nil {
		return nil, err
	}
	if _, err := disabledTools(cfg.Tools.Disabled); err != nil {
		return nil, err
	}

	if err := validateDashboardOrigin(cfg.Dashboard.Origin); err != nil {
		return nil, err
	}
	switch {
	case cfg.Prompt.MaxTokens < 0:
		return nil, fmt.Errorf("prompt.max_tokens must be zero (derive from the model window) or positive")
	case cfg.Prompt.RecentTurns <= 0:
		return nil, fmt.Errorf("prompt.recent_turns must be positive")
	case cfg.Agency.SubagentMaxToolCalls < 0:
		return nil, fmt.Errorf("agency.subagent_max_tool_calls must be zero (take the default) or positive")
	case cfg.Agency.SubagentMaxToolRounds < 0:
		return nil, fmt.Errorf("agency.subagent_max_tool_rounds must be zero (take the default) or positive")
	case cfg.Prompt.MaxToolResultChars < 0:
		return nil, fmt.Errorf("prompt.max_tool_result_chars cannot be negative")
	case cfg.Memory.BackfillSeconds < 0:
		return nil, fmt.Errorf("memory.backfill_seconds must be zero (take the default) or positive")
	case cfg.Memory.BackfillRows < 0:
		return nil, fmt.Errorf("memory.backfill_rows must be zero (take the default) or positive")
	case cfg.Memory.BackfillLocalSeconds < 0:
		return nil, fmt.Errorf("memory.backfill_local_seconds must be zero (take the default) or positive")
	case cfg.Memory.ConversationVectorWindow < 0:
		return nil, fmt.Errorf("memory.conversation_vector_window must be zero (take the default) or positive")
	case cfg.Memory.EmbedCueSeconds < 0:
		return nil, fmt.Errorf("memory.embed_cue_seconds must be zero (take the default) or positive")
	case cfg.Memory.EmbedMemorySeconds < 0:
		return nil, fmt.Errorf("memory.embed_memory_seconds must be zero (take the default) or positive")
	case cfg.Memory.SourceCheckRetrySeconds < 0:
		return nil, fmt.Errorf("memory.source_check_retry_seconds must be zero (take the default) or positive")
	case cfg.Memory.ReplyHoldSeconds < 0:
		return nil, fmt.Errorf("memory.reply_hold_seconds must be zero (take the default) or positive")
	case cfg.Maintenance.OnDemandSpacingSeconds < 0 || cfg.Maintenance.OnDemandSpacingSeconds > 86400:
		return nil, fmt.Errorf("maintenance.on_demand_spacing_seconds must be zero (take the default, 600) or between 1 and 86400")
	case cfg.Prompt.PulseIntervalSeconds < 0:
		return nil, fmt.Errorf("prompt.pulse_interval_seconds cannot be negative")
	case cfg.Agency.MaxToolRounds <= 0:
		return nil, fmt.Errorf("agency.max_tool_rounds must be positive")
	case cfg.Agency.MaxSubagentDepth < 0:
		return nil, fmt.Errorf("agency.max_subagent_depth cannot be negative")
	case cfg.Agency.MaxParallelSubagents < 0:
		return nil, fmt.Errorf("agency.max_parallel_subagents cannot be negative")
	case cfg.Agency.SubagentWallSeconds <= 0:
		return nil, fmt.Errorf("agency.subagent_wall_seconds must be positive")
	case cfg.Agency.SubagentWallSecondsLocal <= 0:
		return nil, fmt.Errorf("agency.subagent_wall_seconds_local must be positive")
	case cfg.Agency.SubagentMaxMints <= 0:
		return nil, fmt.Errorf("agency.subagent_max_mints must be positive")
	}

	held, err := holdCognition(&cfg)
	if err != nil {
		return nil, err
	}
	for _, said := range held {
		logsink.Warn("config.decision", "%s: %s", loadedFrom, said)
	}
	if err := validCognition(cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (r PluginResources) startupTimeout() (time.Duration, error) {
	if r.StartupTimeoutMS == nil {
		return 0, nil
	}
	return positiveMilliseconds(*r.StartupTimeoutMS)
}

func (r PluginRuntimeConfig) startupCeiling() (time.Duration, error) {
	return positiveMilliseconds(r.MaxStartupMS)
}

func positiveMilliseconds(ms int64) (time.Duration, error) {
	if ms <= 0 || ms > int64((1<<63-1)/time.Millisecond) {
		return 0, fmt.Errorf("must be a positive, representable duration in milliseconds")
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (c *ToolsConfig) UnmarshalJSON(data []byte) error {
	type fields ToolsConfig
	seen := false
	wire := struct {
		*fields
		LegacyShellTimeoutSeconds configIntAlias `json:"bash_timeout_seconds"`
	}{(*fields)(c), configIntAlias{&c.ShellTimeoutSeconds, &seen}}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return err
	}
	if seen {
		logsink.Warn("config.decision", "tools.bash_timeout_seconds is a previous name for the shell timeout: read as tools.shell_timeout_seconds; the next save writes only the current name")
	}
	return nil
}

type configIntAlias struct {
	target *int
	seen   *bool
}

func (a configIntAlias) UnmarshalJSON(data []byte) error {
	*a.seen = true
	return json.Unmarshal(data, a.target)
}

const legacyShellName = "bash"

type disabledToolError struct{ Entry string }

func (e *disabledToolError) Error() string {
	return fmt.Sprintf("tools.disabled: %q names no tool that can be switched off — name a builtin (%s) or a plugin operation (pl_<plugin>_<operation>)",
		e.Entry, strings.Join(tools.AllBuiltins(), ", "))
}

func disabledTools(entries []string) ([]string, error) {
	builtins := tools.AllBuiltins()
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch {
		case entry == legacyShellName:
			entry = "shell"
		case strings.HasPrefix(entry, "pl_"), slices.Contains(builtins, entry):
		default:
			return nil, &disabledToolError{Entry: entry}
		}
		names = append(names, entry)
	}
	return names, nil
}

func applyDisabledTools(reg *tools.Registry, entries []string) error {
	names, err := disabledTools(entries)
	if err != nil {
		return err
	}
	if slices.Contains(entries, legacyShellName) {
		logsink.Info("config.decision", "tools.disabled names %q, a previous name for the shell: read as \"shell\", so the shell is off (the file keeps its spelling)", legacyShellName)
	}
	off := make(map[string]bool, len(names))
	for _, name := range names {
		off[name] = true
		reg.SetToolEnabled(name, false)
		logsink.Debug("ring.decision", "tool %q disabled by operator config", name)
	}
	for _, state := range reg.ToolStates() {
		if !off[state.Name] {
			reg.SetToolEnabled(state.Name, true)
		}
	}
	return nil
}

func (c *PluginsConfig) UnmarshalJSON(data []byte) error {
	type fields PluginsConfig
	type grant struct {
		broker.Grant
		Roots json.RawMessage `json:"roots"`
	}
	wire := struct {
		*fields
		Grants map[string]grant `json:"grants"`
	}{fields: (*fields)(c)}

	if c.Grants != nil {
		wire.Grants = make(map[string]grant, len(c.Grants))
		for id, g := range c.Grants {
			wire.Grants[id] = grant{Grant: g}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return err
	}
	c.Grants = nil
	if wire.Grants != nil {
		c.Grants = make(map[string]broker.Grant, len(wire.Grants))
	}
	var notes []string
	for id, g := range wire.Grants {
		c.Grants[id] = g.Grant
		if g.Roots == nil {
			continue
		}
		var roots []struct {
			Name, Path string
		}
		_ = json.Unmarshal(g.Roots, &roots)
		var named []string
		for _, r := range roots {
			named = append(named, r.Path)
		}
		notes = append(notes, fmt.Sprintf("plugins.grants.%s.roots is retired and not applied (%s): a plugin's files are the identity's sandbox — tick files on the plugin's card, and add any of these folders outside the identity's home in Settings → Sandbox; the next save removes it from the file",
			id, strings.Join(named, ", ")))
	}
	sort.Strings(notes)
	for _, note := range notes {
		logsink.Warn("config.decision", "%s", note)
	}
	return nil
}

type retiredConfigKey struct {
	Path   string
	Sample string
	Fate   string
}

var retiredConfigKeys = [...]retiredConfigKey{

	{"agency.heuristic_nudges", "true", "dropped: the prose it switched was deleted"},
	{"agency.breadth_nudge", "false", "dropped: the prose it switched was deleted"},
	{"agency.plan_nudge", "true", "dropped: the prose it switched was deleted"},
	{"agency.planning_brief", "false", "dropped: the prose it switched was deleted"},

	{"tools.bash_timeout_seconds", "300", "read as tools.shell_timeout_seconds"},

	{"plugins.grants.<id>.roots", `[{"name":"work","path":"/tmp/work","write":true}]`, "dropped: grants nothing; the rest of the grant applies"},

	{"dashboard.auth_token_sha256", `"0000000000000000000000000000000000000000000000000000000000000000"`, "retired by the first boot that reads it: a fresh token is minted and the file saved without it (dashboard_auth.go)"},
}

func (c *AgencyConfig) UnmarshalJSON(data []byte) error {
	type fields AgencyConfig
	wire := struct {
		*fields
		HeuristicNudges json.RawMessage `json:"heuristic_nudges"`
		BreadthNudge    json.RawMessage `json:"breadth_nudge"`
		PlanNudge       json.RawMessage `json:"plan_nudge"`
		PlanningBrief   json.RawMessage `json:"planning_brief"`
	}{fields: (*fields)(c)}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return err
	}
	present := map[string]bool{
		"agency.heuristic_nudges": wire.HeuristicNudges != nil,
		"agency.breadth_nudge":    wire.BreadthNudge != nil,
		"agency.plan_nudge":       wire.PlanNudge != nil,
		"agency.planning_brief":   wire.PlanningBrief != nil,
	}
	var named []string
	for _, k := range retiredConfigKeys {
		if present[k.Path] {
			named = append(named, k.Path)
		}
	}
	if len(named) > 0 {
		logsink.Warn("config.decision", "%s retired and not applied: the coaching prompts they switched were deleted (D6), so nothing they set has an effect; the next save removes them from the file",
			strings.Join(named, ", "))
	}
	return nil
}

func saveConfig(cfg *Config) (bool, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal: %w", err)
	}
	path := cfg.SourcePath
	if path == "" {
		path = install.ConfigPathIn("")
	}
	published, err := writeFileAtomic(path, data)
	if published {
		cfg.cognitionHeld = nil
	}
	return published, err
}

func (a *App) persistLocked(candidate *Config, persist func(*Config) (bool, error)) (published bool, err error) {
	published, err = persist(candidate)
	if !published {
		if err != nil {
			return false, fmt.Errorf("persist config: %w", err)
		}
		return false, nil
	}
	*a.cfg = *candidate
	if err != nil {
		return true, fmt.Errorf("config was published and applied live, but directory durability is unconfirmed: %w", err)
	}
	return true, nil
}

func (cfg *Config) UnmarshalJSON(data []byte) error {
	type fields Config
	wire := struct {
		*fields
		Contacts json.RawMessage `json:"contacts"`
	}{fields: (*fields)(cfg)}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	cfg.contactsPresent = wire.Contacts != nil
	if !cfg.contactsPresent {
		cfg.Contacts = nil
		return nil
	}
	contacts := json.NewDecoder(bytes.NewReader(wire.Contacts))
	contacts.DisallowUnknownFields()
	return contacts.Decode(&cfg.Contacts)
}

func (cfg Config) MarshalJSON() ([]byte, error) {
	type fields Config
	var contacts *[]Contact
	if cfg.contactsPresent || cfg.Contacts != nil {
		contacts = &cfg.Contacts
	}
	return json.Marshal(struct {
		*fields
		Contacts *[]Contact `json:"contacts,omitempty"`
	}{(*fields)(&cfg), contacts})
}

func (a *App) configSnapshot() Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return *a.cfg
}

func writeFileAtomic(path string, data []byte) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}
	published, err := atomicfile.WriteReplace(path, data, 0o600)
	if err != nil {
		return published, fmt.Errorf("replace config: %w", err)
	}
	return true, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func namedInSection(data []byte, section string) map[string]bool {
	named := map[string]bool{}
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return named
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(top[section], &keys) != nil {
		return named
	}
	for k := range keys {
		named[k] = true
	}
	return named
}
