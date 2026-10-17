package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestPluginRuntimeCeilingsAreOperatorConfiguration(t *testing.T) {
	cfg := defaultConfig()
	d := packagefmt.DefaultTreeLimits
	if r := cfg.Plugins.Runtime; r.MaxInstalledBytes != 0 || r.MaxFiles != d.MaxFiles || r.MaxFileBytes != 0 ||
		r.MaxCompressedBytes != 0 || r.MaxDepth != d.MaxDepth || r.RootsKept != 2 {
		t.Fatalf("defaults not applied: %+v", r)
	}
	cfg.Plugins.Runtime = PluginRuntimeConfig{MaxFiles: 10}
	applyDefaults(cfg)
	if r := cfg.Plugins.Runtime; r.MaxFiles != 10 || r.MaxInstalledBytes != 0 || r.RootsKept != 2 {
		t.Fatalf("a set value stays, the rest take the defaults: %+v", r)
	}
	if l := cfg.Plugins.Runtime.TreeLimits(); l.MaxFiles != 10 || l.MaxDepth != d.MaxDepth {
		t.Fatalf("the profile reaches the reader: %+v", l)
	}

	cfg = defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.pluginOpts = &pluginhost.Options{}
	for _, key := range []string{"plugins.runtime.max_files", "plugins.runtime.max_depth", "plugins.runtime.roots_kept"} {
		if _, err := a.applyConfigChange(map[string]any{key: 0.0}); err == nil || !strings.Contains(err.Error(), "positive") {
			t.Fatalf("%s=0 must be refused: %v", key, err)
		}
	}
	for _, key := range []string{"plugins.runtime.max_installed_bytes", "plugins.runtime.max_file_bytes", "plugins.runtime.max_compressed_bytes"} {
		if _, err := a.applyConfigChange(map[string]any{key: -1.0}); err == nil || !strings.Contains(err.Error(), "positive") {
			t.Fatalf("%s=-1 must be refused: %v", key, err)
		}
	}
	if r := a.configSnapshot().Plugins.Runtime; r.MaxFiles != d.MaxFiles {
		t.Fatalf("a refused change stored something: %+v", r)
	}
	state, err := a.applyConfigChange(map[string]any{
		"plugins.runtime.max_installed_bytes": float64(2 << 30), "plugins.runtime.max_files": 40000.0, "plugins.runtime.roots_kept": 3.0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := a.configSnapshot().Plugins.Runtime; r.MaxInstalledBytes != 2<<30 || r.MaxFiles != 40000 || r.RootsKept != 3 || r.MaxDepth != d.MaxDepth {
		t.Fatalf("the operator's numbers are held, the rest unchanged: %+v", r)
	}
	if v := state.Plugins.Runtime; v == nil || v.MaxInstalledBytes != 2<<30 || v.MaxFiles != 40000 || v.RootsKept != 3 {
		t.Fatalf("the view shows the operator's numbers: %+v", v)
	}
	for _, key := range []string{"plugins.runtime.max_installed_bytes", "plugins.runtime.max_files", "plugins.runtime.roots_kept"} {
		if slices.Contains(state.RestartRequired, key) {
			t.Fatalf("%s is listed as waiting for a restart: %v", key, state.RestartRequired)
		}
	}
	if o := a.pluginOptions(); o.RuntimeLimits.MaxInstalledBytes != 2<<30 || o.RuntimeLimits.MaxFiles != 40000 {
		t.Fatalf("the live options did not take the operator's numbers: %+v", o.RuntimeLimits)
	}
	onDisk, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.Plugins.Runtime.MaxFiles != 40000 || onDisk.Plugins.Runtime.RootsKept != 3 {
		t.Fatalf("published to disk: %+v", onDisk.Plugins.Runtime)
	}
}
