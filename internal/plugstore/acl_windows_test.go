//go:build windows

package plugstore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// sddl is path's DACL as text, and whether it is protected.
func sddl(t *testing.T, path string) (string, bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	return sd.String(), control&windows.SE_DACL_PROTECTED != 0
}

// capNames is path's capability SIDs as text.
func capNames(t *testing.T, path string) []string {
	t.Helper()
	caps, err := Capabilities(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range caps {
		out = append(out, c.String())
	}
	slices.Sort(out)
	return out
}

func capOf(t *testing.T, e Entry) string {
	t.Helper()
	sid, err := CapabilitySID(e)
	if err != nil {
		t.Fatal(err)
	}
	return sid.String()
}

// inheritable says any ACE of path's DACL is inherited by what is made
// beneath it.
func inheritable(t *testing.T, path string) bool {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatal(err)
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceFlags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) != 0 {
			return true
		}
	}
	return false
}

// twoViews is a store whose one blob two views link, granted as the
// writer grants them.
func twoViews(t *testing.T) (w *Writer, blob, v1, v2 string) {
	t.Helper()
	w, err := OpenWriter(filepath.Join(t.TempDir(), "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	blob = filepath.Join(w.Dir, DirBlobs, strings.Repeat("a", 64))
	if err := os.WriteFile(blob, []byte("one model, two views"), 0o400); err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"1111111111111111", "2222222222222222"} {
		dir := filepath.Join(w.Dir, DirViews, key, "sub")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(blob, filepath.Join(dir, "m.bin")); err != nil {
			t.Fatal(err)
		}
		view := filepath.Join(w.Dir, DirViews, key)
		if err := GrantView(view, key); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			v1 = view
		} else {
			v2 = view
		}
	}
	return w, blob, v1, v2
}

// A BLOB CARRIES THE CAPABILITY OF EVERY VIEW THAT LINKS IT, ON A DACL OF
// ITS OWN (H5): protected, no ACE of it or of a view's directory
// inheritable; revoking one view leaves the other's.
func TestEachBlobCarriesEveryLinkingViewsCapabilityProtected(t *testing.T) {
	_, blob, v1, v2 := twoViews(t)
	c1, c2 := capOf(t, Entry{Kind: DirViews, ID: "1111111111111111"}), capOf(t, Entry{Kind: DirViews, ID: "2222222222222222"})
	want := []string{c1, c2}
	slices.Sort(want)
	if got := capNames(t, blob); !slices.Equal(got, want) {
		t.Fatalf("the blob's capabilities: %v, want exactly %v", got, want)
	}
	if _, protected := sddl(t, blob); !protected {
		t.Fatal("the blob's DACL is not protected")
	}
	for _, dir := range []string{v1, filepath.Join(v1, "sub"), v2} {
		if inheritable(t, dir) {
			t.Fatalf("%s carries an inheritable ACE", dir)
		}
	}
	if err := RevokeView(v2, "2222222222222222"); err != nil {
		t.Fatal(err)
	}
	if got := capNames(t, blob); !slices.Equal(got, []string{c1}) {
		t.Fatalf("after one view is revoked: %v, want %v", got, []string{c1})
	}
}

// AN INHERITABLE GRANT MADE ON THE STORE AFTERWARDS REACHES NO BLOB (H5): an
// operator, or a tool, that adds an inheritable ACE to the store's
// directory, propagated to everything beneath it, leaves each blob's DACL
// exactly as it was.
func TestAnInheritableGrantOnTheStoreLeavesEveryBlobAsItWas(t *testing.T) {
	w, blob, _, _ := twoViews(t)
	before, _ := sddl(t, blob)
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(w.Dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{access(users, windows.FILE_GENERIC_READ, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT)}, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(w.Dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	after, _ := sddl(t, blob)
	if after != before {
		t.Fatalf("an inheritable grant on the store reached a blob:\nbefore %s\nafter  %s", before, after)
	}
}

// A ROOT'S CAPABILITY IS INHERITED BY ITS WHOLE TREE: granted once on its
// directory, every file in it carries it.
func TestARootsCapabilityIsInheritedByItsTree(t *testing.T) {
	w, err := OpenWriter(filepath.Join(t.TempDir(), "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	root := filepath.Join(w.Dir, DirRoots, "3333333333333333.partial-x")
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "lib", "engine.dll")
	if err := os.WriteFile(file, []byte("code"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := GrantRootAt(root, "3333333333333333"); err != nil {
		t.Fatal(err)
	}
	if got, want := capNames(t, file), []string{capOf(t, Entry{Kind: DirRoots, ID: "3333333333333333"})}; !slices.Equal(got, want) {
		t.Fatalf("a root's file carries %v, want %v", got, want)
	}
}
