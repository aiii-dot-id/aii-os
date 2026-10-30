//go:build windows

package fileperm

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func CreateTempForReplacement(path string) (*os.File, error) {

	path = filepath.Clean(path)
	label, err := LabelOf(path)
	if errors.Is(err, os.ErrNotExist) || err == nil && label == "" {
		return os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	}
	if err != nil {
		return nil, fmt.Errorf("read replacement label: %w", err)
	}
	var creationLabel string
	for _, class := range []Label{LabelKept, LabelReadable, LabelIdentity} {
		explicitFileLabel := strings.Replace(labelSDDL[class], ";OICI;", ";;", 1)
		if label == labelSDDL[class] || label == labelInheritedSDDL(class, false) || label == explicitFileLabel {
			creationLabel = explicitFileLabel
			break
		}
	}
	if creationLabel == "" {
		return nil, fmt.Errorf("replacement has an unsupported mandatory label: %q", label)
	}
	f, err := createTempWithLabel(path, creationLabel)
	if err != nil {
		return nil, err
	}

	if err := setLabelHandle(windows.Handle(f.Fd()), label); err != nil {
		return nil, errors.Join(fmt.Errorf("restore replacement label flags: %w", err), f.Close(), os.Remove(f.Name()))
	}
	return f, nil
}

func createTempWithLabel(path, label string) (*os.File, error) {
	sd, err := windows.SecurityDescriptorFromString(label)
	if err != nil {
		return nil, fmt.Errorf("prepare replacement label: %w", err)
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))

	for range 10000 {
		name := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-"+rand.Text())
		wide, err := windows.UTF16PtrFromString(longPath(name))
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(wide, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.WRITE_OWNER,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			&sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		runtime.KeepAlive(sd)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "create replacement", Path: name, Err: err}
		}
		return os.NewFile(uintptr(h), name), nil
	}
	return nil, &os.PathError{Op: "create replacement", Path: path, Err: os.ErrExist}
}

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
