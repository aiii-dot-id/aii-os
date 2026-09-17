//go:build windows

package supervisor

import (
	"crypto/sha256"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const containerReadExecute = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE

// .
// .
// .
func changeContainerGrant(path string, sid *windows.SID, allow bool) error {
	name16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// .
	// .
	handle, err := windows.CreateFile(name16, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("open AppContainer grant %s: %w", path, err)
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return fmt.Errorf("AppContainer grant is not a directory: %s", path)
	}
	// .
	// .
	// .
	// .
	root := path
	identity := fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	digest := sha256.Sum256([]byte(identity))
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Global\aiios.acl.%x`, digest))
	// .
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return fmt.Errorf("AppContainer ACL mutex: %w", err)
	}
	defer windows.CloseHandle(mutex)
	status, err := windows.WaitForSingleObject(mutex, 10000)
	if err != nil || (status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED) {
		return fmt.Errorf("AppContainer ACL mutex wait: status %d: %v", status, err)
	}
	defer windows.ReleaseMutex(mutex)
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read AppContainer grant ACL: %w", err)
	}
	old, _, err := sd.DACL()
	if err != nil || old == nil {
		return fmt.Errorf("AppContainer grant requires a DACL: %s (%v)", root, err)
	}
	entry := windows.EXPLICIT_ACCESS{
		AccessMode: windows.REVOKE_ACCESS,
		Trustee:    windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN, TrusteeValue: windows.TrusteeValueFromSID(sid)},
	}
	// .
	dacl, err := setEntriesInAcl([]windows.EXPLICIT_ACCESS{entry}, old)
	if err != nil {
		return fmt.Errorf("revoke AppContainer grant: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(dacl)))
	if allow {
		entry.AccessMode = windows.GRANT_ACCESS
		entry.AccessPermissions = containerReadExecute
		entry.Inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
		updated, err := setEntriesInAcl([]windows.EXPLICIT_ACCESS{entry}, dacl)
		if err != nil {
			return fmt.Errorf("construct AppContainer grant: %w", err)
		}
		defer windows.LocalFree(windows.Handle(unsafe.Pointer(updated)))
		dacl = updated
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("write AppContainer grant ACL: %w", err)
	}
	readback, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read back AppContainer grant: %w", err)
	}
	actual, _, err := readback.DACL()
	if err != nil {
		return err
	}
	found, exact, err := containerGrant(actual, sid)
	if err != nil {
		return err
	}
	if (allow && !exact) || (!allow && found) {
		return fmt.Errorf("AppContainer grant readback mismatch: allowed=%t found=%t exact=%t", allow, found, exact)
	}
	return nil
}

// .
// .
func containerGrant(acl *windows.ACL, sid *windows.SID) (found, exact bool, err error) {
	if acl == nil {
		return false, false, fmt.Errorf("AppContainer ACL is absent")
	}
	var info aclSizeInfo
	if r, _, e := procGetAclInformation.Call(uintptr(unsafe.Pointer(acl)), uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), aclSizeInformation); r == 0 {
		return false, false, fmt.Errorf("inspect AppContainer ACL: %w", e)
	}
	count := 0
	for i := uint32(0); i < info.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return false, false, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if !(*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			continue
		}
		count++
		found = true
		exact = ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && ace.Mask == containerReadExecute &&
			ace.Header.AceFlags == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE
	}
	return found, exact && count == 1, nil
}
