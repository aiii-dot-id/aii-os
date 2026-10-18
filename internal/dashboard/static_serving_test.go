package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStaticServingModuleLayout is the embed/serving proof for the UP1
// module layout: every embedded UI file must be reachable over a STARTED
// server (through the real mux and hostGate) with the correct
// Content-Type — browsers hard-refuse an ES module served as anything but
// JavaScript, so a wrong or missing type here is a dead dashboard that
// every Go test would otherwise still pass.
func TestStaticServingModuleLayout(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	get := func(urlPath string) (int, string, []byte) {
		t.Helper()
		resp, err := testClient.Get("https://" + addr + urlPath)
		if err != nil {
			t.Fatalf("GET %s: %v", urlPath, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("GET %s read: %v", urlPath, err)
		}
		return resp.StatusCode, resp.Header.Get("Content-Type"), body
	}

	wantType := map[string]string{
		".html": "text/html; charset=utf-8",
		".js":   "text/javascript; charset=utf-8",
		".css":  "text/css; charset=utf-8",
		".png":  "image/png", // the tab's icons, served from the embed only
	}

	// The shell at both its addresses (pinned pre-split behavior), and it
	// must reference the split assets it now depends on.
	for _, p := range []string{"/", "/index.html"} {
		code, ctype, body := get(p)
		if code != 200 || ctype != wantType[".html"] {
			t.Fatalf("GET %s = %d %q, want 200 %q", p, code, ctype, wantType[".html"])
		}
		for _, ref := range []string{
			`<script type="module" src="./app.js"></script>`,
			`<script type="module" src="./recovery.js"></script>`,
			`<link rel="stylesheet" href="./theme.css">`,
			`<link rel="stylesheet" href="./layout.css">`,
		} {
			if !strings.Contains(string(body), ref) {
				t.Errorf("GET %s: shell does not reference %s", p, ref)
			}
		}
	}

	// The named load-bearing four, explicitly (readable intent)...
	for _, p := range []string{"/app.js", "/recovery.js", "/ws.js", "/views/chat.js", "/theme.css"} {
		ext := p[strings.LastIndex(p, "."):]
		code, ctype, body := get(p)
		if code != 200 || ctype != wantType[ext] || len(body) == 0 {
			t.Errorf("GET %s = %d %q (%d bytes), want 200 %q non-empty", p, code, ctype, len(body), wantType[ext])
		}
	}

	// ...and then EVERY embedded file, so a new module can never be
	// embedded-but-unservable (or servable-but-unembedded — the walk is
	// over the same FS the handler reads).
	err = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		urlPath := strings.TrimPrefix(p, "static")
		dot := strings.LastIndex(p, ".")
		if dot < 0 {
			t.Errorf("embedded file %s has no extension — the handler cannot type it", p)
			return nil
		}
		want, ok := wantType[p[dot:]]
		if !ok {
			t.Errorf("embedded file %s has extension %q outside the servable set", p, p[dot:])
			return nil
		}
		code, ctype, body := get(urlPath)
		if code != 200 || ctype != want || len(body) == 0 {
			t.Errorf("GET %s = %d %q (%d bytes), want 200 %q non-empty", urlPath, code, ctype, len(body), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Nothing else is servable: unknown files, extensionless paths,
	// directories, and dot-dot forms all miss (the mux cleans "..", so
	// that request resolves to /server.go and 404s on absence).
	for _, p := range []string{"/nope.js", "/nope.css", "/views", "/views/", "/server.go", "/static/../server.go"} {
		if code, _, _ := get(p); code != 404 {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
}

// TestTheFrameRevalidatesOnTheBytesItServes: the frame is no-cache, so a
// browser asks again on every use; the ETag is what lets the answer be a 304
// instead of the whole file, and it must be the hash of the exact bytes that
// address serves. An overlay edit is a new tag and the new bytes; the shipped
// address, which never consults the overlay, never answers the overlay's tag.
func TestTheFrameRevalidatesOnTheBytesItServes(t *testing.T) {
	dir := t.TempDir()
	writeOverlay(t, dir, "app.js", "export const edited = 1;")
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetUIOverlay(dir)

	approveOverlay(t, s, dir, "app.js")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	get := func(urlPath, etag string) (*http.Response, []byte) {
		t.Helper()
		r, _ := http.NewRequest(http.MethodGet, "https://"+addr+urlPath, nil)
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		resp, err := testClient.Do(r)
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
	tagOf := func(body []byte) string {
		sum := sha256.Sum256(body)
		return `"` + hex.EncodeToString(sum[:]) + `"`
	}
	// fresh is a 200 whose tag is its own bytes' hash; again is the
	// revalidation with that tag, a 304 with no body.
	fresh := func(urlPath string, want []byte) string {
		t.Helper()
		resp, body := get(urlPath, "")
		tag := resp.Header.Get("ETag")
		if resp.StatusCode != 200 || string(body) != string(want) || tag != tagOf(body) {
			t.Fatalf("GET %s = %d, %d bytes, ETag %s; want 200, the %d bytes it serves, ETag %s", urlPath, resp.StatusCode, len(body), tag, len(want), tagOf(want))
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("GET %s Cache-Control = %q, want no-cache", urlPath, cc)
		}
		again, body := get(urlPath, tag)
		if again.StatusCode != http.StatusNotModified || len(body) != 0 {
			t.Fatalf("GET %s with its own tag = %d, %d bytes; want 304 and no body", urlPath, again.StatusCode, len(body))
		}
		return tag
	}

	compiledShell, _ := staticFS.ReadFile("static/index.html")
	if fresh("/", compiledShell) != fresh("/index.html", compiledShell) {
		t.Fatal("the shell's two addresses carry different tags for the same bytes")
	}
	compiled, _ := staticFS.ReadFile("static/app.js")
	before := fresh("/app.js", []byte("export const edited = 1;"))
	shipped := fresh("/shipped/app.js", compiled)

	// The overlay's tag is not the compiled file's: the shipped address
	// sends its own bytes to a browser that holds the overlay's.
	if resp, body := get("/shipped/app.js", before); resp.StatusCode != 200 || string(body) != string(compiled) {
		t.Fatalf("/shipped/app.js revalidated with the overlay's tag = %d; want 200 and the compiled bytes", resp.StatusCode)
	}

	// An edit on disk: the old tag no longer matches, and the new bytes
	// arrive under their own.
	writeOverlay(t, dir, "app.js", "export const edited = 2;")
	approveOverlay(t, s, dir, "app.js")
	resp, body := get("/app.js", before)
	if resp.StatusCode != 200 || string(body) != "export const edited = 2;" || resp.Header.Get("ETag") != tagOf(body) {
		t.Fatalf("after an overlay edit, GET /app.js with the old tag = %d %q ETag %s; want 200, the new bytes and their tag", resp.StatusCode, body, resp.Header.Get("ETag"))
	}
	if shipped == before || resp.Header.Get("ETag") == before {
		t.Fatal("two different byte strings carried one tag")
	}
}

// BenchmarkFrameLoad serves every frame file once, the way a page load asks
// for them: fresh, and revalidating with whatever validator each response
// carried, as a reload does. "handler" calls the real mux in process (the
// server's own cost); "tls" is the same load over the loopback listener, as
// a browser on this machine sees it. body-B/op is what crosses the wire.
func BenchmarkFrameLoad(b *testing.B) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Shutdown(context.Background()) })
	var paths []string
	fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasSuffix(p, ".png") {
			paths = append(paths, strings.TrimPrefix(p, "static"))
		}
		return err
	})
	handler := func(p, etag string) (string, int) {
		r := httptest.NewRequest(http.MethodGet, "https://"+addr+p, nil)
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, r)
		return w.Header().Get("ETag"), w.Body.Len()
	}
	wire := func(p, etag string) (string, int) {
		r, _ := http.NewRequest(http.MethodGet, "https://"+addr+p, nil)
		if etag != "" {
			r.Header.Set("If-None-Match", etag)
		}
		resp, err := testClient.Do(r)
		if err != nil {
			b.Fatal(err)
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.Header.Get("ETag"), int(n)
	}
	tags := map[string]string{}
	for _, p := range paths {
		tags[p], _ = handler(p, "")
	}
	for _, via := range []struct {
		name string
		get  func(p, etag string) (string, int)
	}{{"handler", handler}, {"tls", wire}} {
		for _, reload := range []bool{false, true} {
			name := via.name + "/fresh"
			if reload {
				name = via.name + "/reload"
			}
			b.Run(name, func(b *testing.B) {
				body := 0
				for b.Loop() {
					body = 0
					for _, p := range paths {
						tag := ""
						if reload {
							tag = tags[p]
						}
						_, n := via.get(p, tag)
						body += n
					}
				}
				b.ReportMetric(float64(body), "body-B/op")
			})
		}
	}
}
