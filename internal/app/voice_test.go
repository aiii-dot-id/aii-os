package app

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// .
// .
// .
// .
// .
// .
// .
func TestSubstrateDeclaresItsOwnVoice(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "Voice",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	t.Chdir(dir)

	a := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "test", "https://127.0.0.1:1", "m", "sk-x"),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	})

	if got := a.buildFirstbootHandler().Speaker; got != "system" {
		t.Fatalf("before birth the substrate answers AS ITSELF, got speaker %q", got)
	}
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	if got := a.buildLiveHandler().Speaker; got != "identity" {
		t.Fatalf("a born mind speaks for itself, got speaker %q", got)
	}

	// .
	// .
	msg, err := a.handleBootstrapMessage(t.Context(), "hello?")
	if err != nil {
		t.Fatal(err)
	}
	firstPerson := map[string]bool{
		"i": true, "i'm": true, "i've": true, "im": true, "me": true,
		"my": true, "mine": true, "myself": true, "voice": true,
	}
	words := strings.FieldsFunc(strings.ToLower(msg), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	})
	for _, w := range words {
		if firstPerson[w] {
			t.Fatalf("the pre-birth reply speaks in a mind's voice (%q): %s", w, msg)
		}
	}
}

// .
// .
// .
// .
func TestReplayNeverEditsWhatWasSaid(t *testing.T) {
	long := strings.Repeat("the mind's first words, at length. ", 60)
	for _, role := range []string{"resident", "operator"} {
		if got := replayContent(role, long); got != long {
			t.Fatalf("role %q must replay verbatim: %d bytes in, %d out", role, len(long), len(got))
		}
	}
	if got := replayContent("resident", long); strings.Contains(got, "trimmed for replay") {
		t.Fatal("substrate text spliced into what the identity said")
	}
	// .
	toolRow := "→ read(a.txt)\n← " + long
	if got := replayContent("system", toolRow); len(got) >= len(toolRow) {
		t.Fatalf("substrate-authored rows stay bounded, got %d of %d bytes", len(got), len(toolRow))
	}
	unicodeRow := "→ read(界.txt)\n← " + strings.Repeat("界", 1000)
	if got := replayContent("system", unicodeRow); !utf8.ValidString(got) {
		t.Fatal("bounded replay split a UTF-8 rune")
	}
}

// .
// .
// .
// .
// .
// .
func TestKeylessProviderBoots(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "Voice",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	t.Chdir(dir)

	app := New(&Config{
		Identity: IdentityConfig{KeyPath: filepath.Join(dir, "identity.sec"),
			LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		},
		LLM:        withTestProvider(t, dir, "local", "http://127.0.0.1:11434/v1", "llama", ""),
		SourcePath: filepath.Join(dir, "config.json"),
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		Agency:     defaultConfig().Agency,
	})
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("a local endpoint needs no key — boot must not refuse: %v", err)
	}
	app.Stop()
}
