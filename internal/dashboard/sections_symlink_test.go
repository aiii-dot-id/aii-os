package dashboard

import (
	"context"
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
// .
// .
// .
func TestSectionSymlinkEscapeRefused(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.css")
	if err := os.WriteFile(secret, []byte("/* stolen */"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!DOCTYPE html>ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real.css"), []byte("#x{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// .
	if err := os.Symlink(secret, filepath.Join(dir, "leak.css")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	// .
	if err := os.Symlink(outside, filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}

	reg := sections.NewRegistry()
	if err := reg.Register(&sections.Section{
		Decl: sections.Decl{ID: "s", Title: "S", Slot: "panel", Entry: "index.html"},
		Dir:  dir,
	}); err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetSections(reg)
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
	if resp := get("/sections/s/real.css"); resp.StatusCode != http.StatusOK {
		t.Fatalf("an ordinary section file must still serve; got %d", resp.StatusCode)
	}

	if resp := get("/sections/s/leak.css"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a symlinked FILE escaped the section root: got %d, want 404", resp.StatusCode)
	}
	if resp := get("/sections/s/sub/secret.css"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a symlinked PARENT escaped the section root: got %d, want 404", resp.StatusCode)
	}
	// .
	if err := os.Mkdir(filepath.Join(dir, "dir.css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if resp := get("/sections/s/dir.css"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a directory was served as a section asset: got %d, want 404", resp.StatusCode)
	}
}
