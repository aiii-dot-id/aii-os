package main

import (
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// The inventory the release script enforces is whatever this verb
// prints. If the bundle archive is not in it, release.sh can announce a
// complete candidate with no macOS update path — which it did.
func TestTheInventoryNamesTheBundleArchive(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "assets")
	if err != nil {
		t.Fatal(err)
	}
	if rc := run([]string{"assets", "-version", "1.2.3"}, out, os.Stderr); rc != 0 {
		out.Close()
		t.Fatalf("assets exited %d", rc)
	}

	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out.Name())
	for _, tgt := range updates.BundleTargets() {
		want := updates.BundleAssetName("1.2.3", tgt.Platform, tgt.Arch)
		if !strings.Contains(string(b), want+" ") {
			t.Fatalf("inventory omits the bundle archive %q:\n%s", want, b)
		}
	}
	for _, tgt := range updates.SupportedTargets() {
		want := updates.AssetName("1.2.3", tgt.Platform, tgt.Arch)
		if !strings.Contains(string(b), want+" ") {
			t.Fatalf("inventory omits the bare archive %q:\n%s", want, b)
		}
	}

	for _, tgt := range updates.PackageTargets() {
		want := updates.PackageAssetName("1.2.3", tgt.Arch)
		if !strings.Contains(string(b), want+" "+tgt.Platform+" "+tgt.Arch+" ") {
			t.Fatalf("inventory omits the system package %q:\n%s", want, b)
		}
	}
	// And the name is the packager's own.
	deb, err := os.ReadFile("../../packaging/deb/build-deb.sh")
	if err != nil || !strings.Contains(string(deb), `deb="$OUT/aii-os_${VERSION}_${ARCH}.deb"`) {
		t.Fatalf("packaging/deb/build-deb.sh no longer writes the name the contract gives the package (%v)", err)
	}
}

// Each row says what release.sh builds the asset for: its platform and
// arch as the payload states them, and the GOOS a build for that
// platform names. macOS is the one platform whose GOOS differs, so it is
// spelled out here; the script no longer maps names to targets itself.
func TestTheInventoryRowsCarryTheBuildTarget(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "assets")
	if err != nil {
		t.Fatal(err)
	}
	if rc := run([]string{"assets", "-version", "1.2.3"}, out, os.Stderr); rc != 0 {
		out.Close()
		t.Fatalf("assets exited %d", rc)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out.Name())
	want := strings.Join([]string{
		"aii-os_1.2.3_linux_amd64.tar.gz linux amd64 linux",
		"aii-os_1.2.3_linux_arm64.tar.gz linux arm64 linux",
		"aii-os_1.2.3_macos_arm64.tar.gz macos arm64 darwin",
		"aii-os_1.2.3_windows_amd64.zip windows amd64 windows",
		"aii-os-app_1.2.3_macos_arm64.zip macos arm64 darwin",
		"aii-os_1.2.3_amd64.deb linux amd64 linux",
		"aii-os_1.2.3_arm64.deb linux arm64 linux",
	}, "\n") + "\n"
	if string(b) != want {
		t.Fatalf("assets printed:\n%s\nwant:\n%s", b, want)
	}
}
