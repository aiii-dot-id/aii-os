//go:build windows

package supervisor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

var (
	modUserenv  = windows.NewLazySystemDLL("userenv.dll")
	modAdvapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procCreateAppContainerProfile                 = modUserenv.NewProc("CreateAppContainerProfile")
	procDeriveAppContainerSidFromAppContainerName = modUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	procGetAppContainerFolderPath                 = modUserenv.NewProc("GetAppContainerFolderPath")
	procSetEntriesInAclW                          = modAdvapi32.NewProc("SetEntriesInAclW")
	procGetAclInformation                         = modAdvapi32.NewProc("GetAclInformation")
)

// .
const (
	procThreadAttributeSecurityCapabilities = 0x00020009
	procThreadAttributeHandleList           = 0x00020002
	hresultAlreadyExists                    = 0x800700B7
	tokenIsAppContainer                     = 29
	aclSizeInformation                      = 2
)

// .
type aclSizeInfo struct {
	AceCount      uint32
	AclBytesInUse uint32
	AclBytesFree  uint32
}

// .
func setEntriesInAcl(entries []windows.EXPLICIT_ACCESS, old *windows.ACL) (*windows.ACL, error) {
	var out *windows.ACL
	r, _, _ := procSetEntriesInAclW.Call(uintptr(len(entries)), uintptr(unsafe.Pointer(&entries[0])), uintptr(unsafe.Pointer(old)), uintptr(unsafe.Pointer(&out)))
	if r != 0 {
		return nil, syscall.Errno(r)
	}
	return out, nil
}

// .
// .
type securityCapabilities struct {
	AppContainerSid *windows.SID
	Capabilities    *windows.SIDAndAttributes
	CapabilityCount uint32
	Reserved        uint32
}

// .
// .
// .
func ensureProfile(name string) (*windows.SID, error) {
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	desc16, _ := windows.UTF16PtrFromString("AII OS plugin (contained native)")
	var sid *windows.SID
	r, _, _ := procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(name16)), uintptr(unsafe.Pointer(name16)), uintptr(unsafe.Pointer(desc16)),
		0, 0, uintptr(unsafe.Pointer(&sid)))
	switch uint32(r) {
	case 0:
	case hresultAlreadyExists:
		r, _, _ = procDeriveAppContainerSidFromAppContainerName.Call(uintptr(unsafe.Pointer(name16)), uintptr(unsafe.Pointer(&sid)))
		if uint32(r) != 0 {
			return nil, fmt.Errorf("derive AppContainer SID for %q: HRESULT 0x%08x", name, uint32(r))
		}
	default:
		return nil, fmt.Errorf("create AppContainer profile %q: HRESULT 0x%08x", name, uint32(r))
	}
	defer windows.FreeSid(sid)
	return sid.Copy()
}

// .
// .
func containerFolder(sid *windows.SID) (string, error) {
	s, err := windows.UTF16PtrFromString(sid.String())
	if err != nil {
		return "", err
	}
	var path *uint16
	r, _, _ := procGetAppContainerFolderPath.Call(uintptr(unsafe.Pointer(s)), uintptr(unsafe.Pointer(&path)))
	if uint32(r) != 0 || path == nil {
		return "", fmt.Errorf("AppContainer folder: HRESULT 0x%08x", uint32(r))
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(path))
	return windows.UTF16PtrToString(path), nil
}

// .
// .
// .
func grantReadExecute(path string, sid *windows.SID) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the ACL of %s: %w", path, err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read the DACL of %s: %w", path, err)
	}
	if old != nil && aclGrants(old, sid) {
		return nil
	}
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ | windows.GENERIC_EXECUTE,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}
	dacl, err := setEntriesInAcl(entries, old)
	if err != nil {
		return fmt.Errorf("extend the DACL of %s: %w", path, err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(dacl)))
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("write the DACL of %s: %w", path, err)
	}
	return nil
}

// .
// .
func aclGrants(acl *windows.ACL, sid *windows.SID) bool {
	var info aclSizeInfo
	if r, _, _ := procGetAclInformation.Call(uintptr(unsafe.Pointer(acl)), uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), aclSizeInformation); r == 0 {
		return false
	}
	for i := uint32(0); i < info.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return false
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if (*windows.SID)(unsafe.Pointer(&ace.SidStart)).Equals(sid) {
			return true
		}
	}
	return false
}

// .
// .
// .
type launched struct {
	stdin          io.WriteCloser
	stdout, stderr io.ReadCloser
	contained      func()
	containment    string
}

// .
// .
func launchContained(cmd *exec.Cmd, ac *AppContainer, rlimitASBytes uint64) (*launched, error) {
	if ac == nil || ac.Profile == "" {
		return nil, errors.New("AppContainer: no profile named")
	}
	sid, err := ensureProfile(ac.Profile)
	if err != nil {
		return nil, err
	}
	for _, dir := range ac.GrantRead {
		if err := grantReadExecute(dir, sid); err != nil {
			return nil, err
		}
	}
	folder, err := containerFolder(sid)
	if err != nil {
		return nil, err
	}
	temp := filepath.Join(folder, "Temp")
	if err := os.MkdirAll(temp, 0o700); err != nil {
		return nil, fmt.Errorf("AppContainer temp: %w", err)
	}
	env := withHostFacts(cmd.Env, temp)

	// .
	// .
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		return nil, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return nil, err
	}
	childEnds := []*os.File{stdinR, stdoutW, stderrW}
	hostEnds := []*os.File{stdinW, stdoutR, stderrR}
	fail := func(err error) (*launched, error) {
		for _, f := range childEnds {
			f.Close()
		}
		for _, f := range hostEnds {
			f.Close()
		}
		return nil, err
	}
	var handles []windows.Handle
	for _, f := range childEnds {
		h := windows.Handle(f.Fd())
		if err := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return fail(fmt.Errorf("inheritable stdio: %w", err))
		}
		handles = append(handles, h)
	}
	if cmd.SysProcAttr != nil {
		for _, h := range cmd.SysProcAttr.AdditionalInheritedHandles {
			handles = append(handles, windows.Handle(h))
		}
	}

	sc := securityCapabilities{AppContainerSid: sid}
	al, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return fail(fmt.Errorf("attribute list: %w", err))
	}
	defer al.Delete()
	if err := al.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(&sc), unsafe.Sizeof(sc)); err != nil {
		return fail(fmt.Errorf("security capabilities attribute: %w", err))
	}
	if err := al.Update(procThreadAttributeHandleList, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return fail(fmt.Errorf("handle list attribute: %w", err))
	}

	si := windows.StartupInfoEx{ProcThreadAttributeList: al.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = handles[0], handles[1], handles[2]

	argv := cmd.Args
	if len(argv) == 0 {
		argv = []string{cmd.Path}
	}
	app16, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return fail(err)
	}
	cmdline16, err := windows.UTF16PtrFromString(makeCommandLine(argv))
	if err != nil {
		return fail(err)
	}
	dir := cmd.Dir
	if dir == "" {
		dir = filepath.Dir(cmd.Path)
	}
	dir16, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return fail(err)
	}
	envBlock := environmentBlock(env)

	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcess(app16, cmdline16, nil, nil, true, flags, &envBlock[0], dir16, &si.StartupInfo, &pi); err != nil {
		return fail(fmt.Errorf("create the contained process: %w", err))
	}
	// .
	for _, f := range childEnds {
		f.Close()
	}
	// .
	// .
	// .
	job, envelope, err := newJobObject(rlimitASBytes)
	if err == nil {
		err = windows.AssignProcessToJobObject(job, pi.Process)
		if err != nil {
			_ = windows.CloseHandle(job)
			err = fmt.Errorf("assign the contained process to its job: %w", err)
		}
	}
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(pi.Process)
		for _, f := range hostEnds {
			f.Close()
		}
		return nil, err
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		_ = windows.CloseHandle(job)
		_ = windows.CloseHandle(pi.Thread)
		_ = windows.CloseHandle(pi.Process)
		for _, f := range hostEnds {
			f.Close()
		}
		return nil, fmt.Errorf("resume the contained process: %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)
	_ = windows.CloseHandle(pi.Process)
	proc, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		_ = windows.CloseHandle(job)
		for _, f := range hostEnds {
			f.Close()
		}
		return nil, fmt.Errorf("adopt the contained process: %w", err)
	}
	cmd.Process = proc
	cleanup := func() { _ = windows.CloseHandle(job) }
	return &launched{
		stdin: stdinW, stdout: stdoutR, stderr: stderrR, contained: onceFunc(cleanup),
		containment: "contained (AppContainer " + sid.String() + ", no capabilities: no network, no devices, reads only its runtime and models, writes only its container folder; " +
			"one job object assigned before its first instruction: dies with the supervisor, no breakaway, UI-restricted" + envelope + ")",
	}, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func withHostFacts(env []string, temp string) []string {
	out := make([]string, 0, len(env)+5)
	seen := map[string]bool{}
	for _, kv := range env {
		k := strings.ToUpper(kv[:strings.IndexByte(kv+"=", '=')])
		seen[k] = true
		out = append(out, kv)
	}
	for _, kv := range []string{"TEMP=" + temp, "TMP=" + temp, "SystemRoot=" + os.Getenv("SystemRoot"), "SystemDrive=" + os.Getenv("SystemDrive"),
		"LOCALAPPDATA=" + os.Getenv("LOCALAPPDATA")} {
		k := strings.ToUpper(kv[:strings.IndexByte(kv, '=')])
		if seen[k] || strings.HasSuffix(kv, "=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// .
// .
func environmentBlock(env []string) []uint16 {
	sorted := append([]string{}, env...)
	sort.Slice(sorted, func(i, j int) bool { return strings.ToUpper(sorted[i]) < strings.ToUpper(sorted[j]) })
	var block []uint16
	for _, kv := range sorted {
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			continue
		}
		block = append(block, u...)
	}
	return append(block, 0)
}

// .
func makeCommandLine(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = syscall.EscapeArg(a)
	}
	return strings.Join(parts, " ")
}

func onceFunc(f func()) func() {
	var once sync.Once
	return func() { once.Do(f) }
}
