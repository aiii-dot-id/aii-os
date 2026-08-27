package dashboard

// The frame-overlay seam, pinned end to end over HTTP against the
// REAL server built by New() and Start() — no hand-copied handler. An
// operator or identity-authored file in the overlay dir replaces the
// compiled frame for all three extensions (full re-form ruling,
// 2026-08-24), and every overlay response still ships uiCSP, so
// "re-form" can never silently mean "network egress". Containment is
// pinned over the wire too: a symlink planted in ui/ cannot reach out.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// startRealOverlayServer boots the dashboard's real server with
// SetUIOverlay pointed at a temp dir, exactly as app.go wires it.
func startRealOverlayServer(t *testing.T) (addr string, overlayDir string) {
	t.Helper()
	overlayDir = t.TempDir()
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
	s.SetUIOverlay(overlayDir)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return addr, overlayDir
}

func get(t *testing.T, addr, p string) (int, http.Header, string) {
	t.Helper()
	resp, err := testClient.Get("https://" + addr + p)
	if err != nil {
		t.Fatalf("GET %s: %v", p, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body %s: %v", p, err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

// TestOverlaySeamServesAllThreeExtensions pins the ruling: html, js
// and css are all overridable, each with uiCSP and correct type.
func TestOverlaySeamServesAllThreeExtensions(t *testing.T) {
	addr, overlayDir := startRealOverlayServer(t)

	for rel, want := range map[string]string{
		"index.html": "<!DOCTYPE html><title>reformed</title>",
		"app.js":     "export const reformed = true;",
		"theme.css":  "/* re-formed */ :root{}",
	} {
		full := filepath.Join(overlayDir, rel)
		if err := os.WriteFile(full, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for p, ctype := range map[string]string{
		"/index.html": "text/html; charset=utf-8",
		"/app.js":     "text/javascript; charset=utf-8",
		"/theme.css":  "text/css; charset=utf-8",
	} {
		code, hdr, body := get(t, addr, p)
		if code != 200 {
			t.Errorf("GET %s = %d, want 200", p, code)
		}
		if !strings.Contains(body, "reformed") && !strings.Contains(body, "re-formed") {
			tainted := fmt.Sprintf("body %q", body)
			t.Errorf("GET %s served compiled byte, not overlay: %s", p, tainted)
		}
		if got := hdr.Get("Content-Type"); got != ctype {
			t.Errorf("GET %s Content-Type = %q, want %q", p, got, ctype)
		}
		if got := hdr.Get("Content-Security-Policy"); got != uiCSP {
			t.Errorf("GET %s CSP:\n got %q\nwant %q", p, got, uiCSP)
		}
	}
}

// TestOverlaySeamRefusesSymlinkEscape pins containment over the wire.
func TestOverlaySeamRefusesSymlinkEscape(t *testing.T) {
	addr, overlayDir := startRealOverlayServer(t)

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.css"), []byte("body{content:'stolen'}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(overlayDir, "theme.css")
	if err := os.Symlink(filepath.Join(outside, "secret.css"), link); err != nil {
		t.Fatal(err)
	}

	code, _, body := get(t, addr, "/theme.css")
	if code != 200 || strings.Contains(body, "stolen") {
		t.Fatalf("symlink escape over HTTP: got %d, body %q", code, body)
	}
	// The escape was refused, so the compiled theme.css served: the
	// frame as shipped is the fail-closed default.
	if !strings.Contains(body, "token") && !strings.Contains(body, ":root") {
		t.Logf("compiled theme.css body head: %q", body[:min(80, len(body))])
	}
}

// TestOverlaySeamRefusesNonUIExtension pins the allowlist over
// the wire: a .png in ui/ never overrides anything.
func TestOverlaySeamRefusesNonUIExtension(t *testing.T) {
	addr, overlayDir := startRealOverlayServer(t)
	if err := os.WriteFile(filepath.Join(overlayDir, "evil.png"), []byte("not frame"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, _ := get(t, addr, "/evil.png")
	if code != 404 {
		t.Fatalf("GET /evil.png = %d, want 404", code)
	}
}
