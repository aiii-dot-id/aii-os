package packagefmt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
func TestSDKBuiltWindowsVerifyOnThisHost(t *testing.T) {
	raw, err := os.ReadFile("testdata/sdk-windows/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string]map[string]string
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) < 5 {
		t.Fatalf("thin: %d kit-built packages", len(expected))
	}
	for name, want := range expected {
		path := filepath.Join("testdata", "sdk-windows", name+".aiiospkg")
		res, err := VerifyFile(path, TrustRoots{})
		if err != nil {
			t.Errorf("%s: the host reader refused a package the kit built: %v", name, err)
			continue
		}
		if res.Tier != TierT0 {
			t.Errorf("%s: an unsigned fixture verifies as T0, got %s", name, res.Tier)
		}
		if res.Manifest.AiiosMinVersion != want["aiios_min_version"] || res.Manifest.AiiosMaxExclusiveVersion != want["aiios_max_exclusive_version"] {
			t.Errorf("%s: window read as %q..%q, want %q..%q", name,
				res.Manifest.AiiosMinVersion, res.Manifest.AiiosMaxExclusiveVersion, want["aiios_min_version"], want["aiios_max_exclusive_version"])
		}
		// .
		// .
		// .
		// .
		// .
		if err := CheckHostWindow(res.Manifest.AiiosMinVersion, res.Manifest.AiiosMaxExclusiveVersion); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
