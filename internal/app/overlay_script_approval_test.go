package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestAnApprovalOfTheIdentitysScriptIsKeptAndPutInForce(t *testing.T) {
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Identity.KeyPath = filepath.Join(dir, "identity.key")
	cfg.Dashboard.Host, cfg.Dashboard.Port, cfg.Dashboard.TLS = "127.0.0.1", 0, false
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	d, err := a.newDashboard(&dashboard.WSHandler{})
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	ui := filepath.Join(dir, "ui")
	write := func(body string) string {
		t.Helper()
		if err := os.MkdirAll(ui, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ui, "custom.js"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(body))
		return hex.EncodeToString(sum[:])
	}
	d.SetUIOverlay(ui)
	d.SetApprovedScripts(a.configSnapshot().Dashboard.ApprovedScripts)
	addr, err := d.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(context.Background()) })
	served := func() string {
		t.Helper()
		resp, err := tlsClient().Get("https://" + addr + "/custom.js")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	v1 := write("window.v = 1;\n")
	if strings.Contains(served(), "window.v") {
		t.Fatal("an unapproved script was served")
	}
	if err := a.approveOverlayScript(dashboard.ApprovedScript{Path: "custom.js", SHA256: v1}); err != nil {
		t.Fatal(err)
	}
	if got := served(); got != "window.v = 1;\n" {
		t.Fatalf("the approval was not put in force: %q", got)
	}
	kept, err := ReadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Dashboard.ApprovedScripts) != 1 || kept.Dashboard.ApprovedScripts[0] != (dashboard.ApprovedScript{Path: "custom.js", SHA256: v1}) {
		t.Fatalf("the approval is not kept in config.json: %+v", kept.Dashboard.ApprovedScripts)
	}

	// Changed and approved again: one approval for the path, the new digest.
	v2 := write("window.v = 2;\n")
	if strings.Contains(served(), "window.v") {
		t.Fatal("a changed script ran on its earlier approval")
	}
	if err := a.approveOverlayScript(dashboard.ApprovedScript{Path: "custom.js", SHA256: v2}); err != nil {
		t.Fatal(err)
	}
	if kept, _ := ReadConfig(cfg.SourcePath); len(kept.Dashboard.ApprovedScripts) != 1 || kept.Dashboard.ApprovedScripts[0].SHA256 != v2 {
		t.Fatalf("approving the changed file did not replace the earlier approval: %+v", kept.Dashboard.ApprovedScripts)
	}

	// Taken out of the file by hand: a reload stops the script.
	edited, _ := ReadConfig(cfg.SourcePath)
	edited.Dashboard.ApprovedScripts = nil
	if _, err := saveConfig(edited); err != nil {
		t.Fatal(err)
	}
	a.live.Store(true)
	a.reloadConfig()
	if strings.Contains(served(), "window.v") {
		t.Fatal("a reload that took the approval away left the script running")
	}

	// Not through the page's config door, either way.
	if _, err := a.applyConfigChange(map[string]any{"dashboard.approved_scripts": []any{map[string]any{"path": "custom.js", "sha256": v2}}}); err == nil {
		t.Fatal("the page's config door took an approval")
	}
	if strings.Contains(served(), "window.v") {
		t.Fatal("the config door's refusal left the script running")
	}

	// A malformed approval is refused before anything is written.
	if err := a.approveOverlayScript(dashboard.ApprovedScript{Path: "custom.js", SHA256: "not-a-digest"}); err == nil {
		t.Fatal("a malformed digest was approved")
	}
}
