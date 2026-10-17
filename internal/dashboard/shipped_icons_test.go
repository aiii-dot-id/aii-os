package dashboard

// The tab's icons (made by go run ./packaging/icon assets) are served
// from the binary as PNG, under the frame's policy, and nothing in the
// overlay directory replaces them: the overlay stays the three frame
// types and nothing else.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestShippedIconsServeAndNoOverlayReplacesThem(t *testing.T) {
	addr, overlayDir := startRealOverlayServer(t)
	if err := os.WriteFile(filepath.Join(overlayDir, "icon-32.png"), []byte("not the icon"), 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	hrefs := regexp.MustCompile(`href="\./([a-z0-9-]+\.png)"`).FindAllStringSubmatch(string(index), -1)
	if len(hrefs) < 3 {
		t.Fatalf("index.html links %d icons, want the two tab sizes and the home-screen icon", len(hrefs))
	}
	for _, h := range hrefs {
		name := h[1]
		want, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatalf("index.html links %s, which the binary does not carry: %v", name, err)
		}
		code, hdr, body := get(t, addr, "/"+name)
		if code != 200 || hdr.Get("Content-Type") != "image/png" {
			t.Fatalf("GET /%s = %d %q, want 200 image/png", name, code, hdr.Get("Content-Type"))
		}
		if body != string(want) {
			t.Fatalf("GET /%s served other bytes than the shipped icon (an overlay must never replace it)", name)
		}
		if hdr.Get("Content-Security-Policy") != uiCSP || hdr.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("GET /%s is served without the frame's policy", name)
		}
	}
	if code, _, _ := get(t, addr, "/no-such-icon.png"); code != 404 {
		t.Fatalf("GET /no-such-icon.png = %d, want 404", code)
	}
}
