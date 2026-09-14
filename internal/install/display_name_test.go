package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUIName(t *testing.T, slotDir, content string) {
	t.Helper()
	dir := filepath.Join(slotDir, "data", "ui")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "name"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// .
// .
// .
func TestDisplayNameOverridesTheBirthName(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir, `{"seq":1,"type":"ring0.genesis","ring":0,"payload":{"name":"Willow"}}`)
	if got := Name(dir); got != "Willow" {
		t.Fatalf("with no display name, Name = %q, want the birth name %q", got, "Willow")
	}

	writeUIName(t, dir, "Willow of the Long Watch\n")
	if got := Name(dir); got != "Willow of the Long Watch" {
		t.Fatalf("Name = %q, want the display name", got)
	}

	// .
	if err := os.Remove(filepath.Join(dir, "data", "ui", "name")); err != nil {
		t.Fatal(err)
	}
	if got := Name(dir); got != "Willow" {
		t.Fatalf("after removing the override, Name = %q, want %q", got, "Willow")
	}
}

// .
// .
// .
func TestRefusedDisplayNamesFallBackToBirth(t *testing.T) {
	cases := map[string]string{
		"empty":          "\n",
		"blank":          "   \n",
		"control rune":   "Wr\x07en\n",
		"bidi override":  "Willow\u202e\n",
		"too many runes": strings.Repeat("x", 65) + "\n",
		"oversize file":  strings.Repeat("y", 300),
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			root := t.TempDir()
			dir, err := Create(root, 0)
			if err != nil {
				t.Fatal(err)
			}
			writeLedger(t, dir, `{"seq":1,"type":"ring0.genesis","ring":0,"payload":{"name":"Reed"}}`)
			writeUIName(t, dir, content)
			if got := Name(dir); got != "Reed" {
				t.Fatalf("a refused display name (%s) was shown as %q; want the birth name %q", label, got, "Reed")
			}
		})
	}
}

// .
func TestDisplayNameTakesTheFirstLineOnly(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeLedger(t, dir, `{"seq":1,"type":"ring0.genesis","ring":0,"payload":{"name":"Reed"}}`)
	writeUIName(t, dir, "Aster\nignored second line\n")
	if got := Name(dir); got != "Aster" {
		t.Fatalf("Name = %q, want %q", got, "Aster")
	}
}
