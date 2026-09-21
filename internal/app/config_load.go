package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func LoadConfig(path string) (*Config, error) {
	// .
	// .
	// .
	loadedFrom := path
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// .
		// .
		if path == "" {
			loadedFrom = DefaultConfigPath()
		}
		logsink.Info("config.start", "no config found — creating the default, FIRSTBOOT")
		cfg := defaultConfig()
		cfg.SourcePath = loadedFrom
		if _, err := saveConfig(cfg); err != nil {
			return nil, fmt.Errorf("cannot write default config: %w", err)
		}
		return cfg, nil
	}

	// .
	// .
	// .
	cfg := *defaultConfig()
	// .
	// .
	// .
	// .
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", renamedKeyAdvice(err))
	}
	if len(bytes.TrimSpace(data[decoder.InputOffset():])) != 0 {
		return nil, fmt.Errorf("cannot parse config: trailing data")
	}

	cfg.SourcePath = loadedFrom
	if !store.ValidDatabaseFormat(cfg.Identity.DBFormat) {
		return nil, fmt.Errorf("identity.db_format must be empty, sqlite, or zstd")
	}
	if _, err := cfg.Plugins.Runtime.startupCeiling(); err != nil {
		return nil, fmt.Errorf("plugins.runtime.max_startup_ms: %w", err)
	}
	if r := cfg.Plugins.Runtime; r.AdmissionMemoryReserveBytes < 0 || r.AdmissionMemoryBudgetBytes < 0 || r.MaxConcurrentStarts < 0 {
		return nil, fmt.Errorf("plugins.runtime: admission_memory_reserve_bytes, admission_memory_budget_bytes and max_concurrent_starts must not be negative")
	}
	for id, resource := range cfg.Plugins.Resources {
		if _, err := resource.startupTimeout(); err != nil {
			return nil, fmt.Errorf("plugins.resources.%s.startup_timeout_ms: %w", id, err)
		}
	}
	// .
	// .
	if err := validateVoiceMode(cfg.Speech.Mode); err != nil {
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
	case cfg.Prompt.Ring3MaxChars < 0:
		return nil, fmt.Errorf("prompt.ring3_max_chars must be zero (take the default) or positive")
	case cfg.Prompt.SurfacingMaxChars < 0:
		return nil, fmt.Errorf("prompt.surfacing_max_chars must be zero (take the default) or positive")
	case cfg.Prompt.DreamConversationMaxChars < 0:
		return nil, fmt.Errorf("prompt.dream_conversation_max_chars must be zero (take the default) or positive")
	case cfg.Maintenance.OnDemandSpacingSeconds < 0 || cfg.Maintenance.OnDemandSpacingSeconds > 86400:
		return nil, fmt.Errorf("maintenance.on_demand_spacing_seconds must be zero (take the default, 600) or between 1 and 86400")
	case cfg.Prompt.TensionsMaxChars < 0:
		return nil, fmt.Errorf("prompt.tensions_max_chars must be zero (take the default) or positive")
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
	case cfg.Agency.RhythmSeconds <= 0:
		return nil, fmt.Errorf("agency.rhythm_seconds must be positive")
	case cfg.Agency.OutcomeWindow <= 0:
		return nil, fmt.Errorf("agency.outcome_window must be positive")
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

// .
// .
// .
// .
// .
// .
// .
var renamedConfigKeys = map[string]string{
	"bash_timeout_seconds": "tools.shell_timeout_seconds",
}

// .
// .
// .
// .
// .
func renamedKeyAdvice(err error) error {
	rest, ok := strings.CutPrefix(err.Error(), "json: unknown field ")
	if !ok {
		return err
	}
	current, renamed := renamedConfigKeys[strings.Trim(rest, `"`)]
	if !renamed {
		return err
	}
	return fmt.Errorf("%w — renamed to %q; rename the key in the config file", err, current)
}

func saveConfig(cfg *Config) (bool, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal: %w", err)
	}
	path := cfg.SourcePath
	if path == "" {
		path = DefaultConfigPath()
	}
	return writeFileAtomic(path, data)
}

func (a *App) configSnapshot() Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return *a.cfg
}

// .
// .
// .
// .
func writeFileAtomic(path string, data []byte) (published bool, retErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, fmt.Errorf("create temporary config: %w", err)
	}
	tmp := f.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
		if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary config: %w", err))
		}
	}()

	// .
	// .
	// .
	// .
	// .
	if err := fileperm.RestrictToOwner(f); err != nil {
		return false, fmt.Errorf("protect temporary config: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return false, fmt.Errorf("write temporary config: %w", err)
	}
	if err := f.Sync(); err != nil {
		return false, fmt.Errorf("sync temporary config: %w", err)
	}
	err = f.Close()
	closed = true
	if err != nil {
		return false, fmt.Errorf("close temporary config: %w", err)
	}
	published, err = atomicfile.Replace(tmp, path)
	if err != nil {
		return published, fmt.Errorf("replace config: %w", err)
	}
	return true, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
