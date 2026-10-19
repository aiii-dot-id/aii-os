package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CAPABILITY IS A MODEL FACT, AND GETTING IT WRONG IS A 400.
//
// The registry's own Claude entry offers claude-opus-5 and
// claude-haiku-4-5 together. Anthropic's documentation is explicit:
// thinking.type "enabled" returns a 400 on 4.7 and later, and
// "adaptive" returns a 400 on 4.5 and earlier. A single per-entry
// thinking_mode is therefore wrong for one of those two models
// whichever way it is set — which is why every SOTA multi-provider
// translator keys this by model, and why we now do.
func TestTheThinkingShapeFollowsTheModelNotTheEntry(t *testing.T) {
	entry := providerEntry{Name: "Claude (Max/Pro)", APIType: "anthropic", ThinkingMode: "adaptive"}

	if got := thinkingShapeFor(newEffectiveCaps(nil), entry, "claude-opus-5"); got != "adaptive" {
		t.Errorf("claude-opus-5 takes adaptive thinking, got %q", got)
	}
	// The entry says adaptive; this model rejects it. The vendor wins.
	if got := thinkingShapeFor(newEffectiveCaps(nil), entry, "claude-haiku-4-5"); got != "enabled" {
		t.Errorf("claude-haiku-4-5 is a 4.5-generation model: adaptive returns a 400 there, so the shape must be %q, got %q", "enabled", got)
	}
	// A model nobody catalogued falls back to what the operator set.
	if got := thinkingShapeFor(newEffectiveCaps(nil), entry, "some-unlisted-model"); got != "adaptive" {
		t.Errorf("an uncatalogued model keeps the operator's setting, got %q", got)
	}
}

// Effort support is per model too: haiku-4-5 has none at all while its
// siblings take five levels.
func TestTheEffortVocabularyFollowsTheModel(t *testing.T) {
	entry := providerEntry{
		Name: "Claude (Max/Pro)", APIType: "anthropic",
		EffortLevels: []string{"low", "medium", "high", "xhigh", "max"},
	}

	full := effortLevelsFor(newEffectiveCaps(nil), entry, "claude-opus-5")
	if len(full) != 5 {
		t.Fatalf("claude-opus-5 takes five effort levels, got %v", full)
	}

	// PRESENT BUT EMPTY — the vendor documents no effort parameter for
	// this model. That must not read as "uncatalogued", which would send
	// the operator's value and earn a 400.
	none := effortLevelsFor(newEffectiveCaps(nil), entry, "claude-haiku-4-5")
	if none == nil {
		t.Fatal("claude-haiku-4-5 supports no effort parameter; that is an ANSWER (empty), not an absence (nil) — nil would pass the value through")
	}
	if len(none) != 0 {
		t.Fatalf("claude-haiku-4-5 has no effort parameter, got %v", none)
	}

	// Uncatalogued falls back to the provider-level list, still non-nil.
	fallback := effortLevelsFor(newEffectiveCaps(nil), entry, "some-unlisted-model")
	if len(fallback) != 5 {
		t.Fatalf("an uncatalogued model falls back to the entry's list, got %v", fallback)
	}
}

// The shipped table must agree with what the vendors publish, so a
// careless edit is caught here rather than by a provider's 400.
func TestTheShippedCapabilityTableMatchesTheDocumentedFacts(t *testing.T) {
	for _, c := range []struct {
		model    string
		thinking string
		effort   []string
	}{
		{"claude-opus-5", "adaptive", []string{"low", "medium", "high", "xhigh", "max"}},
		{"claude-sonnet-5", "adaptive", []string{"low", "medium", "high", "xhigh", "max"}},
		{"claude-fable-5", "adaptive", []string{"low", "medium", "high", "xhigh", "max"}},
		// The models that support max but NOT xhigh. These are the rows
		// the old dialect fallback got wrong: it handed them xhigh.
		{"claude-opus-4-6", "adaptive", []string{"low", "medium", "high", "max"}},
		{"claude-sonnet-4-6", "adaptive", []string{"low", "medium", "high", "max"}},
		// "the only extended-thinking-only model that supports effort",
		// and the one that pins the floor: it accepts exactly these.
		{"claude-opus-4-5-20251101", "enabled", []string{"low", "medium", "high"}},
		{"claude-haiku-4-5", "enabled", []string{}},
		{"gpt-5.6-sol", "", []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}},
	} {
		got, ok := newEffectiveCaps(nil).capabilityFor(c.model)
		if !ok {
			t.Errorf("%s is offered by the registry but has no capability row", c.model)
			continue
		}
		if got.Thinking != c.thinking {
			t.Errorf("%s thinking = %q, want %q", c.model, got.Thinking, c.thinking)
		}
		if len(got.Effort) != len(c.effort) {
			t.Errorf("%s effort = %v, want %v", c.model, got.Effort, c.effort)
			continue
		}
		for i := range c.effort {
			if got.Effort[i] != c.effort[i] {
				t.Errorf("%s effort[%d] = %q, want %q", c.model, i, got.Effort[i], c.effort[i])
			}
		}
	}
}

func TestTheShippedImageCapabilitiesMatchTheVerifiedSeedModels(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg providerRegistry
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	verified := map[string]bool{
		"claude-opus-5-5":          true,
		"claude-opus-5":            true,
		"claude-sonnet-5":          true,
		"claude-haiku-4-5":         true,
		"claude-fable-5":           true,
		"anthropic/claude-opus-5":  true,
		"gpt-5.6-sol":              true,
		"gpt-5.6-terra":            true,
		"gpt-5.6-luna":             true,
		"gpt-5.5":                  true,
		"gpt-5.4":                  true,
		"gpt-5.4-mini":             true,
		"gpt-5.4-pro":              true,
		"openai/gpt-5.6-sol":       true,
		"gemini-3.8-flash":         true,
		"gemini-3.6-flash":         true,
		"gemini-3.5-flash-lite":    true,
		"gemini-3.1-pro-preview":   true,
		"google/gemini-3.8-flash":  true,
		"kimi-k3":                  true,
		"moonshotai/kimi-k3":       true,
		"qwen3.8-max":              true,
		"qwen/qwen3.8-max":         true,
		"qwen/qwen3.6-27b":         true,
		"x-ai/grok-4.6":            true,
		"glm-5.3":                  false,
		"glm-5.2":                  false,
		"z-ai/glm-5.3":             false,
		"deepseek-v4-pro":          false,
		"deepseek-v4-flash":        false,
		"deepseek/deepseek-v4-pro": false,
		"openai/gpt-oss-120b":      false,
		"openai/gpt-oss-20b":       false,
	}
	seen := make(map[string]bool)
	for _, entry := range reg.Providers {
		for _, model := range entry.Models {
			want, checked := verified[model]
			if !checked {
				continue // newly discovered models need evidence, not a guess
			}
			seen[model] = true
			got := reg.ModelCapabilities[model].Images
			if got == nil {
				t.Errorf("%s has a verified image capability but the shipped row leaves it unknown", model)
			} else if *got != want {
				t.Errorf("%s images = %t, want verified %t", model, *got, want)
			}
		}
	}
	for model := range verified {
		if !seen[model] {
			t.Errorf("image evidence for %s no longer corresponds to a seeded model; review its baseline", model)
		}
	}
	for _, model := range []string{"gpt-5.4-codex", "gpt-5.4-codex-spark"} {
		if got := reg.ModelCapabilities[model].Images; got != nil {
			t.Errorf("%s returned no catalogue answer; images must stay unknown, got %t", model, *got)
		}
	}
}

// The vendor fill must never be written back into the operator's file.
// A value that looks explicitly set can never be replaced by a shipped
// correction — the failure provider_facts_test.go documents for
// credential options, and this fill had no test of its own.
func TestTheEffortFillIsNeverWrittenBackAsAnOperatorChoice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")
	raw := `{"providers":[{"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Providers[0].EffortLevels) == 0 {
		t.Fatal("the registry must SUPPLY the vocabulary at load, or the control has nothing to offer")
	}

	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(back, &out); err != nil {
		t.Fatal(err)
	}
	if lv, present := out.Providers[0]["effort_levels"]; present {
		t.Fatalf("the vendor fill was written back as an operator choice (%v) — a shipped correction could never replace it", lv)
	}

	// An operator's OWN value, by contrast, must survive the round trip.
	raw2 := `{"providers":[{"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com","effort_levels":["low"]}]}`
	if err := os.WriteFile(path, []byte(raw2), 0o600); err != nil {
		t.Fatal(err)
	}
	reg2, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg2.Providers[0].EffortLevels; len(got) != 1 || got[0] != "low" {
		t.Fatalf("an operator's own vocabulary must not be overwritten by the fill, got %v", got)
	}
	if _, err := saveProvidersFile(path, reg2); err != nil {
		t.Fatal(err)
	}
	back2, _ := os.ReadFile(path)
	if !strings.Contains(string(back2), `"low"`) {
		t.Fatalf("an operator's own vocabulary must survive the save: %s", back2)
	}
}

func TestTheReportedEffortPlanIsThePlanTheWireUses(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir, providerEntry{
		Name: "claude", APIType: "anthropic", URL: "https://api.anthropic.example",
		APIKey: "k", DefaultModel: "claude-haiku-4-5",
		Models: []string{"claude-haiku-4-5"}, ReasoningEffort: "xhigh",
	})
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "claude"
	cfg.LLM.Model = "claude-haiku-4-5"
	a := New(cfg)

	st := a.configState()
	if st.LLM.Error != "" {
		t.Fatalf("the pointer must resolve for this test to mean anything: %s", st.LLM.Error)
	}
	// claude-haiku-4-5 has NO effort parameter. The panel must say so.
	if !strings.Contains(st.LLM.EffortPlan, "NOT SENT") {
		t.Fatalf("the panel reports %q for a model with no effort parameter — it is reporting the entry's vocabulary, not the model's, which is exactly the fork", st.LLM.EffortPlan)
	}
	if st.LLM.EffortModel != "claude-haiku-4-5" {
		t.Errorf("the panel must name the model its answer is about, got %q", st.LLM.EffortModel)
	}

	// And the settings control offers that model's vocabulary, not the
	// entry's — so it cannot present a level the model rejects.
	if got := effortLevelsFor(newEffectiveCaps(nil), providerEntry{
		EffortLevels: []string{"low", "medium", "high", "xhigh", "max"},
	}, "claude-haiku-4-5"); len(got) != 0 {
		t.Fatalf("the control would offer %v for a model that accepts none", got)
	}
}

func TestAnUncataloguedModelInheritsTheDialectFloorNotAUnion(t *testing.T) {
	for _, c := range []struct {
		dialect string
		forbid  []string
	}{
		{"anthropic", []string{"xhigh", "max"}},
		{"openai", []string{"none", "minimal", "xhigh", "max"}},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "providers.json")
		// A name the shipped table does NOT carry, so the per-name
		// index misses and the dialect fallback is what answers.
		raw := `{"providers":[{"name":"Some Operator Proxy","api_type":"` + c.dialect +
			`","url":"https://proxy.example"}]}`
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		reg, err := loadProvidersFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got := reg.Providers[0].EffortLevels
		if len(got) == 0 {
			t.Fatalf("%s: a custom entry must still get a vocabulary — an effort control with nothing in it cannot be used", c.dialect)
		}
		for _, level := range got {
			for _, bad := range c.forbid {
				if level == bad {
					t.Errorf("%s: an uncatalogued model was offered %q (got %v) — that level is a per-model addition, not a dialect guarantee, so the panel offers what the wire refuses",
						c.dialect, bad, got)
				}
			}
		}
	}
}

// The floor must be what it claims: a level offered to every
// uncatalogued model of a dialect has to be one that EVERY catalogued
// effort-supporting model of that dialect accepts. Otherwise the floor
// is just a shorter union, and picking it was arithmetic rather than a
// documented fact.
func TestTheFloorIsBelowEveryCataloguedModelOfItsDialect(t *testing.T) {
	reg, err := loadProvidersFile(filepath.Join("..", "..", "config", "providers.json"))
	if err != nil {
		t.Fatal(err)
	}
	dialect := map[string]string{}
	for _, e := range reg.Providers {
		dialect[e.Name] = e.APIType
	}
	for model, cap := range reg.ModelCapabilities {
		if len(cap.Effort) == 0 {
			continue // no effort parameter at all; the floor cannot apply
		}
		d := "anthropic"
		if !strings.HasPrefix(model, "claude") && !strings.Contains(model, "/claude") {
			d = "openai"
		}
		accepts := map[string]bool{}
		for _, l := range cap.Effort {
			accepts[l] = true
		}
		for _, l := range reg.DialectEffortFloor[d] {
			if !accepts[l] {
				t.Errorf("%s (%s) does not accept %q, but the %s floor offers it to every uncatalogued model",
					model, d, l, d)
			}
		}
	}
}
