package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/publicname"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

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

	requireToken := a.dashboard != nil && a.dashboard.AccessTokenRequired()

	llmSt := dashboard.LLMConfigState{
		Provider: c.LLM.Provider, Model: c.LLM.Model,
		TimeoutSeconds:         c.LLM.TimeoutSeconds,
		ProbeTimeoutSeconds:    c.LLM.ProbeTimeoutSeconds,
		RefusedRetryMaxSeconds: c.LLM.RefusedRetryMaxSeconds,
		RefusedRetryMaxDefault: refusedRetryMaxDefaultSeconds,
	}
	reg, err := a.loadProviders()
	speechSt := speechState(c, reg, err, a.speechEngines())
	speechSt.Spent = a.speechSpend()
	speechSt.Resets = a.speechResets(time.Now()).Format("2 January")
	speechSt.Speakers = a.speakerPolicyState()
	speechSt.OperatorSpeakers = append([]string(nil), c.Speech.OperatorSpeakers...)
	speechSt.Mode = a.voiceModeState()
	if err != nil {
		llmSt.Error = err.Error()
	} else if cc, entry, err := a.resolveLLMConfig(c, reg); err != nil {
		llmSt.Error = err.Error()
	} else {
		llmSt.ResolvedProvider = entry.Name
		llmSt.ResolvedModel = cc.Model
		llmSt.Endpoint = cc.Endpoint
		llmSt.APIKeyMasked = maskKey(cc.APIKey)
		llmSt.ThinkingBudget = entry.ThinkingBudget
		llmSt.MaxOutputTokens = entry.MaxOutputTokens
		llmSt.ContextLength = entry.ContextLength

		if budget, source := a.currentPromptBudget(); source != "" {
			llmSt.PromptBudget, llmSt.PromptBudgetSource = budget, string(source)
		}
		llmSt.ReasoningEffort = entry.ReasoningEffort

		dialect := llm.DialectFor(cc.Provider)

		llmSt.EffortPlan = llm.PlanEffort(dialect, entry.ReasoningEffort, effortLevelsFor(reg.effective(), entry, cc.Model)).Summary()
		llmSt.EffortModel = cc.Model
		switch pictures := a.imagePolicyForRegistry(reg, entry.Name, cc.Model); pictures.takes {
		case capYes:
			llmSt.ImagesShown = "yes"
			llmSt.ImageMaxBytes, llmSt.ImagesMaxBytes = pictures.maxBytes, pictures.maxTotal
			llmSt.ImageMaxSide, llmSt.ImagesMaxCount = pictures.maxSide, pictures.maxCount
		case capNo:
			llmSt.ImagesShown = "no"
		}

		if levels := effortLevelsFor(reg.effective(), entry, cc.Model); len(levels) > 0 {
			plan := llm.PlanEffort(dialect, entry.ReasoningEffort, levels)
			choice := &dashboard.EffortChoice{Levels: levels}
			if plan.Sent {
				choice.InForce = plan.Wire
			}
			declared := declaredEffortLevels(reg.effective(), cc.Model)
			for _, l := range levels {
				if !slices.Contains(declared, l) {
					choice.Checked = append(choice.Checked, l)
				}
			}
			llmSt.EffortChoice = choice
		}
		llmSt.ThinkingApplies = dialect == llm.DialectAnthropic
	}
	return &dashboard.ConfigState{
		Maintenance: dashboard.MaintenanceConfigState{Enabled: continuity.MaintenanceEnabled(c.Maintenance), BackupKeep: continuity.MaintenanceKeep(c.Maintenance)},
		Database:    a.databaseState(),
		LLM:         llmSt,
		Speech:      speechSt,
		Dashboard: dashboard.DashboardState{Host: c.Dashboard.Host, Port: c.Dashboard.Port, TLS: c.Dashboard.TLS, Origin: a.advertisedOrigin(), RequireToken: requireToken, TokenShort: a.dashboard != nil && a.dashboard.AccessTokenShort(), AccessCodeSeconds: accessCodeSeconds(c.Dashboard.AccessCodeSeconds),
			Settings: append(c.Dashboard.inForce(), c.Certificate.InForce()...)},
		PublicName:      a.publicNameState(c),
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

			Ring3MaxChars:             c.Prompt.Ring3MaxChars,
			SurfacingMaxChars:         c.Prompt.SurfacingMaxChars,
			SelfModelMaxTokens:        c.Prompt.SelfModelMaxTokens,
			DreamConversationMaxChars: c.Prompt.DreamConversationMaxChars,
			TensionsMaxChars:          c.Prompt.TensionsMaxChars,
			BriefMaxChars:             c.Prompt.BriefMaxChars,
			AlarmFirings:              c.Prompt.AlarmFirings,
			ExcerptChars:              c.Prompt.ExcerptChars,
			Ring3RecentExperiences:    c.Prompt.Ring3RecentExperiences,
		},
		Agency: dashboard.AgencyConfigState{
			PreferLocalForRoles: c.Agency.PreferLocalForRoles,
			RhythmSeconds:       c.Agency.RhythmSeconds,
			OutcomeWindow:       c.Agency.OutcomeWindow,
			TurnTokenBudget:     c.Agency.TurnTokenBudget,
			ToolSchemaRefusals:  c.Agency.ToolSchemaRefusals,
			TurnCheckpointCalls: c.Agency.TurnCheckpointCalls, TurnCheckpointGraceRounds: c.Agency.TurnCheckpointGraceRounds,
			MaxTurnContinuations:    c.Agency.MaxTurnContinuations,
			TurnCheckpointSlack:     c.Agency.TurnCheckpointSlack,
			ContinuationWallSeconds: c.Agency.ContinuationWallSeconds,
			AlarmWallSeconds:        c.Agency.AlarmWallSeconds,
			HarvestWallSeconds:      c.Agency.HarvestWallSeconds,
		},
		Cognition: a.cognitionState(c, reg),
		Updates:   dashboard.UpdatesConfigState{Automatic: c.Updates.Automatic},
		Logs: dashboard.LogsConfigState{
			Dir: c.Logs.Dir, MaxBackups: c.Logs.MaxBackups, CompressDays: c.Logs.CompressDays,
			MaxBytes: c.Logs.MaxBytes, MaxBytesDefault: logsink.DefaultMaxBytes,
			MaxTotalBytes: c.Logs.MaxTotalBytes, MaxTotalBytesDefault: logsink.DefaultMaxTotalBytes(c.Logs.MaxBytes, c.Logs.MaxBackups),
		},
		Timezone: c.Timezone,
		Contacts: contactStates(c.Contacts),
	}
}

func contactStates(contacts []Contact) []dashboard.ContactState {
	out := make([]dashboard.ContactState, 0, len(contacts))
	for _, c := range contacts {
		out = append(out, dashboard.ContactState{Name: c.Name, Channel: c.Channel, Address: c.Address, Wake: c.Wake, Operator: c.Operator})
	}
	return out
}

var errConfigMoved = errors.New("the configuration changed while this change was checked; nothing was saved — save it again")

func (a *App) commitConfig(orig Config, mutate func(*Config), custody func() error, settle func()) error {
	candidate := orig
	mutate(&candidate)
	published := false
	err := func() error {
		a.providers.custody.mu.Lock()
		defer a.providers.custody.mu.Unlock()
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		if !reflect.DeepEqual(*a.cfg, orig) {
			return errConfigMoved
		}
		if custody != nil {
			if err := custody(); err != nil {
				return err
			}
		}
		var err error
		published, err = a.persistLocked(&candidate, saveConfig)
		if published && settle != nil {
			settle()
		}
		return err
	}()
	if published {
		a.profileChanged()
	}
	return err
}

const ceilingBound = 1_000_000_000

func (a *App) applyConfigChange(changes map[string]any) (*dashboard.ConfigState, error) {
	return a.applyConfigChangeWith(changes, saveConfig)
}

func (a *App) applyConfigChangeWith(changes map[string]any, persist func(*Config) (bool, error)) (*dashboard.ConfigState, error) {
	var restart []string
	llmChanged := false
	substrateChanged := false
	pluginsChanged := false
	limitsChanged := false
	grantsChanged := false
	catalogChanged := false

	sttChanged := false
	ttsChanged := false
	contactsChanged := false

	routeChanged := false
	relayTuned := false
	prepareChanged := false

	noticesOn := false

	cognitionChanged := false

	logsChanged := false

	orig := a.configSnapshot()
	candidate := orig
	cfg := &candidate

	str := func(k string, v any) (string, error) {
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("%s: want a string", k)
		}
		return s, nil
	}
	integer := func(k string, v any) (int, error) {
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

		if strings.HasPrefix(key, "plugins.settings.") {
			if err := a.applyPluginSetting(cfg, key, v); err != nil {
				return nil, err
			}
			continue
		}

		if strings.HasPrefix(key, "plugins.grants.") {
			if err := applyPluginGrant(cfg, key, v); err != nil {
				return nil, err
			}
			grantsChanged = true
			continue
		}

		if handled, err := applyCognitionChange(cfg, key, v, integer); handled {
			if err != nil {
				return nil, err
			}
			cognitionChanged = true
			continue
		}
		switch key {
		case "overrides":

			o, err := doorOverrides(v, changes["overrides_digest"], orig.Overrides)
			if err != nil {
				return nil, err
			}
			cfg.Overrides = o

			llmChanged, cognitionChanged = true, true
		case "overrides_digest":
			if _, with := changes["overrides"]; !with {
				return nil, fmt.Errorf("overrides_digest: it is sent with a save of the overrides block, and alone changes nothing")
			}
		case "identity.db_optimize_levels", "identity.db_learn_dictionary":
			enabled, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("%s: want a boolean", key)
			}
			if key == "identity.db_optimize_levels" {
				cfg.Identity.DBOptimizeLevels = enabled
			} else {
				cfg.Identity.DBLearnDictionary = enabled
			}
		case "identity.db_format":
			format, err := str(key, v)
			if err != nil {
				return nil, err
			}
			if !store.ValidDatabaseFormat(format) {
				return nil, fmt.Errorf("identity.db_format must be empty, sqlite, or zstd")
			}
			cfg.Identity.DBFormat = format
			restart = append(restart, key)

		case "llm.provider":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}

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

			if s != cfg.LLM.Model {
				cfg.LLM.Model = s
				substrateChanged = true
				llmChanged = true
			}
		case "llm.probe_timeout_seconds":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n <= 0 {
				return nil, fmt.Errorf("llm.probe_timeout_seconds: must be positive")
			}
			cfg.LLM.ProbeTimeoutSeconds = n
		case "llm.refused_retry_max_seconds":

			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			if err := validRefusedRetryMax(n); err != nil {
				return nil, err
			}
			cfg.LLM.RefusedRetryMaxSeconds = n
		case "llm.timeout_seconds", "llm.retries", "llm.retry_backoff_ms":

			changed, err := tableNumber(cfg, key, v, integer)
			if err != nil {
				return nil, err
			}
			llmChanged = llmChanged || changed

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

		case "dashboard.expert":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("dashboard.expert: want a boolean")
			}
			cfg.Dashboard.Expert = b
		case "dashboard.access_code_seconds":

			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n != 0 && (n < dashboard.MinAccessCodeSeconds || n > dashboard.MaxAccessCodeSeconds) {
				return nil, fmt.Errorf("dashboard.access_code_seconds: 0 takes the default (%d); otherwise %d to %d", dashboard.DefaultAccessCodeSeconds, dashboard.MinAccessCodeSeconds, dashboard.MaxAccessCodeSeconds)
			}
			cfg.Dashboard.AccessCodeSeconds = n
		case "dashboard.refused_pauses":

			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n != 0 && (n < dashboard.MinRefusedPauses || n > dashboard.MaxRefusedPauses) {
				return nil, fmt.Errorf("dashboard.refused_pauses: 0 takes the default (%d); otherwise %d to %d", dashboard.DefaultRefusedPauses, dashboard.MinRefusedPauses, dashboard.MaxRefusedPauses)
			}
			cfg.Dashboard.RefusedPauses = n
		case "dashboard.unsigned_requests_per_second", "dashboard.unsigned_request_burst":

			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			unset, least, most := dashboard.DefaultUnsignedRequestsPerSecond, dashboard.MinUnsignedRequestsPerSecond, dashboard.MaxUnsignedRequestsPerSecond
			if key == "dashboard.unsigned_request_burst" {
				unset, least, most = dashboard.DefaultUnsignedRequestBurst, dashboard.MinUnsignedRequestBurst, dashboard.MaxUnsignedRequestBurst
			}
			if n != 0 && (n < least || n > most) {
				return nil, fmt.Errorf("%s: 0 takes the default (%d); otherwise %d to %d", key, unset, least, most)
			}
			if key == "dashboard.unsigned_request_burst" {
				cfg.Dashboard.UnsignedRequestBurst = n
			} else {
				cfg.Dashboard.UnsignedRequestsPerSecond = n
			}
		case "dashboard.record.page_rows", "dashboard.record.first_view_bytes":

			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			unset, least, most := dashboard.DefaultRecordPageRows, dashboard.MinRecordPageRows, dashboard.MaxRecordPageRows
			if key == "dashboard.record.first_view_bytes" {
				unset, least, most = dashboard.DefaultRecordFirstViewBytes, dashboard.MinRecordFirstViewBytes, dashboard.MaxRecordFirstViewBytes
			}
			if n != 0 && (n < least || n > most) {
				return nil, fmt.Errorf("%s: 0 takes the default (%d); otherwise %d to %d", key, unset, least, most)
			}
			if key == "dashboard.record.first_view_bytes" {
				cfg.Dashboard.Record.FirstViewBytes = n
			} else {
				cfg.Dashboard.Record.PageRows = n
			}
		case "dashboard.sent_requests.kept":

			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < dashboard.MinSentKept || n > dashboard.MaxSentKept {
				return nil, fmt.Errorf("%s: %d to %d (0 keeps none)", key, dashboard.MinSentKept, dashboard.MaxSentKept)
			}
			cfg.Dashboard.SentRequests.Kept = &n
		case "dashboard.sent_requests.max_bytes":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n != 0 && (n < dashboard.MinSentMaxBytes || n > dashboard.MaxSentMaxBytes) {
				return nil, fmt.Errorf("%s: 0 takes the default (%d); otherwise %d to %d", key, dashboard.DefaultSentMaxBytes, dashboard.MinSentMaxBytes, dashboard.MaxSentMaxBytes)
			}
			cfg.Dashboard.SentRequests.MaxBytes = n

		case "dashboard.channels":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("dashboard.channels: want a boolean")
			}
			cfg.Dashboard.Channels = &b
		case "dashboard.direct_wait_ms", "dashboard.direct_ready_allowance_ms", "dashboard.channel_sessions", "dashboard.channel_queue_bytes", "dashboard.channel_stall_ms":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			bounds := map[string][3]int{
				"dashboard.direct_wait_ms":            {dashboard.DefaultDirectWaitMS, dashboard.MinDirectWaitMS, dashboard.MaxDirectWaitMS},
				"dashboard.direct_ready_allowance_ms": {dashboard.DefaultDirectReadyAllowanceMS, dashboard.MinDirectReadyAllowanceMS, dashboard.MaxReadyAllowanceMS},
				"dashboard.channel_sessions":          {dashboard.DefaultChannelSessions, dashboard.MinChannelSessions, dashboard.MaxChannelSessions},
				"dashboard.channel_queue_bytes":       {dashboard.DefaultChannelQueueBytes, dashboard.MinChannelQueueBytes, dashboard.MaxChannelQueueBytes},
				"dashboard.channel_stall_ms":          {dashboard.DefaultChannelStallMS, dashboard.MinChannelStallMS, dashboard.MaxChannelStallMS},
			}[key]
			if n != 0 && (n < bounds[1] || n > bounds[2]) {
				return nil, fmt.Errorf("%s: 0 takes the default (%d); otherwise %d to %d", key, bounds[0], bounds[1], bounds[2])
			}
			switch key {
			case "dashboard.direct_wait_ms":
				cfg.Dashboard.DirectWaitMS = n
			case "dashboard.direct_ready_allowance_ms":
				cfg.Dashboard.DirectReadyAllowanceMS = n
			case "dashboard.channel_sessions":
				cfg.Dashboard.ChannelSessions = n
			case "dashboard.channel_queue_bytes":
				cfg.Dashboard.ChannelQueueBytes = n
			default:
				cfg.Dashboard.ChannelStallMS = n
			}
		case "dashboard.ice_servers":

			var list *[]dashboard.ICEServer
			if v != nil {
				raw, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("dashboard.ice_servers: %w", err)
				}
				var servers []dashboard.ICEServer
				if err := json.Unmarshal(raw, &servers); err != nil {
					return nil, fmt.Errorf("dashboard.ice_servers: want a list of servers, each with urls, or null to follow the relay")
				}
				if err := dashboard.CheckICEServers(servers); err != nil {
					return nil, err
				}
				list = &servers
			}
			cfg.Dashboard.ICEServers = list
		case "dashboard.carried_idle_seconds":

			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n != 0 && (n < dashboard.MinCarriedIdleSeconds || n > dashboard.MaxCarriedIdleSeconds) {
				return nil, fmt.Errorf("dashboard.carried_idle_seconds: 0 takes the default (%d); otherwise %d to %d", dashboard.DefaultCarriedIdleSeconds, dashboard.MinCarriedIdleSeconds, dashboard.MaxCarriedIdleSeconds)
			}
			cfg.Dashboard.CarriedIdleSeconds = n
			restart = append(restart, key)
		case "dashboard.notices_off_dashboard":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("dashboard.notices_off_dashboard: want a boolean")
			}
			cfg.Dashboard.NoticesOffDashboard, noticesOn = b, b
		case "agency.prefer_local_for_roles":

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
			if err := validDashboardPort(n); err != nil {
				return nil, err
			}
			cfg.Dashboard.Port = n
			restart = append(restart, key)

		case "certificate.route_mode", "certificate.relay_endpoint":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			s = strings.TrimSpace(s)
			if key == "certificate.route_mode" {
				routeChanged = routeChanged || s != cfg.Certificate.RouteMode
				cfg.Certificate.RouteMode = s
			} else {
				routeChanged = routeChanged || s != cfg.Certificate.RelayEndpoint
				cfg.Certificate.RelayEndpoint = s
			}

		case "certificate.prepare", "certificate.prepare_certificate":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("%s: want a boolean", key)
			}
			if key == "certificate.prepare" {
				cfg.Certificate.Prepare = &b
			} else {
				cfg.Certificate.PrepareCertificate = b
			}

			prepareChanged = true

		case "certificate.relay_ping_seconds", "certificate.relay_backoff_base_ms", "certificate.relay_backoff_max_ms", "certificate.relay_answer_seconds",
			"certificate.relay_carried_max", "certificate.relay_attaches_per_second", "certificate.relay_attach_burst":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if err := publicname.CheckRelayTuning(key, n); err != nil {
				return nil, err
			}
			switch key {
			case "certificate.relay_ping_seconds":
				cfg.Certificate.RelayPingSeconds = n
			case "certificate.relay_backoff_base_ms":
				cfg.Certificate.RelayBackoffBaseMS = n
			case "certificate.relay_backoff_max_ms":
				cfg.Certificate.RelayBackoffMaxMS = n
			case "certificate.relay_answer_seconds":
				cfg.Certificate.RelayAnswerSeconds = n
			case "certificate.relay_carried_max":
				cfg.Certificate.RelayCarriedMax = n
			case "certificate.relay_attaches_per_second":
				cfg.Certificate.RelayAttachesPerSecond = n
			default:
				cfg.Certificate.RelayAttachBurst = n
			}
			relayTuned = true

		case "certificate.direct_addresses":
			var list *[]string
			if v != nil {
				raw, ok := v.([]any)
				if !ok {
					return nil, fmt.Errorf("certificate.direct_addresses: want a list of IP addresses, or null to work them out")
				}
				out := make([]string, 0, len(raw))
				for _, e := range raw {
					s, ok := e.(string)
					if !ok {
						return nil, fmt.Errorf("certificate.direct_addresses: want a list of IP addresses, or null to work them out")
					}
					out = append(out, strings.TrimSpace(s))
				}
				if err := publicname.CheckDirectAddresses(out); err != nil {
					return nil, err
				}
				list = &out
			}
			routeChanged = routeChanged || !reflect.DeepEqual(cfg.Certificate.DirectAddresses, list)
			cfg.Certificate.DirectAddresses = list
		case "logs.dir":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Logs.Dir = s
			restart = append(restart, key)
		case "maintenance.enabled":

			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("maintenance.enabled: must be a boolean")
			}
			cfg.Maintenance.Enabled = &b
		case "maintenance.backup_keep":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < 1 || n > 365 {
				return nil, fmt.Errorf("maintenance.backup_keep: between 1 and 365 daily snapshots (each holds the whole database)")
			}
			cfg.Maintenance.BackupKeep = n
		case "maintenance.on_demand_spacing_seconds":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < 0 || n > 86400 {
				return nil, fmt.Errorf("maintenance.on_demand_spacing_seconds: 0 takes the default (600); at most 86400")
			}
			cfg.Maintenance.OnDemandSpacingSeconds = n
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
		case "logs.max_days":
			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n < -1 {
				return nil, fmt.Errorf("logs.max_days: -1 turns the day floor off (the count decides alone), 0 uses the default (30), a positive number is the age in days")
			}
			cfg.Logs.MaxDays = n
			restart = append(restart, key)
		case "logs.max_bytes":

			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			if err := validLogMaxBytes(int64(n)); err != nil {
				return nil, err
			}
			cfg.Logs.MaxBytes = int64(n)
			logsChanged = true
			restart = append(restart, key)
		case "logs.max_total_bytes":

			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			cfg.Logs.MaxTotalBytes = int64(n)
			logsChanged = true
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

		case "plugins.runtime.max_installed_bytes", "plugins.runtime.max_file_bytes", "plugins.runtime.max_compressed_bytes":

			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			if n < 0 {
				return nil, fmt.Errorf("%s: must be positive, or 0 to clear it so that free disk decides", key)
			}
			if retiredByteDefault(key, int64(n)) {
				return nil, fmt.Errorf("%s: %d (%d MiB) was the former default, which every identity's file was given and is read as not set; to hold the ceiling near it, set another value, such as %d MiB or %d MiB, or clear it so that free disk decides", key, n, n>>20, n>>20-1, n>>20+1)
			}
			r := &cfg.Plugins.Runtime
			switch key {
			case "plugins.runtime.max_installed_bytes":
				r.MaxInstalledBytes = int64(n)
			case "plugins.runtime.max_file_bytes":
				r.MaxFileBytes = int64(n)
			case "plugins.runtime.max_compressed_bytes":
				r.MaxCompressedBytes = int64(n)
			}
			limitsChanged = true
		case "plugins.runtime.max_files", "plugins.runtime.max_depth", "plugins.runtime.roots_kept":

			n, err := integer(key, v)
			if err != nil {
				return nil, err
			}
			if n <= 0 {
				return nil, fmt.Errorf("%s: must be positive", key)
			}
			r := &cfg.Plugins.Runtime
			switch key {
			case "plugins.runtime.max_files":
				r.MaxFiles = n
			case "plugins.runtime.max_depth":
				r.MaxDepth = n
			case "plugins.runtime.roots_kept":
				r.RootsKept = n
			}
			limitsChanged = true
		case "plugins.runtime.hardware_query_ms":

			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			if err := validHardwareQueryMs(n); err != nil {
				return nil, err
			}
			cfg.Plugins.Runtime.HardwareQueryMs = n
			limitsChanged = true

		case "speech.stt.monthly_minutes", "speech.tts.monthly_characters":
			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			if n < 0 {
				return nil, fmt.Errorf("%s: a ceiling cannot be negative", key)
			}
			if n > ceilingBound {
				return nil, fmt.Errorf("%s: a ceiling of %d is past any bill; %d is the most it can be", key, n, ceilingBound)
			}
			if key == "speech.stt.monthly_minutes" {
				cfg.Speech.STT.MonthlyMinutes = n
			} else {
				cfg.Speech.TTS.MonthlyCharacters = n
			}

		case "speech.open_wait_seconds":
			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			if err := validVoiceOpenWait(n); err != nil {
				return nil, err
			}
			cfg.Speech.OpenWaitSeconds = n

		case "speech.heard_history.capacity", "speech.heard_history.retention_seconds",
			"speech.heard_history.text_bytes", "speech.heard_history.page_rows":
			hs, _ := heardSettingFor(key)
			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			if err := hs.check(n); err != nil {
				return nil, err
			}
			*hs.field(&cfg.Speech.HeardHistory) = n

		case meaningSourceKey:
			choice, err := str(key, v)
			if err != nil {
				return nil, err
			}
			if err := validMeaningChoice(choice); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			if id, isPlugin := strings.CutPrefix(choice, meaningChoicePlugin); isPlugin {
				running := false
				for _, ap := range a.embeddingsPlugins() {
					running = running || ap.ID == id
				}
				if !running {
					return nil, fmt.Errorf("%s: %s is not running as a source of vectors (the dashboard's Plugins view)", key, id)
				}
			}
			cfg.Memory.MeaningSource = choice
		case "speech.operator_speakers":
			ids, err := operatorSpeakersFromChange(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			cfg.Speech.OperatorSpeakers = ids
		case "speech.speakers":
			pol, err := speakerPolicyFromChange(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			pol.Revision = cfg.Speech.Speakers.Revision + 1
			cfg.Speech.Speakers = pol

		case "speech.mode":
			m, err := voiceModeFromChange(v, cfg.Speech.Mode)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			m.Revision = cfg.Speech.Mode.Revision + 1
			cfg.Speech.Mode = m
		case "speech.stt.provider", "speech.stt.model", "speech.stt.language",
			"speech.tts.provider", "speech.tts.model", "speech.tts.voice":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			s = strings.TrimSpace(s)
			switch key {
			case "speech.stt.provider":
				cfg.Speech.STT.Provider = s
			case "speech.stt.model":
				cfg.Speech.STT.Model = s
			case "speech.stt.language":
				cfg.Speech.STT.Language = s
			case "speech.tts.provider":
				cfg.Speech.TTS.Provider = s
			case "speech.tts.model":
				cfg.Speech.TTS.Model = s
			case "speech.tts.voice":
				cfg.Speech.TTS.Voice = s
			}
			if strings.HasPrefix(key, "speech.stt.") {
				sttChanged = true
			} else {
				ttsChanged = true
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
		case "genesis.fetch_attempts", "genesis.fetch_timeout_ms":

			n := 0
			if v != nil {
				got, err := integer(key, v)
				if err != nil {
					return nil, err
				}
				n = got
			}
			g := cfg.Genesis
			if key == "genesis.fetch_attempts" {
				g.FetchAttempts = n
			} else {
				g.FetchTimeoutMs = n
			}
			if err := validGenesisPatience(g); err != nil {
				return nil, err
			}
			cfg.Genesis = g
			restart = append(restart, key)
		case "genesis.bootstrap_url":
			s, err := str(key, v)
			if err != nil {
				return nil, err
			}
			cfg.Genesis.BootstrapURL = s
			restart = append(restart, key)
		case "prompt.max_tokens", "prompt.max_tool_result_chars":

			if _, err := tableNumber(cfg, key, v, integer); err != nil {
				return nil, err
			}
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

			cognitionChanged = true

		case "contacts":
			list, err := contactsFromChange(v)
			if err != nil {
				return nil, err
			}
			cfg.Contacts, cfg.contactsPresent = list, true
			contactsChanged = true

		case "updates.automatic":
			b, ok := v.(bool)
			if !ok {
				return nil, fmt.Errorf("updates.automatic: want a boolean")
			}
			cfg.Updates.Automatic = b

		default:

			return nil, fmt.Errorf("%s is not an operator-settable field (substrate-owned or unknown — rejected)", key)
		}
	}

	if !reflect.DeepEqual(orig.Plugins.Grants, cfg.Plugins.Grants) {
		grantsChanged = true
	}

	if routeChanged {
		if err := a.routeRefusal(orig, *cfg); err != nil {
			return nil, err
		}
	}
	if logsChanged {
		if err := validLogMaxTotalBytes(cfg.Logs.MaxTotalBytes, cfg.Logs.MaxBytes); err != nil {
			return nil, err
		}
	}
	if cognitionChanged {

		if err := validCognition(*cfg); err != nil {
			return nil, err
		}
	}
	if relayTuned {
		if err := cfg.Certificate.CheckRelayBackoff(); err != nil {
			return nil, err
		}
	}

	if noticesOn && len(operatorLines(cfg.Contacts)) == 0 {
		return nil, &NoticeRouteRefusal{Requirement: needsOperatorLine}
	}

	var validatedClient *llm.Client
	var measured substrateCapability
	probed := false
	var resolvedEntry providerEntry
	var resolvedRegistry *providerRegistry
	var providerPath string
	if llmChanged {
		providerPath = a.providersPath()
		reg, rerr := loadProvidersFile(providerPath)
		if rerr != nil {
			return nil, fmt.Errorf("substrate refused: %w", rerr)
		}
		cc, entry, rerr := a.resolveLLMConfig(*cfg, reg)
		if rerr != nil {
			return nil, fmt.Errorf("substrate refused: %w", rerr)
		}
		resolvedRegistry = reg
		candidateBudget, _ := promptBudgetFor(entry, promptCeilingFor(*cfg, entry, cc.Model))
		validatedClient = a.newLLMClient(cc, candidateBudget)
		if substrateChanged {
			m, err := a.probeSubstrate(validatedClient, cc, entry, reg, cfg.LLM.ProbeTimeoutSeconds)
			if err != nil {
				return nil, err
			}
			measured, probed = m, true
		}
		resolvedEntry = entry
	}

	sttNetwork := sttChanged && strings.TrimSpace(cfg.Speech.STT.Provider) != "" && !a.engineInstalled(cfg.Speech.STT.Provider)
	ttsNetwork := ttsChanged && strings.TrimSpace(cfg.Speech.TTS.Provider) != "" && !a.engineInstalled(cfg.Speech.TTS.Provider)
	if sttNetwork || ttsNetwork {
		if resolvedRegistry == nil {
			providerPath = a.providersPath()
			reg, rerr := loadProvidersFile(providerPath)
			if rerr != nil {
				return nil, fmt.Errorf("speech refused: %w", rerr)
			}
			resolvedRegistry = reg
		}
		if err := a.checkSpeech(cfg, resolvedRegistry, sttNetwork, ttsNetwork); err != nil {
			return nil, err
		}
	}

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
			return errConfigMoved
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
		var err error
		configPublished, err = a.persistLocked(cfg, persist)
		if configPublished && cognitionChanged && a.live.Load() {

			a.applyAgency(*cfg)
			if cfg.Agency.RhythmSeconds != orig.Agency.RhythmSeconds && a.timeFac != nil {
				if rerr := a.armRhythm(cfg.Agency.RhythmSeconds); rerr != nil {
					logsink.Warn("config.refusal", "metabolism rhythm not re-armed, it keeps its cadence until the next boot: %v", rerr)
				}
			}
			a.applyCognition(*cfg)
		}
		if configPublished {
			a.publishVoiceMode(cfg.Speech.Mode)

			if a.store != nil && !reflect.DeepEqual(orig.Speech.OperatorSpeakers, cfg.Speech.OperatorSpeakers) {
				a.store.SetOperatorVoices(cfg.Speech.OperatorSpeakers)
			}

			if holdTurn {
				a.activateLLMRuntime(validatedClient, resolvedEntry, *cfg)
				if probed {
					a.setSubstrateCapability(measured)
				}
			}
		}
		return err
	}()
	if holdTurn {
		a.releaseTurn()
	}
	doorStep("published")
	if configPublished && pluginsChanged {
		a.pokePluginSweep()
	}
	if configPublished && limitsChanged {
		if err := a.applyPluginLimits(); err != nil {
			logsink.Warn("config.refusal", "plugin limits not applied live: %v", err)
		}
	}
	if configPublished && pluginSettingsMoved(&orig, cfg) {

		a.runBackground(a.reconcileLateModels)
	}
	if configPublished && catalogChanged {
		a.catalog.Poke()
	}
	if configPublished && grantsChanged {
		a.publishGrants(*cfg)
	}
	if configPublished && contactsChanged {

		a.pokeOutbox()
	}
	if configPublished && (routeChanged || prepareChanged) {

		a.pn.Signal()
	}
	if configPublished && !reflect.DeepEqual(orig.Speech.Mode, cfg.Speech.Mode) {

		a.voiceModeCommitted(orig.Speech.Mode, cfg.Speech.Mode)
	}
	if commitErr != nil {
		return nil, commitErr
	}

	st := a.configState()
	st.RestartRequired = restart
	return st, nil
}

func validDashboardPort(n int) error {
	if n < 1 || n > 65535 {
		return fmt.Errorf("dashboard.port: %d is out of range (1-65535)", n)
	}
	return nil
}

func SetDashboardPort(path string, port int) error {
	if err := validDashboardPort(port); err != nil {
		return err
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		return err
	}
	cfg.Dashboard.Port = port
	published, err := saveConfig(cfg)
	switch {
	case err != nil && published:
		return fmt.Errorf("the port was written to %s, but the directory durability is unconfirmed: %w", path, err)
	case err != nil:
		return fmt.Errorf("the port was not written to %s: %w", path, err)
	}
	return nil
}

func accessCodeSeconds(set int) int {
	if set <= 0 {
		return dashboard.DefaultAccessCodeSeconds
	}
	return set
}

func (a *App) publicNameState(cfg Config) dashboard.PublicNameState {
	st := a.pn.State()
	if st.Origin == "" {
		return st
	}
	for name, p := range cfg.Plugins.AuthProfiles {
		if strings.HasPrefix(p.RedirectURI, st.Origin+"/") {
			st.MovesWith = append(st.MovesWith, "sign-in profile "+name)
		}
	}
	if cfg.Dashboard.Origin == "" {
		a.pluginMu.Lock()
		for _, p := range a.plugins {
			if len(p.Webhooks) > 0 {
				st.MovesWith = append(st.MovesWith, "webhooks of "+p.ID)
			}
		}
		a.pluginMu.Unlock()
	}
	sort.Strings(st.MovesWith)
	return st
}

var errRelayNeedsToken = errors.New("certificate.route_mode: a relay carries the public internet to this dashboard, and the running dashboard is not asking for an access token — turn the token on first (Settings → Dashboard → Ask for an access token), then choose the relay")

type RelayTokenShort struct{ Length, Line int }

func (e *RelayTokenShort) Error() string {
	return fmt.Sprintf("certificate.route_mode: a relay exposes this dashboard's sign-in to the internet, and its access token is %d characters long, shorter than %d — Rotate access token (Settings → Dashboard) makes a new one, then choose the relay", e.Length, e.Line)
}

func (a *App) routeRefusal(was, cfg Config) error {
	mode, err := publicname.RouteMode(cfg.Certificate)
	if err != nil {
		return fmt.Errorf("certificate.%w", err)
	}
	if mode == publicname.RouteDirect && !reflect.DeepEqual(was.Certificate.DirectAddresses, cfg.Certificate.DirectAddresses) {
		return a.pn.DirectChoiceRefusal(publicNameSettingsOf(cfg))
	}
	if mode != publicname.RouteRelay {
		return nil
	}
	if a.dashboard == nil || !a.dashboard.AccessTokenRequired() {
		return errRelayNeedsToken
	}
	if a.dashboard.AccessTokenShort() {
		return &RelayTokenShort{Length: a.dashboard.AccessTokenLength(), Line: dashboard.ShortAccessToken}
	}
	return a.pn.RelayChoiceRefusal(publicNameSettingsOf(was), publicNameSettingsOf(cfg))
}

func probeTimeout(cc llm.ClientConfig, ceilingSeconds int) time.Duration {
	return checkTimeout(cc.TimeoutSeconds, ceilingSeconds)
}

func checkTimeout(serviceSeconds, ceilingSeconds int) time.Duration {
	if ceilingSeconds <= 0 {
		ceilingSeconds = 45
	}
	ceiling := time.Duration(ceilingSeconds) * time.Second
	if serviceSeconds > 0 {
		if d := time.Duration(serviceSeconds) * time.Second; d < ceiling {
			return d
		}
	}
	return ceiling
}

type substrateProbeClient interface {
	Chat(context.Context, []llm.Message, llm.ChatOptions) (*llm.Response, error)
}

func (a *App) probeSubstrate(client substrateProbeClient, cc llm.ClientConfig, entry providerEntry, reg *providerRegistry, timeoutSeconds int) (substrateCapability, error) {
	if a.bgCtx == nil {
		return substrateCapability{}, fmt.Errorf("substrate refused: application lifecycle is unavailable")
	}
	bound := probeTimeout(cc, timeoutSeconds)
	vctx, cancel := context.WithTimeout(a.bgCtx, bound)
	defer cancel()

	type toolAnswer struct {
		state capState
		note  string
	}
	toolCh := make(chan toolAnswer, 1)
	go func() {
		state, note := a.probeToolSelection(vctx, client, entry, cc.Model)
		toolCh <- toolAnswer{state, note}
	}()

	modalityURL := modelModalitiesURL(entry, cc.Model, reg.modelCatalogueURL())

	mctx, mcancel := context.WithTimeout(a.bgCtx, 2*min(bound, modalityTimeout))
	accepted := false
	defer func() {
		if !accepted {
			mcancel()
		}
	}()
	guard := modalityGuard
	discover := selectedModelDiscovery
	if !a.runBackground(func() {
		defer mcancel()
		a.refreshSelectedModalities(mctx, entry, cc.Model, reg, guard, discover)
	}) {
		mcancel()
	}

	resp, err := client.Chat(vctx, []llm.Message{{
		Role: "user", Content: "Reply with the single word OK.",
	}}, llm.ChatOptions{ThinkingBudget: cc.ThinkingBudget})
	if err == nil {

		err = vctx.Err()
		if deadline, ok := vctx.Deadline(); err == nil && ok && !time.Now().Before(deadline) {
			err = context.DeadlineExceeded
		}
	}
	if err != nil {
		if cc.APIKey == "" && cc.Credential == nil {
			return substrateCapability{}, fmt.Errorf("substrate refused: provider %q has no configured credential; if it requires one, store it in Settings → Providers → %s; current substrate kept: %w", entry.Name, entry.Name, err)
		}

		if errors.Is(err, context.DeadlineExceeded) {
			return substrateCapability{}, fmt.Errorf("substrate refused: provider %q model %q did not answer a minimal inference request within %s; current substrate kept: %w", entry.Name, cc.Model, bound, err)
		}
		return substrateCapability{}, fmt.Errorf("substrate refused: provider %q model %q cannot complete a minimal inference request; current substrate kept: %w", entry.Name, cc.Model, err)
	}
	if len(resp.Choices) == 0 || strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		return substrateCapability{}, fmt.Errorf("substrate refused: provider %q model %q returned no visible answer to a minimal inference request; current substrate kept", entry.Name, cc.Model)
	}

	tool := <-toolCh
	accepted = true
	if tool.note != "" {
		logsink.Info("boot.decision", "%s", tool.note)
	}
	return substrateCapability{
		provider:    entry.Name,
		model:       cc.Model,
		toolCalls:   tool.state,
		note:        tool.note,
		modalityURL: modalityURL,
		checkedAt:   time.Now(),
	}, nil
}

func (a *App) probeToolSelection(ctx context.Context, client substrateProbeClient, entry providerEntry, model string) (capState, string) {
	tool := llm.ToolDefinition{Type: "function"}
	tool.Function.Name = "report_ready"
	tool.Function.Description = "Report that you are ready. Call this to answer."
	tool.Function.Parameters = map[string]any{
		"type":       "object",
		"properties": map[string]any{"ready": map[string]any{"type": "boolean"}},
		"required":   []string{"ready"},
	}
	resp, err := client.Chat(ctx, []llm.Message{{
		Role: "user", Content: "Call report_ready with ready=true. Do not answer in words.",
	}}, llm.ChatOptions{Tools: []llm.ToolDefinition{tool}})
	if err != nil {

		return capUnknown, fmt.Sprintf("provider %q model %q rejected a tool-bearing request (%v) — this identity acts through tools; expect it to be unable to work", entry.Name, model, err)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
		return capNo, fmt.Sprintf("provider %q model %q answered a tool-bearing request WITHOUT calling the tool — it may be unable to act, and this identity does most of its work through tools", entry.Name, model)
	}
	return capYes, ""
}

func (a *App) newLLMClient(cc llm.ClientConfig, inputBudget int) *llm.Client {

	cc.MaxInputTokens = inputBudget
	return llm.New(&cc)
}

func timeLoadLocation(name string) (*time.Location, error) {
	return time.LoadLocation(name)
}

func (a *App) advertisedOrigin() string {
	if a.dashboard == nil {
		return ""
	}
	return a.dashboard.AdvertisedOrigin()
}

func (a *App) wireSettingsHooks(h *dashboard.WSHandler) {
	h.GetConfig = a.configState
	h.SetConfig = a.applyConfigChange
	h.GetSandbox = a.sandboxState
	h.SetSandbox = a.setSandboxRoots
	h.SetSandboxOff = a.setSandboxOff
	h.GetTools = func() []dashboard.ToolState {
		states := a.toolReg.ToolStates()
		out := make([]dashboard.ToolState, len(states))
		for i, ts := range states {
			out[i] = dashboard.ToolState{Name: ts.Name, Description: ts.Description, Enabled: ts.Enabled, Plugin: ts.Plugin}
		}
		return out
	}
	h.SetToolFunc = a.setToolEnabled
	h.ListLogs = a.listLogs
	h.TailLogs = a.tailLogs
	h.DatabaseExport = a.continuity.ExportDatabase
	h.UpdateCheck = a.checkForUpdateNow
	h.Uninstall = a.openUninstallApp
}
