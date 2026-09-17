//go:build windows

package supervisor

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
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

	procCreateAppContainerProfile = modUserenv.NewProc("CreateAppContainerProfile")
	procDeleteAppContainerProfile = modUserenv.NewProc("DeleteAppContainerProfile")
	procGetAppContainerFolderPath = modUserenv.NewProc("GetAppContainerFolderPath")
	procSetEntriesInAclW          = modAdvapi32.NewProc("SetEntriesInAclW")
	procGetAclInformation         = modAdvapi32.NewProc("GetAclInformation")
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
func createChildProfile(scope string) (string, *windows.SID, error) {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(append([]byte(scope+"\x00"), nonce[:]...))
	name := "aiios." + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:]))
	name16, _ := windows.UTF16PtrFromString(name)
	desc16, _ := windows.UTF16PtrFromString("AII OS contained plugin child")
	var sid *windows.SID
	r, _, _ := procCreateAppContainerProfile.Call(uintptr(unsafe.Pointer(name16)), uintptr(unsafe.Pointer(name16)), uintptr(unsafe.Pointer(desc16)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if uint32(r) != 0 {
		return "", nil, fmt.Errorf("create AppContainer: HRESULT 0x%08x", uint32(r))
	}
	defer windows.FreeSid(sid)
	copied, err := sid.Copy()
	if err != nil {
		return "", nil, errors.Join(err, deleteChildProfile(name))
	}
	return name, copied, nil
}

func deleteChildProfile(name string) error {
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	r, _, _ := procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(name16)))
	if uint32(r) != 0 {
		return fmt.Errorf("delete AppContainer %s: HRESULT 0x%08x", name, uint32(r))
	}
	return nil
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
type launched struct {
	stdin          io.WriteCloser
	stdout, stderr io.ReadCloser
	contained      func() error
	containment    Containment
}

// .
// .
func launchContained(cmd *exec.Cmd, ac *AppContainer, rlimitASBytes uint64) (result *launched, launchErr error) {
	if ac == nil || ac.Profile == "" {
		return nil, errors.New("AppContainer: no profile named")
	}
	name, sid, err := createChildProfile(ac.Profile)
	if err != nil {
		return nil, err
	}
	var granted []string
	releaseGrants := func() error {
		var errs []error
		for _, dir := range granted {
			if err := changeContainerGrant(dir, sid, false); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) == 0 {
			errs = append(errs, deleteChildProfile(name))
		}
		return errors.Join(errs...)
	}
	defer func() {
		if result == nil {
			launchErr = errors.Join(launchErr, releaseGrants())
		}
	}()
	for _, dir := range ac.GrantRead {
		// .
		granted = append(granted, dir)
		if err := changeContainerGrant(dir, sid, true); err != nil {
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
	cleanup := func() error {
		// .
		err := terminateAndWaitJob(job)
		err = errors.Join(err, windows.CloseHandle(job))
		if err != nil {
			return err
		}
		return releaseGrants()
	}
	return &launched{
		stdin: stdinW, stdout: stdoutR, stderr: stderrR, contained: onceCleanup(cleanup),
		containment: Containment{NetworkDenied: true, FilesystemRestricted: true, AppContainerSID: sid.String(), Description: "contained (AppContainer " + sid.String() + ", no requested capabilities: no network, reads granted runtime/models and Windows AppContainer system resources, writes its container folder; " +
			"one job object assigned before its first instruction: dies with the supervisor, no breakaway, UI-restricted" + envelope + ")"},
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

func onceCleanup(f func() error) func() error {
	var once sync.Once
	var err error
	return func() error { once.Do(func() { err = f() }); return err }
}
