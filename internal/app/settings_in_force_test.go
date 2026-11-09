package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// EVERY KEY IS READ, AND NO SECRET IS. The list is the configuration's own
// types, so a key added to them is on the page without being added to a list.
// The value in force and the one with nothing stated are told apart from
// whether the file states it. A key that holds a secret says only that it is
// set, and a key whose name suggests one must be named here as not holding one.
func TestSettingsInForceReadsEveryKeyAndNoSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"witness_server":{"interval_events":50},"cognition":{"consolidate_batch":25}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.SourcePath = path
	cfg.Witness.IntervalEvents = 50
	cfg.Cognition.ConsolidateBatch = 25
	cfg.Dashboard.AccessToken = "a-secret-marker"
	a := &App{cfg: cfg}
	rows := a.settingsInForce().Settings
	by := map[string]dashboard.SettingInForce{}
	for _, r := range rows {
		by[r.Key] = r
	}
	for _, line := range configKeyPaths(reflect.TypeOf(Config{})) {
		if p, leaf := strings.CutPrefix(line, "L "); leaf && !strings.ContainsAny(p, "*[") {
			if _, ok := by[p]; !ok {
				t.Errorf("%s is not read", p)
			}
		}
	}
	if r := by["witness_server.interval_events"]; r.Value != "50" || !r.Stated || r.Usual == "50" || r.Usual == "" {
		t.Errorf("a stated key: %+v", r)
	}
	if r := by["cognition.consolidate_batch"]; r.Value != "25" || !r.Stated || r.Usual != "30" {
		t.Errorf("a table's key: %+v", r)
	}
	if r := by["cognition.consolidate_max_ops"]; r.Value != "32" || r.Stated || r.Usual != "32" {
		t.Errorf("a table's key at its default: %+v", r)
	}
	if r := by["dashboard.port"]; r.Value == "" || r.Stated {
		t.Errorf("a key the file does not state: %+v", r)
	}
	if r := by["dashboard.access_token"]; !r.Hidden || r.Value != "" || r.Usual != "" {
		t.Errorf("the token is read out: %+v", r)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "a-secret-marker") {
		t.Fatal("a secret reached the rows")
	}
	// Names that suggest a secret and are not one: the path to a key, an
	// environment variable's name, a switch, a limit in tokens.
	notSecret := map[string]string{
		"dashboard.require_token":                "a switch",
		"dashboard.skip_signin_with_valid_token": "a switch",
		"agency.turn_token_budget":               "a limit in tokens",
		"agency.daily_token_ceiling":             "a limit in tokens",
		"genesis.agent_memory.secret_patterns":   "the patterns that find a secret",
		"llm.api_key_env":                        "an environment variable's name",
		"speech.stt.api_key_env":                 "an environment variable's name",
		"speech.tts.api_key_env":                 "an environment variable's name",
	}
	suggests := regexp.MustCompile(`(^|[._])(token|secret|password|credential|cookie|private|api_key)($|[._])`)
	for _, r := range rows {
		if suggests.MatchString(r.Key) && !r.Hidden && notSecret[r.Key] == "" {
			t.Errorf("%s suggests a secret and is read out; hide it, or name why it is not one", r.Key)
		}
	}
}

// WHAT THE FILE IS, IS SAID AS IT IS: no file states nothing, and that is known; a
// file that cannot be read states nothing that can be known, and the rows still say
// what is in force. A hidden key is present only if the file holds something in it.
func TestSettingsInForceSaysWhatTheFileIs(t *testing.T) {
	dir := t.TempDir()
	read := func(t *testing.T, name, body string, token string) (dashboard.SettingsInForce, map[string]dashboard.SettingInForce) {
		t.Helper()
		cfg := defaultConfig()
		cfg.SourcePath = filepath.Join(dir, name)
		cfg.Dashboard.AccessToken = token
		if body != "" {
			if err := os.WriteFile(cfg.SourcePath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got := (&App{cfg: cfg}).settingsInForce()
		by := map[string]dashboard.SettingInForce{}
		for _, r := range got.Settings {
			by[r.Key] = r
		}
		return got, by
	}
	t.Run("no file", func(t *testing.T) {
		got, by := read(t, "absent.json", "", "a-secret-marker")
		if got.FileUnread != "" {
			t.Errorf("no file is not an unread file: %q", got.FileUnread)
		}
		if r := by["dashboard.access_token"]; !r.Hidden || r.Stated || r.Value != "" {
			t.Errorf("a secret the file does not hold: %+v", r)
		}
	})
	for name, body := range map[string]string{"not json": "{ not json", "more follows": "{} and more", "a stray closer": "{} ]", "not settings": "[1]", "empty": " "} {
		t.Run(name, func(t *testing.T) {
			got, by := read(t, strings.ReplaceAll(name, " ", "-")+".json", body, "")
			if got.FileUnread == "" {
				t.Fatalf("%q reads as a file stating nothing", body)
			}
			if r := by["dashboard.port"]; r.Value == "" || r.Stated {
				t.Errorf("what is in force is still said, and nothing said of the file: %+v", r)
			}
		})
	}
	t.Run("a folder", func(t *testing.T) {
		if err := os.Mkdir(filepath.Join(dir, "folder.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		if got, _ := read(t, "folder.json", "", ""); got.FileUnread == "" {
			t.Error("what cannot be opened reads as a file stating nothing")
		}
	})
	t.Run("secrets", func(t *testing.T) {
		got, by := read(t, "secrets.json", `{"dashboard":{"access_token":"a-secret-marker","auth_token_sha256":"","ice_servers":[]}}`, "a-secret-marker")
		if got.FileUnread != "" {
			t.Fatalf("a good file: %q", got.FileUnread)
		}
		if r := by["dashboard.access_token"]; !r.Hidden || !r.Stated || r.Value != "" {
			t.Errorf("a secret the file holds: %+v", r)
		}
		for _, k := range []string{"dashboard.auth_token_sha256", "dashboard.ice_servers"} {
			if r := by[k]; !r.Hidden || r.Stated {
				t.Errorf("%s holds nothing and is said to be set: %+v", k, r)
			}
		}
		// An empty list stated as the default keeps its default, to be compared.
		_, by = read(t, "contacts.json", `{"contacts":[]}`, "")
		if r := by["contacts"]; r.Value != "[]" || r.Usual != "[]" || !r.Stated {
			t.Errorf("an empty list stated as its default: %+v", r)
		}
		_, by = read(t, "servers.json", `{"dashboard":{"ice_servers":[{"urls":"a-secret-marker"}]}}`, "")
		if r := by["dashboard.ice_servers"]; !r.Hidden || !r.Stated || r.Value != "" {
			t.Errorf("a list the file holds: %+v", r)
		}
	})
}
