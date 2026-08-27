package dashboard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

/*
Pins the T1 frame overlay boundary (docs/THREAT_MODEL-ui-disk-overlay.md).

FULL RE-FORM, by operator ruling (James, 2026-08-24): "The operator and
the AI identity should be able to override and re-form the UI." All
three servable frame extensions — .html, .js, .css — may be replaced
from disk. This reverses the presentation-only tier that shipped first;
the threat model keeps that history and the residual risks the ruling
accepts (redressing; overlay scripts hold operator-session authority).

What the ruling does NOT grant is network egress or containment escape:
connect-src 'self' and os.Root still hold, and these tests pin both.
Every refusal path returns "use the compiled byte", so the failure mode
of the whole tier is: the frame looks as shipped.
*/

func TestFrameOverlayServesFullReForm(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"theme.css":  ":root{--accent:#bada55}",
		"app.js":     "/*reformed*/ export const x = 1",
		"index.html": "<!doctype html><title>re-formed</title>",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{}

	// Unwired is the default and must never touch disk.
	if _, ok := s.overlayAsset("/theme.css"); ok {
		t.Fatal("an unwired overlay must not resolve anything")
	}

	s.SetUIOverlay(dir)
	wants := map[string]string{
		"/theme.css":  "#bada55",
		"/app.js":     "reformed",
		"/index.html": "re-formed",
	}
	for p, want := range wants {
		got, ok := s.overlayAsset(p)
		if !ok {
			t.Fatalf("%s must be served from the re-form overlay — the operator ruling (2026-08-24) grants all three frame extensions", p)
		}
		if !strings.Contains(string(got), want) {
			t.Fatalf("%s must serve the operator's bytes, %q not found in %q", p, want, got)
		}
	}

	// A file the operator did NOT override falls through to the embed.
	if _, ok := s.overlayAsset("/layout.css"); ok {
		t.Fatal("an absent overlay file must fall through to the compiled byte")
	}
}

func TestFrameOverlayRefusesNonFrameExtensions(t *testing.T) {
	dir := t.TempDir()
	// Only .html/.js/.css are frame. A data dir can hold anything; the
	// overlay serves frame and only frame — a planted .png, .json, or
	// .svg never becomes servable by virtue of sitting in ui/.
	for _, name := range []string{"secret.png", "config.json", "evil.svg", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not frame"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{}
	s.SetUIOverlay(dir)
	for _, p := range []string{"/secret.png", "/config.json", "/evil.svg", "/notes.txt"} {
		if _, ok := s.overlayAsset(p); ok {
			t.Fatalf("%s was honoured from disk — only .html/.js/.css are frame and servable", p)
		}
	}
}

func TestFrameOverlayRefusesEscapeAndNonFiles(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.css")
	if err := os.WriteFile(outside, []byte("body{content:'stolen'}"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{}
	s.SetUIOverlay(dir)

	// A directory named like a frame file is not a frame file.
	if err := os.Mkdir(filepath.Join(dir, "dir.css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.overlayAsset("/dir.css"); ok {
		t.Fatal("a directory must not be served as frame")
	}

	// Oversized falls back rather than being served.
	big := make([]byte, maxOverlayBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, "big.js"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.overlayAsset("/big.js"); ok {
		t.Fatal("an oversized overlay file must fall back to the compiled byte")
	}

	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows — unproven there, see threat model")
	}

	// The escape that a string fence passes and os.Root refuses: a
	// symlink inside the overlay dir pointing out of it.
	if err := os.Symlink(outside, filepath.Join(dir, "escape.css")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, ok := s.overlayAsset("/escape.css"); ok {
		t.Fatalf("a symlink escaped the overlay root and served %q — "+
			"containment must be kernel-enforced, not name-checked", got)
	}
}
