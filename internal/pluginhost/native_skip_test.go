package pluginhost

import (
	"bytes"
	"os/exec"
	"runtime"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func skipWhereNativeIsRefused(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" && !WindowsContainedNativeQualified {
		t.Skip("native T3 is not admitted on Windows until the wall is qualified with the real backend")
	}
}

func skipWhereTheSandboxCannotBeEstablished(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	if out, err := exec.Command("bwrap", "--unshare-all", "--die-with-parent",
		"--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", "/bin/true").CombinedOutput(); err != nil {
		t.Skipf("this host cannot establish the bwrap sandbox — user namespaces, or a private /proc inside them, are unavailable (capability absent, not product failure): %v — %s", err, bytes.TrimSpace(out))
	}
}

func skipWhereNativeWallCannotBeEstablished(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bubblewrap is absent: native wall tests require it")
		}
		skipWhereTheSandboxCannotBeEstablished(t)
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec is absent: native wall tests require it")
		}
	default:
		t.Skip("this platform has no argv-based native wall")
	}
}

// exeSuffix is what a built test child must be named on this platform:
// Windows exec finds no executable in a file without an extension.
var exeSuffix = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]

// walled hands cmd what its wall reads at the spawn (Linux: the
// system-call filter), as the supervisor does for a real child. A test
// that runs a contained argv itself must do the same, or bubblewrap finds
// no filter on the descriptor its argv names and runs nothing.
func walled(t *testing.T, cmd *exec.Cmd, wall supervisor.Containment) *exec.Cmd {
	t.Helper()
	release, err := wall.Attach(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return cmd
}

// standInTools is what a test names as its stand-in's own programs when
// the stand-in is a shell script: the system's shell and tools. A real
// package's programs are its verified runtime root, and on macOS a
// contained child starts nothing else.
var standInTools = []string{"/bin", "/usr/bin"}
