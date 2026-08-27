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

// --- Setup: operator-owned configuration through the dashboard ---

// The IDENTITY never gets this surface: config contains credentials and
// substrate paths, and Ring 5's floor bars the identity from config/
// (sub.config: "operator-controlled; credentials that are not yours").
// The dashboard is the OPERATOR's interface — this is the operator
// exercising ownership, not a resident verb.

// maskKey shows the last 4 characters only — enough to recognize WHICH
// key is configured, never enough to use it.
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
	// The llm section shows the RESOLVED substrate: the pointer as it
	// dereferences (provider name, effective model) plus the provider
	// data read-only from the providers.json entry. A pointer that does
	// not resolve reports its error and zero data — honest, never
	// invented (the data fields have no config existence anymore).
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
	}
	return &dashboard.ConfigState{
		LLM:             llmSt,
		Dashboard:       dashboard.DashboardState{Host: c.Dashboard.Host, Port: c.Dashboard.Port, TLS: c.Dashboard.TLS},
		Plugins:         dashboard.PluginsState{Autoload: c.Plugins.Autoload, Skips: a.pluginSkipViews()},
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
		Agency:  dashboard.AgencyConfigState{PreferLocalForRoles: c.Agency.PreferLocalForRoles},
		Updates: dashboard.UpdatesConfigState{Automatic: c.Updates.Automatic},
		Logs: dashboard.LogsConfigState{
			Dir: c.Logs.Dir, MaxBackups: c.Logs.MaxBackups, CompressDays: c.Logs.CompressDays,
		},
		Timezone: c.Timezone,
	}
}

// applyConfigChange applies a whitelisted change map. Returns the fields
// that were saved but need a restart. Unknown or forbidden paths are
// REJECTED (never silently ignored) — identity paths, key paths, and the
// tools cwd and identity paths are substrate-owned.
func (a *App) applyConfigChange(changes map[string]interface{}) (*dashboard.ConfigState, error) {
	return a.applyConfigChangeWith(changes, saveConfig)
}

func (a *App) applyConfigChangeWith(changes map[string]interface{}, persist func(*Config) (bool, error)) (*dashboard.ConfigState, error) {
	var restart []string
	llmChanged := false
	substrateChanged := false
	pluginsChanged := false

	// M4 (external review): the old `for key := range changes` validated
	// in Go's randomized map order — a two-field change was accepted or
	// rejected nondeterministically (thinking_budget vs max_output_tokens),
	// and a rejection left the earlier fields already mutated in memory.
	// Now: keys apply in sorted order, cross-field validation runs ONCE
	// after all fields land, and any rejection restores the pre-change
	// config — collect, validate, apply.
	orig := a.configSnapshot()
	candidate := orig
	cfg := &candidate

	// str/int/float readers with clear errors
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
		switch key {
		// --- LLM: the POINTER (2026-08-20 ruling) — applies LIVE via
		// the swappable client. Provider DATA (endpoint, key, window,
		// budgets, effort) is edited on the providers.json entry
		// (provider_set), never here: config points, the registry owns.
		case "llm.provider":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			// empty is legal: the registry's default-flagged entry
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
			// empty is legal: the entry's default_model
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

		// --- Everything below: saved, applies next boot ---
		case "dashboard.host":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			if s == "" {
				return nil, fmt.Errorf("dashboard.host: empty (use 127.0.0.1 for loopback-only)")
			}
			cfg.Dashboard.Host = s
			restart = append(restart, key)
		case "dashboard.tls":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("dashboard.tls: want a boolean")
			}
			cfg.Dashboard.TLS = b
			restart = append(restart, key)
		case "agency.prefer_local_for_roles":
			// resolveRunTarget reads this per spawn, so the next tagged
			// run honors the new route preference without a restart.
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
				pluginsChanged = true // live: the sweep converges on the new level within seconds
			}

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
			// Applies on next checker tick (within ~1h): the checker's
			// automatic closure reads this field live, not at start.

		default:
			// Forbidden or unknown: identity paths, key paths, dashboard
			// tools cwd — substrate-owned; a crafted message must
			// not redirect the ledger or the key.
			return nil, fmt.Errorf("%s is not an operator-settable field (substrate-owned or unknown — rejected)", key)
		}
	}

	// The use-door: a provider catalogue is not proof that inference works.
	// The live failure that replaced a working client with zAI proved the
	// distinction exactly: /models returned the chosen model while the chat
	// endpoint returned 429 "insufficient balance" for every operator turn.
	// Resolve the candidate and require one real, no-ledger inference before
	// persistence or Swap. Any failure leaves both the config and current
	// client untouched. The catalogue remains discovery, never an availability
	// certificate.
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

	// A provider switch is one boundary relative to the resident's voice:
	// wait for any current turn, then persist the validated pointer and move
	// provenance, prompt budgets, thinking budget, and client together before
	// another turn can begin. The candidate probe above does not hold the gate.
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
			// Every consumer of model data moves together after publication.
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

// probeToolSelection asks whether this substrate can actually CALL a
// tool, and reports rather than refuses.
//
// The identity reaches the world through native tool calls — measured
// over 1,322 real calls, 64.5% of everything it does is bash alone — so
// a substrate that answers text beautifully and never calls a tool
// hosts a resident who can think and cannot act. Until now that was
// discovered at the first turn that needed a tool, silently, as work
// simply not happening.
//
// A WARNING AND NOT A REFUSAL, deliberately. A model that answers in
// prose instead of calling is a false negative, and blocking adoption of
// a provider that works would be a worse failure than the silence this
// replaces. The operator is told; the choice stays theirs.
//
// This is also the capability signal that forced tool choice and native
// facility actions both need. It is produced here rather than declared
// in config because the endpoint is the only authority on what it can
// do, and a config field would be our copy of its fact — the drift this
// codebase has paid for twelve times.
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

// newLLMClient builds the transport client from a resolved config,
// wiring the OAuth token source when configured. OAuth (priority 6):
// the owner-maintained file is read on each acquisition. The runtime
// never spends, rewrites, or copies the owner's credential.
func (a *App) newLLMClient(cc llm.ClientConfig, inputBudget int) *llm.Client {
	// The credential source rides on the resolved config now: it is a
	// property of the PROVIDER, not of the identity, so two providers can
	// each carry their own (2026-08-20).
	cc.MaxInputTokens = inputBudget
	return llm.New(&cc)
}

// timeLoadLocation isolates the tz lookup (testable, no global import churn).
func timeLoadLocation(name string) (*time.Location, error) {
	return time.LoadLocation(name)
}
