package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type overrideSection struct {
	values map[string]limits.Ceiling

	models map[string]*overrideSection

	stream *bool
}

type Overrides map[string]*overrideSection

func overrideRefusal(path, requirement string) error {
	return &CognitionRefusal{Key: path, Requirement: requirement}
}

var overrideRemedy = map[string]string{
	"agency.subagent_wall_seconds_local": "set agency.subagent_wall_seconds on the local provider instead",
}

func (o *Overrides) UnmarshalJSON(b []byte) error {
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(b, &providers); err != nil {
		return overrideRefusal("overrides", "want an object of provider entry names: "+err.Error())
	}
	out := Overrides{}
	for name, raw := range providers {
		sec, err := readOverrideSection("overrides."+name, raw, true)
		if err != nil {
			return err
		}
		out[name] = sec
	}
	*o = out
	return nil
}

func readOverrideSection(path string, raw json.RawMessage, provider bool) (*overrideSection, error) {
	var parts map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, overrideRefusal(path, "want an object: "+err.Error())
	}
	sec := &overrideSection{values: map[string]limits.Ceiling{}}
	for part, body := range parts {
		switch part {
		case "models":
			if !provider {
				return nil, overrideRefusal(path+".models", "a model's overrides hold agency and prompt keys; models are named under their provider")
			}
			var models map[string]json.RawMessage
			if err := json.Unmarshal(body, &models); err != nil {
				return nil, overrideRefusal(path+".models", "want an object of model ids: "+err.Error())
			}
			sec.models = map[string]*overrideSection{}
			for id, mraw := range models {
				m, err := readOverrideSection(path+".models."+id, mraw, false)
				if err != nil {
					return nil, err
				}
				sec.models[id] = m
			}
		case "agency", "prompt", "llm":
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(body, &keys); err != nil {
				return nil, overrideRefusal(path+"."+part, "want an object of settings: "+err.Error())
			}
			for k, vraw := range keys {
				key := part + "." + k
				if key == "llm.stream" {
					if !provider {
						return nil, overrideRefusal(path+"."+key, key+" describes the server: set it on the provider")
					}
					var on bool
					if err := json.Unmarshal(vraw, &on); err != nil {
						return nil, overrideRefusal(path+"."+key, "want true or false")
					}
					sec.stream = &on
					continue
				}
				s, err := overrideSetting(path, key, provider)
				if err != nil {
					return nil, err
				}
				var v limits.Ceiling
				if err := json.Unmarshal(vraw, &v); err != nil {
					return nil, overrideRefusal(path+"."+key, err.Error())
				}
				if v.Unlimited && !s.unlimited {
					return nil, overrideRefusal(path+"."+key, fmt.Sprintf("%s is not a value it takes — it is a number between %d and %d %s", limits.Word, s.min, s.max, s.unit))
				}
				sec.values[key] = v
			}
		default:
			return nil, overrideRefusal(path+"."+part, "not a section of an override: it holds agency, prompt and llm keys, and a provider's models")
		}
	}
	return sec, nil
}

func overrideSetting(path, key string, provider bool) (cognitionSetting, error) {
	s, ok := cognitionSettingFor(key)
	if !ok {
		if configKeyExists(key) {
			return s, overrideRefusal(path+"."+key, key+" is global: it bounds the machine or the rhythm, not a model; set it in the main settings")
		}
		return s, overrideRefusal(path+"."+key, "no such setting")
	}
	switch {
	case s.perModel == "":
		why := "it bounds the machine or the rhythm, not a model; set it in the main settings"
		if r := overrideRemedy[key]; r != "" {
			why = "it is the main setting for every local entry; " + r
		}
		return s, overrideRefusal(path+"."+key, key+" is global: "+why)
	case s.perModel == "provider" && !provider:
		return s, overrideRefusal(path+"."+key, key+" describes the server: set it on the provider")
	}
	return s, nil
}

func (o Overrides) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	for name, sec := range o {
		out[name] = sec.shape()
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func (s *overrideSection) shape() map[string]any {
	out := map[string]any{}
	if s == nil {
		return out
	}
	for key, v := range s.values {
		section, k, _ := strings.Cut(key, ".")
		m, _ := out[section].(map[string]any)
		if m == nil {
			m = map[string]any{}
			out[section] = m
		}
		m[k] = v
	}
	if s.stream != nil {
		m, _ := out["llm"].(map[string]any)
		if m == nil {
			m = map[string]any{}
			out["llm"] = m
		}
		m["stream"] = *s.stream
	}
	if len(s.models) > 0 {
		models := map[string]any{}
		for id, m := range s.models {
			models[id] = m.shape()
		}
		out["models"] = models
	}
	return out
}

type limitSource struct {
	Level           string
	Provider, Model string
}

type Limits struct {
	cfg    Config
	Source map[string]limitSource

	Held map[string]string
}

func (l Limits) Value(s cognitionSetting) limits.Ceiling { return s.value(l.cfg) }

func (l Limits) N(s cognitionSetting) int { return s.n(l.cfg) }

func limitsFor(cfg Config, entry providerEntry, model string) Limits {
	l := Limits{cfg: cfg, Source: map[string]limitSource{}}
	for _, s := range cognitionSettings {
		level := "main"
		if s.get(&cfg).IsZero() {
			level = "default"
		}
		l.Source[s.key] = limitSource{Level: level}
	}
	if entry.Local {
		l.cfg.Agency.SubagentWallSeconds = subWallLocalSetting.n(cfg)
		l.Source[subWallSetting.key] = limitSource{Level: "main"}
	}
	sec := cfg.Overrides[entry.Name]
	if sec != nil {
		l.lay(sec, "provider", entry.Name, "")
		if sec.stream != nil {
			on := *sec.stream
			l.cfg.LLM.Stream = &on
			l.Source["llm.stream"] = limitSource{Level: "provider", Provider: entry.Name}
		}
		if m := sec.models[model]; m != nil {
			l.lay(m, "model", entry.Name, model)
		}
	}

	if l.cfg.Agency.SubagentMaxToolRounds.Above(maxToolRoundsSetting.value(l.cfg)) {
		l.cfg.Agency.SubagentMaxToolRounds = maxToolRoundsSetting.value(l.cfg)
	}
	if !graceFits(l.cfg) {
		key, rounds, most, who := graceShort(l.cfg)
		to := max(most, 0)
		l.cfg.Agency.TurnCheckpointGraceRounds = to
		l.held(overridePath(entry.Name, model, graceRoundsSetting.key), fmt.Sprintf("for %s: agency.turn_checkpoint_grace_rounds leaves no round of %s (%d) for a checkpoint — %s; %s is in force", profileName(entry.Name, model), key, rounds, who, graceInForce(to)))
	}
	return l
}

func (l *Limits) lay(sec *overrideSection, level, provider, model string) {
	for key, v := range sec.values {
		s, ok := cognitionSettingFor(key)
		if !ok || v.IsZero() {
			continue
		}
		if to, why := holdOverride(s, v); why != "" {
			path := overridePath(provider, model, key)
			l.held(path, fmt.Sprintf("%s %s is %s: %s is in force", path, v, why, to))
			v = to
		}
		s.set(&l.cfg, v)
		l.Source[key] = limitSource{Level: level, Provider: provider, Model: model}
	}
}

func (l *Limits) held(path, sentence string) {
	if l.Held == nil {
		l.Held = map[string]string{}
	}
	l.Held[path] = sentence
}

func holdOverride(s cognitionSetting, v limits.Ceiling) (limits.Ceiling, string) {
	switch {
	case v.Unlimited:
		return v, ""
	case v.N < s.min && v.N < 0 && s.min >= 0:
		return s.def(Config{}), "below zero, which is not set"
	case v.N > s.max:
		return limits.Of(s.max), fmt.Sprintf("over the most it may be (%d %s)", s.max, s.unit)
	case v.N < s.min:
		return limits.Of(s.min), fmt.Sprintf("under the least it may be (%d %s)", s.min, s.unit)
	}
	return v, ""
}

func overridePath(provider, model, key string) string {
	if model == "" {
		return "overrides." + provider + "." + key
	}
	return "overrides." + provider + ".models." + model + "." + key
}

func profileName(provider, model string) string {
	if model == "" {
		return provider
	}
	return provider + " · " + model
}

func graceInForce(to int) string {
	if to < 1 {
		return "no checkpoint round"
	}
	return fmt.Sprintf("a grace of %d", to)
}

func unusedOverrides(o Overrides, reg *providerRegistry) []string {
	var out []string
	for name, sec := range o {
		entry := entryNamed(reg, name)
		if entry == nil {
			out = append(out, "overrides."+name)
			continue
		}
		for id := range sec.models {

			if len(entry.Models) > 0 && id != entry.DefaultModel && !modelListed(*entry, id) {
				out = append(out, "overrides."+name+".models."+id)
			}
		}
	}
	sort.Strings(out)
	return out
}

func modelListed(e providerEntry, id string) bool {
	for _, m := range e.Models {
		if m == id {
			return true
		}
	}
	return false
}

func (l Limits) residentBounds() conversation.Bounds {
	return conversation.Bounds{
		Rounds:          l.Value(maxToolRoundsSetting),
		TurnTokens:      l.Value(turnTokenBudgetSetting),
		Calls:           l.Value(maxToolCallsSetting),
		Checkpoint:      l.cfg.Agency.checkpoint(),
		ToolResultChars: l.N(toolResultCharsSetting),
	}
}

func (a *App) residentLimitsFor(cfg Config) Limits {
	entry, client := a.activeRuntime()
	model := cfg.LLM.Model
	if client != nil {
		model = client.ModelName()
	} else if model == "" {
		model = entry.DefaultModel
	}
	return limitsFor(cfg, entry, model)
}

func (a *App) applyResidentLimits(l Limits) {
	if a.conv == nil {
		return
	}
	if err := a.conv.SetTurnBounds(l.residentBounds()); err != nil {
		logsink.Warn("config.error", "the turn's limits were not applied: %v", err)
	}
	if err := a.conv.SetToolSchemaRefusals(toolSchemaRefusalsSetting.n(l.cfg)); err != nil {
		logsink.Warn("config.error", "agency.tool_schema_refusals was not applied: %v", err)
	}
	if line := l.turnLimitsLine(); line != "" {
		logsink.Info("turn.budget", "%s", line)
	}
}

func (l Limits) turnLimitsLine() string {
	var provider, model string
	overridden := false
	for _, src := range l.Source {
		if src.Level == "provider" || src.Level == "model" {
			overridden = true
			if provider == "" || src.Model != "" {
				provider, model = src.Provider, src.Model
			}
		}
	}
	if !overridden {
		return ""
	}
	part := func(name string, s cognitionSetting) string {
		return fmt.Sprintf("%s %s (%s)", name, l.Value(s), l.Source[s.key].Level)
	}
	return fmt.Sprintf("TURN LIMITS: %s: %s, %s, %s", profileName(provider, model),
		part("tokens", turnTokenBudgetSetting), part("rounds", maxToolRoundsSetting), part("calls", maxToolCallsSetting))
}

func promptCeilingFor(cfg Config, entry providerEntry, model string) int {
	return limitsFor(cfg, entry, model).N(promptMaxTokensSetting)
}

func sayUnusedOverrides(o Overrides, reg *providerRegistry) {
	for _, path := range unusedOverrides(o, reg) {
		logsink.Warn("config.decision", "%s", unusedOverrideLine(path))
	}
}

func unusedOverrideLine(path string) string {
	return fmt.Sprintf("config.json sets limits at %s, which names no provider entry or listed model in providers.json: they are kept and unused", path)
}

func overridesOf(c Config, key string) []dashboard.LimitOverride {
	var out []dashboard.LimitOverride
	names := make([]string, 0, len(c.Overrides))
	for name := range c.Overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sec := c.Overrides[name]
		if v, ok := sec.values[key]; ok {
			out = append(out, dashboard.LimitOverride{Provider: name, Value: v, Held: c.cognitionHeld[overridePath(name, "", key)]})
		}
		ids := make([]string, 0, len(sec.models))
		for id := range sec.models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if v, ok := sec.models[id].values[key]; ok {
				out = append(out, dashboard.LimitOverride{Provider: name, Model: id, Value: v, Held: c.cognitionHeld[overridePath(name, id, key)]})
			}
		}
	}
	return out
}

func overridesDigest(o Overrides) string {
	raw, err := json.Marshal(o)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func doorOverrides(v, digest any, current Overrides) (Overrides, error) {
	if d, _ := digest.(string); d != overridesDigest(current) {
		return nil, fmt.Errorf("overrides: %w", errConfigMoved)
	}
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, overrideRefusal("overrides", err.Error())
	}
	var o Overrides
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	for name, sec := range o {
		check := func(model string, s *overrideSection) error {
			for key, cv := range s.values {
				row, _ := cognitionSettingFor(key)
				if cv.IsZero() {
					continue
				}
				if _, why := holdOverride(row, cv); why != "" {
					lifted := ""
					if row.unlimited {
						lifted = fmt.Sprintf(", or %q", limits.Word)
					}
					return overrideRefusal(overridePath(name, model, key), fmt.Sprintf("%s is %s: set it between %d and %d %s%s, or 0 to take the next level", cv, why, row.min, row.max, row.unit, lifted))
				}
			}
			return nil
		}
		if err := check("", sec); err != nil {
			return nil, err
		}
		for id, m := range sec.models {
			if err := check(id, m); err != nil {
				return nil, err
			}
		}
	}
	if len(o) == 0 {
		return nil, nil
	}
	return o, nil
}

func configKeyExists(key string) bool {
	section, name, _ := strings.Cut(key, ".")
	var t reflect.Type
	switch section {
	case "agency":
		t = reflect.TypeOf(AgencyConfig{})
	case "prompt":
		t = reflect.TypeOf(PromptConfig{})
	case "llm":
		t = reflect.TypeOf(LLMConfig{})
	default:
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); tag == name {
			return true
		}
	}
	return false
}
