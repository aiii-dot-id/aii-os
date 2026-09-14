package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestTheThinkingShapeFollowsTheModelNotTheEntry(t *testing.T) {
	entry := providerEntry{Name: "Claude (Max/Pro)", APIType: "anthropic", ThinkingMode: "adaptive"}

	if got := thinkingShapeFor(newEffectiveCaps(nil), entry, "claude-opus-5"); got != "adaptive" {
		t.Errorf("claude-opus-5 takes adaptive thinking, got %q", got)
	}
	// .
	if got := thinkingShapeFor(newEffectiveCaps(nil), entry, "claude-haiku-4-5"); got != "enabled" {
		t.Errorf("claude-haiku-4-5 is a 4.5-generation model: adaptive returns a 400 there, so the shape must be %q, got %q", "enabled", got)
	}
	// .
	if got := thinkingShapeFor(newEffectiveCaps(nil), entry, "some-unlisted-model"); got != "adaptive" {
		t.Errorf("an uncatalogued model keeps the operator's setting, got %q", got)
	}
}

// .
// .
func TestTheEffortVocabularyFollowsTheModel(t *testing.T) {
	entry := providerEntry{
		Name: "Claude (Max/Pro)", APIType: "anthropic",
		EffortLevels: []string{"low", "medium", "high", "xhigh", "max"},
	}

	full := effortLevelsFor(newEffectiveCaps(nil), entry, "claude-opus-5")
	if len(full) != 5 {
		t.Fatalf("claude-opus-5 takes five effort levels, got %v", full)
	}

	// .
	// .
	// .
	none := effortLevelsFor(newEffectiveCaps(nil), entry, "claude-haiku-4-5")
	if none == nil {
		t.Fatal("claude-haiku-4-5 supports no effort parameter; that is an ANSWER (empty), not an absence (nil) — nil would pass the value through")
	}
	if len(none) != 0 {
		t.Fatalf("claude-haiku-4-5 has no effort parameter, got %v", none)
	}

	// .
	fallback := effortLevelsFor(newEffectiveCaps(nil), entry, "some-unlisted-model")
	if len(fallback) != 5 {
		t.Fatalf("an uncatalogued model falls back to the entry's list, got %v", fallback)
	}
}

// .
// .
func TestTheShippedCapabilityTableMatchesTheDocumentedFacts(t *testing.T) {
	for _, c := range []struct {
		model    string
		thinking string
		effort   []string
	}{
		{"claude-opus-5", "adaptive", []string{"low", "medium", "high", "xhigh", "max"}},
		{"claude-sonnet-5", "adaptive", []string{"low", "medium", "high", "xhigh", "max"}},
		{"claude-fable-5", "adaptive", []string{"low", "medium", "high", "xhigh", "max"}},
		// .
		// .
		{"claude-opus-4-6", "adaptive", []string{"low", "medium", "high", "max"}},
		{"claude-sonnet-4-6", "adaptive", []string{"low", "medium", "high", "max"}},
		// .
		// .
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

// .
// .
// .
// .
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

	// .
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
	// .
	if !strings.Contains(st.LLM.EffortPlan, "NOT SENT") {
		t.Fatalf("the panel reports %q for a model with no effort parameter — it is reporting the entry's vocabulary, not the model's, which is exactly the fork", st.LLM.EffortPlan)
	}
	if st.LLM.EffortModel != "claude-haiku-4-5" {
		t.Errorf("the panel must name the model its answer is about, got %q", st.LLM.EffortModel)
	}

	// .
	// .
	if got := effortLevelsFor(newEffectiveCaps(nil), providerEntry{
		EffortLevels: []string{"low", "medium", "high", "xhigh", "max"},
	}, "claude-haiku-4-5"); len(got) != 0 {
		t.Fatalf("the control would offer %v for a model that accepts none", got)
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
// .
// .
// .
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
		// .
		// .
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

// .
// .
// .
// .
// .
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
			continue
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
