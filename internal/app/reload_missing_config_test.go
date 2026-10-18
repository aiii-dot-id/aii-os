package app

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// A RELOAD READS; IT NEVER CREATES. A config that is not there when a
// reload looks — a long remove-and-recreate edit, an accidental delete, a
// failing disk — leaves the running identity's settings as they are and
// the absent path absent. The reader used to be the boot's loader, which
// writes the defaults on a missing file and then activates them: the
// operator's grants, restrictions and model gave way to a fresh install's.
func reloadFixture(t *testing.T, probeURL string) (*App, *Config) {
	t.Helper()
	dir := t.TempDir()
	writeTestProviders(t, dir,
		providerEntry{Name: "old", URL: "https://old.example", DefaultModel: "m1", Default: true},
		providerEntry{Name: "new", URL: probeURL, DefaultModel: "m2"},
	)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.LLM = LLMConfig{Provider: "old", Model: "m1", TimeoutSeconds: 5, Retries: -1}
	cfg.Identity.LedgerPath = filepath.Join(dir, "elsewhere", "ledger.jsonl")
	cfg.Tools.Disabled = []string{"web_fetch"}
	cfg.Agency.MaxParallelSubagents = 2
	cfg.Plugins.Grants = map[string]broker.Grant{"org.example.kept": {KV: true}}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := newBareApp(t, cfg)
	a.live.Store(true)
	a.bgCtx = t.Context()
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "m1"}))
	return a, cfg
}

func assertNothingCreatedOrChanged(t *testing.T, a *App, before Config) {
	t.Helper()
	if _, err := os.Stat(before.SourcePath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the reload wrote a config at the absent path (%v)", err)
	}
	if got := a.configSnapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("the running settings moved: model %q grants %v disabled %v parallel %d",
			got.LLM.Model, got.Plugins.Grants, got.Tools.Disabled, got.Agency.MaxParallelSubagents)
	}
}

func TestAReloadOfAMissingConfigCreatesNothingAndChangesNothing(t *testing.T) {
	a, _ := reloadFixture(t, "https://unused.example")
	before := a.configSnapshot()
	if err := os.Remove(before.SourcePath); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	assertNothingCreatedOrChanged(t, a, before)

	// A valid whole file again: the next reload takes it as any edit.
	edited := before
	edited.Agency.MaxParallelSubagents = 3
	if _, err := saveConfig(&edited); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := a.configSnapshot().Agency.MaxParallelSubagents; got != 3 {
		t.Fatalf("the restored file did not apply: parallel %d", got)
	}
}

// The second look — the recheck under the lock, after a substrate probe —
// is a reader too: the file vanishing while the probe runs leaves the same
// running identity and the same absent path.
func TestAConfigThatVanishesDuringTheProbeIsNotRecreated(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		os.Remove(path)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`))
	}))
	defer server.Close()
	a, _ := reloadFixture(t, server.URL)
	before := a.configSnapshot()
	path = before.SourcePath
	candidate := before
	candidate.LLM.Provider, candidate.LLM.Model = "new", "m2"
	if _, err := saveConfig(&candidate); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	assertNothingCreatedOrChanged(t, a, before)
}
