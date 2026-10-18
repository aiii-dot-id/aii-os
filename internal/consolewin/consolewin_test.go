package consolewin

import (
	"os/exec"
	"runtime"
	"testing"
)

// Everywhere but Windows, Hide leaves the start exactly as it was.
func TestHideChangesNothingOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows is consolewin_windows_test.go")
	}
	cmd := exec.Command("true")
	Hide(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatalf("Hide set %+v", cmd.SysProcAttr)
	}
	if Visible() {
		t.Fatal("Visible is only ever true on Windows")
	}
}
