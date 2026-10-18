package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Loading an existing file is not a migration or a save, even when the
// loader supplies new defaults or refuses something it cannot interpret.
func unchangedConfigFile(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1_500_000_000, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != body {
			t.Errorf("loading changed %s: %q, %v", name, data, err)
		}
		after, err := os.Stat(path)
		if err != nil || !after.ModTime().Equal(before.ModTime()) {
			t.Errorf("loading rewrote %s: %v", name, err)
		}
	})
	return path
}

func TestOldShellTimeoutStillLoadsWithoutRewriting(t *testing.T) {
	for _, tc := range []struct {
		name, tools string
		want        int
	}{
		{"old", `{"bash_timeout_seconds":300}`, 300},
		{"old zero", `{"bash_timeout_seconds":0}`, 0},
		{"old last", `{"shell_timeout_seconds":15,"bash_timeout_seconds":300}`, 300},
		{"current last", `{"bash_timeout_seconds":300,"shell_timeout_seconds":15}`, 15},
		{"current zero last", `{"bash_timeout_seconds":300,"shell_timeout_seconds":0}`, 0},
		{"null does not clear", `{"shell_timeout_seconds":15,"bash_timeout_seconds":null}`, 15},
		{"old null then value", `{"bash_timeout_seconds":null,"bash_timeout_seconds":300}`, 300},
		{"old value then null", `{"bash_timeout_seconds":300,"bash_timeout_seconds":null}`, 300},
		{"null keeps alias attached", `{"bash_timeout_seconds":null,"bash_timeout_seconds":300,"shell_timeout_seconds":15}`, 15},
		{"case insensitive", `{"BASH_TIMEOUT_SECONDS":300}`, 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := unchangedConfigFile(t, "config.json", `{"tools":`+tc.tools+`}`)
			for range 2 {
				cfg, err := LoadConfig(path)
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Tools.ShellTimeoutSeconds != tc.want {
					t.Fatalf("timeout = %d, want operator's %d", cfg.Tools.ShellTimeoutSeconds, tc.want)
				}
				encoded, err := json.Marshal(cfg)
				if err != nil || strings.Contains(string(encoded), "bash_timeout_seconds") {
					t.Fatalf("the writer must keep only the current spelling: %s, %v", encoded, err)
				}
			}
		})
	}
}

func TestLoaderLeavesTheWebFetchTimeoutDefaultToTools(t *testing.T) {
	for _, tc := range []struct {
		name, tools string
		want        int
	}{
		{"unset", `{}`, 0},
		{"operator", `{"webfetch_timeout_seconds":7}`, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfig(unchangedConfigFile(t, "config.json", `{"tools":`+tc.tools+`}`))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Tools.WebFetchTimeoutSeconds != tc.want {
				t.Fatalf("webfetch_timeout_seconds = %d, want %d", cfg.Tools.WebFetchTimeoutSeconds, tc.want)
			}
			// Absent means "take the default": a written file must not
			// say 0, which reads as "no timeout" to the person opening it.
			data, err := json.Marshal(cfg.Tools)
			if err != nil {
				t.Fatal(err)
			}
			if has := strings.Contains(string(data), `"webfetch_timeout_seconds"`); has != (tc.want != 0) {
				t.Fatalf("written tools section %s: key present=%v, want %v", data, has, tc.want != 0)
			}
		})
	}
}

func TestRetiredGrantKeepsTypedJSONSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		depth      int
		files, kv  bool
	}{
		{"repeated section", `{"agency":{"max_subagent_depth":0},"agency":{"max_tool_rounds":31},"plugins":{"grants":{"p":{"roots":[]}}}}`, 0, false, false},
		{"case alias order", `{"agency":{"max_subagent_depth":0},"AGENCY":{"max_subagent_depth":1},"plugins":{"grants":{"p":{"roots":[]}}}}`, 1, false, false},
		{"explicit denial wins", `{"plugins":{"grants":{"p":{"files":true,"FILES":false,"roots":[]}}}}`, 3, false, false},
		{"case insensitive retirement", `{"PLUGINS":{"GRANTS":{"p":{"ROOTS":[],"KV":true}}}}`, 3, false, true},
		{"repeated grants merge", `{"plugins":{"grants":{"p":{"kv":true,"roots":[]}},"grants":{"q":{"memory":true}}}}`, 3, false, true},
		{"repeated plugins merge", `{"plugins":{"grants":{"p":{"kv":true,"roots":[]}}},"plugins":{"autoload":"none"}}`, 3, false, true},
		{"null clears grants", `{"plugins":{"grants":{"p":{"files":true,"roots":[]}}},"plugins":{"grants":null}}`, 3, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := unchangedConfigFile(t, "config.json", tc.body)
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			g := cfg.Plugins.Grants["p"]
			if cfg.Agency.MaxSubagentDepth != tc.depth || g.Files != tc.files || g.KV != tc.kv {
				t.Fatalf("typed values changed: depth=%d grant=%+v", cfg.Agency.MaxSubagentDepth, g)
			}
		})
	}
}

func TestConfigCompatibilityDoesNotHideInvalidValues(t *testing.T) {
	for _, body := range []string{
		`{"tools":{"bash_timeout_seconds":"slow"}}`,
		`{"tools":{"shell_timeout_seconds":"slow","bash_timeout_seconds":300}}`,
		`{"plugins":{"grants":{"p":{"files":"yes","roots":[]}}}}`,
	} {
		path := unchangedConfigFile(t, "config.json", body)
		_, err := LoadConfig(path)
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			t.Errorf("%s: want typed JSON failure, got %v", body, err)
		}
	}
	for _, body := range []string{
		`{"tools":{"shell_timout_seconds":300}}`,
		`{"plugins":{"future_setting":true}}`,
		`{"plugins":{"grants":{"p":{"future_grant":true,"roots":[]}}}}`,
	} {
		path := unchangedConfigFile(t, "config.json", body)
		if _, err := LoadConfig(path); err == nil {
			t.Errorf("unsupported settings must not be applied or removed: %s", body)
		}
	}
}

func TestMissingConfigFieldsInheritWithoutRewriting(t *testing.T) {
	path := unchangedConfigFile(t, "config.json", `{"agency":{"max_subagent_depth":0},"llm":{"api_key_env":""},"logs":{}}`)
	for range 2 {
		cfg, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Agency.MaxToolRounds != defaultConfig().Agency.MaxToolRounds ||
			cfg.Agency.MaxSubagentDepth != 0 || cfg.LLM.APIKeyEnv != "" || cfg.Logs.Dir != "" {
			t.Fatalf("defaults replaced an operator value or failed to fill: %+v", cfg)
		}
	}
}

func TestProviderInheritanceDoesNotReconcileTheFile(t *testing.T) {
	path := unchangedConfigFile(t, "providers.json", `{"providers":[{"name":"Claude (Max/Pro)","api_type":"anthropic","url":"https://api.anthropic.com","credential":"claude-code"},{"name":"future","api_type":"not-a-dialect","future_setting":9007199254740993}]}`)
	for range 2 {
		reg, err := loadProvidersFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(reg.Providers) != 1 || len(reg.broken) != 1 {
			t.Fatalf("the valid provider and the unmodified broken entry must both survive: %+v", reg)
		}
		shipped := embeddedRegistry()
		var want map[string]string
		for _, p := range shipped.Providers {
			if p.Name == reg.Providers[0].Name {
				want = p.CredentialOptions
			}
		}
		if want == nil || reg.Providers[0].CredentialOptions["client_version"] != want["client_version"] {
			t.Fatal("the missing vendor fact did not inherit from the shipped registry")
		}
		var before, after map[string]json.RawMessage
		if err := json.Unmarshal(reg.broken[0].raw, &before); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(withBrokenEntries(reg.operatorView()))
		if err != nil {
			t.Fatal(err)
		}
		var out struct{ Providers []json.RawMessage }
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out.Providers[1], &after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("saving would discard the broken entry's original values")
		}
	}
}
