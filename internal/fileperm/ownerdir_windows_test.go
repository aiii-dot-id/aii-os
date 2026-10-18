//go:build windows

package fileperm

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestAnOwnerOnlyDirIsClosedUnderAFolderThatGrantsEveryone(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "open")
	openFolder(t, parent)
	others := map[string]*windows.SID{
		"Users":               wellKnown(t, windows.WinBuiltinUsersSid),
		"Authenticated Users": wellKnown(t, windows.WinAuthenticatedUserSid),
		"Everyone":            wellKnown(t, windows.WinWorldSid),
	}
	trusted, err := ownerOnlyTrustees()
	if err != nil {
		t.Fatal(err)
	}

	// Control half.
	plain := filepath.Join(parent, "plain")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	plainFile := filepath.Join(plain, "ledger.jsonl")
	if err := os.WriteFile(plainFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{plain, plainFile} {
		if len(grantedAmong(readDACL(t, p), others)) == 0 {
			t.Fatalf("control: %s, made the ordinary way under the open folder, grants none of Users, Authenticated Users, Everyone — the rig opens nothing and the checks below would prove nothing", p)
		}
	}
	if ok, err := IsOwnerOnlyDir(plain); err != nil || ok {
		t.Fatalf("control: IsOwnerOnlyDir(%s) = %v, %v; want false — it read a directory open to everyone as closed", plain, ok, err)
	}

	// The slot root: protected, and granting only the owner, SYSTEM and
	// Administrators, each to everything made beneath it.
	root := filepath.Join(parent, "identity-0")
	if err := MkdirOwnerOnly(root); err != nil {
		t.Fatal(err)
	}
	d := readDACL(t, root)
	if !d.protected {
		t.Fatalf("%s: the DACL is not protected, so the open folder's grants flow into it", root)
	}
	if granted := grantedAmong(d, others); len(granted) != 0 {
		t.Fatalf("%s grants %v", root, granted)
	}
	const toAll = windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE
	for _, e := range d.entries {
		if !e.allowed {
			continue
		}
		if !slices.ContainsFunc(trusted, e.sid.Equals) {
			t.Errorf("%s grants %s, who is not the owner, SYSTEM or Administrators", root, e.sid)
		}
		if e.flags&toAll != toAll {
			t.Errorf("%s: the grant to %s is not inherited by both files and directories (flags %#x)", root, e.sid, e.flags)
		}
	}
	if !slices.ContainsFunc(d.allowedSIDs(), trusted[0].Equals) {
		t.Fatalf("%s does not grant the account that made it", root)
	}
	if ok, err := IsOwnerOnlyDir(root); err != nil || !ok {
		t.Fatalf("IsOwnerOnlyDir(%s) = %v, %v; want true", root, ok, err)
	}

	// What is made inside with nothing of its own — a data directory, the
	// ledger, the database, a snapshot directory — carries the slot root's
	// grants and no others.
	data := filepath.Join(root, "data")
	backups := filepath.Join(data, "backups")
	if err := os.MkdirAll(backups, 0o700); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(data, "ledger.jsonl")
	db := filepath.Join(data, "aii.db")
	for _, f := range []string{ledger, db} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{data, backups, ledger, db} {
		d := readDACL(t, p)
		if granted := grantedAmong(d, others); len(granted) != 0 {
			t.Errorf("%s grants %v", p, granted)
		}
		for _, e := range d.entries {
			if e.flags&windows.INHERITED_ACE == 0 {
				t.Errorf("%s carries an entry for %s that the slot root did not hand down", p, e.sid)
			}
			if e.allowed && !slices.ContainsFunc(trusted, e.sid.Equals) {
				t.Errorf("%s grants %s, who is not the owner, SYSTEM or Administrators", p, e.sid)
			}
		}
		if !slices.ContainsFunc(d.allowedSIDs(), trusted[0].Equals) {
			t.Errorf("%s does not grant the account that made it", p)
		}
	}
}

type daclEntry struct {
	sid     *windows.SID
	allowed bool
	flags   uint8
}

type daclView struct {
	protected bool
	entries   []daclEntry
}

func (d daclView) allowedSIDs() []*windows.SID {
	var out []*windows.SID
	for _, e := range d.entries {
		if e.allowed {
			out = append(out, e.sid)
		}
	}
	return out
}

// readDACL is the test's own reading of path's DACL, independent of
// IsOwnerOnlyDir, which it also checks.
func readDACL(t *testing.T, path string) daclView {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read the DACL of %s: %v", path, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatalf("%s has no DACL: open to everyone", path)
	}
	view := daclView{protected: control&windows.SE_DACL_PROTECTED != 0}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		sid, err := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Copy()
		if err != nil {
			t.Fatal(err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE, windows.ACCESS_DENIED_ACE_TYPE:
		default:
			t.Fatalf("%s carries an entry of type %d that this test does not read", path, ace.Header.AceType)
		}
		view.entries = append(view.entries, daclEntry{
			sid:     sid,
			allowed: ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE,
			flags:   ace.Header.AceFlags,
		})
	}
	return view
}

// grantedAmong names those in who that d grants anything to.
func grantedAmong(d daclView, who map[string]*windows.SID) []string {
	var out []string
	for name, sid := range who {
		if slices.ContainsFunc(d.allowedSIDs(), sid.Equals) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// openFolder makes dir with what the triage measured on a drive root: the
// user in full, Users read, Authenticated Users modify, Everyone read,
// all inherited by what is made inside. Protected, so the test does not
// depend on what its temp directory hands down.
func openFolder(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	trusted, err := ownerOnlyTrustees()
	if err != nil {
		t.Fatal(err)
	}
	grant := func(sid *windows.SID, mask windows.ACCESS_MASK) windows.EXPLICIT_ACCESS {
		return windows.EXPLICIT_ACCESS{
			AccessPermissions: mask,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		}
	}
	read := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE)
	modify := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | windows.FILE_GENERIC_EXECUTE | windows.DELETE)
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		grant(trusted[0], fileAllAccess),
		grant(wellKnown(t, windows.WinBuiltinUsersSid), read),
		grant(wellKnown(t, windows.WinAuthenticatedUserSid), modify),
		grant(wellKnown(t, windows.WinWorldSid), read),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The directory is new and empty, so the walk a DACL write makes over
	// its descendants has nothing to visit.
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		t.Fatalf("open %s to every account: %v", dir, err)
	}
}

func wellKnown(t *testing.T, typ windows.WELL_KNOWN_SID_TYPE) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(typ)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}
