package packagefmt

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

func TestAnInventoryIsReadExactly(t *testing.T) {
	inv := Inventory{Files: []InventoryEntry{{Path: "bin/x", Size: 1, SHA256: digestOf([]byte("a")), Mode: "exec"}}, InstalledBytes: 1}
	marshalled, err := json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	good := string(marshalled)
	for name, raw := range map[string]string{
		"as it is written":             good,
		"a newline after it":           good + "\n",
		"white space around it":        " \t" + good + "\r\n ",
		"white space within it":        strings.Replace(good, `,"installed_bytes"`, ",\n  \"installed_bytes\"", 1),
		"its members in another order": `{"installed_bytes":1,"files":[{"mode":"exec","sha256":"` + inv.Files[0].SHA256 + `","size":1,"path":"bin/x"}]}`,
	} {
		got, err := ParseInventory([]byte(raw), TreeLimits{})
		if err != nil {
			t.Errorf("%s: refused, and it is the same inventory: %v", name, err)
			continue
		}
		if len(got.Files) != 1 || got.Files[0] != inv.Files[0] || got.InstalledBytes != inv.InstalledBytes {
			t.Errorf("%s: read as %+v", name, got)
		}
	}

	const exact = "must match its field exactly and appear once"
	for name, c := range map[string]struct {
		raw   string
		after bool // content after the value, not a member's name
	}{
		"its closer again":                  {good + "}", true},
		"another closer after it":           {good + "]", true},
		"a second inventory after it":       {good + " " + good, true},
		"text after it":                     {good + " trailing", true},
		"files named in another case":       {strings.Replace(good, `"files"`, `"Files"`, 1), false},
		"an entry's path in another case":   {strings.Replace(good, `"path"`, `"Path"`, 1), false},
		"installed_bytes given twice":       {strings.Replace(good, `"installed_bytes":1`, `"installed_bytes":1,"installed_bytes":1`, 1), false},
		"an entry's size given twice":       {strings.Replace(good, `"size":1`, `"size":1,"size":1`, 1), false},
		"an entry's size in both spellings": {strings.Replace(good, `"size":1`, `"Size":2,"size":1`, 1), false},
	} {
		if c.raw == good {
			t.Fatalf("%s: the mutation changed nothing", name)
		}
		_, err := ParseInventory([]byte(c.raw), TreeLimits{})
		switch {
		case err == nil:
			t.Errorf("%s is read as the inventory: %s", name, c.raw)
		case c.after && !errors.Is(err, canonicaljson.ErrTrailingContent):
			t.Errorf("%s is refused, but not as content after the value: %v", name, err)
		case !c.after && !strings.Contains(err.Error(), exact):
			t.Errorf("%s is refused, but not for its name: %v", name, err)
		}
	}
}

// Through the archive: the pinned digest is of the bytes the archive
// carries, so a publisher could pin an inventory with a second value after
// it and the tree was installed against the first. It is refused as the
// inventory is read, before a file of the tree is written.
func TestExtractTreeRefusesAnInventoryWithContentAfterIt(t *testing.T) {
	for name, rewrite := range map[string]func(raw []byte) []byte{
		"a closer after it": func(raw []byte) []byte { return append(raw, '}') },
		"a second value":    func(raw []byte) []byte { return append(append(raw, ' '), raw...) },
		"a name in another case": func(raw []byte) []byte {
			return bytes.Replace(raw, []byte(`"installed_bytes"`), []byte(`"Installed_bytes"`), 1)
		},
	} {
		archive, pin := buildTreeRaw(t, "cp1", treeFixture, nil, rewrite)
		dest := filepath.Join(t.TempDir(), "root.partial")
		if err := os.Mkdir(dest, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := ExtractTree(bytes.NewReader(archive), pin, TreeLimits{}, dest)
		var refusal *Error
		if err == nil || !errors.As(err, &refusal) || refusal.Reason != ReasonManifestInvalid || !strings.Contains(err.Error(), "inventory") {
			t.Errorf("%s: the tree is not refused for its inventory: %v", name, err)
			continue
		}
		if left, _ := os.ReadDir(dest); len(left) != 0 {
			t.Errorf("%s: %d entries were written before the refusal", name, len(left))
		}
	}
	// The same archive with the inventory as it is marshalled extracts.
	archive, pin := buildTreeRaw(t, "cp1", treeFixture, nil, func(raw []byte) []byte { return append(raw, '\n') })
	dest := filepath.Join(t.TempDir(), "root.partial")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTree(bytes.NewReader(archive), pin, TreeLimits{}, dest); err != nil {
		t.Fatalf("an inventory with a newline at its end is the same inventory, and its tree is refused: %v", err)
	}
}
