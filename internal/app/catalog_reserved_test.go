package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
)

// A NEW PLUGIN UNDER ONE OF THE HOST'S WORDS IS REFUSED AT THE INSTALL DOOR,
// AND ONE ALREADY INSTALLED UNDER SUCH AN ID KEEPS WORKING. The
// door is where "new" is knowable: no package stands in the slot. Refused,
// it leaves nothing, and says why in the operator's words; with a package
// in the slot the same id is an update and the door takes it.
func TestANewPluginUnderAHostWordIsRefusedAtInstallAndAnInstalledOneIsUpdated(t *testing.T) {
	dir := t.TempDir()
	pkgBytes := []byte("bytes of a release under a reserved id")
	url, sha := servePackage(t, pkgBytes)
	catDir, _, root := signedCatalogDir(t, dir, plugincatalog.Index{Version: 1, Plugins: []plugincatalog.CatalogEntry{
		{ID: "host.example", Version: "1.0.1", Tier: "T1", Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: url, SHA256: sha, Size: int64(len(pkgBytes))}}},
	}})
	app := &App{}
	app.catalog.Open(catDir, "", root, nil)
	app.pkgFetch = loopbackFetcher()
	t.Chdir(dir)

	err := app.InstallFromCatalog(context.Background(), "host.example")
	var r *CatalogInstallRefusal
	if !errors.As(err, &r) || r.Reason != "reserved_id" || r.Cleanup != nil || !strings.Contains(err.Error(), `begins with "host"`) {
		t.Fatalf("a new plugin under a host word: want a clean reserved_id refusal, got %v", err)
	}
	assertNoInstallResidue(t, dir, "host.example")

	// Installed before the rule: a package stands in its slot.
	slot := filepath.Join(dir, "plugins", "host.example")
	if err := os.MkdirAll(slot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slot, "host.example-1.0.0.aiiospkg"), []byte("the release installed before the rule"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.InstallFromCatalog(context.Background(), "host.example"); err != nil {
		t.Fatalf("an update of a plugin already installed under a host word was refused: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(slot, "host.example-1.0.1.aiiospkg")); err != nil || string(got) != string(pkgBytes) {
		t.Fatalf("the update is not in the slot: %v", err)
	}
}

// AN INSTALLED PLUGIN UNDER SUCH AN ID STILL ACTIVATES. The rule is the
// Install door's and nothing after it asks: a package that stands in its
// slot is verified and activated by the sweep as any other.
func TestAPluginAlreadyInstalledUnderAHostWordStillActivates(t *testing.T) {
	dir := catalogInstallDir(t)
	pkg := buildResponderVer(t, dir, "host.example", "1.0.0", "")
	slot := filepath.Join(dir, "plugins", "host.example")
	if err := os.MkdirAll(slot, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pkg, filepath.Join(slot, filepath.Base(pkg))); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	app := startCatalogApp(t, dir, plugincatalog.Index{Version: 1})
	const tool = "pl_host_example_ping"
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if _, ok := app.toolReg.Get(tool); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a plugin installed under a host word did not activate; tools: %v", app.toolReg.Names())
		}
	}
}
