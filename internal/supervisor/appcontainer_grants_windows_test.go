//go:build windows

package supervisor

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func testChildProfile(t *testing.T, scope string) *windows.SID {
	t.Helper()
	name, sid, err := createChildProfile(scope)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := deleteChildProfile(name); err != nil {
			t.Error(err)
		}
	})
	return sid
}

func readContainerGrant(t *testing.T, path string, sid *windows.SID) (bool, bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	found, exact, err := containerGrant(dacl, sid)
	if err != nil {
		t.Fatal(err)
	}
	return found, exact
}

func TestAppContainerChildProfilesNeverReuseAnOldSID(t *testing.T) {
	a := testChildProfile(t, "same.plugin.and.identity")
	b := testChildProfile(t, "same.plugin.and.identity")
	if a.Equals(b) {
		t.Fatal("new child inherited an older child's profile")
	}
}

func TestAppContainerConcurrentGrantsRetireIndependently(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	a, b := testChildProfile(t, "first"), testChildProfile(t, "second")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, sid := range []*windows.SID{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- changeContainerGrant(dir, sid, true) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, sid := range []*windows.SID{a, b} {
		if _, exact := readContainerGrant(t, dir, sid); !exact {
			t.Fatal("concurrent grant was lost or broader than read/execute")
		}
	}
	if found, _ := readContainerGrant(t, filepath.Join(dir, "existing.txt"), a); !found {
		t.Fatal("read grant did not reach existing file")
	}
	if err := changeContainerGrant(dir, a, false); err != nil {
		t.Fatal(err)
	}
	if found, _ := readContainerGrant(t, dir, a); found {
		t.Fatal("retired SID retained access")
	}
	if found, _ := readContainerGrant(t, filepath.Join(dir, "existing.txt"), a); found {
		t.Fatal("retired SID retained inherited file access")
	}
	if _, exact := readContainerGrant(t, dir, b); !exact {
		t.Fatal("retiring one child erased another's grant")
	}
	if err := changeContainerGrant(dir, b, false); err != nil {
		t.Fatal(err)
	}
	if found, _ := readContainerGrant(t, dir, b); found {
		t.Fatal("last retired SID retained access")
	}
}

func TestAppContainerGrantChecksMaskAndInheritance(t *testing.T) {
	dir := t.TempDir()
	sid := testChildProfile(t, "mask.probe")
	if err := changeContainerGrant(dir, sid, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := changeContainerGrant(dir, sid, false); err != nil {
			t.Error(err)
		}
	})
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	// .
	for i := uint32(0); ; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			t.Fatal("our SID's ACE not found")
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			continue
		}
		oldMask, oldFlags := ace.Mask, ace.Header.AceFlags
		ace.Mask = oldMask | windows.FILE_WRITE_DATA
		_, exact, err := containerGrant(acl, sid)
		ace.Mask = oldMask
		if err != nil || exact {
			t.Fatalf("write permission accepted: exact=%t err=%v", exact, err)
		}
		ace.Header.AceFlags = 0
		_, exact, err = containerGrant(acl, sid)
		ace.Header.AceFlags = oldFlags
		if err != nil || exact {
			t.Fatalf("missing inheritance accepted: exact=%t err=%v", exact, err)
		}
		break
	}
}
