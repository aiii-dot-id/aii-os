package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhysicalShortLaunchPathKeepsImageAndDirectory(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"simple", "a directory with spaces", "unicode-é", strings.Repeat("a", 64)} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "worker.exe")
		if err := os.WriteFile(path, []byte("bound image"), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := physicalShortLaunchPath(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]string{{path, got}, {dir, filepath.Dir(got)}} {
			a, e := os.Stat(pair[0])
			if e != nil {
				t.Fatal(e)
			}
			b, e := os.Stat(pair[1])
			if e != nil {
				t.Fatal(e)
			}
			if !os.SameFile(a, b) {
				t.Fatal("physical identity changed")
			}
		}
	}
}

func TestPhysicalShortLaunchPathPreservesUnavailableName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "worker.exe")
	got, err := physicalShortLaunchPath(path)
	if err != nil || got != path {
		t.Fatalf("unavailable alias must not invent a path: %q %v", got, err)
	}
	if _, err := physicalShortLaunchPath("invalid\x00path"); err == nil {
		t.Fatal("NUL accepted")
	}
}
