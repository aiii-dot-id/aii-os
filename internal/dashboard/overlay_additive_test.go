package dashboard

// The additive morph layer. custom.css and custom.js ship EMPTY and are
// loaded last, so an operator or identity adjusts the frame by adding
// rather than by replacing. This exists because replace-only override is
// a fork: overlaying layout.css means owning 32 KiB that can never
// receive the next release's fixes, and the overlay reports "accepted"
// while it silently goes stale. Adding cannot fork.
//
// Order is not cosmetic here — it IS the mechanism. If custom.css were
// linked before layout.css the cascade would defeat every rule in it,
// and the layer would be inert while looking perfectly well wired. That
// is the exact silent-inertness class this codebase keeps paying for,
// so the order is pinned by test, not by comment.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdditiveLayerShipsAndLoadsLast pins both halves: the empty layer
// is really served, and it is positioned after the sheets it adjusts.
func TestAdditiveLayerShipsAndLoadsLast(t *testing.T) {
	addr, _ := startRealOverlayServer(t)

	status, hdr, body := get(t, addr, "/index.html")
	if status != 200 {
		t.Fatalf("index.html: got %d, want 200", status)
	}
	iLayout := strings.Index(body, "./layout.css")
	iCustomCSS := strings.Index(body, "./custom.css")
	iApp := strings.Index(body, "./app.js")
	iCustomJS := strings.Index(body, "./custom.js")

	if iCustomCSS < 0 {
		t.Error("index.html does not link custom.css — the additive CSS layer is unreachable")
	}
	if iCustomJS < 0 {
		t.Error("index.html does not load custom.js — the additive behaviour layer is unreachable")
	}
	if iCustomCSS >= 0 && iLayout >= 0 && iCustomCSS < iLayout {
		t.Error("custom.css is linked BEFORE layout.css: the cascade defeats it and the layer is inert")
	}
	if iCustomJS >= 0 && iApp >= 0 && iCustomJS < iApp {
		t.Error("custom.js executes BEFORE app.js: it would observe an unbooted frame")
	}
	_ = hdr

	// Both files must actually exist as compiled frame, or the link is
	// a 404 the browser swallows in silence.
	for _, p := range []string{"/custom.css", "/custom.js"} {
		st, h, _ := get(t, addr, p)
		if st != 200 {
			t.Errorf("%s: got %d, want 200 — the shipped empty layer is missing", p, st)
		}
		if ct := h.Get("Content-Security-Policy"); ct == "" {
			t.Errorf("%s: served without uiCSP", p)
		}
	}
}

// TestAdditiveLayerIsOverridable proves the layer needs no new
// mechanism: R71's existing overlay already serves it from disk.
func TestAdditiveLayerIsOverridable(t *testing.T) {
	addr, overlayDir := startRealOverlayServer(t)

	want := ":root{--accent:#ff0000}"
	if err := os.WriteFile(filepath.Join(overlayDir, "custom.css"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _, body := get(t, addr, "/custom.css")
	if status != 200 {
		t.Fatalf("custom.css: got %d, want 200", status)
	}
	if !strings.Contains(body, want) {
		t.Errorf("overlaid custom.css was not served; got %q", body)
	}
}
