package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The project-files route (§9.3a, read direction) serves a project's
// directory under os.Root containment. This seam test pins every wall
// the threat model names, over a real server, control-first: if the
// ordinary-file control fails, every refusal below proves nothing
// (the sections_symlink law).

func TestProjectFileServingWalls(t *testing.T) {
	// Ground truth outside every project root.
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("# stolen"), 0o600); err != nil {
		t.Fatal(err)
	}

	proj := t.TempDir()
	must := func(p string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(proj, p), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	must("README.md", []byte("# hello\n"))
	must("data.csv", []byte("a,b\n1,2\n"))
	must("active.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.cookie)</script></svg>`))
	must("big.png", make([]byte, projectFileMaxBytes+1))
	if err := os.MkdirAll(filepath.Join(proj, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	must("sub/note.txt", []byte("nested"))
	// Escape 1: a servable-looking name that is really a link out.
	if err := os.Symlink(secret, filepath.Join(proj, "leak.md")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	// Escape 2: an innocent-looking subdirectory that links out.
	if err := os.Symlink(outside, filepath.Join(proj, "linkdir")); err != nil {
		t.Fatal(err)
	}

	h := &WSHandler{GetProjectRoot: func(id string) (string, bool) {
		if id == "p1" {
			return proj, true
		}
		return "", false
	}}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	get := func(p string) *http.Response {
		t.Helper()
		resp, err := testClient.Get("https://" + addr + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	// Control FIRST: ordinary files must serve, or every refusal
	// below proves nothing.
	if resp := get("/p/p1/README.md"); resp.StatusCode != http.StatusOK {
		t.Fatalf("control: an ordinary file must serve; got %d", resp.StatusCode)
	}
	if ct := get("/p/p1/README.md").Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("markdown must serve as text/plain (the viewer renders it, not the browser); got %q", ct)
	}
	if resp := get("/p/p1/sub/note.txt"); resp.StatusCode != http.StatusOK {
		t.Errorf("nested regular file must serve; got %d", resp.StatusCode)
	}
	// SVG keeps its honest type; the wall against a NAVIGATED svg is the
	// CSP (default-src 'none'; sandbox) that every served byte carries,
	// not a downgraded Content-Type that would also break the viewer's
	// <img> rendering. See TestProjectFilesRequireTheAccessTokenAndServeInertly.
	if ct := get("/p/p1/active.svg").Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("SVG must be inert text even under direct navigation; got %q", ct)
	}

	// Containment: symlinked FILE.
	if resp := get("/p/p1/leak.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a symlinked FILE escaped the project root: got %d, want 404", resp.StatusCode)
	}
	// Containment: symlinked PARENT DIRECTORY.
	if resp := get("/p/p1/linkdir/secret.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a symlinked DIRECTORY escaped the project root: got %d, want 404", resp.StatusCode)
	}
	// Traversal shapes against the kernel fence.
	if resp := get("/p/p1/../secret.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("dotdot traversal served outside the root: got %d, want 404", resp.StatusCode)
	}
	if resp := get("/p/p1/..%2fsecret.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("encoded traversal served outside the root: got %d, want 404", resp.StatusCode)
	}

	// Regular files only: directories 404.
	if resp := get("/p/p1/sub"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a directory must not serve as a file; got %d, want 404", resp.StatusCode)
	}

	// MIME allowlist consulted before the first byte: an oversized
	// .bin (refused extension) must 404, not 413 — the wall order
	// means the size of a file you may not read is never disclosed.
	must("big.bin", make([]byte, projectFileMaxBytes+1))
	if resp := get("/p/p1/big.bin"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("refused extension must 404 unread (no size disclosure); got %d", resp.StatusCode)
	}

	// Declared cap: an oversized SERVABLE file refuses with 413.
	if resp := get("/p/p1/big.png"); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized servable file must 413; got %d, resp %v", resp.StatusCode, resp)
	}

	// Unknown project: the uniform 404 that leaks nothing.
	if resp := get("/p/nosuch/README.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown project must 404; got %d", resp.StatusCode)
	}

	// Nil-root and nil-handler shapes: uniform 404, no panic.
	h2 := &WSHandler{}
	s2 := New("127.0.0.1", 0, h2)
	addr2, err := s2.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Shutdown(context.Background()) })
	resp, err := testClient.Get("https://" + addr2 + "/p/p1/README.md")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("nil GetProjectRoot must 404 uniformly; got %d", resp.StatusCode)
	}
}

func TestProjectFileServingHonorsConfiguredAccessToken(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "notes.md"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &WSHandler{GetProjectRoot: func(id string) (string, bool) { return proj, id == "p1" }}
	s := New("127.0.0.1", 0, h)
	s.SetAccessToken(true, "right-token")

	request := func(cookie string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/p/p1/notes.md", nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: cookie})
		}
		return r
	}
	for _, tc := range []struct {
		name, token string
		want        int
		// A refused token answers 404, not 403: every wall on this
		// route returns the same status so an unauthorized caller
		// cannot use it to learn which files exist.
	}{{"missing", "", http.StatusNotFound}, {"wrong", "wrong-token", http.StatusNotFound}, {"right", s.accessHash(), http.StatusOK}} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.serveProjectFile(rr, request(tc.token), "p1", "notes.md")
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d", rr.Code, tc.want)
			}
		})
	}
}

// THE PROJECT ROUTE STANDS BEHIND THE SAME WALL AS THE CHAT'S. Its files
// are read through one function (serveFileIn), so a request a browser
// says came from another site's page, or from a page on another port of
// this host, is refused there too, before the file is looked at, with the
// reason; a request from this dashboard's own page, a typed address and a
// caller that sends no fetch metadata are answered as before, and every
// answer tells the browser to keep the bytes from another origin's page.
func TestTheProjectRouteRefusesAnotherSitesPage(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "README.md"), []byte("# hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1", 0, &WSHandler{GetProjectRoot: func(id string) (string, bool) { return proj, id == "p1" }})
	for site, want := range map[string]int{"": http.StatusOK, "none": http.StatusOK, "same-origin": http.StatusOK,
		"same-site": http.StatusForbidden, "cross-site": http.StatusForbidden} {
		for _, file := range []string{"README.md", "absent.md"} {
			r := httptest.NewRequest(http.MethodGet, "/p/p1/"+file, nil)
			if site != "" {
				r.Header.Set("Sec-Fetch-Site", site)
			}
			rr := httptest.NewRecorder()
			s.serveProjectFile(rr, r, "p1", file)
			switch {
			case want == http.StatusForbidden && (rr.Code != want || !strings.Contains(rr.Body.String(), "a page of another site") || strings.Contains(rr.Body.String(), "hello")):
				t.Errorf("%s, a request the browser says is %q: %d %q, want 403 saying why and nothing of the file", file, site, rr.Code, rr.Body.String())
			case want == http.StatusOK && file == "README.md" && (rr.Code != want || rr.Header().Get("Cross-Origin-Resource-Policy") != "same-origin"):
				t.Errorf("a request the browser says is %q: %d %v, want the file with the resource-policy header", site, rr.Code, rr.Header())
			}
		}
	}
}
