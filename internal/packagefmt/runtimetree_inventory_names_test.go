package packagefmt

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTreeRefusesATreeThatCollidesWithItsInventory(t *testing.T) {
	extract := func(t *testing.T, rels ...string) error {
		t.Helper()
		var files []treeFile
		for _, rel := range rels {
			files = append(files, treeFile{rel: rel, content: []byte("x = 1\n")})
		}
		archive, want := buildTree(t, "cp1", files, nil)
		dest := filepath.Join(t.TempDir(), "root.partial")
		if err := os.Mkdir(dest, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := ExtractTree(bytes.NewReader(archive), want, TreeLimits{}, dest)
		return err
	}
	for name, rel := range map[string]string{
		"a directory of its name":          "inventory.json/child.py",
		"a file that differs in case":      "Inventory.json",
		"a directory that differs in case": "INVENTORY.JSON/child.py",
	} {
		t.Run(name, func(t *testing.T) {
			err := extract(t, "lib/a.py", rel)
			if err == nil {
				t.Fatalf("a tree that carries %s beside its own inventory was admitted", rel)
			}
			t.Logf("refused: %v", err)
		})
	}
	// Below the top level the inventory has no sibling: the name is free
	// there in any letter case, and so is a top-level name that only
	// begins like it.
	if err := extract(t, "lib/a.py", "lib/inventory.json", "data/Inventory.json", "share/inventory.json/x.py", "inventory.jsonl", "Inventory"); err != nil {
		t.Fatalf("a tree that does not collide with its inventory is refused: %v", err)
	}
}
