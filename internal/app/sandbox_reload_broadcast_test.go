package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// A RELOAD THAT CHANGES THE ROOTS TELLS EVERY OPEN PAGE. The operator edits
// tools.extra_roots in config.json; the reload applies the new roots to the
// running tools, and an open page's Sandbox card must list them: a page that
// still listed a root the file took away, or missed one it added, would say
// the identity reaches where they cannot, or cannot reach where they can.
// The page asked nothing, so the list arrives with no request id.
func TestAReloadThatChangesTheRootsTellsEveryOpenPage(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	work := filepath.Join(dir, "work")
	for _, d := range []string{home, work} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.key")
	cfg.Tools.CWD = home
	cfg.Dashboard.Host, cfg.Dashboard.Port, cfg.Dashboard.TLS = "127.0.0.1", 0, false
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.rings = ring.NewManager()
	a.toolReg = tools.NewRegistry(home, nil, tools.Timeouts{})
	d, err := a.newDashboard(&dashboard.WSHandler{GetSandbox: a.sandboxState})
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	if _, err := d.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	page := dialWSS(t, d.LocalURL())
	waitPageOpen(t, d)

	edited, err := ReadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	edited.Tools.ExtraRoots = []string{work}
	if _, err := saveConfig(edited); err != nil {
		t.Fatal(err)
	}
	a.live.Store(true)
	a.reloadConfig()
	if _, held := a.toolReg.Roots(); !slices.Contains(held, realPath(work)) {
		t.Fatalf("rig: the reload did not apply the roots: %v", held)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, raw, err := page.Read(ctx)
		if err != nil {
			t.Fatalf("A RELOAD THAT CHANGED THE ROOTS DID NOT TELL THE OPEN PAGE: no sandbox message (%v); its card still lists the roots from before", err)
		}
		var m dashboard.ServerMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m.Type != "sandbox" {
			continue
		}
		if m.RequestID != "" || m.Sandbox == nil || !slices.Contains(m.Sandbox.ExtraRoots, realPath(work)) {
			t.Fatalf("the page was told id=%q sandbox=%+v, want the reloaded roots with %s", m.RequestID, m.Sandbox, work)
		}
		return
	}
}

// waitPageOpen waits until the dashboard holds an open page.
func waitPageOpen(t *testing.T, d *dashboard.Server) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !d.PageOpen() {
		if time.Now().After(deadline) {
			t.Fatal("rig: the page's socket never reached the dashboard")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
