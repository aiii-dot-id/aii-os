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

// .
// .
// .
// .
// .
// .
// .
// .
// .
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

	// .
	cfg, err := LoadConfig(write(t, `{"prompt":{"max_tokens":0,"recent_turns":20}}`))
	if err != nil {
		t.Fatalf("max_tokens 0 must load — it is the derive sentinel: %v", err)
	}
	if cfg.Prompt.MaxTokens != 0 {
		t.Fatalf("max_tokens 0 was rewritten to %d during load; the derivation can never see it", cfg.Prompt.MaxTokens)
	}

	// .
	cfg2, err := LoadConfig(write(t, `{"prompt":{"recent_turns":20}}`))
	if err != nil {
		t.Fatalf("absent max_tokens must load: %v", err)
	}
	if cfg2.Prompt.MaxTokens != 0 {
		t.Fatalf("absent max_tokens became %d, want 0 (derive)", cfg2.Prompt.MaxTokens)
	}

	// .
	if _, err := LoadConfig(write(t, `{"prompt":{"max_tokens":-5,"recent_turns":20}}`)); err == nil {
		t.Fatal("negative max_tokens must be refused")
	}

	// .
	// .
	entry := providerEntry{ContextLength: 1_000_000, MaxOutputTokens: 32_000}
	got, source := promptBudgetFor(entry, 0)
	want := 1_000_000 - 32_000 - promptSafetyTokens
	if got != want {
		t.Fatalf("derived budget = %d, want %d (window - output - margin)", got, want)
	}
	if source != budgetDerived {
		t.Fatalf("a budget taken from the window is %q, want %q", source, budgetDerived)
	}

	// .
	if got, source := promptBudgetFor(entry, 50_000); got != 50_000 || source != budgetDeclared {
		t.Fatalf("explicit budget = %d/%q, want 50000/%q — an operator ceiling is not overridden", got, source, budgetDeclared)
	}

	// .
	// .
	if got, source := promptBudgetFor(providerEntry{Name: "Local (oMLX)"}, 0); got != 32000 || source != budgetFallback {
		t.Fatalf("undeclared window = %d/%q, want 32000/%q", got, source, budgetFallback)
	}
}

// .
// .
// .
// .
func TestLoadConfigRefusesANegativeRing3Bound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"ring3_max_chars":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "prompt.ring3_max_chars") {
		t.Fatalf("got %v, want prompt.ring3_max_chars refusal", err)
	}

	// .
	// .
	zero := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(zero, []byte(`{"prompt":{"recent_turns":20,"ring3_max_chars":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(zero)
	if err != nil || cfg.Prompt.Ring3MaxChars != 0 {
		t.Fatalf("zero ring3_max_chars: cfg=%v err=%v", cfg.Prompt.Ring3MaxChars, err)
	}
}

// .
// .
func TestLoadConfigAcceptsAUTF8ByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := "\xef\xbb\xbf" + `{"prompt": {"recent_turns": 7}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a byte-order mark is not content: %v", err)
	}
	if cfg.Prompt.RecentTurns != 7 {
		t.Fatalf("the file behind the mark was not read: recent_turns=%d", cfg.Prompt.RecentTurns)
	}
}
