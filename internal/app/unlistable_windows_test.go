//go:build windows

package app

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func unlistable(t *testing.T, path string) {
	t.Helper()
	withoutBackupPrivilege(t)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	inherited, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.FILE_LIST_DIRECTORY,
		AccessMode:        windows.DENY_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, inherited)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	// The rig proves itself: a directory this account can still list would
	// ask the probe about nothing.
	if _, err := os.ReadDir(path); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("rig: %s still lists for this account: %v", path, err)
	}
}

// withoutBackupPrivilege disables SeBackupPrivilege on this process's token
// if it is enabled, and enables it again when the test ends.
func withoutBackupPrivilege(t *testing.T) {
	t.Helper()
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		t.Fatal(err)
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeBackupPrivilege"), &luid); err != nil {
		tok.Close()
		t.Fatal(err)
	}
	set := func(attrs uint32) (prev windows.Tokenprivileges, err error) {
		tp := windows.Tokenprivileges{PrivilegeCount: 1, Privileges: [1]windows.LUIDAndAttributes{{Luid: luid, Attributes: attrs}}}
		var n uint32
		err = windows.AdjustTokenPrivileges(tok, false, &tp, uint32(unsafe.Sizeof(prev)), &prev, &n)
		return prev, err
	}
	prev, err := set(0)
	if err != nil {
		tok.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer tok.Close()
		// prev names the privilege only if this call changed it.
		if prev.PrivilegeCount == 1 && prev.Privileges[0].Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
			if _, err := set(windows.SE_PRIVILEGE_ENABLED); err != nil {
				t.Error(err)
			}
		}
	})
}
