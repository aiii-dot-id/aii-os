//go:build windows

package supervisor

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createNamedProfile makes a profile named name, as a crashed spawn leaves
// one: no process ever ran in it.
func createNamedProfile(t *testing.T, name string) {
	t.Helper()
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	var sid *windows.SID
	r, _, _ := procCreateAppContainerProfile.Call(uintptr(unsafe.Pointer(name16)), uintptr(unsafe.Pointer(name16)), uintptr(unsafe.Pointer(name16)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if uint32(r) != 0 {
		t.Fatalf("create AppContainer %s: HRESULT 0x%08x", name, uint32(r))
	}
	windows.FreeSid(sid)
}

// monikers are the account's profiles' names, as Windows records them.
func monikers(t *testing.T) []string {
	t.Helper()
	var out []string
	if _, err := deleteProfiles(func(m string) bool { out = append(out, m); return false }); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOnlyOurContainerProfilesAreSwept(t *testing.T) {
	var nonce [6]byte
	_, _ = rand.Read(nonce[:])
	token := hex.EncodeToString(nonce[:])
	ours, theirs := profilePrefix+"sweeptest"+token, "other.aiios.sweeptest"+token
	createNamedProfile(t, ours)
	createNamedProfile(t, theirs)
	t.Cleanup(func() { _ = deleteChildProfile(theirs); _ = deleteChildProfile(ours) })
	if got := monikers(t); !slices.Contains(got, ours) || !slices.Contains(got, theirs) {
		t.Fatalf("rig: the two profiles are not recorded: %v", got)
	}
	deleted, err := deleteProfiles(func(m string) bool { return ourProfile(m) && strings.HasSuffix(m, token) })
	if err != nil {
		t.Fatalf("the sweep: %v", err)
	}
	if !slices.Equal(deleted, []string{ours}) {
		t.Fatalf("the sweep deleted %v, want %s alone", deleted, ours)
	}
	got := monikers(t)
	if slices.Contains(got, ours) || !slices.Contains(got, theirs) {
		t.Fatalf("after the sweep the profiles are %v: ours must be gone, the other kept", got)
	}
	for m, want := range map[string]bool{"aiios.abc": true, "aiios": false, "other.aiios.x": false, "aiiosx": false, "AIIOS.x": false} {
		if ourProfile(m) != want {
			t.Errorf("ourProfile(%q) = %v, want %v", m, !want, want)
		}
	}
}
