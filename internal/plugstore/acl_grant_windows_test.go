//go:build windows

package plugstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A FILE THAT GOES WHILE THE GRANT WALKS PAST IT NEEDS NO GRANT, and a link
// still under its temporary name is never granted under it. Another writer
// linking a model into the same view renames its temporary link into place;
// the walk that listed the temporary name failed on it a moment later, and
// the acquisition with it (the 0.1.16 native suite on the guest).
func TestAGrantWalkPassesAnEntryThatGoesMidWalk(t *testing.T) {
	w, err := OpenWriter(filepath.Join(t.TempDir(), "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	key := "4444444444444444"
	view := filepath.Join(w.Dir, DirViews, key)
	if err := os.MkdirAll(view, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"a.bin", "b.bin", "c.bin" + LinkSuffix + "0123"} {
		if err := os.WriteFile(filepath.Join(view, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gone := filepath.Join(view, "b.bin")
	grantingFile = func(path string) {
		if path == gone {
			if err := os.Remove(path); err != nil {
				t.Errorf("rig: %v", err)
			}
		}
	}
	defer func() { grantingFile = func(string) {} }()
	if err := GrantView(view, key); err != nil {
		t.Fatalf("a grant walk past an entry that went: %v", err)
	}
	sid, err := CapabilitySID(Entry{Kind: DirViews, ID: key})
	if err != nil {
		t.Fatal(err)
	}
	has := func(path string) bool {
		caps, err := Capabilities(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range caps {
			if windows.EqualSid(c, sid) {
				return true
			}
		}
		return false
	}
	if !has(filepath.Join(view, "a.bin")) {
		t.Fatal("the file that stayed was not granted")
	}
	tmp := filepath.Join(view, "c.bin"+LinkSuffix+"0123")
	if has(tmp) {
		t.Fatal("a temporary link was granted under its passing name")
	}
	if !strings.Contains(tmp, LinkSuffix) {
		t.Fatal("rig")
	}
}
