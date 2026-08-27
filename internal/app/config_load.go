package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

func LoadConfig(path string) (*Config, error) {
	// M5 (external review): remember which file was actually loaded —
	// saveConfig previously hardcoded config/config.json, so a dashboard
	// edit could write a DIFFERENT file than the one in effect.
	loadedFrom := path
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		// ONE config, beside the binary (operator law 2026-08-20:
		// entirely local — there is no other config location).
		if path == "" {
			loadedFrom = "config.json"
		}
		log.Println("No config found. Creating default config — FIRSTBOOT.")
		cfg := defaultConfig()
		cfg.SourcePath = loadedFrom
		if _, err := saveConfig(cfg); err != nil {
			return nil, fmt.Errorf("cannot write default config: %w", err)
		}
		return cfg, nil
	}

	// Decode over defaults: an absent field keeps its default, while an
	// explicit zero or empty string remains an operator value. In particular,
	// agency.max_subagent_depth=0 disables spawning as its contract says.
	cfg := *defaultConfig()
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", renamedKeyAdvice(err))
	}
	if len(bytes.TrimSpace(data[decoder.InputOffset():])) != 0 {
		return nil, fmt.Errorf("cannot parse config: trailing data")
	}

	cfg.SourcePath = loadedFrom
	switch {
	case cfg.Prompt.MaxTokens < 0:
		return nil, fmt.Errorf("prompt.max_tokens must be zero (derive from the model window) or positive")
	case cfg.Prompt.RecentTurns <= 0:
		return nil, fmt.Errorf("prompt.recent_turns must be positive")
	case cfg.Prompt.MaxToolResultChars < 0:
		return nil, fmt.Errorf("prompt.max_tool_result_chars cannot be negative")
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
	case cfg.Agency.SubagentMaxMints <= 0:
		return nil, fmt.Errorf("agency.subagent_max_mints must be positive")
	case cfg.Agency.RhythmSeconds <= 0:
		return nil, fmt.Errorf("agency.rhythm_seconds must be positive")
	}
	return &cfg, nil
}

// renamedConfigKeys maps a key this project renamed to the dotted name
// that replaced it. A rename here is a hard cut — the runtime is
// unreleased, so there is no alias and no migration layer — which
// leaves the refusal as the ONLY place an operator whose file predates
// the rename can learn what to type. Keyed on the LEAF name because
// encoding/json reports only the leaf, so an entry is safe only while
// that leaf is unique in the whole config tree.
var renamedConfigKeys = map[string]string{
	"bash_timeout_seconds": "tools.shell_timeout_seconds", // 2026-08-27 bash → shell
}

// renamedKeyAdvice makes encoding/json's unknown-field refusal
// actionable. A live install carrying a renamed key died at boot naming
// a key that no longer exists anywhere in the tree, with nothing to act
// on. Reading the stdlib message is PRESENTATION only — no control flow
// anywhere depends on this text.
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
		path = "config.json"
	}
	return writeFileAtomic(path, data)
}

func (a *App) configSnapshot() Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return *a.cfg
}

// writeFileAtomic is the one persistence path for operator configuration.
// Both config.json and providers.json may contain credentials: a unique 0600
// temporary file is synced before replacement, then the directory entry is
// made durable by the platform seam.
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

	if err := f.Chmod(0600); err != nil {
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
