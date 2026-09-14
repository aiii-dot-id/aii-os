package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

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
	sum := sha256.Sum256([]byte(token))
	s.SetAccessToken(true, hex.EncodeToString(sum[:]))

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
			req.AddCookie(&http.Cookie{Name: "aii_token", Value: tok})
		}
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	// .
	// .
	ok := get("/p/p1/README.md", token)
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("control: the correct token must serve the file; got %d", ok.StatusCode)
	}

	// .
	if resp := get("/p/p1/README.md", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a tokenless GET received %d — required-token mode does not protect project files", resp.StatusCode)
	}
	// .
	// .
	if resp := get("/p/p1/README.md", "not-the-token"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a wrong token received %d, want 404", resp.StatusCode)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	const wantCSP = "default-src 'none'; sandbox"
	if got := ok.Header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("project file CSP = %q, want %q — a navigated SVG would run as the dashboard origin", got, wantCSP)
	}
	if got := ok.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff = %q, want nosniff", got)
	}

	// .
	// .
	svg := get("/p/p1/logo.svg", token)
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
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
	sum := sha256.Sum256([]byte(token))
	s.SetAccessToken(true, hex.EncodeToString(sum[:]))

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
			req.AddCookie(&http.Cookie{Name: "aii_token", Value: tok})
		}
		resp, err := testClient.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	// .
	if resp := get(token); resp.StatusCode != http.StatusOK {
		t.Fatalf("control: a registered section must serve with the token; got %d", resp.StatusCode)
	}
	// .
	if resp := get(""); resp.StatusCode == http.StatusOK {
		t.Error("a tokenless section GET received 200 under required-token mode")
	}
}

// .
// .
func TestTokenAuthorizedRefusesRatherThanFallingOpen(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	req, _ := http.NewRequest("GET", "https://example/", nil)

	// .
	if !s.tokenAuthorized(req) {
		t.Error("with no token required every request must pass")
	}

	// .
	s.SetAccessToken(true, "not-hex")
	if s.tokenAuthorized(req) {
		t.Error("a malformed token hash must refuse, not fall open")
	}
	s.SetAccessToken(true, "")
	if s.tokenAuthorized(req) {
		t.Error("an empty token hash must refuse, not fall open")
	}

	// .
	const token = "t"
	sum := sha256.Sum256([]byte(token))
	s.SetAccessToken(true, hex.EncodeToString(sum[:]))
	if s.tokenAuthorized(req) {
		t.Error("a request with no cookie must be refused")
	}
	req.AddCookie(&http.Cookie{Name: "aii_token", Value: token})
	if !s.tokenAuthorized(req) {
		t.Error("the correct token must be accepted")
	}
}
