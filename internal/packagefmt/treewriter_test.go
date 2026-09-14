package packagefmt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestWriteTreeRoundTripsThroughExtractTree(t *testing.T) {
	src := t.TempDir()
	long := strings.Repeat("directory-name-", 8) + "deep"
	files := map[string][]byte{
		"bin/carrier":                []byte("#!carrier\n"),
		"lib/libtorch.so":            bytes.Repeat([]byte("t"), 70000),
		long + "/" + long + "/x.txt": []byte("far"),
		"python/pyvenv.cfg":          []byte("home = here\n"),
	}
	for rel, content := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if rel == "bin/carrier" {
			mode = 0o755
		}
		if err := os.WriteFile(p, content, mode); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	inventory, sha, err := WriteTree(&archive, src, "cp1", TreeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sha, "sha256:") || len(inventory) == 0 {
		t.Fatalf("writer report: %s %d", sha, len(inventory))
	}
	if _, err := ParseInventory(inventory, TreeLimits{}); err != nil {
		t.Fatalf("the writer's inventory must parse: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "root.partial")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	rep, err := ExtractTree(bytes.NewReader(archive.Bytes()), InventoryDigest(inventory), TreeLimits{}, dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if rep.Files != 4 || !bytes.Equal(rep.InventoryRaw, inventory) {
		t.Fatalf("report %+v", rep)
	}
	for rel, content := range files {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(got, content) {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	if st, _ := os.Stat(filepath.Join(dest, "bin", "carrier")); st.Mode().Perm()&0o100 == 0 {
		t.Fatal("the exec bit must survive the round trip")
	}
	// .
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "lib", "escape")); err == nil {
		if _, _, err := WriteTree(&bytes.Buffer{}, src, "cp1", TreeLimits{}); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("a symlink must be refused: %v", err)
		}
	}
	// .
	if _, _, err := WriteTree(&bytes.Buffer{}, src, "a/b", TreeLimits{}); err == nil {
		t.Fatal("a root with a slash must be refused")
	}
}
