package app

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type cognitionSetting struct {
	key       string
	min, max  int
	unit      string
	floor     string
	zero      bool
	signed    bool
	perModel  string
	unlimited bool

	card string
	def  func(Config) limits.Ceiling
	num  func(*Config) *int
	ceil func(*Config) *limits.Ceiling
}

func (s cognitionSetting) place() string {
	switch s.card {
	case "agency":
		return "Settings → Agency"
	case "prompt":
		return "Settings → Prompt"
	case "substrate":
		return "Settings → Substrate"
	}
	return "Settings → Identity → Cognition"
}

func (s cognitionSetting) get(c *Config) limits.Ceiling {
	if s.ceil != nil {
		return *s.ceil(c)
	}
	return limits.Of(*s.num(c))
}

func (s cognitionSetting) set(c *Config, v limits.Ceiling) {
	if s.ceil != nil {
		*s.ceil(c) = v
		return
	}
	*s.num(c) = v.N
}

type CognitionRefusal struct {
	Key         string
	Requirement string
}

func (e *CognitionRefusal) Error() string { return e.Key + ": " + e.Requirement }

func (s cognitionSetting) value(c Config) limits.Ceiling {
	return s.get(&c).Or(s.def(c))
}

func (s cognitionSetting) n(c Config) int { return s.value(c).N }

func (s cognitionSetting) check(c Config, cv limits.Ceiling) error {
	if cv.Unlimited {
		if s.unlimited {
			return nil
		}
		return &CognitionRefusal{Key: s.key, Requirement: fmt.Sprintf("%s is not a value it takes — it is a number between %d and %d %s", limits.Word, s.min, s.max, s.unit)}
	}
	v := cv.N
	if v == 0 && s.zero {
		return nil
	}
	if v < s.min || v > s.max {
		dflt := ""
		if s.zero {
			dflt = fmt.Sprintf(", or 0 for the default (%s)", s.def(c))
		}
		why := ""
		if v < s.min && s.floor != "" {
			why = fmt.Sprintf(" — %d is %s", s.min, s.floor)
		}
		return &CognitionRefusal{Key: s.key, Requirement: fmt.Sprintf("%d is out of bounds — between %d and %d %s%s%s", v, s.min, s.max, s.unit, dflt, why)}
	}
	return nil
}

func fixed(n int) func(Config) limits.Ceiling {
	return func(Config) limits.Ceiling { return limits.Of(n) }
}

const defaultRhythmSeconds = 600

var (
	defaultSelfModelEveryMinutes      = int(cognitive.DefaultSelfModelEvery / time.Minute)
	defaultIdentityReviewEveryMinutes = int(cognitive.DefaultIdentityReviewEvery / time.Minute)
)

var rhythmSecondsSetting = cognitionSetting{key: "agency.rhythm_seconds", min: 1, max: 86400, unit: "seconds",
	def: fixed(defaultRhythmSeconds), num: func(c *Config) *int { return &c.Agency.RhythmSeconds }}

var spacingSetting = cognitionSetting{key: "cognition.consolidate_spacing_seconds", min: 2, max: 3 * 86400, unit: "seconds", zero: true,

	def: func(c Config) limits.Ceiling {
		return limits.Of(max(int(cognitive.DefaultSpacing/time.Second), 3*rhythmSecondsSetting.n(c)))
	},
	num: func(c *Config) *int { return &c.Cognition.ConsolidateSpacingSeconds }}

var checkpointCallsSetting = cognitionSetting{key: "agency.turn_checkpoint_calls", min: 1, max: 1_000_000_000, unit: "tool calls", zero: true, perModel: "model", unlimited: true,
	def:  fixed(limits.DefaultCheckpointCalls),
	ceil: func(c *Config) *limits.Ceiling { return &c.Agency.TurnCheckpointCalls }}

var graceRoundsSetting = cognitionSetting{key: "agency.turn_checkpoint_grace_rounds", min: 1, max: 1_000_000, unit: "rounds", zero: true, perModel: "model",
	def: fixed(limits.DefaultGraceRounds),
	num: func(c *Config) *int { return &c.Agency.TurnCheckpointGraceRounds }}

var slackSetting = cognitionSetting{key: "agency.turn_checkpoint_slack", min: 1, max: 1_000, unit: "times the estimate", zero: true, perModel: "model",
	def: fixed(limits.DefaultSlack),
	num: func(c *Config) *int { return &c.Agency.TurnCheckpointSlack }}

var continuationsSetting = cognitionSetting{key: "agency.max_turn_continuations", min: 1, max: 1_000_000, unit: "turns", zero: true, perModel: "model", unlimited: true,
	def:  fixed(limits.DefaultContinuations),
	ceil: func(c *Config) *limits.Ceiling { return &c.Agency.MaxTurnContinuations }}

var continuationWallSetting = cognitionSetting{key: "agency.continuation_wall_seconds", min: 1, max: 86_400, unit: "seconds", zero: true, perModel: "model",
	def: fixed(limits.DefaultContinuationWall),
	num: func(c *Config) *int { return &c.Agency.ContinuationWallSeconds }}

var alarmWallSetting = cognitionSetting{key: "agency.alarm_wall_seconds", min: 1, max: 86_400, unit: "seconds", zero: true, perModel: "model",
	def: fixed(limits.DefaultAlarmWall),
	num: func(c *Config) *int { return &c.Agency.AlarmWallSeconds }}

var harvestWallSetting = cognitionSetting{key: "agency.harvest_wall_seconds", min: 1, max: 86_400, unit: "seconds", zero: true, perModel: "model",
	def: fixed(limits.DefaultHarvestWall),
	num: func(c *Config) *int { return &c.Agency.HarvestWallSeconds }}

func (a *App) turnWall(s cognitionSetting) time.Duration {
	return time.Duration(a.residentLimitsFor(a.configSnapshot()).N(s)) * time.Second
}

func graceFits(c Config) bool {
	_, rounds := graceCeiling(c)
	grace := c.Agency.TurnCheckpointGraceRounds
	return grace == 0 || rounds.Unlimited || rounds.N <= 0 || grace < rounds.N-1
}

func graceCeiling(c Config) (key string, rounds limits.Ceiling) {
	key, rounds = "agency.max_tool_rounds", c.Agency.MaxToolRounds
	child := c.Agency.SubagentMaxToolRounds
	if agencyOn(c.Agency.SubagentContinuation) && (child.Unlimited || child.N > 0) && ((!rounds.Unlimited && rounds.N <= 0) || rounds.Above(child)) {
		key, rounds = "agency.subagent_max_tool_rounds", child
	}
	return key, rounds
}

func graceShort(c Config) (key string, rounds, most int, who string) {
	key, ceiling := graceCeiling(c)
	rounds = ceiling.N
	most = rounds - 2
	grace, resident := c.Agency.TurnCheckpointGraceRounds, c.Agency.MaxToolRounds
	switch {
	case key == "agency.max_tool_rounds":
		who = "no turn of the identity's would be checkpointed or continued"
	case resident.Unlimited || resident.N <= 0 || grace < resident.N-1:
		who = "no leg of a child's would be checkpointed or continued, though the identity's own turns still would"
	default:
		who = "neither the identity's turns nor a child's legs would be checkpointed or continued"
	}
	return key, rounds, most, who
}

var alarmFiringsSetting = cognitionSetting{key: "prompt.alarm_firings", min: 1, max: 100_000, unit: "firings", zero: true,
	def: fixed(identity.DefaultAlarmFirings),
	num: func(c *Config) *int { return &c.Prompt.AlarmFirings }}

func (a *App) alarmFirings() int { return alarmFiringsSetting.n(a.configSnapshot()) }

const minReplyChars = 600

var cognitionSettings = []cognitionSetting{
	rhythmSecondsSetting,
	spacingSetting,
	{key: "cognition.consolidate_batch", min: 1, max: 200, unit: "experiences", zero: true, def: fixed(cognitive.DefaultConsolidateBatch),
		num: func(c *Config) *int { return &c.Cognition.ConsolidateBatch }},
	{key: "cognition.consolidate_max_ops", min: 2, max: 512, unit: "operations", zero: true, def: fixed(cognitive.DefaultConsolidateMaxOps),
		floor: "what replacing one belief takes: an upsert of the new and a supersede of the old",
		num:   func(c *Config) *int { return &c.Cognition.ConsolidateMaxOps }},
	{key: "agency.outcome_window", min: 1, max: 1_000_000, unit: "records", def: fixed(cognitive.DefaultOutcomeWindow),
		num: func(c *Config) *int { return &c.Agency.OutcomeWindow }},
	{key: "cognition.self_model_every_minutes", min: 1, max: 1_000_000, unit: "lived minutes", zero: true, def: fixed(defaultSelfModelEveryMinutes),
		num: func(c *Config) *int { return &c.Cognition.SelfModelEveryMinutes }},
	{key: "cognition.identity_review_every_minutes", min: 1, max: 1_000_000, unit: "lived minutes", zero: true, def: fixed(defaultIdentityReviewEveryMinutes),
		num: func(c *Config) *int { return &c.Cognition.IdentityReviewEveryMinutes }},
	{key: "cognition.self_model_evidence_rows", min: 1, max: 200, unit: "experiences", zero: true, def: fixed(cognitive.DefaultSelfModelEvidenceRows),
		num: func(c *Config) *int { return &c.Cognition.SelfModelEvidenceRows }},
	{key: "cognition.self_model_spoken_rows", min: 1, max: 200, unit: "experiences", zero: true, def: fixed(cognitive.DefaultSelfModelSpokenRows),
		num: func(c *Config) *int { return &c.Cognition.SelfModelSpokenRows }},
	{key: "prompt.self_model_max_tokens", min: minReplyChars / 3, max: 1_000_000, unit: "tokens", zero: true, def: fixed(store.SelfModelDefaultMaxTokens),
		floor: "a hundred words as a portrait is counted, 600 characters at three bytes a token: under it no portrait is admitted, and the current one stands for good",
		num:   func(c *Config) *int { return &c.Prompt.SelfModelMaxTokens }},
	{key: "prompt.ring3_max_chars", min: minReplyChars, max: 10_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultRing3MaxChars),
		floor: "the least a working-truth view is asked to be: a hundred words at six characters a word",
		num:   func(c *Config) *int { return &c.Prompt.Ring3MaxChars }},
	{key: "prompt.surfacing_max_chars", min: minReplyChars, max: 10_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultSurfacingMaxChars),
		floor: "the least a dream note is asked to be: a hundred words at six characters a word",
		num:   func(c *Config) *int { return &c.Prompt.SurfacingMaxChars }},
	{key: "prompt.dream_conversation_max_chars", min: 1, max: 100_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultConversationMaxChars),
		num: func(c *Config) *int { return &c.Prompt.DreamConversationMaxChars }},
	{key: "prompt.tensions_max_chars", min: 1, max: 10_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultTensionsMaxChars),
		num: func(c *Config) *int { return &c.Prompt.TensionsMaxChars }},
	{key: "prompt.brief_max_chars", min: minReplyChars, max: 1_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultBriefMaxChars),
		floor: "a hundred words at six characters a word, the floor of every note a facility writes",
		num:   func(c *Config) *int { return &c.Prompt.BriefMaxChars }},
	alarmFiringsSetting,
	{key: "prompt.excerpt_chars", min: 1, max: 100_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultExcerptChars),
		num: func(c *Config) *int { return &c.Prompt.ExcerptChars }},
	{key: "prompt.ring3_recent_experiences", min: 1, max: 1_000, unit: "experiences", zero: true, def: fixed(cognitive.DefaultRecentExperiences),
		num: func(c *Config) *int { return &c.Prompt.Ring3RecentExperiences }},
	turnTokenBudgetSetting,

	toolSchemaRefusalsSetting,
	checkpointCallsSetting,
	graceRoundsSetting,
	slackSetting,
	continuationsSetting,
	continuationWallSetting,
	alarmWallSetting,
	harvestWallSetting,
	maxToolRoundsSetting,
	maxToolCallsSetting,
	subRoundsSetting,
	subCallsSetting,
	subLegsSetting,
	subWallSetting,
	subWallLocalSetting,
	toolResultCharsSetting,
	promptMaxTokensSetting,
	timeoutSetting,
	retriesSetting,
	retryBackoffSetting,
}

var turnTokenBudgetSetting = cognitionSetting{key: "agency.turn_token_budget", min: 1, max: 1_000_000_000, unit: "tokens", zero: true, signed: true, perModel: "model", unlimited: true,
	def: fixed(limits.DefaultTurnTokens), ceil: func(c *Config) *limits.Ceiling { return &c.Agency.TurnTokenBudget }}

var maxToolCallsSetting = cognitionSetting{key: "agency.max_tool_calls", min: 1, max: 1_000_000_000, unit: "tool calls", zero: true, perModel: "model", unlimited: true, card: "agency",
	def: unlimitedDefault, ceil: func(c *Config) *limits.Ceiling { return &c.Agency.MaxToolCalls }}

func unlimitedDefault(Config) limits.Ceiling { return limits.Unlimited }

var maxToolRoundsSetting = cognitionSetting{key: "agency.max_tool_rounds", min: 1, max: 1_000_000, unit: "rounds", perModel: "model", unlimited: true, card: "agency",
	def: fixed(limits.DefaultRounds), ceil: func(c *Config) *limits.Ceiling { return &c.Agency.MaxToolRounds }}

var subRoundsSetting = cognitionSetting{key: "agency.subagent_max_tool_rounds", min: 1, max: 1_000_000, unit: "rounds", zero: true, perModel: "model", unlimited: true, card: "agency",
	def: fixed(limits.DefaultSubRounds), ceil: func(c *Config) *limits.Ceiling { return &c.Agency.SubagentMaxToolRounds }}

var subCallsSetting = cognitionSetting{key: "agency.subagent_max_tool_calls", min: 1, max: 1_000_000_000, unit: "tool calls", zero: true, perModel: "model", unlimited: true, card: "agency",
	def: fixed(limits.DefaultSubCalls), ceil: func(c *Config) *limits.Ceiling { return &c.Agency.SubagentMaxToolCalls }}

var subLegsSetting = cognitionSetting{key: "agency.subagent_max_legs", min: 1, max: 1_000_000, unit: "legs", zero: true, perModel: "model", unlimited: true, card: "agency",
	def: fixed(limits.DefaultSubLegs), ceil: func(c *Config) *limits.Ceiling { return &c.Agency.SubagentMaxLegs }}

var subWallSetting = cognitionSetting{key: "agency.subagent_wall_seconds", min: 1, max: 604_800, unit: "seconds", perModel: "model", card: "agency",
	def: fixed(limits.DefaultSubWallSeconds), num: func(c *Config) *int { return &c.Agency.SubagentWallSeconds }}

var subWallLocalSetting = cognitionSetting{key: "agency.subagent_wall_seconds_local", min: 1, max: 604_800, unit: "seconds", card: "agency",
	def: fixed(limits.DefaultSubWallLocal), num: func(c *Config) *int { return &c.Agency.SubagentWallSecondsLocal }}

var toolResultCharsSetting = cognitionSetting{key: "prompt.max_tool_result_chars", min: 1, max: 100_000_000, unit: "characters", zero: true, perModel: "model", card: "prompt",
	def: fixed(limits.DefaultToolResultChars), num: func(c *Config) *int { return &c.Prompt.MaxToolResultChars }}

var promptMaxTokensSetting = cognitionSetting{key: "prompt.max_tokens", min: 1, max: 1_000_000_000, unit: "tokens", zero: true, perModel: "model", card: "prompt",
	def: fixed(0), num: func(c *Config) *int { return &c.Prompt.MaxTokens }}

var timeoutSetting = cognitionSetting{key: "llm.timeout_seconds", min: 1, max: 86_400, unit: "seconds", zero: true, signed: true, perModel: "provider", card: "substrate",
	def: fixed(limits.DefaultTimeoutSeconds), num: func(c *Config) *int { return &c.LLM.TimeoutSeconds }}

var retriesSetting = cognitionSetting{key: "llm.retries", min: -1, max: 100, unit: "retries (-1 for none)", zero: true, perModel: "provider", card: "substrate",
	def: fixed(limits.DefaultRetries), num: func(c *Config) *int { return &c.LLM.Retries }}

var retryBackoffSetting = cognitionSetting{key: "llm.retry_backoff_ms", min: 1, max: 600_000, unit: "milliseconds", zero: true, signed: true, perModel: "provider", card: "substrate",
	def: fixed(limits.DefaultRetryBackoffMS), num: func(c *Config) *int { return &c.LLM.RetryBackoffMS }}

var toolSchemaRefusalsSetting = cognitionSetting{key: "agency.tool_schema_refusals", min: 1, max: maxToolSchemaRefusals, unit: "tools", zero: true,
	def: fixed(limits.DefaultToolSchemaRefusals), num: func(c *Config) *int { return &c.Agency.ToolSchemaRefusals }}

const briefAtKey = "cognition.morning_brief_at"

func cognitionSettingFor(key string) (cognitionSetting, bool) {
	for _, s := range cognitionSettings {
		if s.key == key {
			return s, true
		}
	}
	return cognitionSetting{}, false
}

func validCognition(c Config) error {
	for _, s := range cognitionSettings {
		if err := s.check(c, s.get(&c)); err != nil {
			return err
		}
	}
	if at := c.Cognition.MorningBriefAt; at != "" {
		if _, _, _, err := cognitive.ParseBriefAt(at); err != nil {
			return &CognitionRefusal{Key: briefAtKey, Requirement: fmt.Sprintf("%q — %v", at, err)}
		}
	}
	if spacing, rhythm := c.Cognition.ConsolidateSpacingSeconds, rhythmSecondsSetting.n(c); spacing != 0 && spacing <= rhythm {
		return &CognitionRefusal{Key: spacingSetting.key, Requirement: fmt.Sprintf("%d is not longer than agency.rhythm_seconds (%d) — every tick with raw material would go to consolidation and none to DREAM; set more than %d, or 0 to follow the cadence (%d)",
			spacing, rhythm, rhythm, spacingSetting.def(c).N)}
	}
	if !graceFits(c) {

		key, rounds, most, who := graceShort(c)
		advice := fmt.Sprintf("raise %s to at least 3, or leave the grace unset", key)
		if most >= 1 {
			advice = fmt.Sprintf("set at most %d", most)
			if d := graceRoundsSetting.def(c).N; d <= most {
				advice += fmt.Sprintf(", or 0 for the default (%d)", d)
			}
		}
		return &CognitionRefusal{Key: graceRoundsSetting.key, Requirement: fmt.Sprintf("%d leaves no round of %s (%d) for a checkpoint — %s; %s",
			c.Agency.TurnCheckpointGraceRounds, key, rounds, who, advice)}
	}
	return nil
}

func holdCognition(c *Config) (said []string, err error) {
	for _, s := range cognitionSettings {
		cv := s.get(c)
		if cv.Unlimited {
			if s.unlimited {
				continue
			}
			return nil, s.check(*c, cv)
		}
		v := cv.N
		if v == 0 && s.zero {
			continue
		}
		to, why, advice := v, "", ""
		switch rhythm := rhythmSecondsSetting.n(*c); {
		case v == 0, v < 0 && !s.signed && s.min >= 0:
			return nil, s.check(*c, cv)
		case s.key == graceRoundsSetting.key && !graceFits(*c):

			key, rounds, most, who := graceShort(*c)
			if most >= 1 {
				to, why = most, fmt.Sprintf("too many to leave a round of %s (%d) for a checkpoint — %s", key, rounds, who)
				advice = fmt.Sprintf("set it at most %d in config.json or on Settings → Identity → Cognition", most)
			} else {
				to, why = 0, fmt.Sprintf("too many to leave a round of %s (%d) for a checkpoint, and no grace leaves one; until it is raised to at least 3, %s", key, rounds, who)
				advice = fmt.Sprintf("raise %s to at least 3, or leave the grace unset", key)
			}
		case v > s.max:
			to, why = s.max, fmt.Sprintf("over the most it may be (%d %s)", s.max, s.unit)
		case s.key == spacingSetting.key && v <= rhythm:
			to, why = 0, fmt.Sprintf("not longer than agency.rhythm_seconds (%d) — every tick with raw material would go to consolidation and none to DREAM", rhythm)
		case v < 0 && s.signed:
			to, why = 0, "below zero, which was always read as not set"
		case v < s.min:
			to, why = s.min, fmt.Sprintf("under the least it may be (%d %s)", s.min, s.unit)
			if s.floor != "" {
				why += " — " + s.floor
			}
		default:
			continue
		}
		s.set(c, limits.Of(to))
		force, dflt := fmt.Sprint(to), ""
		if to == 0 {
			force = fmt.Sprintf("the default, %s,", s.def(*c))
		}
		if s.zero {
			dflt = fmt.Sprintf(", or to 0 for the default (%s)", s.def(*c))
		}
		if advice == "" {
			advice = fmt.Sprintf("set it between %d and %d in config.json or on %s%s", s.min, s.max, s.place(), dflt)
		}
		sentence := fmt.Sprintf("%s %d is %s: %s is in force, and the file is left as it is; %s", s.key, v, why, force, advice)
		if c.cognitionHeld == nil {
			c.cognitionHeld = map[string]string{}
		}
		c.cognitionHeld[s.key] = sentence
		said = append(said, sentence)
	}
	return said, nil
}

func applyCognitionChange(c *Config, key string, v any, integer func(string, any) (int, error)) (bool, error) {
	if key == briefAtKey {
		at, ok := v.(string)
		if v == nil {
			at, ok = "", true
		}
		if !ok {
			return true, &CognitionRefusal{Key: key, Requirement: fmt.Sprintf("want \"HH:MM\" or %q", cognitive.MorningBriefOff)}
		}
		if at != "" {
			if _, _, _, err := cognitive.ParseBriefAt(at); err != nil {
				return true, &CognitionRefusal{Key: key, Requirement: fmt.Sprintf("%q — %v", at, err)}
			}
		}
		c.Cognition.MorningBriefAt = at
		return true, nil
	}
	s, ok := cognitionSettingFor(key)
	if !ok || s.card == "prompt" || s.card == "substrate" {
		return false, nil
	}
	n, err := doorValue(s, *c, v, integer)
	if err != nil {
		return true, err
	}
	s.set(c, n)
	return true, nil
}

func doorValue(s cognitionSetting, c Config, v any, integer func(string, any) (int, error)) (limits.Ceiling, error) {
	key := s.key
	var n limits.Ceiling
	switch word, isWord := v.(string); {
	case v == nil:
	case isWord && word == limits.Word:
		n = limits.Unlimited
	default:
		got, err := integer(key, v)
		if err != nil {
			want := "want a whole number, or null for the default"
			if s.unlimited {
				want = fmt.Sprintf("want a whole number, %q, or null for the default", limits.Word)
			}
			return limits.Ceiling{}, &CognitionRefusal{Key: key, Requirement: want}
		}
		n = limits.Of(got)
	}
	return n, s.check(c, n)
}

func tableNumber(cfg *Config, key string, v any, integer func(string, any) (int, error)) (changed bool, err error) {
	s, ok := cognitionSettingFor(key)
	if !ok {
		return false, fmt.Errorf("%s: no such setting", key)
	}
	n, err := doorValue(s, *cfg, v, integer)
	if err != nil {
		return false, err
	}
	changed = s.get(cfg) != n
	s.set(cfg, n)
	return changed, nil
}

func reflectionPulses(minutes, pulseSeconds int) int64 {
	if pulseSeconds <= 0 {
		pulseSeconds = int(cognitive.DefaultPulseInterval / time.Second)
	}
	seconds := int64(minutes) * 60
	return max(1, (seconds+int64(pulseSeconds)-1)/int64(pulseSeconds))
}

func (a *App) cognitionState(c Config, reg *providerRegistry) dashboard.CognitionConfigState {
	st := dashboard.CognitionConfigState{
		ConsolidateBatch:           c.Cognition.ConsolidateBatch,
		ConsolidateMaxOps:          c.Cognition.ConsolidateMaxOps,
		ConsolidateSpacingSeconds:  c.Cognition.ConsolidateSpacingSeconds,
		MorningBriefAt:             c.Cognition.MorningBriefAt,
		SelfModelEveryMinutes:      c.Cognition.SelfModelEveryMinutes,
		IdentityReviewEveryMinutes: c.Cognition.IdentityReviewEveryMinutes,
		SelfModelEvidenceRows:      c.Cognition.SelfModelEvidenceRows,
		SelfModelSpokenRows:        c.Cognition.SelfModelSpokenRows,
		MorningBriefDefault:        cognitive.DefaultMorningBriefAt,
		PulseSeconds:               a.pulseInForce(c),
	}

	resident := a.residentLimitsFor(c)
	for _, s := range cognitionSettings {
		def := s.def(c)
		src := resident.Source[s.key]
		st.Settings = append(st.Settings, dashboard.CognitionSettingState{
			Key: s.key, Min: s.min, Max: s.max, Default: def.N, Unit: s.unit, ZeroIsDefault: s.zero, Held: c.cognitionHeld[s.key],
			Card: s.card, PerModel: s.perModel, Unlimited: s.unlimited, DefaultUnlimited: def.Unlimited,
			Value:     s.get(&c),
			Effective: dashboard.LimitEffective{Value: resident.Value(s), Level: src.Level, Provider: src.Provider, Model: src.Model},
			Overrides: overridesOf(c, s.key),
		})
	}
	st.OverridesDigest = overridesDigest(c.Overrides)
	if len(c.Overrides) > 0 {
		if raw, err := json.Marshal(c.Overrides); err == nil {
			st.OverridesBlock = raw
		}
	}
	if reg != nil {
		st.OverridesUnused = unusedOverrides(c.Overrides, reg)
	}
	if budget, source := a.currentFacilityBudget(); source != "" {
		st.InputBudget, st.InputBudgetSource = budget, string(source)
	}
	st.Conditions = a.cognitionConditions()
	return st
}

func (a *App) pulseInForce(c Config) int {
	if set := a.cognition.rhythm.Load(); set != nil {
		return set.pulseSeconds
	}
	return pulseSeconds(c)
}

func pulseSeconds(c Config) int {
	if c.Prompt.PulseIntervalSeconds > 0 {
		return c.Prompt.PulseIntervalSeconds
	}
	return int(cognitive.DefaultPulseInterval / time.Second)
}
