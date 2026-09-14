//go:build windows

// .
// .
// .
// .
// .
// .
package main

import (
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
	const tokenIsAppContainer = 29
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
		return "denied:" + strings.SplitN(err.Error(), ":", 2)[0]
	}
	c.Close()
	return "ok"
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "token" {
		fmt.Println(tokenIsAppContainer())
		return
	}
	fmt.Println("token=" + tokenIsAppContainer())
	fmt.Println("read-granted=" + tryRead(os.Getenv("WALL_GRANTED_FILE")))
	fmt.Println("read-ungranted=" + tryRead(os.Getenv("WALL_UNGRANTED_FILE")))
	fmt.Println("write-temp=" + tryWrite(os.Getenv("TEMP")))
	fmt.Println("write-granted=" + tryWrite(filepath.Dir(os.Getenv("WALL_GRANTED_FILE"))))
	fmt.Println("net-loopback=" + tryConnect(os.Getenv("WALL_LOOPBACK")))
	fmt.Println("net-remote=" + tryConnect("1.1.1.1:443"))
	exe, _ := os.Executable()
	out, err := exec.Command(exe, "token").Output()
	if err != nil {
		fmt.Println("child-token=error:" + err.Error())
	} else {
		fmt.Println("child-token=" + strings.TrimSpace(string(out)))
	}
	// .
	// .
	// .
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

// .
// .
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

// .
// .
// .
// .
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

// .
// .
// .
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
