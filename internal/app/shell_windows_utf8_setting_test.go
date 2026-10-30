package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheWindowsShellsUTF8RelayIsTheOperatorsSwitch(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	a := New(cfg)
	reg, err := a.newToolRegistry(a.configSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !reg.ShellWindowsUTF8() {
		t.Fatal("with nothing set the relay is not on")
	}

	st, err := a.applyConfigChange(map[string]any{"tools.shell_windows_utf8": false})
	if err != nil {
		t.Fatalf("turning it off: %v", err)
	}
	if len(st.RestartRequired) != 0 {
		t.Errorf("the switch is read at each call; restart_required = %v", st.RestartRequired)
	}
	if reg.ShellWindowsUTF8() {
		t.Error("turned off, and the shell's next call still starts through the relay")
	}
	raw, err := os.ReadFile(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Tools map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Tools["shell_windows_utf8"] != false {
		t.Errorf("the file does not hold the switch off: %v", saved.Tools)
	}

	if _, err := a.applyConfigChange(map[string]any{"tools.shell_windows_utf8": true}); err != nil {
		t.Fatalf("turning it on: %v", err)
	}
	if !reg.ShellWindowsUTF8() {
		t.Error("turned back on, and it reads off")
	}
	if _, err := a.applyConfigChange(map[string]any{"tools.shell_windows_utf8": nil}); err != nil {
		t.Fatalf("clearing it: %v", err)
	}
	if !reg.ShellWindowsUTF8() || a.configSnapshot().Tools.ShellWindowsUTF8 != nil {
		t.Error("cleared, and it is not the default")
	}
	if _, err := a.applyConfigChange(map[string]any{"tools.shell_windows_utf8": "off"}); err == nil || !strings.Contains(err.Error(), "want a boolean") {
		t.Errorf("a string was taken: %v", err)
	}

	untouched, err := json.Marshal(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(untouched), `"shell_windows_utf8"`) {
		t.Errorf("a file with the switch unset names it: 0.1.23 refuses the file")
	}
	loaded, err := decodeConfig(cfg.SourcePath, []byte(`{"tools":{"shell_windows_utf8":false}}`))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Tools.shellWindowsUTF8() {
		t.Error("set off in the file, it loads on")
	}
}
