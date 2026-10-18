package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestARetiredFolderGrantDoesNotStopTheBoot(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	written := `{"tools":{"cwd":".","extra_roots":["/srv/example/identity/vega/work"]},
	 "plugins":{"autoload":"T3","runtime":{"max_installed_bytes":1073741825},"grants":{
	  "id.example.memory":{"kv":true,"memory":true},
	  "id.aiii.codequality":{"hosts":["api.typesafe.ai:443"],"credential_handles":["id.aiii.codequality.api_key"],
	   "roots":[{"name":"work","path":"/srv/example/identity/vega/work","write":true}]}}}}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(written), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a retired folder grant must not stop the boot: %v", err)
	}
	g := cfg.Plugins.Grants["id.aiii.codequality"]
	if len(g.Hosts) != 1 || len(g.CredentialHandles) != 1 || g.Files {
		t.Fatalf("the rest of the grant loads as written, and nothing is widened: %+v", g)
	}
	if m := cfg.Plugins.Grants["id.example.memory"]; !m.KV || !m.Memory {
		t.Fatalf("other grants are untouched: %+v", m)
	}
	if cfg.Plugins.Runtime.MaxInstalledBytes != 1073741825 || cfg.Tools.CWD != "." || len(cfg.Tools.ExtraRoots) != 1 {
		t.Fatalf("every other value loads as written: %+v %+v", cfg.Plugins.Runtime, cfg.Tools)
	}
	if strings.Count(logs.String(), "plugins.grants.id.aiii.codequality.roots") != 1 ||
		!logs.Contains("/srv/example/identity/vega/work") || !logs.Contains("Settings → Sandbox") || !logs.Contains("tick files") {
		t.Fatalf("the drop is named, with the folders and where they belong now: %s", logs.String())
	}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), `"roots"`) {
		t.Fatal("the next save leaves the retired field out of the file")
	}
	if err := os.WriteFile(path, []byte(`{"plugins":{"grants":{"p":{"roots":[],"colour":true}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "colour") {
		t.Fatalf("any other unknown field is still refused: %v", err)
	}
}

func TestTheRetiredCoachingKeysDoNotStopTheBoot(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	body := `{"agency":{"max_tool_rounds":31,"heuristic_nudges":true,"BREADTH_NUDGE":false,"plan_nudge":true,"planning_brief":false,"yield_answer":false}}`
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a retired coaching key must not stop the boot: %v", err)
	}
	if cfg.Agency.MaxToolRounds != 31 || cfg.Agency.YieldAnswer == nil || *cfg.Agency.YieldAnswer {
		t.Fatalf("every other agency value loads as written: %+v", cfg.Agency)
	}
	if n := strings.Count(logs.String(), "retired and not applied"); n != 1 {
		t.Fatalf("the drop is named once per read, not %d times: %s", n, logs.String())
	}
	for _, key := range retiredAgencyKeys {
		if !logs.Contains("agency." + key) {
			t.Fatalf("the drop does not name agency.%s: %s", key, logs.String())
		}
	}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range retiredAgencyKeys {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatalf("the next save leaves %s out of the file: %s", key, raw)
		}
	}
	if !strings.Contains(string(raw), `"max_tool_rounds": 31`) {
		t.Fatalf("the save kept the operator's values: %s", raw)
	}
	before := strings.Count(logs.String(), "retired and not applied")
	if _, err := LoadConfig(path); err != nil {
		t.Fatal(err)
	}
	if strings.Count(logs.String(), "retired and not applied") != before {
		t.Fatal("a saved file still names a retired key")
	}
	if err := os.WriteFile(path, []byte(`{"agency":{"heuristic_nudges":true,"heuristic_nudge":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "heuristic_nudge\"") {
		t.Fatalf("any other unknown agency field is still refused: %v", err)
	}
}

// retiredAgencyKeys is the agency section's share of retiredConfigKeys, by
// key name.
var retiredAgencyKeys = func() (names []string) {
	for _, k := range retiredConfigKeys {
		if name, ok := strings.CutPrefix(k.Path, "agency."); ok {
			names = append(names, name)
		}
	}
	return names
}()

func TestEveryRetiredConfigKeyLoads(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	file := map[string]any{}
	for _, k := range retiredConfigKeys {
		parts := strings.Split(strings.ReplaceAll(k.Path, "<id>", "p"), ".")
		node := file
		for _, part := range parts[:len(parts)-1] {
			next, ok := node[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				node[part] = next
			}
			node = next
		}
		node[parts[len(parts)-1]] = json.RawMessage(k.Sample)
		if k.Fate == "" {
			t.Errorf("%s names no fate", k.Path)
		}
	}
	body, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a config carrying every retired key must load: %v\n%s", err, body)
	}
	if cfg.Tools.ShellTimeoutSeconds != 300 {
		t.Errorf("tools.bash_timeout_seconds is read as the shell timeout: %d", cfg.Tools.ShellTimeoutSeconds)
	}
	for _, named := range []string{"agency.heuristic_nudges", "tools.bash_timeout_seconds", "plugins.grants.p.roots"} {
		if strings.Count(logs.String(), named) != 1 {
			t.Errorf("%s is not named exactly once on the read: %s", named, logs.String())
		}
	}
	cfg.Dashboard.LegacyAuthTokenSHA256 = "" // the first boot's retirement
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range retiredConfigKeys {
		parts := strings.Split(k.Path, ".")
		if leaf := parts[len(parts)-1]; strings.Contains(string(raw), `"`+leaf+`"`) {
			t.Errorf("the save kept the retired %s: %s", k.Path, raw)
		}
	}
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("the saved file must load: %v", err)
	}
}
