package plugincatalog

import (
	"strings"
	"testing"
)

// A BUILD FOR A NEWER SYSTEM IS NAMED, NOT DOWNLOADED. A build can need a
// newer version of its own system than the host runs; nothing said so
// until the engine it carried failed to start, with its runtime and
// models already on the disk. The catalog's package states the floor, and
// the answer is the sentence the store shows in place of Install.
func TestABuildIsHeldToItsSystemsFloor(t *testing.T) {
	mac := CatalogPackage{Platform: "macos", Arch: "arm64", OSMinVersion: "26.5"}
	for _, tc := range []struct {
		have string
		ok   bool
		says string
	}{
		{"26.5", true, ""},
		{"26.5.1", true, ""},
		{"27.0.1", true, ""},
		{"26.10", true, ""}, // numbers, not text: ten is more than five
		{"26.4.9", false, "needs macOS 26.5 or newer (this is 26.4.9)"},
		{"26", false, "needs macOS 26.5 or newer (this is 26)"},
		{"15.6.1", false, "needs macOS 26.5 or newer (this is 15.6.1)"},
		{"", false, "needs macOS 26.5 or newer, and this system's version could not be read"},
		{"sequoia", false, "needs macOS 26.5 or newer, and this system's version could not be read"},
	} {
		ok, why := mac.RunsOn(tc.have)
		if ok != tc.ok || why != tc.says {
			t.Errorf("a build that needs macOS 26.5 on %q: %v %q, want %v %q", tc.have, ok, why, tc.ok, tc.says)
		}
	}
	// No floor stated: offered whatever the system says of itself, or does not.
	for _, have := range []string{"", "1.0", "sequoia"} {
		if ok, why := (CatalogPackage{Platform: "linux", Arch: "amd64"}).RunsOn(have); !ok || why != "" {
			t.Errorf("a build that states no floor on %q: %v %q", have, ok, why)
		}
	}
	// A floor of three numbers, as Windows counts.
	win := CatalogPackage{Platform: "windows", Arch: "amd64", OSMinVersion: "10.0.19045"}
	if ok, _ := win.RunsOn("10.0.26100"); !ok {
		t.Error("a Windows build of 26100 is newer than a floor of 19045")
	}
	if ok, why := win.RunsOn("10.0.17763"); ok || !strings.Contains(why, "needs Windows 10.0.19045 or newer") {
		t.Errorf("a Windows build of 17763 under a floor of 19045: %v %q", ok, why)
	}
}

// AN INDEX THAT STATES A FLOOR STATES IT AS NUMBERS. A floor that cannot be
// compared would be a build offered to nobody, or to everybody; the index
// is refused. So is a floor on a build that runs on every system.
func TestAnIndexsFloorIsNumbersAndBelongsToOneSystem(t *testing.T) {
	entry := func(platform, floor string) *Index {
		return &Index{Version: 1, Plugins: []CatalogEntry{{ID: "org.example.engine", Version: "1.0.0", Tier: "T3", Summary: "s",
			Packages: []CatalogPackage{{Platform: platform, Arch: "arm64", URL: "https://example.org/p.aiiospkg", SHA256: strings.Repeat("a", 64), Size: 1, OSMinVersion: floor}}}}}
	}
	if err := entry("macos", "26.5").validate(); err != nil {
		t.Fatalf("a floor of 26.5 for a macOS build: %v", err)
	}
	if err := entry("macos", "").validate(); err != nil {
		t.Fatalf("no floor: %v", err)
	}
	for _, bad := range []string{"26.5 or later", "v26", "26..5", "26.5.", "1.2.3.4.5", "26.x"} {
		if err := entry("macos", bad).validate(); err == nil || !strings.Contains(err.Error(), "os_min_version") {
			t.Errorf("a floor of %q must be refused by its name: %v", bad, err)
		}
	}
	portable := entry("*", "26.5")
	portable.Plugins[0].Packages[0].Arch = "*"
	if err := portable.validate(); err == nil || !strings.Contains(err.Error(), "every system") {
		t.Errorf("a floor on a build for every system must be refused: %v", err)
	}
}
