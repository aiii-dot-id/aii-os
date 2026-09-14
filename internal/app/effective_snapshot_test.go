package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
// .

func writeRegistry(t *testing.T, dir string, reg providerRegistry) string {
	t.Helper()
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "providers.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func syntheticEntry() providerEntry {
	return providerEntry{
		Name: "Custom", APIType: "anthropic", URL: "https://custom.example",
		APIKey: "k", DefaultModel: "synthetic-1", Models: []string{"synthetic-1"},
		ThinkingMode: "adaptive", EffortLevels: []string{"high"},
	}
}

func resolveThrough(t *testing.T, dir string) (thinking string, effort []string) {
	t.Helper()
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	cfg.LLM.Provider = "Custom"
	cfg.LLM.Model = "synthetic-1"
	a := New(cfg)
	reg, err := a.loadProviders()
	if err != nil {
		t.Fatal(err)
	}
	cc, _, err := a.resolveLLMConfig(cfg.LLM, reg)
	if err != nil {
		t.Fatal(err)
	}
	return cc.ThinkingMode, cc.EffortLevels
}

// .
// .
// .
func TestAnOperatorCapabilityRowDrivesResolution(t *testing.T) {
	dir := t.TempDir()
	writeRegistry(t, dir, providerRegistry{
		Providers: []providerEntry{syntheticEntry()},
		ModelCapabilities: map[string]modelCapability{
			"synthetic-1": {Thinking: "enabled", Effort: []string{"low"}},
		},
	})
	thinking, effort := resolveThrough(t, dir)
	if thinking != "enabled" {
		t.Errorf("the operator catalogued synthetic-1 as thinking %q; the wire got %q — the row is inert", "enabled", thinking)
	}
	if len(effort) != 1 || effort[0] != "low" {
		t.Errorf("the operator catalogued synthetic-1 with effort [low]; the wire got %v — the row is inert", effort)
	}
}

// .
// .
func TestAnOperatorDialectFloorReachesACustomProvider(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, providerRegistry{
		Providers:          []providerEntry{{Name: "Some Operator Proxy", APIType: "openai", URL: "https://proxy.example"}},
		DialectEffortFloor: map[string][]string{"openai": {"low"}},
	})
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Providers[0].EffortLevels; len(got) != 1 || got[0] != "low" {
		t.Fatalf("the operator's openai floor is [low]; their custom provider was given %v — the fill read the embed", got)
	}
	// .
	// .
	if _, err := saveProvidersFile(path, reg); err != nil {
		t.Fatal(err)
	}
	back, _ := os.ReadFile(path)
	var out struct {
		Providers []map[string]any    `json:"providers"`
		Floor     map[string][]string `json:"dialect_effort_floor"`
	}
	if err := json.Unmarshal(back, &out); err != nil {
		t.Fatal(err)
	}
	if _, present := out.Providers[0]["effort_levels"]; present {
		t.Errorf("the floor fill was written back into the entry")
	}
	if got := out.Floor["openai"]; len(got) != 1 || got[0] != "low" {
		t.Errorf("the operator's floor did not survive the save: %v", out.Floor)
	}
}

// .
func TestAnAbsentOperatorKeyGetsTheEmbeddedFact(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, providerRegistry{
		Providers:         []providerEntry{syntheticEntry()},
		ModelCapabilities: map[string]modelCapability{"synthetic-1": {Effort: []string{"low"}}},
	})
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	eff := reg.effective()
	c, ok := eff.capabilityFor("claude-haiku-4-5")
	if !ok || c.Thinking != "enabled" || c.Effort == nil || len(c.Effort) != 0 {
		t.Errorf("claude-haiku-4-5 is not in the operator's map; the embedded row must answer: ok=%v %+v", ok, c)
	}
	if floor, ok := eff.dialectFloor("anthropic"); !ok || len(floor) == 0 {
		t.Errorf("no operator floor for anthropic; the embedded floor must answer: ok=%v %v", ok, floor)
	}
}

// .
// .
func TestAnExplicitlyEmptyOperatorValueIsNotReplaced(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, providerRegistry{
		Providers:          []providerEntry{syntheticEntry()},
		ModelCapabilities:  map[string]modelCapability{"claude-opus-5": {Effort: []string{}}},
		DialectEffortFloor: map[string][]string{"anthropic": {}},
	})
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	eff := reg.effective()
	c, ok := eff.capabilityFor("claude-opus-5")
	if !ok {
		t.Fatal("the operator's row for claude-opus-5 vanished")
	}
	if c.Effort == nil || len(c.Effort) != 0 || c.Thinking != "" {
		t.Errorf("the operator wrote an empty row for claude-opus-5 (no effort, no thinking); the embedded row replaced it: %+v", c)
	}
	// .
	// .
	if got := thinkingShapeFor(eff, providerEntry{ThinkingMode: "enabled"}, "claude-opus-5"); got != "enabled" {
		t.Errorf("thinking shape = %q; the operator's row owns the model, the entry setting should apply", got)
	}
	if floor, ok := eff.dialectFloor("anthropic"); !ok || floor == nil || len(floor) != 0 {
		t.Errorf("the operator declared an EMPTY anthropic floor; got ok=%v %v", ok, floor)
	}
}

// .
// .
// .
// .
func TestTwoRegistriesResolveDifferentlyInOneProcess(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	writeRegistry(t, dirA, providerRegistry{
		Providers:         []providerEntry{syntheticEntry()},
		ModelCapabilities: map[string]modelCapability{"synthetic-1": {Effort: []string{"low"}}},
	})
	writeRegistry(t, dirB, providerRegistry{
		Providers:         []providerEntry{syntheticEntry()},
		ModelCapabilities: map[string]modelCapability{"synthetic-1": {Effort: []string{"xhigh"}}},
	})
	_, a := resolveThrough(t, dirA)
	_, b := resolveThrough(t, dirB)
	if reflect.DeepEqual(a, b) {
		t.Fatalf("registry A and registry B catalogue synthetic-1 differently, yet both resolved to %v — a stale process-wide table", a)
	}
	if len(a) != 1 || a[0] != "low" || len(b) != 1 || b[0] != "xhigh" {
		t.Errorf("A resolved %v, B resolved %v", a, b)
	}
}

// .
// .
// .
// .
func TestAnEditsCandidateCarriesItsOwnSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, providerRegistry{Providers: []providerEntry{syntheticEntry()}})
	before, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	add := func(r *providerRegistry) error {
		if r.ModelCapabilities == nil {
			r.ModelCapabilities = map[string]modelCapability{}
		}
		r.ModelCapabilities["synthetic-1"] = modelCapability{Effort: []string{"low"}}
		return nil
	}
	candidate, err := candidateRegistry(before, add)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := candidate.effective().capabilityFor("synthetic-1"); !ok {
		t.Fatal("the candidate's snapshot does not carry the edit — validation would run against the pre-edit facts")
	}
	if _, ok := before.effective().capabilityFor("synthetic-1"); ok {
		t.Fatal("the SOURCE registry's snapshot changed — the candidate aliases it")
	}
	if _, present := before.ModelCapabilities["synthetic-1"]; present {
		t.Fatal("the source registry's map was mutated through the candidate")
	}

	// .
	cfg := &Config{SourcePath: filepath.Join(dir, "config.json")}
	a := New(cfg)
	if err := a.changeProviders("Custom", add); err != nil {
		t.Fatal(err)
	}
	after, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := after.effective().capabilityFor("synthetic-1"); !ok || len(c.Effort) != 1 || c.Effort[0] != "low" {
		t.Fatalf("the edit was not persisted into the effective snapshot: ok=%v %+v", ok, c)
	}
}

// .
// .
// .
// .
func TestADirectFileEditIsObservedByTheReloadLoader(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, providerRegistry{Providers: []providerEntry{syntheticEntry()}})
	v1, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, dir, providerRegistry{
		Providers:         []providerEntry{syntheticEntry()},
		ModelCapabilities: map[string]modelCapability{"synthetic-1": {Thinking: "enabled"}},
	})
	v2, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(v1, v2) {
		t.Fatal("the reload path compares loads with DeepEqual; a capability edit must make them differ")
	}
	if got := thinkingShapeFor(v2.effective(), syntheticEntry(), "synthetic-1"); got != "enabled" {
		t.Errorf("after the file edit the snapshot must answer %q, got %q", "enabled", got)
	}
	if got := thinkingShapeFor(v1.effective(), syntheticEntry(), "synthetic-1"); got != "adaptive" {
		t.Errorf("the earlier load's snapshot must be unchanged (entry setting), got %q", got)
	}
}

// .
// .
// .
func TestShippedBehaviourIsUnchangedWithoutAnOverride(t *testing.T) {
	dir := t.TempDir()
	path := writeRegistry(t, dir, providerRegistry{Providers: []providerEntry{syntheticEntry()}})
	reg, err := loadProvidersFile(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, bare := reg.effective(), newEffectiveCaps(nil)
	for _, model := range []string{"claude-opus-5", "claude-haiku-4-5", "claude-opus-4-5-20251101", "gpt-5.6-sol"} {
		l, lok := loaded.capabilityFor(model)
		b, bok := bare.capabilityFor(model)
		if !lok || !bok || !reflect.DeepEqual(l, b) {
			t.Errorf("%s: loaded-without-override %+v (%v) differs from the shipped table %+v (%v)", model, l, lok, b, bok)
		}
	}
	for _, d := range []string{"anthropic", "openai"} {
		l, _ := loaded.dialectFloor(d)
		b, _ := bare.dialectFloor(d)
		if !reflect.DeepEqual(l, b) || len(l) == 0 {
			t.Errorf("%s floor: loaded %v, shipped %v", d, l, b)
		}
	}
	// .
	c, _ := loaded.capabilityFor("claude-opus-5")
	if len(c.Effort) > 0 {
		c.Effort[0] = "corrupted"
	}
	if again, _ := loaded.capabilityFor("claude-opus-5"); strings.Join(again.Effort, ",") == strings.Join(c.Effort, ",") {
		t.Error("a consumer's write reached the snapshot")
	}
}
