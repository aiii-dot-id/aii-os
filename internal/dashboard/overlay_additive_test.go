package dashboard

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

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
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

	// .
	// .
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

// .
// .
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
