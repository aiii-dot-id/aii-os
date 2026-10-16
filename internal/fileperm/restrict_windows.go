//go:build windows

package fileperm

import (
	"fmt"
	"os"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

func RestrictToOwner(f *os.File) error {

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read process user: %w", err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("build owner-only DACL: %w", err)
	}

	if err := windows.SetNamedSecurityInfo(f.Name(), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		return fmt.Errorf("apply owner-only DACL: %w", err)
	}
	return nil
}

func IsRestrictedToOwner(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil {
		return false, nil
	}

	if dacl.AceCount != 1 {
		return false, nil
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		return false, err
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if sid.Equals(owner) {
		return true, nil
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, fmt.Errorf("read process user: %w", err)
	}
	return sid.Equals(user.User.Sid), nil
}

func IsClosedToOthers(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return false, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return false, err
	}
	if dacl == nil {
		return false, nil
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, fmt.Errorf("read process user: %w", err)
	}
	trusted := []*windows.SID{user.User.Sid}
	for _, known := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid} {
		sid, err := windows.CreateWellKnownSid(known)
		if err != nil {
			return false, err
		}
		trusted = append(trusted, sid)
	}
	if !slices.ContainsFunc(trusted, owner.Equals) {
		return false, nil
	}
	for _, known := range []windows.WELL_KNOWN_SID_TYPE{windows.WinCreatorOwnerSid, windows.WinCreatorOwnerRightsSid} {
		sid, err := windows.CreateWellKnownSid(known)
		if err != nil {
			return false, err
		}
		trusted = append(trusted, sid)
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return false, err
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return false, nil
		}
		if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !slices.ContainsFunc(trusted, sid.Equals) {
			return false, nil
		}
	}
	return true, nil
}
