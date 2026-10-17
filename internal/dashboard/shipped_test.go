package dashboard

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTheDashboardAsShippedConsultsNoOverlay(t *testing.T) {
	dir := t.TempDir()
	overlay := map[string]string{
		"index.html":    "<!doctype html><title>re-formed</title>",
		"recovery.js":   "throw new Error('customized way out')",
		"app.js":        "export const broken = ;",
		"views/chat.js": "throw new Error('customized chat')",
		"theme.css":     "body{display:none}",
		"custom.css":    "body{display:none}",
		"custom.js":     "document.body.remove()",
	}
	for name, body := range overlay {
		writeOverlay(t, dir, name, body)
	}
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetUIOverlay(dir)
	// Approved, so the control half serves them at "/": /shipped/ refuses
	// the identity's scripts whether the operator approved them or not.
	approveOverlay(t, s, dir, "recovery.js", "app.js", "views/chat.js", "custom.js")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	noFollow := *testClient
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(urlPath string) (*http.Response, []byte) {
		t.Helper()
		resp, err := noFollow.Get("https://" + addr + urlPath)
		if err != nil {
			t.Fatalf("GET %s: %v", urlPath, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("GET %s read: %v", urlPath, err)
		}
		return resp, body
	}
	embedded := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	for name, body := range overlay {
		ordinary, got := get("/" + name)
		if ordinary.StatusCode != 200 || string(got) != body {
			t.Fatalf("control: GET /%s = %d %q, want the overlay's bytes", name, ordinary.StatusCode, got)
		}
		shipped, got := get("/shipped/" + name)
		if strings.HasSuffix(name, ".css") {
			if shipped.StatusCode != 200 || string(got) != body {
				t.Errorf("GET /shipped/%s = %d %q, want the identity's stylesheet", name, shipped.StatusCode, got)
			}
		} else if shipped.StatusCode != 200 || !bytes.Equal(got, embedded(name)) {
			t.Errorf("GET /shipped/%s = %d (%d bytes), want the compiled %d bytes", name, shipped.StatusCode, len(got), len(embedded(name)))
		}
		for _, h := range []string{"Content-Type", "Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Cache-Control"} {
			if shipped.Header.Get(h) != ordinary.Header.Get(h) {
				t.Errorf("/shipped/%s %s = %q, the ordinary address says %q", name, h, shipped.Header.Get(h), ordinary.Header.Get(h))
			}
		}
	}

	// The shell at the shipped address, and the address without its slash
	// sent to it: the page's relative imports resolve under the directory.
	if resp, got := get("/shipped/"); resp.StatusCode != 200 || !bytes.Equal(got, embedded("index.html")) {
		t.Errorf("GET /shipped/ = %d, want the compiled shell", resp.StatusCode)
	}
	if resp, _ := get("/shipped"); resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/shipped/" {
		t.Errorf("GET /shipped = %d to %q, want a redirect to /shipped/", resp.StatusCode, resp.Header.Get("Location"))
	}
	// What the frame does not serve, the shipped address does not either.
	if resp, _ := get("/shipped/config.json"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /shipped/config.json = %d, want 404", resp.StatusCode)
	}
	if resp, got := get("/shipped/icon-32.png"); resp.StatusCode != 200 || !bytes.Equal(got, embedded("icon-32.png")) {
		t.Errorf("GET /shipped/icon-32.png = %d, want the compiled icon", resp.StatusCode)
	}
}
