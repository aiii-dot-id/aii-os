package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

func TestEveryIdentitysOldByteCeilingsAreReadAsNotSet(t *testing.T) {
	write := func(runtime map[string]any) string {
		t.Helper()
		raw, err := json.Marshal(defaultConfig())
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		r := doc["plugins"].(map[string]any)["runtime"].(map[string]any)
		for k, v := range runtime {
			r[k] = v
		}
		raw, err = json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	path := write(map[string]any{"max_installed_bytes": 1073741824, "max_file_bytes": 536870912, "max_compressed_bytes": 536870912})
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.Plugins.Runtime.TreeLimits()
	if l.MaxInstalledBytes != 0 || l.MaxFileBytes != 0 || l.MaxCompressedBytes != 0 {
		t.Fatalf("the old loader's numbers decide what this machine runs: %+v", l)
	}
	if l.MaxFiles != 32768 || l.MaxDepth != 24 {
		t.Fatalf("the structural ceilings keep their defaults: %+v", l)
	}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"max_installed_bytes", "max_file_bytes", "max_compressed_bytes"} {
		if strings.Contains(string(saved), key) {
			t.Fatalf("the next save wrote %s back", key)
		}
	}
	// A ceiling the operator set is his, and stays.
	cfg, err = ReadConfig(write(map[string]any{"max_compressed_bytes": 2147483648}))
	if err != nil || cfg.Plugins.Runtime.MaxCompressedBytes != 2147483648 {
		t.Fatalf("the operator's ceiling: %+v, %v", cfg.Plugins.Runtime, err)
	}
}

func TestAByteCeilingIsSetOrClearedFromSettings(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	const key = "plugins.runtime.max_compressed_bytes"
	if _, err := a.applyConfigChange(map[string]any{key: float64(2 << 30)}); err != nil || a.configSnapshot().Plugins.Runtime.MaxCompressedBytes != 2<<30 {
		t.Fatalf("set: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{key: nil}); err != nil || a.configSnapshot().Plugins.Runtime.MaxCompressedBytes != 0 {
		t.Fatalf("cleared by null: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{key: float64(2 << 30)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.applyConfigChange(map[string]any{key: 0.0}); err != nil || a.configSnapshot().Plugins.Runtime.MaxCompressedBytes != 0 {
		t.Fatalf("cleared by 0: %v", err)
	}
	_, err := a.applyConfigChange(map[string]any{key: float64(512 << 20)})
	if err == nil || !strings.Contains(err.Error(), key+": 536870912 (512 MiB) was the former default") || !strings.Contains(err.Error(), "511 MiB or 513 MiB") {
		t.Fatalf("contract's old number: %v", err)
	}
}

// THE JUDGMENT OF EVERY SET REACHES THE PAGE'S PROTOCOL: what the runtime
// judged, through the facility's record, in the wire the plugins view reads.
func TestTheJudgmentOfEverySetReachesTheWire(t *testing.T) {
	roots, pkg := componentSetsPackage(t, "1.0.0", fullSet, smallSet)
	noGPU := func() pluginfacility.Availability {
		return pluginfacility.Availability{HostKnown: true, HostTotal: 32 << 30, HostAvailable: 16 << 30}
	}
	a := selectionApp(t, roots, noGPU, nil)
	a.pluginOpts.FreeDisk = func(string) (int64, error) { return 100 << 30, nil }
	h := newPluginRuntime(a)
	ev, err := h.Verify(t.Context(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := h.Prepare(t.Context(), ev)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(selectionView(p.Selection))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Variant string `json:"variant"`
		Sets    []struct {
			Variant, Why, Storage, Bandwidth, Resources string
			Chosen                                      bool
			Reasons                                     []struct{ Words, Remedy string }
		} `json:"sets"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Variant != "small" || len(wire.Sets) != 2 || wire.Sets[0].Variant != "full" || wire.Sets[0].Chosen || !wire.Sets[1].Chosen {
		t.Fatalf("the wire: %s", raw)
	}
	full := wire.Sets[0]
	if len(full.Reasons) == 0 || !strings.Contains(full.Reasons[0].Words, "the cuda device could not be measured") ||
		!strings.Contains(full.Storage, "more on disk") || !strings.Contains(full.Bandwidth, "to download from example.invalid") {
		t.Fatalf("the passed-over set in words: %s", raw)
	}
	if !strings.Contains(wire.Sets[1].Why, "first in the package's order that this machine can run") {
		t.Fatalf("why the chosen set: %s", raw)
	}
}
