package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// birth_config_test.go — what a birth reads from config.json and writes
// back to it. The birth form is shown by a running program, and before
// birth no reload reads the file (reloadConfig waits for a live
// identity): an edit made while the form shows reaches the birth only if
// the birth reads the file itself.

// formShown is an identity on the birth form, its configuration saved,
// with a stand-in provider for the birth.
func formShown(t *testing.T) (*App, string) {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"birth-model"}]}`)
		case "/v1/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello, I am Mira."},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	a := birthApp(t, providerEntry{Name: "Local", APIType: "openai", URL: provider.URL + "/v1",
		APIKey: "stored-birth-key", DefaultModel: "birth-model", Default: true})
	t.Cleanup(func() { stopApp(t, a) })
	if _, err := saveConfig(a.cfg); err != nil {
		t.Fatal(err)
	}
	return a, provider.URL + "/v1"
}

// editFile changes config.json as an operator's editor does: the file
// read, one key set, the file written.
func editFile(t *testing.T, path string, edit func(doc map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func docSection(doc map[string]any, key string) map[string]any {
	m, _ := doc[key].(map[string]any)
	if m == nil {
		m = map[string]any{}
		doc[key] = m
	}
	return m
}

// AN EDIT MADE WHILE THE FORM SHOWS SURVIVES THE BIRTH, whatever it is:
// the birth writes the substrate pointer it owns into the file as it is
// now, and the identity lives by that file.
func TestAnEditMadeWhileTheFormShowsSurvivesTheBirth(t *testing.T) {
	a, endpoint := formShown(t)
	editFile(t, a.cfg.SourcePath, func(doc map[string]any) {
		docSection(doc, "prompt")["recent_turns"] = 7
		docSection(doc, "logs")["max_backups"] = 3
	})
	if _, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: endpoint, Model: "birth-model"}); err != nil {
		t.Fatal(err)
	}
	after, err := ReadConfig(a.cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Prompt.RecentTurns != 7 || after.Logs.MaxBackups != 3 {
		t.Errorf("the birth erased edits made while the form showed: recent_turns %d, max_backups %d", after.Prompt.RecentTurns, after.Logs.MaxBackups)
	}
	if after.LLM.Provider != "Local" || after.LLM.Model != "birth-model" {
		t.Errorf("the birth did not write what it owns: provider %q model %q", after.LLM.Provider, after.LLM.Model)
	}
	if live := a.configSnapshot(); live.Prompt.RecentTurns != 7 || live.LLM.Provider != "Local" {
		t.Errorf("the born identity does not live by the file: recent_turns %d, provider %q", live.Prompt.RecentTurns, live.LLM.Provider)
	}
}

// A FILE THAT DOES NOT READ REFUSES THE BIRTH, before anything is minted:
// writing the configuration the program started with over it would erase
// whatever the operator was writing, and nothing would say so.
func TestABirthRefusesAConfigThatDoesNotRead(t *testing.T) {
	a, endpoint := formShown(t)
	if err := os.WriteFile(a.cfg.SourcePath, []byte(`{"prompt": {"recent_turns": `), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: endpoint, Model: "birth-model"})
	if err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("a birth over a config.json that does not read was not refused by name: %v", err)
	}
	if fileExists(a.cfg.Identity.LedgerPath) {
		t.Fatal("the refused birth minted an identity")
	}
	if raw, _ := os.ReadFile(a.cfg.SourcePath); string(raw) != `{"prompt": {"recent_turns": ` {
		t.Fatalf("the refused birth wrote over the file: %s", raw)
	}
}

func TestABirthRefusesIdentityPathsEditedWhileTheFormShows(t *testing.T) {
	a, endpoint := formShown(t)
	boot := a.cfg.Identity
	elsewhere := t.TempDir()
	editFile(t, a.cfg.SourcePath, func(doc map[string]any) {
		docSection(doc, "identity")["ledger_path"] = filepath.Join(elsewhere, "ledger.jsonl")
		docSection(doc, "identity")["key_path"] = filepath.Join(elsewhere, "identity.sec")
		docSection(doc, "identity")["db_path"] = filepath.Join(elsewhere, "aii.db")
	})
	before, err := os.ReadFile(a.cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: endpoint, Model: "birth-model"})
	if err == nil || !strings.Contains(err.Error(), "start the program again") || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("a birth after the identity's paths moved was not refused by name: %v", err)
	}
	for _, p := range []string{boot.LedgerPath, boot.KeyPath, filepath.Join(elsewhere, "ledger.jsonl"), filepath.Join(elsewhere, "identity.sec")} {
		if fileExists(p) {
			t.Errorf("the refused birth minted %s", p)
		}
	}
	if after, _ := os.ReadFile(a.cfg.SourcePath); string(after) != string(before) {
		t.Errorf("the refused birth wrote over config.json:\n%s", after)
	}
	if a.configSnapshot().Identity != boot {
		t.Errorf("the refused birth moved the identity's paths in force: %+v", a.configSnapshot().Identity)
	}
}

// THE BORN IDENTITY'S DIRECTORY IS THE ONE THIS PROCESS HOLDS. As the first
// boot does, this process claims the boot's directory; the paths are then
// edited while the form shows. No birth happens elsewhere, the directory
// edited in stays free of any identity, and a second start of the boot's
// directory is still refused.
func TestTheBornIdentitysDirectoryIsTheOneTheFirstBootClaimed(t *testing.T) {
	a, endpoint := formShown(t)
	bootDir := filepath.Dir(a.cfg.Identity.LedgerPath)
	if _, err := a.claimIdentity(bootDir); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	editFile(t, a.cfg.SourcePath, func(doc map[string]any) {
		docSection(doc, "identity")["ledger_path"] = filepath.Join(elsewhere, "ledger.jsonl")
		docSection(doc, "identity")["key_path"] = filepath.Join(elsewhere, "identity.sec")
		docSection(doc, "identity")["db_path"] = filepath.Join(elsewhere, "aii.db")
	})
	if _, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: endpoint, Model: "birth-model"}); err == nil {
		t.Fatal("the birth went ahead with the identity's paths moved")
	}
	if fileExists(filepath.Join(elsewhere, "ledger.jsonl")) {
		t.Fatal("an identity was minted in a directory no process claimed")
	}
	other := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	if release, err := other.claimIdentity(bootDir); err == nil {
		release()
		t.Fatal("a second start may claim the directory this process holds")
	}
}

// A DASHBOARD SETTING EDITED WHILE THE FORM SHOWS TAKES EFFECT AT THE NEXT
// START. The server serving the form was built at the first boot and a
// birth swaps its handler, not the server. The edits stay in config.json,
// the born identity's configuration takes them as it takes every other
// edit, and the log says, for each, that the running dashboard keeps what
// it started with until the next start.
func TestADashboardSettingEditedWhileTheFormShowsWaitsForTheNextStart(t *testing.T) {
	a, endpoint := formShown(t)
	boot := a.cfg.Dashboard
	editFile(t, a.cfg.SourcePath, func(doc map[string]any) {
		docSection(doc, "dashboard")["require_token"] = !boot.RequireToken
		docSection(doc, "dashboard")["port"] = 9191
		docSection(doc, "dashboard")["origin"] = "https://aii.example"
		docSection(doc, "dashboard")["carried_idle_seconds"] = 600
	})
	log := logsink.CaptureForTest(t)
	if _, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{Provider: "Local", Endpoint: endpoint, Model: "birth-model"}); err != nil {
		t.Fatal(err)
	}
	after, err := ReadConfig(a.cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if after.Dashboard.RequireToken == boot.RequireToken || after.Dashboard.Port != 9191 || after.Dashboard.Origin != "https://aii.example" || after.Dashboard.CarriedIdleSeconds != 600 {
		t.Errorf("the birth erased the dashboard edits from config.json: %+v", after.Dashboard)
	}
	if live := a.configSnapshot().Dashboard; live.RequireToken != after.Dashboard.RequireToken || live.Port != 9191 || live.Origin != "https://aii.example" || live.CarriedIdleSeconds != 600 {
		t.Errorf("the configuration in force kept the boot's dashboard values over the file's: require_token %t port %d origin %q carried_idle_seconds %d",
			live.RequireToken, live.Port, live.Origin, live.CarriedIdleSeconds)
	}
	for _, key := range []string{"dashboard.require_token", "dashboard.port", "dashboard.origin", "dashboard.carried_idle_seconds"} {
		if !strings.Contains(log.String(), key+" was changed in config.json while the birth form showed") {
			t.Errorf("the log does not say that %s waits for the next start:\n%s", key, log.String())
		}
	}
	for _, key := range []string{"dashboard.host", "dashboard.tls", "dashboard.access_token"} {
		if strings.Contains(log.String(), key+" was changed") {
			t.Errorf("the log says %s waits for the next start, though the file did not change it:\n%s", key, log.String())
		}
	}
}
