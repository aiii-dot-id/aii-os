package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// .
// .
// .
// .
// .
// .
// .
func TestCoachingSaveIsRestartBoundAndPersisted(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	a := New(cfg)

	st, err := a.applyConfigChange(map[string]interface{}{"agency.heuristic_nudges": true})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Agency.HeuristicNudges {
		t.Error("the readback must echo the saved value")
	}
	found := false
	for _, k := range st.RestartRequired {
		if k == "agency.heuristic_nudges" {
			found = true
		}
	}
	if !found {
		t.Errorf("coaching is captured by the resident's loop at boot; the save must say restart_required, got %v", st.RestartRequired)
	}
	// .
	if !heuristicNudgesOn(a.configSnapshot().Agency.HeuristicNudges) {
		t.Error("the live snapshot — what the next startLive reads — must carry the change")
	}
	raw, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		Agency struct {
			HeuristicNudges *bool `json:"heuristic_nudges"`
		} `json:"agency"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Agency.HeuristicNudges == nil || !*onDisk.Agency.HeuristicNudges {
		t.Errorf("the change must be on disk for the next boot: %s", raw)
	}
}

// .
// .
func TestAnExplicitRouteWinsOverPreferLocal(t *testing.T) {
	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "active", URL: "http://active.invalid", DefaultModel: "main", Default: true},
		providerEntry{Name: "review", URL: "http://review.invalid", DefaultModel: "small", ContextLength: 12000, MaxOutputTokens: 1000},
		providerEntry{Name: "local", URL: "http://local.invalid", DefaultModel: "tiny", Local: true},
	)
	a := &App{
		cfg: &Config{
			SourcePath: filepath.Join(dir, "config.json"),
			LLM:        LLMConfig{Provider: "active", Model: "main"},
			Prompt:     PromptConfig{MaxTokens: 32000},
			Agency: AgencyConfig{
				PreferLocalForRoles: true,
				Roles:               map[string]RoleRoute{"review": {Provider: "review", Model: "small"}},
			},
		},
		composer: prompt.New(ring.NewManager(), 32000),
		llmSwap:  newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: "http://active.invalid", Model: "main"})),
	}
	if got := a.resolveRunTarget("review"); got.modelID != "small" || got.fallback {
		t.Fatalf("with prefer-local on, the explicit route must still win: model %q fallback=%v", got.modelID, got.fallback)
	}
}
