package pluginhost

import (
	"bytes"
	"os/exec"
	"runtime"
	"testing"
)

// .
// .
// .
// .
func skipWhereNativeIsRefused(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" && !WindowsContainedNativeQualified {
		t.Skip("native T3 is not admitted on Windows until the wall is qualified with the real backend")
	}
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
func skipWhereTheSandboxCannotBeEstablished(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	if out, err := exec.Command("bwrap", "--unshare-all", "--die-with-parent",
		"--ro-bind", "/", "/", "--dev", "/dev", "--", "/bin/true").CombinedOutput(); err != nil {
		t.Skipf("this host cannot establish the bwrap sandbox (capability absent, not product failure): %v — %s", err, bytes.TrimSpace(out))
	}
}

// .
// .
var exeSuffix = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]
