//go:build windows

package consolewin

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideKeepsWhatTheStartAlreadyCarries(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "exit", "0")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags:              windows.CREATE_UNICODE_ENVIRONMENT,
		AdditionalInheritedHandles: []syscall.Handle{7},
	}
	Hide(cmd)
	a := cmd.SysProcAttr
	if !a.HideWindow || a.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("not hidden: %+v", a)
	}
	if a.CreationFlags&windows.CREATE_UNICODE_ENVIRONMENT == 0 || len(a.AdditionalInheritedHandles) != 1 {
		t.Fatalf("Hide dropped what the start carried: %+v", a)
	}
	bare := exec.Command("cmd.exe", "/c", "exit", "0")
	Hide(bare)
	if bare.SysProcAttr == nil || bare.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("a bare command was not hidden")
	}
	if err := bare.Run(); err != nil {
		t.Fatalf("a hidden console program must still run: %v", err)
	}
}
