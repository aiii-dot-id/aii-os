package mobile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
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

func TestStaleContainerPathsFindThePreservedIdentity(t *testing.T) {
	live := t.TempDir()
	dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")

	// The preserved identity, exactly where the standard layout put it.
	writeIdentityFile(t, filepath.Join(live, "data", "ledger.jsonl"), "the-record")
	writeIdentityFile(t, filepath.Join(live, "data", "aii.db"), "the-projection")
	writeIdentityFile(t, filepath.Join(live, "data", "identity.sec"), "the-key")

	cfg := &app.Config{}
	cfg.Identity.LedgerPath = filepath.Join(dead, "data", "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(dead, "data", "aii.db")
	cfg.Identity.KeyPath = filepath.Join(dead, "data", "identity.sec")

	capture := logsink.CaptureForTest(t)
	if err := rerootContainerPaths(cfg, live); err != nil {
		t.Fatalf("reroot refused a clean container: %v", err)
	}
	if want := "config.decision: mobile: ledger_path pointed outside this container (" + filepath.Join(dead, "data", "ledger.jsonl") +
		") — re-rooted to " + filepath.Join(live, "data", "ledger.jsonl"); !capture.Contains(want) {
		t.Fatalf("the re-root is not told as a configuration decision:\n%s", capture.String())
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

// A config the old flattening bug already damaged (the data/ segment
// stripped from its paths) heals: nothing exists at the flattened
// suffix, so the path lands on the standard layout — where the
// preserved identity actually lives.
func TestFlattenedStalePathsHealToTheStandardLayout(t *testing.T) {
	live := t.TempDir()
	dead := filepath.Join(t.TempDir(), "DD60E10E-DEAD", "Documents")

	writeIdentityFile(t, filepath.Join(live, "data", "ledger.jsonl"), "the-record")

	cfg := &app.Config{}
	cfg.Identity.LedgerPath = filepath.Join(dead, "ledger.jsonl") // flattened shape

	if err := rerootContainerPaths(cfg, live); err != nil {
		t.Fatalf("reroot refused: %v", err)
	}
	b, err := os.ReadFile(cfg.Identity.LedgerPath)
	if err != nil || string(b) != "the-record" {
		t.Fatalf("flattened path did not heal to the preserved record: %s (%v)", cfg.Identity.LedgerPath, err)
	}
}

// A truly blank container with a stale config: nothing to find, so the
// path lands on the STANDARD layout for FIRSTBOOT to build — never a
// flattened basename the rest of the system does not use.
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

// Two files in the container both claiming to be the record — the
// flattening bug's own wake: a preserved data/ledger.jsonl beside a
// forked ledger.jsonl. Choosing silently is minting or erasing an
// identity by side effect; the reroot must refuse, whichever shape the
// stale path arrives in.
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

// A path already inside the container, or a relative one, is correct as
// written — chdir handles relatives, and an absolute inside the live
// container is where it should be.
func TestUsablePathsAreLeftAlone(t *testing.T) {
	live := t.TempDir()
	inside := filepath.Join(live, "data", "ledger.jsonl")

	cfg := &app.Config{}
	cfg.Identity.LedgerPath = inside
	cfg.Identity.DBPath = filepath.Join("data", "aii.db") // relative
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

// A config this build cannot read must never cost the operator their
// identity. On mobile the file is unreachable — the only way to remove a
// bad one is to delete the app, and that deletes the ledger and the key
// with it. So it is set aside, not honoured and not fatal.
func TestUnreadableConfigIsQuarantinedNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// The real shape that broke iOS: a field this build no longer knows.
	if err := os.WriteFile(path, []byte(`{"llm":{"endpoint":"https://api.openai.com/v1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := app.LoadConfig(path); err == nil {
		t.Fatal("fixture is wrong: this config should not parse")
	}

	capture := logsink.CaptureForTest(t)
	cfg, err := quarantineUnreadableConfig(path, os.ErrInvalid)
	if err != nil {
		t.Fatalf("quarantine failed instead of recovering: %v", err)
	}
	if cfg == nil {
		t.Fatal("no default config was produced")
	}
	if !capture.Contains("boot.refusal: mobile: config was unreadable (") {
		t.Fatalf("setting the config aside is not told as a boot refusal:\n%s", capture.String())
	}

	// The bad file is preserved beside, never deleted: it is evidence.
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

func TestEmptyContainerDirIsRefused(t *testing.T) {
	if _, err := Start("config.json", ""); err == nil {
		t.Fatal("an empty container dir was accepted")
	}
}

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

	capture := logsink.CaptureForTest(t)
	cfg, err := quarantineUnreadableConfig(path, os.ErrInvalid)
	if err != nil {
		t.Fatalf("quarantine failed: %v", err)
	}
	if !capture.Contains("config.decision: mobile: salvaged from the unreadable config: ") {
		t.Fatalf("the salvage is not told as a configuration decision:\n%s", capture.String())
	}
	if cfg.Identity.LedgerPath != "vault/ledger.jsonl" || cfg.Identity.KeyPath != "vault/identity.sec" || cfg.Identity.DBPath != "vault/aii.db" {
		t.Fatalf("identity locations were not salvaged: %+v", cfg.Identity)
	}

	// Disk agrees with memory: the salvage survives this process.
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

// Bytes that are not JSON at all salvage nothing and still recover to
// a default — the chooseBoot evidence scan is the guard behind this.
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

// One identity lives in one place: a record resolving into data/ while
// the key resolves into the container root is two identities
// interleaved. The reroot must refuse the mixed set, not pair them.
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

// A CONFIG THAT COULD NOT BE READ IS NOT SET ASIDE. The quarantine read the
// file with its error thrown away and renamed it whatever the load's
// failure was, so a good config that a read failed on (a permission, an
// I/O fault) was moved aside and the shell started from a default. Only a
// config that was read and not understood is set aside; a read failure is
// returned and the file stays where it is.
func TestAConfigThatCouldNotBeReadStaysInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// A directory where the file belongs fails the read for every user,
	// root included.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, loadErr := app.LoadConfig(path)
	if loadErr == nil {
		t.Fatal("inconclusive: the load did not fail")
	}
	if cfg, err := quarantineUnreadableConfig(path, loadErr); err == nil || cfg != nil {
		t.Fatalf("a config that could not be read was set aside for a default: %+v %v", cfg, err)
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		t.Fatalf("the config is not where it was: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable-") {
			t.Fatalf("a config that could not be read was renamed aside: %s", e.Name())
		}
	}
}
