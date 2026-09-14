package mobile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

func writeIdentityFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
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
func TestStaleContainerPathsFindThePreservedIdentity(t *testing.T) {
	live := t.TempDir()
	dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")

	// .
	writeIdentityFile(t, filepath.Join(live, "data", "ledger.jsonl"), "the-record")
	writeIdentityFile(t, filepath.Join(live, "data", "aii.db"), "the-projection")
	writeIdentityFile(t, filepath.Join(live, "data", "identity.sec"), "the-key")

	cfg := &app.Config{}
	cfg.Identity.LedgerPath = filepath.Join(dead, "data", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dead, "data", "aii.db")
	cfg.Identity.KeyPath = filepath.Join(dead, "data", "identity.sec")

	if err := rerootContainerPaths(cfg, live); err != nil {
		t.Fatalf("reroot refused a clean container: %v", err)
	}

	for name, tc := range map[string]struct{ got, want string }{
		"ledger": {cfg.Identity.LedgerPath, "the-record"},
		"db":     {cfg.Identity.DBPath, "the-projection"},
		"key":    {cfg.Identity.KeyPath, "the-key"},
	} {
		b, err := os.ReadFile(tc.got)
		if err != nil {
			t.Fatalf("%s does not point at the preserved file: %s (%v)", name, tc.got, err)
		}
		if string(b) != tc.want {
			t.Fatalf("%s points at the wrong file: %s", name, tc.got)
		}
	}
}

// .
// .
// .
// .
func TestFlattenedStalePathsHealToTheStandardLayout(t *testing.T) {
	live := t.TempDir()
	dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")

	writeIdentityFile(t, filepath.Join(live, "data", "ledger.jsonl"), "the-record")

	cfg := &app.Config{}
	cfg.Identity.LedgerPath = filepath.Join(dead, "ledger.jsonl")

	if err := rerootContainerPaths(cfg, live); err != nil {
		t.Fatalf("reroot refused: %v", err)
	}
	b, err := os.ReadFile(cfg.Identity.LedgerPath)
	if err != nil || string(b) != "the-record" {
		t.Fatalf("flattened path did not heal to the preserved record: %s (%v)", cfg.Identity.LedgerPath, err)
	}
}

// .
// .
// .
func TestNothingPreservedLandsOnTheStandardLayout(t *testing.T) {
	live := t.TempDir()
	dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")

	cfg := &app.Config{}
	cfg.Identity.LedgerPath = filepath.Join(dead, "data", "ledger.jsonl")

	if err := rerootContainerPaths(cfg, live); err != nil {
		t.Fatalf("reroot refused a blank container: %v", err)
	}
	want := filepath.Join(live, "data", "ledger.jsonl")
	if cfg.Identity.LedgerPath != want {
		t.Fatalf("blank container: want the standard layout %s, got %s", want, cfg.Identity.LedgerPath)
	}
}

// .
// .
// .
// .
// .
func TestAmbiguousIdentityRecordsRefuse(t *testing.T) {
	for name, stale := range map[string][]string{
		"layered":   {"data", "ledger.jsonl"},
		"flattened": {"ledger.jsonl"},
	} {
		t.Run(name, func(t *testing.T) {
			live := t.TempDir()
			dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")

			writeIdentityFile(t, filepath.Join(live, "data", "ledger.jsonl"), "original")
			writeIdentityFile(t, filepath.Join(live, "ledger.jsonl"), "forked")

			cfg := &app.Config{}
			cfg.Identity.LedgerPath = filepath.Join(append([]string{dead}, stale...)...)

			err := rerootContainerPaths(cfg, live)
			if err == nil {
				t.Fatalf("two identity records and no refusal — resolved to %s", cfg.Identity.LedgerPath)
			}
			if !strings.Contains(err.Error(), "ambiguous") {
				t.Fatalf("refusal does not name the ambiguity: %v", err)
			}
		})
	}
}

// .
// .
// .
func TestUsablePathsAreLeftAlone(t *testing.T) {
	live := t.TempDir()
	inside := filepath.Join(live, "data", "ledger.jsonl")

	cfg := &app.Config{}
	cfg.Identity.LedgerPath = inside
	cfg.Identity.DBPath = filepath.Join("data", "aii.db")
	cfg.Identity.KeyPath = ""

	if err := rerootContainerPaths(cfg, live); err != nil {
		t.Fatalf("reroot refused usable paths: %v", err)
	}

	if cfg.Identity.LedgerPath != inside {
		t.Fatalf("a path inside the container was moved: %s", cfg.Identity.LedgerPath)
	}
	if cfg.Identity.DBPath != filepath.Join("data", "aii.db") {
		t.Fatalf("a relative path was rewritten: %s", cfg.Identity.DBPath)
	}
	if cfg.Identity.KeyPath != "" {
		t.Fatalf("an empty path was invented: %s", cfg.Identity.KeyPath)
	}
}

// .
// .
// .
// .
func TestUnreadableConfigIsQuarantinedNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// .
	if err := os.WriteFile(path, []byte(`{"llm":{"endpoint":"https://api.openai.com/v1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := app.LoadConfig(path); err == nil {
		t.Fatal("fixture is wrong: this config should not parse")
	}

	cfg, err := quarantineUnreadableConfig(path, os.ErrInvalid)
	if err != nil {
		t.Fatalf("quarantine failed instead of recovering: %v", err)
	}
	if cfg == nil {
		t.Fatal("no default config was produced")
	}

	// .
	entries, _ := os.ReadDir(dir)
	var aside bool
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable-") {
			aside = true
		}
	}
	if !aside {
		t.Fatalf("the unreadable config was not set aside: %v", entries)
	}
}

// .
// .
// .
func TestEmptyContainerDirIsRefused(t *testing.T) {
	if _, err := Start("config.json", ""); err == nil {
		t.Fatal("an empty container dir was accepted")
	}
}

// .
// .
// .
// .
func TestQuarantineSalvagesIdentityLocations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"llm":{"endpoint":"https://api.openai.com/v1"},"identity":{"ledger_path":"vault/ledger.jsonl","db_path":"vault/aii.db","key_path":"vault/identity.sec"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.LoadConfig(path); err == nil {
		t.Fatal("fixture is wrong: this config should not parse strictly")
	}

	cfg, err := quarantineUnreadableConfig(path, os.ErrInvalid)
	if err != nil {
		t.Fatalf("quarantine failed: %v", err)
	}
	if cfg.Identity.LedgerPath != "vault/ledger.jsonl" || cfg.Identity.KeyPath != "vault/identity.sec" || cfg.Identity.DBPath != "vault/aii.db" {
		t.Fatalf("identity locations were not salvaged: %+v", cfg.Identity)
	}

	// .
	ondisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vault/ledger.jsonl", "vault/identity.sec", "vault/aii.db"} {
		if !strings.Contains(string(ondisk), want) {
			t.Fatalf("persisted config lost the salvage (%q missing):\n%s", want, ondisk)
		}
	}
}

// .
// .
func TestQuarantineCorruptBytesStillRecover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{{{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.LoadConfig(path); err == nil {
		t.Fatal("fixture is wrong: this config should not parse")
	}
	cfg, err := quarantineUnreadableConfig(path, os.ErrInvalid)
	if err != nil {
		t.Fatalf("quarantine failed on corrupt bytes: %v", err)
	}
	if cfg.Identity.LedgerPath != filepath.Join("data", "ledger.jsonl") {
		t.Fatalf("corrupt bytes should leave defaults untouched, got %q", cfg.Identity.LedgerPath)
	}
}

// .
// .
// .
func TestMixedIdentityResolutionsRefuse(t *testing.T) {
	live := t.TempDir()
	dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")

	writeIdentityFile(t, filepath.Join(live, "data", "ledger.jsonl"), "the-record")
	writeIdentityFile(t, filepath.Join(live, "identity.sec"), "a-stray-key")

	cfg := &app.Config{}
	cfg.Identity.LedgerPath = filepath.Join(dead, "data", "ledger.jsonl")
	cfg.Identity.KeyPath = filepath.Join(dead, "data", "identity.sec")

	err := rerootContainerPaths(cfg, live)
	if err == nil {
		t.Fatalf("a mixed identity set was accepted: ledger=%s key=%s", cfg.Identity.LedgerPath, cfg.Identity.KeyPath)
	}
	if !strings.Contains(err.Error(), "different directories") {
		t.Fatalf("refusal does not name the mix: %v", err)
	}
}
