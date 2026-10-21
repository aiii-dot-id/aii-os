package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/store"
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
	if cfg.Agency.MaxToolRounds != limits.Of(30) || cfg.Agency.MaxParallelSubagents != 3 {
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
	got, source := promptBudgetFor(entry, 0)
	want := 1_000_000 - 32_000 - promptSafetyTokens
	if got != want {
		t.Fatalf("derived budget = %d, want %d (window - output - margin)", got, want)
	}
	if source != budgetDerived {
		t.Fatalf("a budget taken from the window is %q, want %q", source, budgetDerived)
	}

	// A positive value is the operator's deliberate ceiling, honoured as given.
	if got, source := promptBudgetFor(entry, 50_000); got != 50_000 || source != budgetDeclared {
		t.Fatalf("explicit budget = %d/%q, want 50000/%q — an operator ceiling is not overridden", got, source, budgetDeclared)
	}

	// Nothing declared and nothing discovered: the number is a fallback and
	// says so, because that is the case the operator must be able to see.
	if got, source := promptBudgetFor(providerEntry{Name: "Local (oMLX)"}, 0); got != 32000 || source != budgetFallback {
		t.Fatalf("undeclared window = %d/%q, want 32000/%q", got, source, budgetFallback)
	}
}

// prompt.ring3_max_chars bounds the Ring 3 working-truth view at its
// producer. Zero is "unset, take the default" like the other prompt
// bounds; a negative one is not a bound at all (it would refuse every
// view ever written) and is refused where the operator can still fix it.
func TestLoadConfigRefusesANegativeRing3Bound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"ring3_max_chars":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "prompt.ring3_max_chars") {
		t.Fatalf("got %v, want prompt.ring3_max_chars refusal", err)
	}

	// Zero loads and stays zero: the facility resolves it to the default,
	// and a loader that rewrote it would hide the operator's silence.
	zero := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(zero, []byte(`{"prompt":{"recent_turns":20,"ring3_max_chars":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(zero)
	if err != nil || cfg.Prompt.Ring3MaxChars != 0 {
		t.Fatalf("zero ring3_max_chars: cfg=%v err=%v", cfg.Prompt.Ring3MaxChars, err)
	}
}

// prompt.surfacing_max_chars bounds one DREAM note at its producer, the
// same way: zero is "unset, take the default", and a negative one would
// refuse every note ever written, so it is refused at load.
func TestLoadConfigRefusesANegativeSurfacingBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"surfacing_max_chars":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "prompt.surfacing_max_chars") {
		t.Fatalf("got %v, want prompt.surfacing_max_chars refusal", err)
	}
	set := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(set, []byte(`{"prompt":{"recent_turns":20,"surfacing_max_chars":1800}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(set)
	if err != nil || cfg.Prompt.SurfacingMaxChars != 1800 {
		t.Fatalf("surfacing_max_chars 1800: cfg=%v err=%v", cfg.Prompt.SurfacingMaxChars, err)
	}
	// And the number reaches the facility: a bound the operator wrote and
	// the facility never heard of is a default with a config key beside it.
	if got := dreamConfig(*cfg); got.MaxChars != 1800 || got.Threshold != 1 {
		t.Fatalf("the operator's bound did not reach DREAM: %+v", got)
	}
}

func TestLoadConfigRefusesANegativePortraitBudgetAndABootHandsTheStoreTheOperatorsNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"self_model_max_tokens":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "prompt.self_model_max_tokens") {
		t.Fatalf("got %v, want prompt.self_model_max_tokens refusal", err)
	}
	set := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(set, []byte(`{"prompt":{"recent_turns":20,"self_model_max_tokens":3200}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(set)
	if err != nil || cfg.Prompt.SelfModelMaxTokens != 3200 {
		t.Fatalf("self_model_max_tokens 3200: cfg=%v err=%v", cfg.Prompt.SelfModelMaxTokens, err)
	}

	for _, want := range []int{3200, 0} {
		dir := t.TempDir()
		keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Portrait")
		boot := safebootConfig(t, dir, "Portrait", keyPath, ledgerPath, dbPath)
		boot.Prompt.SelfModelMaxTokens = want
		a := New(boot)
		t.Cleanup(func() { stopApp(t, a) })
		if err := startLiveForTest(a); err != nil {
			t.Fatalf("boot: %v", err)
		}
		expect := want
		if expect == 0 {
			expect = store.SelfModelDefaultMaxTokens
		}
		if got := a.store.SelfModelMaxTokens(); got != expect {
			t.Fatalf("with prompt.self_model_max_tokens %d the store holds a new portrait to %d; want %d", want, got, expect)
		}
	}
}

// prompt.tensions_max_chars bounds the contradiction view a facility is
// shown: the same convention, refused negative at load, and the number
// reaches BOTH facilities that read the view — with the other numbers
// each already carried, which the move to a function must not have lost.
func TestLoadConfigRefusesANegativeTensionsBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"tensions_max_chars":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "prompt.tensions_max_chars") {
		t.Fatalf("got %v, want prompt.tensions_max_chars refusal", err)
	}
	set := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(set, []byte(`{"prompt":{"recent_turns":20,"tensions_max_chars":1500,"ring3_max_chars":9000,"surfacing_max_chars":1800}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(set)
	if err != nil {
		t.Fatal(err)
	}
	if got := dreamConfig(*cfg); got.TensionsMaxChars != 1500 || got.MaxChars != 1800 || got.Threshold != 1 {
		t.Fatalf("the operator's numbers did not reach DREAM: %+v", got)
	}
	if got := consolidateConfig(*cfg); got.TensionsMaxChars != 1500 || got.Ring3MaxChars != 9000 || got.Threshold != 3 ||
		got.Salience != cfg.Memory.Salience {
		t.Fatalf("the operator's numbers did not reach CONSOLIDATE: %+v", got)
	}
}

// A config.json saved by Notepad or Windows PowerShell begins with a
// UTF-8 byte-order mark; the loader reads it as the same file.
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

// agency.outcome_window is how many records one metabolism tick reads
// looking for an outcome: refused when not positive, 512 when unset, and
// it reaches the facility that probes — with the bound the intake's
// observation shares with a dream note.
func TestLoadConfigRefusesANegativeOutcomeWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20},"agency":{"outcome_window":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "agency.outcome_window") {
		t.Fatalf("got %v, want agency.outcome_window refusal", err)
	}
	unset := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(unset, []byte(`{"prompt":{"recent_turns":20}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(unset)
	if err != nil || cfg.Agency.OutcomeWindow != 512 {
		t.Fatalf("unset outcome_window: cfg=%v err=%v, want 512", cfg, err)
	}
	set := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(set, []byte(`{"prompt":{"recent_turns":20,"surfacing_max_chars":1800},"agency":{"outcome_window":64}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(set)
	if err != nil {
		t.Fatal(err)
	}
	if got := consolidateConfig(*cfg); got.OutcomeWindow != 64 || got.ObservationMaxChars != 1800 {
		t.Fatalf("the operator's numbers did not reach the outcome intake: %+v", got)
	}
}

// prompt.dream_conversation_max_chars bounds the unread conversation one
// DREAM pass reads: refused negative at load, zero takes the default, and
// it reaches DREAM — with the room note, whose text is the voice host's
// and is handed down, never copied.
func TestLoadConfigRefusesANegativeConversationBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20,"dream_conversation_max_chars":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "prompt.dream_conversation_max_chars") {
		t.Fatalf("got %v, want prompt.dream_conversation_max_chars refusal", err)
	}
	set := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(set, []byte(`{"prompt":{"recent_turns":20,"dream_conversation_max_chars":24000}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(set)
	if err != nil {
		t.Fatal(err)
	}
	got := dreamConfig(*cfg)
	if got.ConversationMaxChars != 24000 {
		t.Fatalf("the operator's number did not reach DREAM: %+v", got)
	}
	if got.RoomNotePrefix != voiceMarker+voiceRoomNote || got.RoomNotePrefix == "" {
		t.Fatalf("DREAM was handed the room note %q, want the voice host's own %q", got.RoomNotePrefix, voiceMarker+voiceRoomNote)
	}
}

// THE SPACING OF THE IDENTITY'S DOOR IS A NUMBER IN CONFIG, held at the
// boundary: negative, or longer than a day, is refused; unset takes the
// default; a set one reaches the door.
func TestLoadConfigHoldsTheOnDemandSpacingToItsBounds(t *testing.T) {
	for _, bad := range []string{`-1`, `86401`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(`{"prompt":{"recent_turns":20},"maintenance":{"on_demand_spacing_seconds":`+bad+`}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "maintenance.on_demand_spacing_seconds") {
			t.Errorf("%s: got %v, want a refusal naming the key", bad, err)
		}
	}
	unset := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(unset, []byte(`{"prompt":{"recent_turns":20}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(unset)
	if err != nil || continuity.OnDemandSpacing(cfg.Maintenance) != continuity.DefaultOnDemandSpacing {
		t.Fatalf("unset: %v %v, want the default", cfg, err)
	}
	set := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(set, []byte(`{"prompt":{"recent_turns":20},"maintenance":{"on_demand_spacing_seconds":90}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err = LoadConfig(set); err != nil || continuity.OnDemandSpacing(cfg.Maintenance) != 90*time.Second {
		t.Fatalf("set to 90: %v %v", cfg, err)
	}
}
