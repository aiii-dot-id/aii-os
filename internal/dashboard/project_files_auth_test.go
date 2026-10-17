package dashboard

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

func TestProjectFilesRequireTheAccessTokenAndServeInertly(t *testing.T) {
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "README.md"), []byte("# hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "logo.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}

	h := &WSHandler{GetProjectRoot: func(id string) (string, bool) {
		if id == "p1" {
			return proj, true
		}
		return "", false
	}}
	s := New("127.0.0.1", 0, h)

	const token = "the-operator-token"
	s.SetAccessToken(true, token)

	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	get := func(p, tok string) *http.Response {
		t.Helper()
		req, err := http.NewRequest("GET", "https://"+addr+p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if tok != "" {
			req.AddCookie(&http.Cookie{Name: dashboardCookieName(req), Value: tok})
		}
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	// CONTROL FIRST: with the right token the file serves. Without
	// this, every refusal below proves only that the route is broken.
	ok := get("/p/p1/README.md", s.accessHash())
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("control: the correct token must serve the file; got %d", ok.StatusCode)
	}

	// The finding: no token, required mode armed.
	if resp := get("/p/p1/README.md", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a tokenless GET received %d — required-token mode does not protect project files", resp.StatusCode)
	}
	// A wrong token is refused the same way, and by constant-time
	// comparison rather than by length or prefix.
	if resp := get("/p/p1/README.md", "not-the-token"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a wrong token received %d, want 404", resp.StatusCode)
	}

	// Inert on arrival, however it is reached. The EXACT policy is
	// asserted, not merely its presence: "a header exists" would pass
	// against a policy that permits everything, and the sections route
	// pins its own wall byte for byte for the same reason. The string
	// is written out here rather than read from the code, so a change
	// to the shipped policy has to be made twice — deliberately.
	const wantCSP = "default-src 'none'; sandbox"
	if got := ok.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("project file CSP = %q, want %q — a navigated SVG would run as the dashboard origin", got, wantCSP)
	}
	if got := ok.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff = %q, want nosniff", got)
	}

	// SVG is the sharp case and still serves — the wall is the header,
	// not the removal of the capability.
	svg := get("/p/p1/logo.svg", s.accessHash())
	if svg.StatusCode != http.StatusOK {
		t.Fatalf("control: svg must still serve; got %d", svg.StatusCode)
	}
	if got := svg.Header.Get("Content-Type"); got != "image/svg+xml" {
		t.Errorf("svg Content-Type = %q, want image/svg+xml (serving it as text would be a different fix)", got)
	}
	if got := svg.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("svg CSP = %q, want %q — every served byte gets the same wall", got, wantCSP)
	}
}

// The same wall on the other route that serves files out of the
// identity's directories. Section assets were reachable tokenless for
// exactly the same reason.
//
// This registers a REAL section and checks the control first. The
// first version of this test did neither — it asked an empty server
// for a section that did not exist, so 404 was the answer either way
// and removing the wall could not fail it. A test that cannot fail is
// not a wall, it is a decoration.
func TestSectionFilesRequireTheAccessToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!DOCTYPE html>hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg := sections.NewRegistry()
	if err := reg.Register(&sections.Section{
		Decl: sections.Decl{ID: "hello", Title: "Hello", Slot: "panel", Entry: "index.html"},
		Dir:  dir,
	}); err != nil {
		t.Fatal(err)
	}

	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetSections(reg)
	const token = "the-operator-token"
	s.SetAccessToken(true, token) // before Start, as documented

	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })

	get := func(tok string) *http.Response {
		t.Helper()
		req, err := http.NewRequest("GET", "https://"+addr+"/sections/hello/index.html", nil)
		if err != nil {
			t.Fatal(err)
		}
		if tok != "" {
			req.AddCookie(&http.Cookie{Name: dashboardCookieName(req), Value: tok})
		}
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	// CONTROL: the section really does serve with the right token.
	if resp := get(s.accessHash()); resp.StatusCode != http.StatusOK {
		t.Fatalf("control: a registered section must serve with the token; got %d", resp.StatusCode)
	}
	// THE FINDING.
	if resp := get(""); resp.StatusCode == http.StatusOK {
		t.Error("a tokenless section GET received 200 under required-token mode")
	}
}

// The shared helper, directly: required mode with nothing verifiable on
// record must refuse rather than fall open.
func TestTokenAuthorizedRefusesRatherThanFallingOpen(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	req, _ := http.NewRequest("GET", "https://example/", nil)

	// Not required: open by construction.
	if !s.tokenAuthorized(req) {
		t.Error("with no token required every request must pass")
	}

	// Required with no configured original: refused, never open.
	s.SetAccessToken(true, "")
	if s.tokenAuthorized(req) {
		t.Error("an empty token must refuse, not fall open")
	}

	// Required and correct.
	const token = "t"
	s.SetAccessToken(true, token)
	if s.tokenAuthorized(req) {
		t.Error("a request with no cookie must be refused")
	}
	req.AddCookie(&http.Cookie{Name: dashboardCookieName(req), Value: s.accessHash()})
	if !s.tokenAuthorized(req) {
		t.Error("the correct token must be accepted")
	}
}
