package dashboard

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An operator-authorable surface owes its author a readback: accepted,
// rejected, or inert. Silence is the one answer it may not give, because
// silence is indistinguishable from "your file was used and looks like
// that". These tests pin each outcome as an observable event.

// captureLog redirects the standard logger for the duration of fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

func newOverlayServer(t *testing.T, dir string) *Server {
	t.Helper()
	s := New("127.0.26.1", 0, &WSHandler{IdentityName: "readback"})
	s.SetUIOverlay(dir)
	return s
}

func TestOverlayReportsAccepted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	s := newOverlayServer(t, dir)

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/theme.css"); !ok {
			t.Fatal("a valid overlay file must be served")
		}
	})
	if !strings.Contains(out, "accepted") || !strings.Contains(out, "/theme.css") {
		t.Fatalf("an accepted overlay must say so; log was %q", out)
	}
}

func TestOverlayReportsRejectedExtension(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.png"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	s := newOverlayServer(t, dir)

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/secret.png"); ok {
			t.Fatal("a non-frame extension must not be served")
		}
	})
	if !strings.Contains(out, "rejected") {
		t.Fatalf("a rejected extension must be reported, not silently ignored; log was %q", out)
	}
}

func TestOverlayReportsOversizedAsInert(t *testing.T) {
	dir := t.TempDir()
	big := bytes.Repeat([]byte("a"), maxOverlayBytes+1)
	if err := os.WriteFile(filepath.Join(dir, "app.js"), big, 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	s := newOverlayServer(t, dir)

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/app.js"); ok {
			t.Fatal("an oversized overlay must fall back to the compiled byte")
		}
	})
	if !strings.Contains(out, "inert") || !strings.Contains(out, "ceiling") {
		t.Fatalf("an oversized overlay is inert and must say why; log was %q", out)
	}
}

// Absent is the normal case — overriding part of the frame is ordinary
// use, not a mistake — so it must stay silent. A readback that fires on
// every un-overridden file is a flood, and a flood is silence.
func TestOverlayAbsentIsSilent(t *testing.T) {
	s := newOverlayServer(t, t.TempDir())

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/index.html"); ok {
			t.Fatal("an absent overlay file must not be served")
		}
	})
	if out != "" {
		t.Fatalf("an absent overlay file is normal and must stay silent; log was %q", out)
	}
}

// The readback is deduped per path+outcome so a per-request decision does
// not become a per-page-load log line.
func TestOverlayReadbackIsDeduped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	s := newOverlayServer(t, dir)

	out := captureLog(t, func() {
		for i := 0; i < 5; i++ {
			s.overlayAsset("/theme.css")
		}
	})
	if n := strings.Count(out, "accepted"); n != 1 {
		t.Fatalf("readback must be deduped: got %d accepted lines, want 1; log was %q", n, out)
	}
}
