package dashboard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// .
// .

// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
// .

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

	// .
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

	// .
	if _, ok := s.overlayAsset("/layout.css"); ok {
		t.Fatal("an absent overlay file must fall through to the compiled byte")
	}
}

func TestFrameOverlayRefusesNonFrameExtensions(t *testing.T) {
	dir := t.TempDir()
	// .
	// .
	// .
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

	// .
	if err := os.Mkdir(filepath.Join(dir, "dir.css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.overlayAsset("/dir.css"); ok {
		t.Fatal("a directory must not be served as frame")
	}

	// .
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

	// .
	// .
	if err := os.Symlink(outside, filepath.Join(dir, "escape.css")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got, ok := s.overlayAsset("/escape.css"); ok {
		t.Fatalf("a symlink escaped the overlay root and served %q — "+
			"containment must be kernel-enforced, not name-checked", got)
	}
}
