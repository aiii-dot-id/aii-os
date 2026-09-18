//go:build windows

// .
// .
// .
// .
// .
// .
// .
// .
// .
package tools

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// .
// .
// .
const shellDialect = "PowerShell"

// .
// .
// .
// .
// .
func shellInvocation() (string, []string) {
	// .
	// .
	// .
	// .
	if pwsh, err := exec.LookPath("pwsh.exe"); err == nil {
		return pwsh, []string{"-NoProfile", "-NonInteractive", "-Command"}
	}
	ps := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	return ps, []string{"-NoProfile", "-NonInteractive", "-Command"}
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
// .
// .
func shellEnv(sandbox string) []string {
	sr := os.Getenv("SystemRoot")
	psHome := filepath.Join(sr, `System32\WindowsPowerShell\v1.0`)

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
	path := os.Getenv("PATH")
	if path == "" {
		path = sr + `\System32;` + sr + `;` + psHome
	}
	if !strings.Contains(strings.ToLower(path), strings.ToLower(psHome)) {
		path += ";" + psHome
	}

	// .
	// .
	// .
	psModule := os.Getenv("PSModulePath")
	if psModule == "" {
		psModule = filepath.Join(psHome, "Modules")
	}

	env := []string{
		"SystemRoot=" + sr,
		"PATH=" + path,
		"PATHEXT=" + envOr("PATHEXT", ".COM;.EXE;.BAT;.CMD;.PS1"),
		"PSModulePath=" + psModule,
		// .
		// .
		// .
		"USERPROFILE=" + sandbox,
		"APPDATA=" + sandbox,
		"LOCALAPPDATA=" + sandbox,
		"TEMP=" + sandbox,
		"TMP=" + sandbox,
	}
	// .
	// .
	// .
	for _, k := range []string{"SystemDrive", "ProgramFiles", "ProgramFiles(x86)", "ProgramData", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "COMPUTERNAME"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

// .
// .
func prepareTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

// .
// .
// .
// .
// .
// .
type shellTree struct {
	mu  sync.Mutex
	job windows.Handle
}

// .
// .
// .
// .
// .
func (t *shellTree) adopt(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	t.mu.Lock()
	t.job = job
	t.mu.Unlock()
}

// .
// .
func (t *shellTree) kill(cmd *exec.Cmd) error {
	t.mu.Lock()
	job := t.job
	t.mu.Unlock()
	if job != 0 {
		return windows.TerminateJobObject(job, 1)
	}
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// .
// .
// .
// .
// .
func (t *shellTree) close() {
	t.mu.Lock()
	job := t.job
	t.job = 0
	t.mu.Unlock()
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}

// .
// .
// .
// .
// .
func normalizeShellOutput(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}
