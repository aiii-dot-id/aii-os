//go:build windows

package consolewin

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func Hide(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}

var getConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

func Visible() bool {
	if getConsoleWindow.Find() != nil {
		return false
	}
	h, _, _ := getConsoleWindow.Call()
	return h != 0 && windows.IsWindowVisible(windows.HWND(h))
}
