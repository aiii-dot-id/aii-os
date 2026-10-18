package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/prompt"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

func TestTheAgencySaveWritesNoRetiredKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agency":{"heuristic_nudges":true,"plan_nudge":false}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	st, err := a.applyConfigChange(map[string]any{"agency.prefer_local_for_roles": true})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Agency.PreferLocalForRoles || len(st.RestartRequired) != 0 {
		t.Errorf("role routing applies live and is echoed; nothing is restart-bound: %+v, restart %v", st.Agency, st.RestartRequired)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk struct {
		Agency map[string]json.RawMessage `json:"agency"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	for _, key := range retiredAgencyKeys {
		if _, ok := onDisk.Agency[key]; ok {
			t.Errorf("the save wrote the retired agency.%s: %s", key, raw)
		}
	}
	if string(onDisk.Agency["prefer_local_for_roles"]) != "true" {
		t.Errorf("the save did not write role routing: %s", raw)
	}
	if _, err := a.applyConfigChange(map[string]any{"agency.heuristic_nudges": true}); err == nil || !strings.Contains(err.Error(), "not an operator-settable field") {
		t.Errorf("the retired key must be refused at the door: %v", err)
	}
}

// An explicit agency.roles route wins over the prefer-local checkbox: the
// route is consulted first, before any local entry is even looked for.
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
	if got := a.resolveRunTarget("review"); got.modelID != "small" || got.fellBack() {
		t.Fatalf("with prefer-local on, the explicit route must still win: model %q fallback=%v", got.modelID, got.fellBack())
	}
}
