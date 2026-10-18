//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

type extentVectors struct {
	Floor       string          `json:"floor"`
	Base        json.RawMessage `json:"base"`
	Declaration []struct {
		Name   string `json:"name"`
		Extent string `json:"extent"`
		Min    string `json:"min"`
		Host   bool   `json:"host"`
	} `json:"declaration"`
	Preflight []struct {
		Name    string           `json:"name"`
		Extent  bool             `json:"extent"`
		Limits  json.RawMessage  `json:"limits"`
		Refused map[string]int64 `json:"refused"`
	} `json:"preflight"`
}

func readExtentVectors(t *testing.T) extentVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "runtime_extent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v extentVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Floor != packagefmt.RuntimeExtentMinHost {
		t.Fatalf("the vectors' floor is %s, the host's %s: they change in one landing", v.Floor, packagefmt.RuntimeExtentMinHost)
	}
	return v
}

// declaredWith splices an extent fragment into the base declaration before
// its closing brace, as the vectors say.
func declaredWith(base json.RawMessage, extent string) []byte {
	b := strings.TrimSpace(string(base))
	return []byte(`{"runtimes":[` + b[:len(b)-1] + extent + `}]}`)
}

// THE HOST ACCEPTS WHAT THE KIT'S VECTORS SAY IT ACCEPTS: the decode, the
// extent's bounds and the 0.1.14 floor, case by case.
func TestRuntimeExtentSharedVectors(t *testing.T) {
	v := readExtentVectors(t)
	accepted := 0
	for _, c := range v.Declaration {
		decls, err := ParseRuntimes(declaredWith(v.Base, c.Extent))
		if err == nil {
			err = runtimeExtentFloor(decls, c.Min)
		}
		if (err == nil) != c.Host {
			t.Errorf("%s: accepted=%v (%v), the vectors say %v", c.Name, err == nil, err, c.Host)
		}
		if err == nil {
			accepted++
		}
	}
	if accepted == 0 || accepted == len(v.Declaration) {
		t.Fatalf("%d of %d accepted: the vectors decide nothing", accepted, len(v.Declaration))
	}
}

// THE CEILINGS A RUNTIME EXCEEDS, BY KEY AND DECLARED VALUE, before the
// download: exactly the vectors' refusals for each set of operator limits
// (a missing key is the host's default).
func TestRuntimeExtentPreflightVectors(t *testing.T) {
	v := readExtentVectors(t)
	plain, err := ParseRuntimes(declaredWith(v.Base, ""))
	if err != nil {
		t.Fatal(err)
	}
	var withExtent []RuntimeDecl
	for _, c := range v.Declaration {
		if c.Name == "extent-at-floor" {
			if withExtent, err = ParseRuntimes(declaredWith(v.Base, c.Extent)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if withExtent == nil {
		t.Fatal("no extent-at-floor case in the vectors")
	}
	for _, c := range v.Preflight {
		var limits packagefmt.TreeLimits
		if err := json.Unmarshal(c.Limits, &limits); err != nil {
			t.Fatal(err)
		}
		d := plain[0]
		if c.Extent {
			d = withExtent[0]
		}
		got := map[string]int64{}
		for _, over := range runtimeDeclarationRefusals(d, limits) {
			got[over.key] = over.declared
		}
		if !maps.Equal(got, c.Refused) {
			t.Errorf("%s: refused %v, the vectors say %v", c.Name, got, c.Refused)
		}
	}
}

// fixtureExtent is the runtime fixture's actual extent, from its inventory.
func fixtureExtent(t *testing.T, fx *runtimeFixture) (int64, int) {
	t.Helper()
	inv, err := packagefmt.ParseInventory(fx.inv, packagefmt.TreeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	var largest int64
	depth := 0
	for _, f := range inv.Files {
		largest = max(largest, f.Size)
		depth = max(depth, strings.Count(f.Path, "/")+1)
	}
	return largest, depth
}

// EVERY DOWNLOAD MEETS THE CEILINGS FIRST. A runtime chosen explicitly, or
// in a package without resource-aware selection, reaches EnsureRuntime
// without selection's check: each of the five quantities over its ceiling
// is refused there, by its key, as the operator's to change, before any
// fetch and without touching the archive cache.
func TestEnsureRuntimeRefusesOverItsCeilingsBeforeAnyFetch(t *testing.T) {
	for _, quantity := range []string{"compressed", "installed", "files", "largest", "depth"} {
		t.Run(quantity, func(t *testing.T) {
			fx := newRuntimeFixture(t)
			largest, depth := fixtureExtent(t, fx)
			fx.decl.LargestFileBytes, fx.decl.Depth = &largest, &depth
			var limits packagefmt.TreeLimits
			key := ""
			switch quantity {
			case "compressed":
				limits.MaxCompressedBytes, key = fx.decl.Size-1, "plugins.runtime.max_compressed_bytes"
			case "installed":
				limits.MaxInstalledBytes, key = fx.decl.InstalledBytes-1, "plugins.runtime.max_installed_bytes"
			case "files":
				limits.MaxFiles, key = fx.decl.Files-1, "plugins.runtime.max_files"
			case "largest":
				limits.MaxFileBytes, key = largest-1, "plugins.runtime.max_file_bytes"
			case "depth":
				limits.MaxDepth, key = depth-1, "plugins.runtime.max_depth"
			}
			fetch := func(context.Context, string, int64, io.Writer) (int64, error) {
				t.Error("a runtime over the operator's ceilings was fetched")
				return 0, errors.New("refused by the test")
			}
			w := openStore(t)
			_, err := storeRoot(t, w, &fx.decl, fx.entry, fetch, limits)
			var rerr *RuntimeError
			if !errors.As(err, &rerr) || rerr.WaitsOnPackage() || !strings.Contains(err.Error(), key) {
				t.Fatalf("refused %v; want a RuntimeError the operator can lift, naming %s", err, key)
			}
			if left, _ := os.ReadDir(filepath.Join(w.Dir, plugstore.DirArchives)); len(left) != 0 {
				t.Fatalf("the store's archives were touched: %v", left)
			}
		})
	}
}

// THE EXTENT IS THE INVENTORY'S, EXACTLY. A declaration whose extent the
// pinned inventory does not bear out, larger, smaller or a segment off, is
// the package's lie: refused as the package's, with its archive removed. A
// matching extent installs.
func TestTheDeclaredExtentIsTheInventorys(t *testing.T) {
	for _, c := range []struct {
		name          string
		largest, deep int
	}{
		{"matching", 0, 0},
		{"larger", 1, 0},
		{"smaller", -1, 0},
		{"deeper", 0, 1},
		{"shallower", 0, -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newRuntimeFixture(t)
			largest, depth := fixtureExtent(t, fx)
			largest += int64(c.largest)
			depth += c.deep
			fx.decl.LargestFileBytes, fx.decl.Depth = &largest, &depth
			w := openStore(t)
			root, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{})
			if c.name == "matching" {
				if err != nil || root == "" {
					t.Fatalf("a matching extent did not install: %v", err)
				}
				return
			}
			var rerr *RuntimeError
			if !errors.As(err, &rerr) || !rerr.WaitsOnPackage() || !strings.Contains(err.Error(), "is not its inventory's") {
				t.Fatalf("an extent the inventory does not bear out: %v", err)
			}
			if archive, _ := w.Archive(fx.decl.SHA256); isRegular(archive) {
				t.Fatal("the archive of a lying declaration was kept")
			}
		})
	}
}

func TestOneDeclarationOneVerdictWarmOrCold(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(d *RuntimeDecl)
		refuse string
	}{
		{"matching", func(*RuntimeDecl) {}, ""},
		{"extent omitted", func(d *RuntimeDecl) { d.LargestFileBytes, d.Depth = nil, nil }, ""},
		{"largest file off", func(d *RuntimeDecl) { v := *d.LargestFileBytes + 1; d.LargestFileBytes = &v }, "is not its inventory's"},
		{"depth off", func(d *RuntimeDecl) { v := *d.Depth + 1; d.Depth = &v }, "is not its inventory's"},
		{"files off", func(d *RuntimeDecl) { d.Files++ }, "the declaration says"},
		{"bytes off", func(d *RuntimeDecl) { d.InstalledBytes++ }, "the declaration says"},
	} {
		t.Run(c.name, func(t *testing.T) {
			warm := newRuntimeFixture(t)
			largest, depth := fixtureExtent(t, warm)
			warm.decl.LargestFileBytes, warm.decl.Depth = &largest, &depth
			w := openStore(t)
			root, err := storeRoot(t, w, &warm.decl, warm.entry, warm.fetch, packagefmt.TreeLimits{})
			if err != nil {
				t.Fatal(err)
			}
			// The warm path is the reader of a published root.
			later := warm.decl
			c.change(&later)
			m := runtimeMaterial(t, w, "id.example.voice", "1", warm)
			m.Runtime = &later
			got, held, werr := m.verify(context.Background())
			held.release()
			if warm.fetches != 1 {
				t.Fatalf("the warm path fetched (%d fetches)", warm.fetches)
			}
			if err := verifyRootIn(t, w, root, &warm.decl, warm.entry, packagefmt.TreeLimits{}); err != nil {
				t.Fatalf("the good root did not survive: %v", err)
			}
			cold := newRuntimeFixture(t)
			coldLargest, coldDepth := fixtureExtent(t, cold)
			cold.decl.LargestFileBytes, cold.decl.Depth = &coldLargest, &coldDepth
			c.change(&cold.decl)
			_, cerr := storeRoot(t, openStore(t), &cold.decl, cold.entry, cold.fetch, packagefmt.TreeLimits{})
			if c.refuse == "" {
				if werr != nil || got != root || cerr != nil {
					t.Fatalf("warm %v (root %s), cold %v: both must install", werr, got, cerr)
				}
				return
			}
			for side, err := range map[string]error{"warm": werr, "cold": cerr} {
				var rerr *RuntimeError
				if !errors.As(err, &rerr) || !rerr.WaitsOnPackage() || !strings.Contains(err.Error(), c.refuse) {
					t.Errorf("%s: %v, want the package's refusal naming %q", side, err, c.refuse)
				}
			}
		})
	}

	// The operator's ceiling, lowered below the root's largest file, still
	// refuses it, and as the operator's to change, not the package's.
	fx := newRuntimeFixture(t)
	largest, depth := fixtureExtent(t, fx)
	fx.decl.LargestFileBytes, fx.decl.Depth = &largest, &depth
	w := openStore(t)
	if _, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{}); err != nil {
		t.Fatal(err)
	}
	m := runtimeMaterial(t, w, "id.example.voice", "1", fx)
	m.limits = packagefmt.TreeLimits{MaxFileBytes: largest - 1}
	_, held, err := m.verify(context.Background())
	held.release()
	var rerr *RuntimeError
	if !errors.As(err, &rerr) || rerr.WaitsOnPackage() {
		t.Fatalf("a lowered ceiling over a warm root: %v, want a refusal that is not the package's", err)
	}
}
