//go:build windows

package plugstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

func CapabilityName(e Entry) string { return "aiios.store." + e.Kind + "." + e.ID }

func CapabilitySID(e Entry) (*windows.SID, error) { return fileperm.CapabilitySID(CapabilityName(e)) }

const readExecute = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE

const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

func storeTrustees() ([]*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	out := []*windows.SID{user.User.Sid}
	for _, known := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid} {
		sid, err := windows.CreateWellKnownSid(known)
		if err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	return out, nil
}

func access(sid *windows.SID, mask windows.ACCESS_MASK, inherit uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{AccessPermissions: mask, AccessMode: windows.GRANT_ACCESS, Inheritance: inherit,
		Trustee: windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_UNKNOWN, TrusteeValue: windows.TrusteeValueFromSID(sid)}}
}

func setProtected(path string, caps []*windows.SID, inherit uint32) error {
	trustees, err := storeTrustees()
	if err != nil {
		return err
	}
	var entries []windows.EXPLICIT_ACCESS
	for _, t := range trustees {
		entries = append(entries, access(t, fileAllAccess, inherit))
	}
	for _, c := range caps {
		entries = append(entries, access(c, readExecute, inherit))
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func Capabilities(path string) ([]*windows.SID, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return nil, err
	}
	var out []*windows.SID
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return nil, err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if strings.HasPrefix(sid.String(), "S-1-15-3-") {
			c, err := sid.Copy()
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func GrantRootAt(dir, key string) error {
	sid, err := CapabilitySID(Entry{Kind: DirRoots, ID: key})
	if err != nil {
		return err
	}
	if err := setProtected(dir, []*windows.SID{sid}, windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT); err != nil {
		return fmt.Errorf("grant root %s its capability: %w", key, err)
	}
	return nil
}

func GrantView(view, key string) error {
	sid, err := CapabilitySID(Entry{Kind: DirViews, ID: key})
	if err != nil {
		return err
	}
	return filepath.WalkDir(view, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		grantingFile(path)
		var gerr error
		switch {
		case d.IsDir():
			gerr = setProtected(path, []*windows.SID{sid}, windows.NO_INHERITANCE)
		case !d.Type().IsRegular(), strings.Contains(d.Name(), LinkSuffix):
			return nil
		default:
			gerr = addCapability(path, sid)
		}
		if errors.Is(gerr, fs.ErrNotExist) {
			return nil
		}
		return gerr
	})
}

var grantingFile = func(string) {}

func GrantViewFile(blob, key string) error {
	sid, err := CapabilitySID(Entry{Kind: DirViews, ID: key})
	if err != nil {
		return err
	}
	return addCapability(blob, sid)
}

func RevokeView(view, key string) error {
	sid, err := CapabilitySID(Entry{Kind: DirViews, ID: key})
	if err != nil {
		return err
	}
	return filepath.WalkDir(view, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return removeCapability(path, sid)
	})
}

func CopyCapabilities(from, to string) error {
	caps, err := Capabilities(from)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return setProtected(to, caps, windows.NO_INHERITANCE)
}

func addCapability(path string, sid *windows.SID) error {
	caps, err := Capabilities(path)
	if err != nil {
		return err
	}
	for _, c := range caps {
		if windows.EqualSid(c, sid) {
			return ensureProtected(path, caps)
		}
	}
	return setProtected(path, append(caps, sid), windows.NO_INHERITANCE)
}

func removeCapability(path string, sid *windows.SID) error {
	caps, err := Capabilities(path)
	if err != nil {
		return err
	}
	keep := caps[:0]
	for _, c := range caps {
		if !windows.EqualSid(c, sid) {
			keep = append(keep, c)
		}
	}
	return setProtected(path, keep, windows.NO_INHERITANCE)
}

func ensureProtected(path string, caps []*windows.SID) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err == nil && control&windows.SE_DACL_PROTECTED != 0 {
		return nil
	}
	return setProtected(path, caps, windows.NO_INHERITANCE)
}
