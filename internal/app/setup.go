package app

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// .

// .
// .
// .
// .
// .

// .
// .
func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return "••••"
	}
	return "••••" + k[len(k)-4:]
}

func (a *App) configState() *dashboard.ConfigState {
	c := a.configSnapshot()
	// .
	// .
	// .
	// .
	// .
	llmSt := dashboard.LLMConfigState{
		Provider: c.LLM.Provider, Model: c.LLM.Model,
		TimeoutSeconds: c.LLM.TimeoutSeconds,
	}
	reg, err := a.loadProviders()
	if err != nil {
		llmSt.Error = err.Error()
	} else if cc, entry, err := a.resolveLLMConfig(c.LLM, reg); err != nil {
		llmSt.Error = err.Error()
	} else {
		llmSt.ResolvedProvider = entry.Name
		llmSt.ResolvedModel = cc.Model
		llmSt.Endpoint = cc.Endpoint
		llmSt.APIKeyMasked = maskKey(cc.APIKey)
		llmSt.ThinkingBudget = entry.ThinkingBudget
		llmSt.MaxOutputTokens = entry.MaxOutputTokens
		llmSt.ContextLength = entry.ContextLength
		llmSt.ReasoningEffort = entry.ReasoningEffort
		// .
		// .
		// .
		dialect := llm.DialectFor(cc.Provider)
		// .
		// .
		// .
		// .
		// .
		// .
		llmSt.EffortPlan = llm.PlanEffort(dialect, entry.ReasoningEffort, effortLevelsFor(reg.effective(), entry, cc.Model)).Summary()
		llmSt.EffortModel = cc.Model
		llmSt.ThinkingApplies = dialect == llm.DialectAnthropic
	}
	return &dashboard.ConfigState{
		LLM:             llmSt,
		Dashboard:       dashboard.DashboardState{Host: c.Dashboard.Host, Port: c.Dashboard.Port, TLS: c.Dashboard.TLS, Origin: a.advertisedOrigin()},
		PublicName:      a.publicNameState(),
		Plugins:         a.pluginsState(&c),
		CredentialKinds: oauth.Kinds(),
		Witness: dashboard.WitnessConfigState{
			URL: c.Witness.URL, IntervalEvents: c.Witness.IntervalEvents,
			PlatformPubkeyPath: c.Witness.PlatformPubkeyPath, TLSSPKISHA256: c.Witness.TLSSPKISHA256,
		},
		Genesis: dashboard.GenesisConfigState{
			ServerURL: c.Genesis.ServerURL, FirewallURL: c.Genesis.FirewallURL, BootstrapURL: c.Genesis.BootstrapURL,
		},
		Prompt: dashboard.PromptConfigState{
			MaxTokens: c.Prompt.MaxTokens, RecentTurns: c.Prompt.RecentTurns,
			MaxToolResultChars: c.Prompt.MaxToolResultChars,
		},
		Agency: dashboard.AgencyConfigState{
			PreferLocalForRoles: c.Agency.PreferLocalForRoles,
			HeuristicNudges:     c.Agency.HeuristicNudges != nil && *c.Agency.HeuristicNudges,
		},
		Updates: dashboard.UpdatesConfigState{Automatic: c.Updates.Automatic},
		Logs: dashboard.LogsConfigState{
			Dir: c.Logs.Dir, MaxBackups: c.Logs.MaxBackups, CompressDays: c.Logs.CompressDays,
		},
		Timezone: c.Timezone,
	}
}

// .
// .
// .
// .
func (a *App) applyConfigChange(changes map[string]interface{}) (*dashboard.ConfigState, error) {
	return a.applyConfigChangeWith(changes, saveConfig)
}

func (a *App) applyConfigChangeWith(changes map[string]interface{}, persist func(*Config) (bool, error)) (*dashboard.ConfigState, error) {
	var restart []string
	llmChanged := false
	substrateChanged := false
	pluginsChanged := false
	grantsChanged := false
	catalogChanged := false

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	orig := a.configSnapshot()
	candidate := orig
	cfg := &candidate

	// .
	str := func(k string, v interface{}) (string, error) {
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("%s: want a string", k)
		}
		return s, nil
	}
	integer := func(k string, v interface{}) (int, error) {
		switch n := v.(type) {
		case float64:
			i := int(n)
			if float64(i) != n {
				return 0, fmt.Errorf("%s: want an integer", k)
			}
			return i, nil
		case int:
			return n, nil
		default:
			return 0, fmt.Errorf("%s: want a number", k)
		}
	}
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := changes[key]
		// .
		// .
		// .
		if strings.HasPrefix(key, "plugins.settings.") {
			if err := a.applyPluginSetting(cfg, key, v); err != nil {
				return nil, err
			}
			continue
		}
		// .
		// .
		// .
		// .
		if strings.HasPrefix(key, "plugins.grants.") {
			if err := applyPluginGrant(cfg, key, v); err != nil {
				return nil, err
			}
			grantsChanged = true
			continue
		}
		switch key {
		// .
		// .
		// .
		// .
		case "llm.provider":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			// .
			if s != cfg.LLM.Provider {
				cfg.LLM.Provider = s
				substrateChanged = true
				llmChanged = true
			}
		case "llm.model":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			// .
			if s != cfg.LLM.Model {
				cfg.LLM.Model = s
				substrateChanged = true
				llmChanged = true
			}
		case "llm.timeout_seconds":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n <= 0 {
				return nil, fmt.Errorf("llm.timeout_seconds: must be positive")
			}
			if n != cfg.LLM.TimeoutSeconds {
				cfg.LLM.TimeoutSeconds = n
				llmChanged = true
			}

		// .
		case "dashboard.host":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			if s == "" {
				return nil, fmt.Errorf("dashboard.host: empty (use 127.0.0.1 for loopback-only)")
			}
			cfg.Dashboard.Host = s
			if !dashboard.IsLoopback(s) {
				cfg.Dashboard.TLS = true
			}
			restart = append(restart, key)
		case "dashboard.tls":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("dashboard.tls: want a boolean")
			}
			if !b && !dashboard.IsLoopback(cfg.Dashboard.Host) {
				return nil, fmt.Errorf("dashboard.tls: a bind on %s serves TLS — plain HTTP across a network publishes the conversation; bind loopback for plain HTTP", cfg.Dashboard.Host)
			}
			cfg.Dashboard.TLS = b
			restart = append(restart, key)
		case "agency.heuristic_nudges":
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("agency.heuristic_nudges: want a boolean")
			}
			cfg.Agency.HeuristicNudges = &b
			restart = append(restart, key)
		case "agency.prefer_local_for_roles":
			// .
			// .
			// .
			// .
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("agency.prefer_local_for_roles: want a boolean")
			}
			cfg.Agency.PreferLocalForRoles = b
		case "dashboard.port":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < 1 || n > 65535 {
				return nil, fmt.Errorf("dashboard.port: out of range")
			}
			cfg.Dashboard.Port = n
			restart = append(restart, key)
		case "logs.dir":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Logs.Dir = s
			restart = append(restart, key)
		case "logs.max_backups":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < -1 {
				return nil, fmt.Errorf("logs.max_backups: -1 keeps all, 0 uses the default (9), a positive number is the cap")
			}
			cfg.Logs.MaxBackups = n
			restart = append(restart, key)
		case "logs.compress_days":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < -1 {
				return nil, fmt.Errorf("logs.compress_days: -1 never compresses, 0 uses the default (7), a positive number is the age in days")
			}
			cfg.Logs.CompressDays = n
			restart = append(restart, key)
		case "plugins.autoload":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			switch s {
			case "none", "T0", "T1", "T2", "T3":
			default:
				return nil, fmt.Errorf("plugins.autoload: %q is not a level (none, T0, T1, T2, T3)", s)
			}
			if s != cfg.Plugins.Autoload {
				cfg.Plugins.Autoload = s
				pluginsChanged = true
			}
		case "plugins.catalog_url":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			s = strings.TrimSpace(s)
			if s != "" && !strings.HasPrefix(s, "https://") {
				return nil, fmt.Errorf("plugins.catalog_url: must be an https URL, or empty for no catalog")
			}
			if s != cfg.Plugins.CatalogURL {
				cfg.Plugins.CatalogURL = s
				catalogChanged = true
			}

		case "plugins.runtime.max_installed_bytes", "plugins.runtime.max_files", "plugins.runtime.max_file_bytes",
			"plugins.runtime.max_compressed_bytes", "plugins.runtime.max_depth", "plugins.runtime.roots_kept":
			// .
			// .
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n <= 0 {
				return nil, fmt.Errorf("%s: must be positive", key)
			}
			r := &cfg.Plugins.Runtime
			switch key {
			case "plugins.runtime.max_installed_bytes":
				r.MaxInstalledBytes = int64(n)
			case "plugins.runtime.max_files":
				r.MaxFiles = n
			case "plugins.runtime.max_file_bytes":
				r.MaxFileBytes = int64(n)
			case "plugins.runtime.max_compressed_bytes":
				r.MaxCompressedBytes = int64(n)
			case "plugins.runtime.max_depth":
				r.MaxDepth = n
			case "plugins.runtime.roots_kept":
				r.RootsKept = n
			}
			pluginsChanged = true
		case "witness.url":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Witness.URL = s
			restart = append(restart, key)
		case "witness.interval_events":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < 0 {
				return nil, fmt.Errorf("witness.interval_events: negative")
			}
			cfg.Witness.IntervalEvents = n
			restart = append(restart, key)
		case "witness.platform_pubkey_path":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Witness.PlatformPubkeyPath = s
			restart = append(restart, key)
		case "witness.tls_spki_sha256":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Witness.TLSSPKISHA256 = s
			restart = append(restart, key)
		case "genesis.server_url":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Genesis.ServerURL = s
			restart = append(restart, key)
		case "genesis.firewall_url":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Genesis.FirewallURL = s
			restart = append(restart, key)
		case "genesis.bootstrap_url":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Genesis.BootstrapURL = s
			restart = append(restart, key)
		case "prompt.max_tokens":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < 0 {
				return nil, fmt.Errorf("prompt.max_tokens: must be zero (derive from the model window) or positive")
			}
			cfg.Prompt.MaxTokens = n
			restart = append(restart, key)
		case "prompt.recent_turns":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n <= 0 {
				return nil, fmt.Errorf("prompt.recent_turns: must be positive")
			}
			cfg.Prompt.RecentTurns = n
			restart = append(restart, key)
		case "prompt.pulse_interval_seconds":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < 30 {
				return nil, fmt.Errorf("prompt.pulse_interval_seconds: minimum 30 (a faster pulse is a busier life)")
			}
			cfg.Prompt.PulseIntervalSeconds = n
			restart = append(restart, key)
		case "prompt.max_tool_result_chars":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < 0 {
				return nil, fmt.Errorf("prompt.max_tool_result_chars: negative")
			}
			cfg.Prompt.MaxToolResultChars = n
			restart = append(restart, key)
		case "timezone":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			if s != "" {
				if _, err := timeLoadLocation(s); err != nil {
					return nil, fmt.Errorf("timezone: unknown (%s)", s)
				}
			}
			cfg.Timezone = s
			restart = append(restart, key)

		case "updates.automatic":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("updates.automatic: want a boolean")
			}
			cfg.Updates.Automatic = b
			// .
			// .

		default:
			// .
			// .
			// .
			return nil, fmt.Errorf("%s is not an operator-settable field (substrate-owned or unknown — rejected)", key)
		}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var validatedClient *llm.Client
	var resolvedEntry providerEntry
	var resolvedRegistry *providerRegistry
	var providerPath string
	if llmChanged {
		providerPath = a.providersPath()
		reg, rerr := loadProvidersFile(providerPath)
		if rerr != nil {
			return nil, fmt.Errorf("substrate refused: %w", rerr)
		}
		cc, entry, rerr := a.resolveLLMConfig(cfg.LLM, reg)
		if rerr != nil {
			return nil, fmt.Errorf("substrate refused: %w", rerr)
		}
		resolvedRegistry = reg
		validatedClient = a.newLLMClient(cc, promptBudgetFor(entry, cfg.Prompt.MaxTokens))
		if substrateChanged {
			if err := a.probeSubstrate(validatedClient, cc, entry); err != nil {
				return nil, err
			}
		}
		resolvedEntry = entry
	}

	// .
	// .
	// .
	// .
	holdTurn := llmChanged && a.llmSwap != nil
	if holdTurn {
		if a.bgCtx == nil {
			return nil, fmt.Errorf("substrate change refused: application lifecycle is unavailable")
		}
		if err := a.acquireTurn(a.bgCtx); err != nil {
			return nil, err
		}
	}
	configPublished := false
	commitErr := func() error {
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		if !reflect.DeepEqual(*a.cfg, orig) {
			return fmt.Errorf("config changed while the candidate was checked; retry")
		}
		if resolvedRegistry != nil {
			current, err := loadProvidersFile(providerPath)
			if err != nil {
				return fmt.Errorf("recheck providers: %w", err)
			}
			if !reflect.DeepEqual(current, resolvedRegistry) {
				return fmt.Errorf("providers changed while the candidate was checked; retry")
			}
		}
		published, persistErr := persist(cfg)
		if persistErr != nil && !published {
			return fmt.Errorf("persist config: %w", persistErr)
		}
		configPublished = published
		if published {
			*a.cfg = *cfg
			// .
			if holdTurn {
				a.activateLLMRuntime(validatedClient, resolvedEntry, cfg.Prompt.MaxTokens)
			}
		}
		if persistErr != nil {
			return fmt.Errorf("config was published and applied live, but directory durability is unconfirmed: %w", persistErr)
		}
		return nil
	}()
	if holdTurn {
		a.releaseTurn()
	}
	if configPublished && pluginsChanged {
		a.pokePluginSweep()
	}
	if configPublished && catalogChanged {
		a.pokeCatalog()
	}
	if configPublished && grantsChanged {
		a.publishGrants(*cfg)
	}
	if commitErr != nil {
		return nil, commitErr
	}

	st := a.configState()
	st.RestartRequired = restart
	return st, nil
}

func (a *App) probeSubstrate(client *llm.Client, cc llm.ClientConfig, entry providerEntry) error {
	probeSeconds := cc.TimeoutSeconds
	if probeSeconds <= 0 {
		probeSeconds = 120
	}
	if a.bgCtx == nil {
		return fmt.Errorf("substrate refused: application lifecycle is unavailable")
	}
	vctx, cancel := context.WithTimeout(a.bgCtx, time.Duration(probeSeconds)*time.Second)
	defer cancel()
	resp, err := client.Chat(vctx, []llm.Message{{
		Role: "user", Content: "Reply with the single word OK.",
	}}, llm.ChatOptions{ThinkingBudget: cc.ThinkingBudget})
	if err != nil {
		if cc.APIKey == "" && cc.Credential == nil {
			return fmt.Errorf("substrate refused: provider %q has no configured credential; if it requires one, store it in Settings → Providers → %s; current substrate kept: %w", entry.Name, entry.Name, err)
		}
		return fmt.Errorf("substrate refused: provider %q model %q cannot complete a minimal inference request; current substrate kept: %w", entry.Name, cc.Model, err)
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return fmt.Errorf("substrate refused: provider %q model %q returned no visible answer to a minimal inference request; current substrate kept", entry.Name, cc.Model)
	}
	if note := a.probeToolSelection(vctx, client, entry, cc.Model); note != "" {
		log.Printf("SUBSTRATE: %s", note)
	}
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
// .
// .
func (a *App) probeToolSelection(ctx context.Context, client *llm.Client, entry providerEntry, model string) string {
	tool := llm.ToolDefinition{Type: "function"}
	tool.Function.Name = "report_ready"
	tool.Function.Description = "Report that you are ready. Call this to answer."
	tool.Function.Parameters = map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"ready": map[string]interface{}{"type": "boolean"}},
		"required":   []string{"ready"},
	}
	resp, err := client.Chat(ctx, []llm.Message{{
		Role: "user", Content: "Call report_ready with ready=true. Do not answer in words.",
	}}, llm.ChatOptions{Tools: []llm.ToolDefinition{tool}})
	if err != nil {
		return fmt.Sprintf("provider %q model %q rejected a tool-bearing request (%v) — this identity acts through tools; expect it to be unable to work", entry.Name, model, err)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
		return fmt.Sprintf("provider %q model %q answered a tool-bearing request WITHOUT calling the tool — it may be unable to act, and this identity does most of its work through tools", entry.Name, model)
	}
	return ""
}

// .
// .
// .
// .
func (a *App) newLLMClient(cc llm.ClientConfig, inputBudget int) *llm.Client {
	// .
	// .
	// .
	cc.MaxInputTokens = inputBudget
	return llm.New(&cc)
}

// .
func timeLoadLocation(name string) (*time.Location, error) {
	return time.LoadLocation(name)
}

// .
func (a *App) advertisedOrigin() string {
	if a.dashboard == nil {
		return ""
	}
	return a.dashboard.Origin()
}
