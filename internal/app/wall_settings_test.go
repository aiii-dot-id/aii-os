package app

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// wallSettingKeys are the walls' settings on Linux: the shell's devices
// (tools.shell_wall_devices), how long either wall may take to be built
// (tools.wall_start_seconds) and a native engine's view
// (plugins.native_view).
var wallSettingKeys = []string{"shell_wall_devices", "wall_start_seconds", "native_view"}

func TestTheWallsSettingsAreNeverWrittenUnsetAndLoadAsWritten(t *testing.T) {
	saved, err := json.Marshal(defaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range wallSettingKeys {
		if strings.Contains(string(saved), `"`+key+`"`) {
			t.Errorf("%s is written with nothing set: 0.1.23 refuses the file", key)
		}
	}

	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := decodeConfig(path, []byte(`{"tools":{"shell_wall_devices":["/dev/kvm"],"wall_start_seconds":3},
		"plugins":{"native_view":["/opt/engines"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Tools.ShellWallDevices, []string{"/dev/kvm"}) || cfg.Tools.WallStartSeconds != 3 ||
		!slices.Equal(cfg.Plugins.NativeView, []string{"/opt/engines"}) {
		t.Fatalf("the walls' settings do not load as written: %+v %v", cfg.Tools, cfg.Plugins.NativeView)
	}

	for body, says := range map[string]string{
		`{"tools":{"shell_wall_devices":["/etc/shadow"]}}`: "tools.shell_wall_devices",
		`{"tools":{"shell_wall_devices":["/dev/./null"]}}`: "tools.shell_wall_devices",
		`{"tools":{"wall_start_seconds":-1}}`:              "tools.wall_start_seconds",
		`{"tools":{"wall_start_seconds":121}}`:             "tools.wall_start_seconds",
		`{"plugins":{"native_view":["/opt/engines/"]}}`:    "plugins.native_view",
		`{"plugins":{"native_view":["relative/engines"]}}`: "plugins.native_view",
		`{"tools":{"shell_wall_view":["/usr"]}}`:           "shell_wall_view",
		`{"tools":{"shell_wall_temp":["/tmp"]}}`:           "shell_wall_temp",
		`{"tools":{"shell_wall_start_seconds":3}}`:         "shell_wall_start_seconds",
	} {
		if _, err := decodeConfig(path, []byte(body)); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s must be refused at the load, naming %s: %v", body, says, err)
		}
	}

	for _, edit := range []func(*Config){
		func(c *Config) { c.Tools.ShellWallDevices = []string{"/dev/kvm"} },
		func(c *Config) { c.Tools.WallStartSeconds = 5 },
		func(c *Config) { c.Plugins.NativeView = []string{"/opt/engines"} },
	} {
		x, y := *defaultConfig(), *defaultConfig()
		edit(&y)
		blankLiveAppliable(&x)
		blankLiveAppliable(&y)
		if !reflect.DeepEqual(x, y) {
			t.Errorf("a wall setting read at each call survives blankLiveAppliable, so a reload would call it saved for next boot")
		}
	}
}
