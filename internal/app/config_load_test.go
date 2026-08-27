package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigRejectsNegativePromptLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"max_tokens":-1,"recent_turns":20}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "prompt.max_tokens") {
		t.Fatalf("got %v, want prompt.max_tokens refusal", err)
	}
}

func TestLoadConfigRejectsRemovedField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"max_tokens":32000,"recent_turns":20,"self_ref_max_share":0.3}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "self_ref_max_share") {
		t.Fatalf("got %v, want unknown-field refusal", err)
	}
}

func TestLoadConfigDoesNotReplaceReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "read config") {
		t.Fatalf("got %v, want read failure", err)
	}
	if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
		t.Fatalf("read failure target was changed: info=%v err=%v", info, statErr)
	}
}

func TestLoadConfigDistinguishesAbsentAndExplicitZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
		"llm":{"api_key_env":""},
		"agency":{"max_subagent_depth":0},
		"genesis":{"server_url":"","firewall_url":"","bootstrap_url":""}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agency.MaxSubagentDepth != 0 {
		t.Fatalf("explicit zero depth became %d; zero must disable spawning", cfg.Agency.MaxSubagentDepth)
	}
	if cfg.Agency.MaxToolRounds != 30 || cfg.Agency.MaxParallelSubagents != 3 {
		t.Fatalf("absent agency fields lost defaults: %+v", cfg.Agency)
	}
	if cfg.LLM.APIKeyEnv != "" {
		t.Fatalf("explicitly cleared api_key_env became %q", cfg.LLM.APIKeyEnv)
	}
	if cfg.Genesis != (GenesisConfig{}) {
		t.Fatalf("explicitly cleared genesis endpoints became %+v", cfg.Genesis)
	}
}

func TestLoadConfigRefusesZeroRequiredAgencyBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"agency":{"subagent_max_mints":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "agency.subagent_max_mints") {
		t.Fatalf("got %v, want explicit zero refusal", err)
	}
}

func TestShippedConfigLoads(t *testing.T) {
	if _, err := LoadConfig(filepath.Join("..", "..", "config", "config.json")); err != nil {
		t.Fatalf("shipped config.json is invalid: %v", err)
	}
}

// prompt.max_tokens: zero means DERIVE, and the derivation must be
// reachable from config.
//
// Zero carried three contradictory meanings until 2026-08-23 — config.go
// defaulted it to 32000, config_load rejected it, providers.go treated
// it as "derive". The derivation was therefore unreachable, and a live
// identity on a 1,000,000-token model ran a 32,000-token budget, which
// folded its own working truth out of its prompt. Same convention as
// llm.retries: zero is "unset, do the sensible thing".
func TestPromptMaxTokensZeroMeansDerive(t *testing.T) {
	dir := t.TempDir()
	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// Zero survives loading — it must reach promptBudgetFor to mean anything.
	cfg, err := LoadConfig(write(t, `{"prompt":{"max_tokens":0,"recent_turns":20}}`))
	if err != nil {
		t.Fatalf("max_tokens 0 must load — it is the derive sentinel: %v", err)
	}
	if cfg.Prompt.MaxTokens != 0 {
		t.Fatalf("max_tokens 0 was rewritten to %d during load; the derivation can never see it", cfg.Prompt.MaxTokens)
	}

	// Absent behaves the same as explicit zero.
	cfg2, err := LoadConfig(write(t, `{"prompt":{"recent_turns":20}}`))
	if err != nil {
		t.Fatalf("absent max_tokens must load: %v", err)
	}
	if cfg2.Prompt.MaxTokens != 0 {
		t.Fatalf("absent max_tokens became %d, want 0 (derive)", cfg2.Prompt.MaxTokens)
	}

	// Negative is still refused, and names both accepted forms.
	if _, err := LoadConfig(write(t, `{"prompt":{"max_tokens":-5,"recent_turns":20}}`)); err == nil {
		t.Fatal("negative max_tokens must be refused")
	}

	// And the derivation actually fires: a declared window beats the
	// 32000 fallback rather than losing to it.
	entry := providerEntry{ContextLength: 1_000_000, MaxOutputTokens: 32_000}
	got := promptBudgetFor(entry, 0)
	want := 1_000_000 - 32_000 - promptSafetyTokens
	if got != want {
		t.Fatalf("derived budget = %d, want %d (window - output - margin)", got, want)
	}

	// A positive value is the operator's deliberate ceiling, honoured as given.
	if got := promptBudgetFor(entry, 50_000); got != 50_000 {
		t.Fatalf("explicit budget = %d, want 50000 — an operator ceiling is not overridden", got)
	}
}
