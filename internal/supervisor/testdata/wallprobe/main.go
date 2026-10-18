//go:build windows

// wallprobe is the child the Windows wall's tests run inside the
// AppContainer. It answers, one line each on stdout, the questions the
// wall must settle: is my token an AppContainer's; can I read what was
// granted and what was not; can I write to my TEMP and not to a grant;
// can I reach the loopback and the network; does a child I spawn stay
// in the container; can I open a named pipe a service of the same user
// serves; can I have the shell start a program for me. Every answer is
// an observation, never a claim.
package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func tokenIsAppContainer() string {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return "error:" + err.Error()
	}
	defer token.Close()
	var flag, n uint32
	const tokenIsAppContainer = 29 // TOKEN_INFORMATION_CLASS.TokenIsAppContainer
	if err := windows.GetTokenInformation(token, tokenIsAppContainer, (*byte)(unsafe.Pointer(&flag)), uint32(unsafe.Sizeof(flag)), &n); err != nil {
		return "error:" + err.Error()
	}
	if flag != 0 {
		return "appcontainer"
	}
	return "not"
}

func tryRead(path string) string {
	if _, err := os.ReadFile(path); err != nil {
		if os.IsPermission(err) {
			return "denied"
		}
		return "error:" + err.Error()
	}
	return "ok"
}

func tryWrite(dir string) string {
	if err := os.WriteFile(filepath.Join(dir, "wallprobe.txt"), []byte("x"), 0o600); err != nil {
		if os.IsPermission(err) {
			return "denied"
		}
		return "error:" + err.Error()
	}
	return "ok"
}

func tryConnect(addr string) string {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		if errors.Is(err, windows.WSAEACCES) || os.IsPermission(err) {
			return "denied:access"
		}
		var timeout net.Error
		if (errors.As(err, &timeout) && timeout.Timeout()) || errors.Is(err, windows.WSAETIMEDOUT) {
			return "timeout"
		}
		return "error:" + err.Error()
	}
	c.Close()
	return "ok"
}

// tryPipe opens a named pipe by name, as a client of a local service
// would. A pipe is this platform's socket on the file system: a service
// of the same user that serves one takes requests from whoever may open
// it.
func tryPipe(name string) string {
	if name == "" {
		return "unset"
	}
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		if os.IsPermission(err) {
			return "denied"
		}
		return "error:" + err.Error()
	}
	f.Close()
	return "ok"
}

// tryUnix connects to an AF_UNIX stream socket by its path: the socket a
// service of the same user binds in a directory closed to every other
// account.
func tryUnix(path string) string {
	if path == "" {
		return "unset"
	}
	c, err := net.DialTimeout("unix", path, 3*time.Second)
	if err != nil {
		if os.IsPermission(err) || errors.Is(err, windows.WSAEACCES) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return "denied"
		}
		return "error:" + err.Error()
	}
	c.Close()
	return "ok"
}

// tryShellStart asks the shell to open a file, which is how a program
// has another program started for it without creating the process
// itself. The file is a script whose only act is to create a marker
// where the container cannot write: whether a program ran OUTSIDE the
// wall is the marker's to say, and the parent reads it. What comes back
// here is only what the shell answered.
func tryShellStart(script string) string {
	if script == "" {
		return "unset"
	}
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(script)
	if err != nil {
		return "error:" + err.Error()
	}

	if err := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW").Find(); err != nil {
		return "unavailable:" + err.Error()
	}
	const swHide = 0
	if err := windows.ShellExecute(0, verb, file, nil, nil, swHide); err != nil {
		if os.IsPermission(err) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return "denied"
		}
		return "error:" + err.Error()
	}
	return "asked"
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "token" {
		fmt.Println(tokenIsAppContainer())
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "reach" {
		// The host's half of the two controls: the same attempts, made
		// uncontained, must work or their refusal inside proves nothing.
		fmt.Println("pipe=" + tryPipe(os.Getenv("WALL_PIPE")))
		fmt.Println("shell-start=" + tryShellStart(os.Getenv("WALL_SHELL_SCRIPT")))
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "store" {
		// The account's plugin store, read by capability (store.go).
		storeProbe()
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "link" {
		// The machine service's link, and the route the design named
		// first, from wherever this runs.
		fmt.Println("link=" + tryPipe(os.Getenv("WALL_LINK")))
		fmt.Println("unix=" + tryUnix(os.Getenv("WALL_UNIX")))
		return
	}
	fmt.Println("token=" + tokenIsAppContainer())
	fmt.Println("read-granted=" + tryRead(os.Getenv("WALL_GRANTED_FILE")))
	fmt.Println("read-ungranted=" + tryRead(os.Getenv("WALL_UNGRANTED_FILE")))
	fmt.Println("write-temp=" + tryWrite(os.Getenv("TEMP")))
	fmt.Println("write-granted=" + tryWrite(filepath.Dir(os.Getenv("WALL_GRANTED_FILE"))))
	fmt.Println("net-loopback=" + tryConnect(os.Getenv("WALL_LOOPBACK")))
	fmt.Println("net-remote=" + tryConnect("1.1.1.1:443"))
	fmt.Println("pipe=" + tryPipe(os.Getenv("WALL_PIPE")))
	fmt.Println("shell-start=" + tryShellStart(os.Getenv("WALL_SHELL_SCRIPT")))
	exe, _ := os.Executable()
	out, err := exec.Command(exe, "token").Output()
	if err != nil {
		fmt.Println("child-token=error:" + err.Error())
	} else {
		fmt.Println("child-token=" + strings.TrimSpace(string(out)))
	}

	for _, mode := range []struct {
		name string
		flag uintptr
	}{{"dos", 0}, {"guid", 1}, {"nt", 2}, {"none", 4}} {
		fmt.Println("finalpath-" + mode.name + "=" + finalPathName(exe, mode.flag))
	}
	fmt.Println("self-job=" + selfJob())
	fmt.Println("taskkill=" + taskkillFromSystem32())
	fmt.Println("done")
}

// finalPathName is GetFinalPathNameByHandleW on our own executable with
// FILE_NAME_NORMALIZED and the given VOLUME_NAME_* flag.
func finalPathName(path string, volumeFlag uintptr) string {
	f, err := os.Open(path)
	if err != nil {
		return "open-error:" + err.Error()
	}
	defer f.Close()
	buf := make([]uint16, 32768)
	fn := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")
	n, _, callErr := fn.Call(f.Fd(), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), volumeFlag)
	if n == 0 {
		return "error:" + callErr.Error()
	}
	name := windows.UTF16ToString(buf[:n])
	if len(name) > 40 {
		name = name[:40] + "…"
	}
	return "ok:" + name
}

// selfJob is what a carrier that puts itself in its own kill-on-close
// job sees when it is already inside the host's job. The handle is
// kept for the process's life, as such a carrier keeps it: closing it
// would end this process (kill on close).
var selfJobHandle windows.Handle

func selfJob() string {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return "create-error:" + err.Error()
	}
	selfJobHandle = job
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE}}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return "limits-error:" + err.Error()
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		return "assign-error:" + err.Error()
	}
	return "ok"
}

// taskkillFromSystem32 spawns a child and ends it with the system's
// taskkill by full path — what a carrier's forced cleanup needs with
// no PATH in its environment.
func taskkillFromSystem32() string {
	sysdir, err := windows.GetSystemDirectory()
	if err != nil {
		return "sysdir-error:" + err.Error()
	}
	child := exec.Command(filepath.Join(sysdir, "cmd.exe"), "/c", "timeout /t 20 /nobreak >nul")
	if err := child.Start(); err != nil {
		return "child-error:" + err.Error()
	}
	out, err := exec.Command(filepath.Join(sysdir, "taskkill.exe"), "/PID", fmt.Sprint(child.Process.Pid), "/T", "/F").CombinedOutput()
	_ = child.Wait()
	if err != nil {
		return "error:" + err.Error() + ":" + strings.TrimSpace(string(out))
	}
	return "ok"
}
