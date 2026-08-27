package dashboard

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// A section directory is operator-writable ground. Dev sections point
// straight at it and, by their own comment, never went through the
// extractor — so the serving edge cannot assume the tree it walks was
// ever normalised. The old fence compared cleaned names with
// filepath.Rel, which never touches disk and therefore cannot see a
// symlink: the join stayed lexically "inside" while the open landed
// outside. os.Root resolves each segment in the kernel instead.
//
// The two probes below are the escape shapes that matter: a symlinked
// FILE and a symlinked PARENT DIRECTORY. The third case is the control
// that keeps this honest — an ordinary file must still serve, or the
// test would pass just as well against a handler that refused
// everything.
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
	// Escape 1: a servable-looking name that is really a link out.
	if err := os.Symlink(secret, filepath.Join(dir, "leak.css")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	// Escape 2: an innocent-looking subdirectory that is really a link out.
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
	s := New("127.0.0.1", 0, &WSHandler{IdentityName: "X"})
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

	// Control FIRST: if this fails, every refusal below proves nothing.
	if resp := get("/sections/s/real.css"); resp.StatusCode != http.StatusOK {
		t.Fatalf("an ordinary section file must still serve; got %d", resp.StatusCode)
	}

	if resp := get("/sections/s/leak.css"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a symlinked FILE escaped the section root: got %d, want 404", resp.StatusCode)
	}
	if resp := get("/sections/s/sub/secret.css"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a symlinked PARENT escaped the section root: got %d, want 404", resp.StatusCode)
	}
	// A directory is not an asset, whatever its extension.
	if err := os.Mkdir(filepath.Join(dir, "dir.css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if resp := get("/sections/s/dir.css"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a directory was served as a section asset: got %d, want 404", resp.StatusCode)
	}
}
