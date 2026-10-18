package app

import (
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type cognitionSetting struct {
	key      string
	min, max int
	unit     string
	floor    string
	zero     bool
	signed   bool
	def      func(Config) int
	get      func(*Config) int
	set      func(*Config, int)
}

type CognitionRefusal struct {
	Key         string
	Requirement string
}

func (e *CognitionRefusal) Error() string { return e.Key + ": " + e.Requirement }

func (s cognitionSetting) value(c Config) int {
	if v := s.get(&c); v != 0 {
		return v
	}
	return s.def(c)
}

func (s cognitionSetting) check(c Config, v int) error {
	if v == 0 && s.zero {
		return nil
	}
	if v < s.min || v > s.max {
		dflt := ""
		if s.zero {
			dflt = fmt.Sprintf(", or 0 for the default (%d)", s.def(c))
		}
		why := ""
		if v < s.min && s.floor != "" {
			why = fmt.Sprintf(" — %d is %s", s.min, s.floor)
		}
		return &CognitionRefusal{Key: s.key, Requirement: fmt.Sprintf("%d is out of bounds — between %d and %d %s%s%s", v, s.min, s.max, s.unit, dflt, why)}
	}
	return nil
}

func fixed(n int) func(Config) int { return func(Config) int { return n } }

const defaultRhythmSeconds = 600

const defaultTurnTokenBudget = 600_000

var (
	defaultSelfModelEveryMinutes      = int(cognitive.DefaultSelfModelEvery / time.Minute)
	defaultIdentityReviewEveryMinutes = int(cognitive.DefaultIdentityReviewEvery / time.Minute)
)

var rhythmSecondsSetting = cognitionSetting{key: "agency.rhythm_seconds", min: 1, max: 86400, unit: "seconds",
	def: fixed(defaultRhythmSeconds), get: func(c *Config) int { return c.Agency.RhythmSeconds }, set: func(c *Config, v int) { c.Agency.RhythmSeconds = v }}

var spacingSetting = cognitionSetting{key: "cognition.consolidate_spacing_seconds", min: 2, max: 3 * 86400, unit: "seconds", zero: true,

	def: func(c Config) int {
		return max(int(cognitive.DefaultSpacing/time.Second), 3*rhythmSecondsSetting.value(c))
	},
	get: func(c *Config) int { return c.Cognition.ConsolidateSpacingSeconds }, set: func(c *Config, v int) { c.Cognition.ConsolidateSpacingSeconds = v }}

var checkpointCallsSetting = cognitionSetting{key: "agency.turn_checkpoint_calls", min: 1, max: 1_000_000_000, unit: "tool calls", zero: true,
	def: fixed(conversation.DefaultCheckpointCalls),
	get: func(c *Config) int { return c.Agency.TurnCheckpointCalls }, set: func(c *Config, v int) { c.Agency.TurnCheckpointCalls = v }}

var graceRoundsSetting = cognitionSetting{key: "agency.turn_checkpoint_grace_rounds", min: 1, max: 1_000_000, unit: "rounds", zero: true,
	def: fixed(conversation.DefaultCheckpointGraceRounds),
	get: func(c *Config) int { return c.Agency.TurnCheckpointGraceRounds }, set: func(c *Config, v int) { c.Agency.TurnCheckpointGraceRounds = v }}

var slackSetting = cognitionSetting{key: "agency.turn_checkpoint_slack", min: 1, max: 1_000, unit: "times the estimate", zero: true,
	def: fixed(conversation.DefaultCheckpointSlack),
	get: func(c *Config) int { return c.Agency.TurnCheckpointSlack }, set: func(c *Config, v int) { c.Agency.TurnCheckpointSlack = v }}

var continuationsSetting = cognitionSetting{key: "agency.max_turn_continuations", min: 1, max: 1_000_000, unit: "turns", zero: true,
	def: fixed(defaultMaxTurnContinuations),
	get: func(c *Config) int { return c.Agency.MaxTurnContinuations }, set: func(c *Config, v int) { c.Agency.MaxTurnContinuations = v }}

var continuationWallSetting = cognitionSetting{key: "agency.continuation_wall_seconds", min: 1, max: 86_400, unit: "seconds", zero: true,
	def: fixed(defaultContinuationWallSeconds),
	get: func(c *Config) int { return c.Agency.ContinuationWallSeconds }, set: func(c *Config, v int) { c.Agency.ContinuationWallSeconds = v }}

var alarmWallSetting = cognitionSetting{key: "agency.alarm_wall_seconds", min: 1, max: 86_400, unit: "seconds", zero: true,
	def: fixed(defaultAlarmWallSeconds),
	get: func(c *Config) int { return c.Agency.AlarmWallSeconds }, set: func(c *Config, v int) { c.Agency.AlarmWallSeconds = v }}

var harvestWallSetting = cognitionSetting{key: "agency.harvest_wall_seconds", min: 1, max: 86_400, unit: "seconds", zero: true,
	def: fixed(defaultHarvestWallSeconds),
	get: func(c *Config) int { return c.Agency.HarvestWallSeconds }, set: func(c *Config, v int) { c.Agency.HarvestWallSeconds = v }}

const (
	defaultContinuationWallSeconds = 45 * 60
	defaultAlarmWallSeconds        = 15 * 60
	defaultHarvestWallSeconds      = 15 * 60
)

func (a *App) turnWall(s cognitionSetting) time.Duration {
	return time.Duration(s.value(a.configSnapshot())) * time.Second
}

func graceFits(c Config) bool {
	_, rounds := graceCeiling(c)
	grace := c.Agency.TurnCheckpointGraceRounds
	return grace == 0 || rounds <= 0 || grace < rounds-1
}

func graceCeiling(c Config) (key string, rounds int) {
	key, rounds = "agency.max_tool_rounds", c.Agency.MaxToolRounds
	child := c.Agency.SubagentMaxToolRounds
	if agencyOn(c.Agency.SubagentContinuation) && child > 0 && (rounds <= 0 || child < rounds) {
		key, rounds = "agency.subagent_max_tool_rounds", child
	}
	return key, rounds
}

func graceShort(c Config) (key string, rounds, most int, who string) {
	key, rounds = graceCeiling(c)
	most = rounds - 2
	grace, resident := c.Agency.TurnCheckpointGraceRounds, c.Agency.MaxToolRounds
	switch {
	case key == "agency.max_tool_rounds":
		who = "no turn of the identity's would be checkpointed or continued"
	case resident <= 0 || grace < resident-1:
		who = "no leg of a child's would be checkpointed or continued, though the identity's own turns still would"
	default:
		who = "neither the identity's turns nor a child's legs would be checkpointed or continued"
	}
	return key, rounds, most, who
}

var alarmFiringsSetting = cognitionSetting{key: "prompt.alarm_firings", min: 1, max: 100_000, unit: "firings", zero: true,
	def: fixed(identity.DefaultAlarmFirings),
	get: func(c *Config) int { return c.Prompt.AlarmFirings }, set: func(c *Config, v int) { c.Prompt.AlarmFirings = v }}

func (a *App) alarmFirings() int { return alarmFiringsSetting.value(a.configSnapshot()) }

const minReplyChars = 600

var cognitionSettings = []cognitionSetting{
	rhythmSecondsSetting,
	spacingSetting,
	{key: "cognition.consolidate_batch", min: 1, max: 200, unit: "experiences", zero: true, def: fixed(cognitive.DefaultConsolidateBatch),
		get: func(c *Config) int { return c.Cognition.ConsolidateBatch }, set: func(c *Config, v int) { c.Cognition.ConsolidateBatch = v }},
	{key: "cognition.consolidate_max_ops", min: 2, max: 512, unit: "operations", zero: true, def: fixed(cognitive.DefaultConsolidateMaxOps),
		floor: "what replacing one belief takes: an upsert of the new and a supersede of the old",
		get:   func(c *Config) int { return c.Cognition.ConsolidateMaxOps }, set: func(c *Config, v int) { c.Cognition.ConsolidateMaxOps = v }},
	{key: "agency.outcome_window", min: 1, max: 1_000_000, unit: "records", def: fixed(cognitive.DefaultOutcomeWindow),
		get: func(c *Config) int { return c.Agency.OutcomeWindow }, set: func(c *Config, v int) { c.Agency.OutcomeWindow = v }},
	{key: "cognition.self_model_every_minutes", min: 1, max: 1_000_000, unit: "lived minutes", zero: true, def: fixed(defaultSelfModelEveryMinutes),
		get: func(c *Config) int { return c.Cognition.SelfModelEveryMinutes }, set: func(c *Config, v int) { c.Cognition.SelfModelEveryMinutes = v }},
	{key: "cognition.identity_review_every_minutes", min: 1, max: 1_000_000, unit: "lived minutes", zero: true, def: fixed(defaultIdentityReviewEveryMinutes),
		get: func(c *Config) int { return c.Cognition.IdentityReviewEveryMinutes }, set: func(c *Config, v int) { c.Cognition.IdentityReviewEveryMinutes = v }},
	{key: "cognition.self_model_evidence_rows", min: 1, max: 200, unit: "experiences", zero: true, def: fixed(cognitive.DefaultSelfModelEvidenceRows),
		get: func(c *Config) int { return c.Cognition.SelfModelEvidenceRows }, set: func(c *Config, v int) { c.Cognition.SelfModelEvidenceRows = v }},
	{key: "cognition.self_model_spoken_rows", min: 1, max: 200, unit: "experiences", zero: true, def: fixed(cognitive.DefaultSelfModelSpokenRows),
		get: func(c *Config) int { return c.Cognition.SelfModelSpokenRows }, set: func(c *Config, v int) { c.Cognition.SelfModelSpokenRows = v }},
	{key: "prompt.self_model_max_tokens", min: minReplyChars / 3, max: 1_000_000, unit: "tokens", zero: true, def: fixed(store.SelfModelDefaultMaxTokens),
		floor: "a hundred words as a portrait is counted, 600 characters at three bytes a token: under it no portrait is admitted, and the current one stands for good",
		get:   func(c *Config) int { return c.Prompt.SelfModelMaxTokens }, set: func(c *Config, v int) { c.Prompt.SelfModelMaxTokens = v }},
	{key: "prompt.ring3_max_chars", min: minReplyChars, max: 10_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultRing3MaxChars),
		floor: "the least a working-truth view is asked to be: a hundred words at six characters a word",
		get:   func(c *Config) int { return c.Prompt.Ring3MaxChars }, set: func(c *Config, v int) { c.Prompt.Ring3MaxChars = v }},
	{key: "prompt.surfacing_max_chars", min: minReplyChars, max: 10_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultSurfacingMaxChars),
		floor: "the least a dream note is asked to be: a hundred words at six characters a word",
		get:   func(c *Config) int { return c.Prompt.SurfacingMaxChars }, set: func(c *Config, v int) { c.Prompt.SurfacingMaxChars = v }},
	{key: "prompt.dream_conversation_max_chars", min: 1, max: 100_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultConversationMaxChars),
		get: func(c *Config) int { return c.Prompt.DreamConversationMaxChars }, set: func(c *Config, v int) { c.Prompt.DreamConversationMaxChars = v }},
	{key: "prompt.tensions_max_chars", min: 1, max: 10_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultTensionsMaxChars),
		get: func(c *Config) int { return c.Prompt.TensionsMaxChars }, set: func(c *Config, v int) { c.Prompt.TensionsMaxChars = v }},
	{key: "prompt.brief_max_chars", min: minReplyChars, max: 1_000_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultBriefMaxChars),
		floor: "a hundred words at six characters a word, the floor of every note a facility writes",
		get:   func(c *Config) int { return c.Prompt.BriefMaxChars }, set: func(c *Config, v int) { c.Prompt.BriefMaxChars = v }},
	alarmFiringsSetting,
	{key: "prompt.excerpt_chars", min: 1, max: 100_000, unit: "characters", zero: true, def: fixed(cognitive.DefaultExcerptChars),
		get: func(c *Config) int { return c.Prompt.ExcerptChars }, set: func(c *Config, v int) { c.Prompt.ExcerptChars = v }},
	{key: "prompt.ring3_recent_experiences", min: 1, max: 1_000, unit: "experiences", zero: true, def: fixed(cognitive.DefaultRecentExperiences),
		get: func(c *Config) int { return c.Prompt.Ring3RecentExperiences }, set: func(c *Config, v int) { c.Prompt.Ring3RecentExperiences = v }},
	{key: "agency.turn_token_budget", min: 1, max: 1_000_000_000, unit: "tokens", zero: true, signed: true, def: fixed(defaultTurnTokenBudget),
		get: func(c *Config) int { return c.Agency.TurnTokenBudget }, set: func(c *Config, v int) { c.Agency.TurnTokenBudget = v }},

	{key: "agency.tool_schema_refusals", min: 1, max: maxToolSchemaRefusals, unit: "tools", zero: true, def: fixed(conversation.DefaultToolSchemaRefusals),
		get: func(c *Config) int { return c.Agency.ToolSchemaRefusals }, set: func(c *Config, v int) { c.Agency.ToolSchemaRefusals = v }},
	checkpointCallsSetting,
	graceRoundsSetting,
	slackSetting,
	continuationsSetting,
	continuationWallSetting,
	alarmWallSetting,
	harvestWallSetting,
}

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
	if spacing, rhythm := c.Cognition.ConsolidateSpacingSeconds, rhythmSecondsSetting.value(c); spacing != 0 && spacing <= rhythm {
		return &CognitionRefusal{Key: spacingSetting.key, Requirement: fmt.Sprintf("%d is not longer than agency.rhythm_seconds (%d) — every tick with raw material would go to consolidation and none to DREAM; set more than %d, or 0 to follow the cadence (%d)",
			spacing, rhythm, rhythm, spacingSetting.def(c))}
	}
	if !graceFits(c) {

		key, rounds, most, who := graceShort(c)
		advice := fmt.Sprintf("raise %s to at least 3, or leave the grace unset", key)
		if most >= 1 {
			advice = fmt.Sprintf("set at most %d", most)
			if d := graceRoundsSetting.def(c); d <= most {
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
		v := s.get(c)
		if v == 0 && s.zero {
			continue
		}
		to, why, advice := v, "", ""
		switch rhythm := rhythmSecondsSetting.value(*c); {
		case v == 0, v < 0 && !s.signed:
			return nil, s.check(*c, v)
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
		case v < 0:
			to, why = 0, "below zero, which the turn loop always read as not set"
		case v < s.min:
			to, why = s.min, fmt.Sprintf("under the least it may be (%d %s)", s.min, s.unit)
			if s.floor != "" {
				why += " — " + s.floor
			}
		default:
			continue
		}
		s.set(c, to)
		force, dflt := fmt.Sprint(to), ""
		if to == 0 {
			force = fmt.Sprintf("the default, %d,", s.def(*c))
		}
		if s.zero {
			dflt = fmt.Sprintf(", or to 0 for the default (%d)", s.def(*c))
		}
		if advice == "" {
			advice = fmt.Sprintf("set it between %d and %d in config.json or on Settings → Identity → Cognition%s", s.min, s.max, dflt)
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
	if !ok {
		return false, nil
	}
	n := 0
	if v != nil {
		got, err := integer(key, v)
		if err != nil {
			return true, &CognitionRefusal{Key: key, Requirement: "want a whole number, or null for the default"}
		}
		n = got
	}
	if err := s.check(*c, n); err != nil {
		return true, err
	}
	s.set(c, n)
	return true, nil
}

func reflectionPulses(minutes, pulseSeconds int) int64 {
	if pulseSeconds <= 0 {
		pulseSeconds = int(cognitive.DefaultPulseInterval / time.Second)
	}
	seconds := int64(minutes) * 60
	return max(1, (seconds+int64(pulseSeconds)-1)/int64(pulseSeconds))
}

func (a *App) cognitionState(c Config) dashboard.CognitionConfigState {
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
	for _, s := range cognitionSettings {
		st.Settings = append(st.Settings, dashboard.CognitionSettingState{
			Key: s.key, Min: s.min, Max: s.max, Default: s.def(c), Unit: s.unit, ZeroIsDefault: s.zero, Held: c.cognitionHeld[s.key],
		})
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
