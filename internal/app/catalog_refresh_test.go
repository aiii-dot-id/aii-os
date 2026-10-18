package app

import (
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/plugincatalog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestCatalogRefreshFailurePushesThePluginsConfig(t *testing.T) {
	for _, background := range []bool{false, true} {
		name := "manual"
		if background {
			name = "background"
		}
		t.Run(name, func(t *testing.T) {
			dir := catalogInstallDir(t)
			cat := plugincatalog.Index{Version: 1, Generated: "2026-09-09T00:00:00Z", Plugins: []plugincatalog.CatalogEntry{{ID: "org.example.kept", Version: "1.0.0", Tier: "T1", Summary: "kept", Packages: []plugincatalog.CatalogPackage{{Platform: "*", Arch: "*", URL: "https://fixture.invalid/package", SHA256: "sha256:aa", Size: 1}}}}}
			catDir, root, _ := signedCatalogDir(t, dir, cat)
			cfg := safebootConfig(t, dir, "Catalog", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db"))
			cfg.Plugins = PluginsConfig{Autoload: "T0", PlatformRoot: root, CatalogDir: catDir, CatalogURL: "https://127.0.0.1:1/catalog"}
			a := New(cfg)
			if err := startLiveForTest(a); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stopApp(t, a) })
			conn := dialWSS(t, a.dashboard.Origin())
			sendQuery(t, conn, "config")
			readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "config" && m.Config != nil })
			// Change only the in-memory test URL, without the config-change
			// owner's own notifications or Poke. The message must come from
			// the refresh path being tested. The guard refuses before any I/O.
			a.cfgMu.Lock()
			a.cfg.Plugins.CatalogURL = "https://127.0.0.2:1/catalog"
			a.cfgMu.Unlock()
			if background {
				a.catalog.Poke()
			} else if err := a.buildLiveHandler().CatalogRefresh(); !errors.Is(err, firewall.ErrEgressBlocked) {
				t.Fatalf("refresh refusal: %v", err)
			}
			m := readUntil(t, conn, func(m *dashboard.ServerMessage) bool {
				return m.Type == "config" && m.Config != nil && strings.Contains(m.Config.Plugins.CatalogError, "127.0.0.2")
			})
			if len(m.Config.Plugins.Catalog) != 1 || m.Config.Plugins.Catalog[0].ID != "org.example.kept" {
				t.Fatal("a refresh error removed the usable verified index")
			}
		})
	}
}
