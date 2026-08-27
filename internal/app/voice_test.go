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

// The substrate never speaks in a voice that is not its own. This is
// pinned STRUCTURALLY rather than by reading strings: the speaker is a
// key the handler declares, and the browser renders the identity's voice
// only for an explicit "identity". Before this, the live frame carried
// no speaker and the browser inferred one from whether an identity
// existed — so the firstboot pointer spoke in the identity's bubble
// (review 2026-08-20).
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
		Identity: IdentityConfig{
			Name: "Voice", KeyPath: filepath.Join(dir, "identity.sec"),
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

	// And the pre-birth reply carries no borrowed voice of its own: no
	// first person, no name, no claim to exist.
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

// The founding record is never edited by the substrate. A long turn by
// the identity or the operator replays WHOLE; only substrate-authored
// tool rows are bounded, where the marker is the substrate trimming its
// own text.
func TestReplayNeverEditsWhatWasSaid(t *testing.T) {
	long := strings.Repeat("the mind's first words, at length. ", 60) // ≫ 900 bytes
	for _, role := range []string{"resident", "operator"} {
		if got := replayContent(role, long); got != long {
			t.Fatalf("role %q must replay verbatim: %d bytes in, %d out", role, len(long), len(got))
		}
	}
	if got := replayContent("resident", long); strings.Contains(got, "trimmed for replay") {
		t.Fatal("substrate text spliced into what the identity said")
	}
	// Tool rows still get bounded — the substrate may edit its own.
	toolRow := "→ read(a.txt)\n← " + long
	if got := replayContent("system", toolRow); len(got) >= len(toolRow) {
		t.Fatalf("substrate-authored rows stay bounded, got %d of %d bytes", len(got), len(toolRow))
	}
	unicodeRow := "→ read(界.txt)\n← " + strings.Repeat("界", 1000)
	if got := replayContent("system", unicodeRow); !utf8.ValidString(got) {
		t.Fatal("bounded replay split a UTF-8 rune")
	}
}

// A provider that needs no key must boot. Birth PROVES the substrate
// answers before it mints, so refusing the same configuration at boot
// was a heuristic standing in for a test that already ran — and because
// both retry guards refuse a resubmission, that refusal was permanent:
// a local model produced an identity that existed and could not open
// (review 2026-08-20).
func TestKeylessProviderBoots(t *testing.T) {
	dir := t.TempDir()
	result := genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "Keyless",
		KeyPath:    filepath.Join(dir, "identity.sec"),
		LedgerPath: filepath.Join(dir, "ledger.jsonl"),
		DBPath:     filepath.Join(dir, "aii.db"),
	})
	result.Ledger.Close()
	t.Chdir(dir)

	app := New(&Config{
		Identity: IdentityConfig{
			Name: "Keyless", KeyPath: filepath.Join(dir, "identity.sec"),
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
