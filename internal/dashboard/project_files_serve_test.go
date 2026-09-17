package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .

func TestProjectFileServingWalls(t *testing.T) {
	// .
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
	// .
	if err := os.Symlink(secret, filepath.Join(proj, "leak.md")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	// .
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

	// .
	// .
	if resp := get("/p/p1/README.md"); resp.StatusCode != http.StatusOK {
		t.Fatalf("control: an ordinary file must serve; got %d", resp.StatusCode)
	}
	if ct := get("/p/p1/README.md").Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("markdown must serve as text/plain (the viewer renders it, not the browser); got %q", ct)
	}
	if resp := get("/p/p1/sub/note.txt"); resp.StatusCode != http.StatusOK {
		t.Errorf("nested regular file must serve; got %d", resp.StatusCode)
	}
	// .
	// .
	// .
	// .
	if ct := get("/p/p1/active.svg").Header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("SVG must be inert text even under direct navigation; got %q", ct)
	}

	// .
	if resp := get("/p/p1/leak.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a symlinked FILE escaped the project root: got %d, want 404", resp.StatusCode)
	}
	// .
	if resp := get("/p/p1/linkdir/secret.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a symlinked DIRECTORY escaped the project root: got %d, want 404", resp.StatusCode)
	}
	// .
	if resp := get("/p/p1/../secret.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("dotdot traversal served outside the root: got %d, want 404", resp.StatusCode)
	}
	if resp := get("/p/p1/..%2fsecret.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("encoded traversal served outside the root: got %d, want 404", resp.StatusCode)
	}

	// .
	if resp := get("/p/p1/sub"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a directory must not serve as a file; got %d, want 404", resp.StatusCode)
	}

	// .
	// .
	// .
	must("big.bin", make([]byte, projectFileMaxBytes+1))
	if resp := get("/p/p1/big.bin"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("refused extension must 404 unread (no size disclosure); got %d", resp.StatusCode)
	}

	// .
	if resp := get("/p/p1/big.png"); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized servable file must 413; got %d, resp %v", resp.StatusCode, resp)
	}

	// .
	if resp := get("/p/nosuch/README.md"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown project must 404; got %d", resp.StatusCode)
	}

	// .
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
		// .
		// .
		// .
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
