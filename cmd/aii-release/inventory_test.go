package main

import (
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// .
// .
// .
func TestTheInventoryNamesTheBundleArchive(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "assets")
	if err != nil {
		t.Fatal(err)
	}
	if rc := run([]string{"assets", "-version", "1.2.3"}, out, os.Stderr); rc != 0 {
		out.Close()
		t.Fatalf("assets exited %d", rc)
	}
	// .
	// .
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out.Name())
	for _, tgt := range updates.BundleTargets() {
		want := updates.BundleAssetName("1.2.3", tgt.Platform, tgt.Arch)
		if !strings.Contains(string(b), want+"\n") {
			t.Fatalf("inventory omits the bundle archive %q:\n%s", want, b)
		}
	}
	for _, tgt := range updates.SupportedTargets() {
		want := updates.AssetName("1.2.3", tgt.Platform, tgt.Arch)
		if !strings.Contains(string(b), want+"\n") {
			t.Fatalf("inventory omits the bare archive %q:\n%s", want, b)
		}
	}
}
