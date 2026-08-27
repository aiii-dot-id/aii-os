package dashboard

// The section sandbox walls, pinned from the outside (threat rows S2,
// S5 and the §9.1 evidence): exact CSP on every section response, the
// Origin:null refusal that makes the H2 gate the section wall, no POST
// command surface at all, 404 for everything unregistered, and the
// dev-serve discipline (no-store + SAFE refusal).

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sections"
	"github.com/coder/websocket"
)

// startWithSections boots a server with a real registry holding one
// verified-shaped section ("hello", from a cache-like dir) and one dev
// section ("devsec"). safeFlag drives the registry's SAFE answer.
func startWithSections(t *testing.T) (addr string, s *Server, setSafe func(bool)) {
	t.Helper()
	mkdir := func(files map[string]string) string {
		dir := t.TempDir()
		for rel, body := range files {
			full := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	helloDir := mkdir(map[string]string{
		"index.html":    "<!DOCTYPE html><script type=\"module\" src=\"./module.js\"></script>",
		"module.js":     "import { ready } from '/section-api.js';",
		"styles.css":    "#x{}",
		"sub/extra.css": ".y{}",
		"notes.txt":     "not servable",
	})
	devDir := mkdir(map[string]string{"index.html": "<!DOCTYPE html>dev"})

	var mu sync.Mutex
	safe := false
	reg := sections.NewRegistry()
	reg.SetSafeSource(func() (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if safe {
			return "test reason", true
		}
		return "", false
	})
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(reg.Register(&sections.Section{
		Decl: sections.Decl{ID: "hello", Title: "Hello", Slot: "panel",
			Commands: []string{"project.select"}, Topics: []string{"status"}, Entry: "index.html"},
		Dir: helloDir,
	}))
	must(reg.Register(&sections.Section{
		Decl: sections.Decl{ID: "devsec", Title: "Dev", Slot: "rail", Entry: "index.html"},
		Dir:  devDir, Dev: true,
	}))

	s = New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
	s.SetSections(reg)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return addr, s, func(v bool) { mu.Lock(); safe = v; mu.Unlock() }
}

func TestSectionServingCSPAndWalls(t *testing.T) {
	addr, _, _ := startWithSections(t)

	get := func(p string) *http.Response {
		t.Helper()
		resp, err := testClient.Get("https://" + addr + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	// The exact wall, byte for byte, on every servable type.
	wantCSP := "default-src 'none'; " +
		"script-src 'self' https://" + addr + "/sections/hello/ https://" + addr + "/section-api.js; " +
		"style-src 'self' https://" + addr + "/sections/hello/; " +
		"img-src 'self' https://" + addr + "/sections/hello/ data:; " +
		"connect-src 'none'; " +
		"frame-ancestors 'self'; " +
		"base-uri 'none'; " +
		"form-action 'none'"
	for p, ctype := range map[string]string{
		"/sections/hello/index.html":    "text/html; charset=utf-8",
		"/sections/hello/module.js":     "text/javascript; charset=utf-8",
		"/sections/hello/styles.css":    "text/css; charset=utf-8",
		"/sections/hello/sub/extra.css": "text/css; charset=utf-8",
	} {
		resp := get(p)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 || len(body) == 0 {
			t.Fatalf("GET %s = %d (%d bytes)", p, resp.StatusCode, len(body))
		}
		if got := resp.Header.Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("GET %s CSP:\n got %q\nwant %q", p, got, wantCSP)
		}
		if got := resp.Header.Get("Content-Type"); got != ctype {
			t.Errorf("GET %s Content-Type = %q, want %q", p, got, ctype)
		}
		if got := resp.Header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
			t.Errorf("GET %s X-Frame-Options = %q, want SAMEORIGIN", p, got)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s nosniff missing (%q)", p, got)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "" {
			t.Errorf("verified section must not disable caching, got %q", cc)
		}
	}

	// Everything unregistered or outside the walls is 404: unknown ids,
	// non-allowlisted extensions, traversal shapes.
	for _, p := range []string{
		"/sections/ghost/index.html",   // never registered
		"/sections/hello/notes.txt",    // extension outside the allowlist
		"/sections/hello/",             // no file
		"/sections/hello/../hello.css", // traversal (mux-cleaned, then fenced)
		"/sections/hello/%2e%2e/x.css", // encoded traversal
		"/sections/hello/missing.js",   // absent file
	} {
		if resp := get(p); resp.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
		}
	}

	// The frame-owned client is served from the frame tree.
	resp := get("/section-api.js")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("/section-api.js = %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

// TestSectionOriginNullRefused pins the H2 gate AS the section wall
// (threat row S2): a sandboxed iframe has an opaque origin, so any
// socket it opens sends `Origin: null` — and the standing wsAuthorized
// policy (present, parseable, same-host http Origin) refuses it. No
// new gate; the test makes the old one's coverage of sections a
// regression guarantee. A command POST has no route at all — the WS is
// the only command surface, so the wall is one door wide.
func TestSectionOriginNullRefused(t *testing.T) {
	addr, _, _ := startWithSections(t)

	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	req := "GET /ws HTTP/1.1\r\nHost: " + addr + "\r\n" +
		"Origin: null\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := c.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(line, " 101 ") {
		t.Fatalf("Origin:null upgrade must be refused (the section wall), got: %s", strings.TrimSpace(line))
	}

	// No POST command surface exists — for the WS route or a section
	// path. (Go's method-scoped mux answers 405 Method Not Allowed.)
	for _, p := range []string{"/ws", "/sections/hello/index.html", "/"} {
		resp, err := testClient.Post("https://"+addr+p, "application/json", strings.NewReader(`{"type":"chat"}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 405/404 (no POST surface)", p, resp.StatusCode)
		}
	}
}

// TestDevSectionDiscipline: dev responses re-read disk every load
// (no-store) and SAFE refuses dev files ENTIRELY while verified
// sections keep serving (the frame unmounts them; static verified
// bytes are not an authority surface).
func TestDevSectionDiscipline(t *testing.T) {
	addr, _, setSafe := startWithSections(t)

	resp, err := testClient.Get("https://" + addr + "/sections/devsec/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("dev section = %d Cache-Control %q, want 200 no-store", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}

	setSafe(true)
	resp, err = testClient.Get("https://" + addr + "/sections/devsec/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("dev section under SAFE = %d, want 404 (refused entirely)", resp.StatusCode)
	}
	resp, err = testClient.Get("https://" + addr + "/sections/hello/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("verified section under SAFE = %d, want 200 (frame unmounts; serving static verified bytes stays honest)", resp.StatusCode)
	}
}

// TestSectionsAndLayoutOverWS: the two frame queries answer from
// server state (mode-independent), SAFE empties the section list with
// its reason attached, and BroadcastLayout pushes to live connections
// (the ui-layout.json hot-reload path).
func TestSectionsAndLayoutOverWS(t *testing.T) {
	addr, s, setSafe := startWithSections(t)
	var layoutMu sync.Mutex
	layout := []byte(`{"v":1,"profiles":{"desktop":{"panel":["hello"]}}}`)
	s.SetLayoutSource(func() []byte { layoutMu.Lock(); defer layoutMu.Unlock(); return layout })

	conn := dialWS(t, addr)

	send := func(q string) {
		t.Helper()
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"query","query":"`+q+`"}`)); err != nil {
			t.Fatal(err)
		}
	}

	send("sections")
	m := drainUntil(t, conn, "sections")
	if len(m.Sections) != 2 || m.Sections[0].ID != "devsec" || !m.Sections[0].Dev || m.Sections[1].ID != "hello" {
		t.Fatalf("sections answer wrong: %+v", m.Sections)
	}
	if len(m.Sections[1].Commands) != 1 || m.Sections[1].Commands[0] != "project.select" {
		t.Fatalf("declared commands must ride to the frame: %+v", m.Sections[1])
	}

	send("ui_layout")
	m = drainUntil(t, conn, "layout")
	var parsed struct {
		V        int                            `json:"v"`
		Profiles map[string]map[string][]string `json:"profiles"`
	}
	if err := json.Unmarshal(m.Layout, &parsed); err != nil || parsed.V != 1 || parsed.Profiles["desktop"]["panel"][0] != "hello" {
		t.Fatalf("layout answer wrong: %s (%v)", string(m.Layout), err)
	}

	// Hot reload: the app-side watcher calls BroadcastLayout; every
	// live connection hears the new profiles unasked.
	layoutMu.Lock()
	layout = []byte(`{"v":1,"profiles":{"desktop":{}}}`)
	layoutMu.Unlock()
	s.BroadcastLayout()
	m = drainUntil(t, conn, "layout")
	if strings.Contains(string(m.Layout), "hello") {
		t.Fatalf("broadcast must carry the fresh layout, got %s", string(m.Layout))
	}

	// SAFE empties the list, names the reason, and the entry broadcast
	// (applySafeState → BroadcastSections) reaches the live socket.
	setSafe(true)
	s.BroadcastSections()
	m = drainUntil(t, conn, "sections")
	if len(m.Sections) != 0 || m.Message != "test reason" {
		t.Fatalf("SAFE sections answer must be empty with the reason, got %+v %q", m.Sections, m.Message)
	}
}
