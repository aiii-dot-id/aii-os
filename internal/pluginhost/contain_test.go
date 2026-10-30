package pluginhost

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestTheNativeChildIsWrappedNotRunBare(t *testing.T) {
	skipWhereNativeWallCannotBeEstablished(t)
	argv, telemetry, err := containArgv(context.Background(), []string{"/tmp/artifact"}, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused a plain argv: %v", err)
	}
	if telemetry.Description == "" {
		t.Fatal("containment said nothing — the operator cannot tell whether it happened")
	}

	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec is absent; containment refuses, which the fail-closed test covers")
		}
		if !strings.HasSuffix(argv[0], "sandbox-exec") {
			t.Fatalf("the native child was not run under Seatbelt: %v", argv)
		}
		joined := strings.Join(argv, " ")

		for _, want := range []string{"deny network*", "deny file-write*", "deny mach-lookup", "deny iokit-open", "deny ipc-posix-shm*",
			`(deny file-read* file-test-existence (regex #"^/dev/(tty|pty|ptmx|console)"))`, "deny lsopen", "deny appleevent-send",
			"deny job-creation", "(deny signal)\n(allow signal (target same-sandbox))", "deny process-exec*", `(allow process-exec* (literal "/tmp/artifact"))`} {
			if !strings.Contains(joined, want) {
				t.Errorf("the Seatbelt profile does not %q: %v", want, argv)
			}
		}
		if strings.Contains(joined, "allow mach-lookup") || strings.Contains(joined, "allow iokit-open") {
			t.Errorf("a child with no compute backend may look up a system service or open a device driver: %v", argv)
		}
		if argv[len(argv)-1] != "/tmp/artifact" {
			t.Fatalf("the artifact is not what gets run: %v", argv)
		}
		return
	case "linux":

	default:
		if len(argv) != 1 || argv[0] != "/tmp/artifact" {
			t.Fatalf("a platform with no argv mechanism altered the argv: %v", argv)
		}
		return
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		if !strings.Contains(telemetry.Description, "not installed") {
			t.Fatalf("bubblewrap is absent and the telemetry does not say so: %q", telemetry)
		}
		return
	}

	if argv[len(argv)-1] != "/tmp/artifact" {
		t.Fatalf("the artifact is not the command being run: %v", argv)
	}
	if !strings.HasSuffix(argv[0], "bwrap") {
		t.Fatalf("the child is not wrapped: %v", argv)
	}

	joined := strings.Join(argv, " ")

	if !strings.Contains(joined, "--unshare-all") {
		t.Fatalf("the child keeps its own network — net.outbound means the broker, not a socket it opens: %v", argv)
	}
	if !strings.Contains(joined, "--die-with-parent") {
		t.Fatalf("the child could outlive its supervisor: %v", argv)
	}
	if !strings.Contains(joined, "--new-session") {
		t.Fatalf("the child shares the host's session, and with it the terminal the host was started from: %v", argv)
	}
	for i, a := range argv {
		if (a == "--ro-bind" || a == "--bind") && i+2 < len(argv) && argv[i+2] == "/" {
			t.Fatalf("the child is shown the whole root: %v", argv)
		}
		if a == "--bind" {
			t.Fatalf("the child is shown a place it may write: %v", argv)
		}
	}
	if !strings.Contains(joined, "--ro-bind /usr /usr") {
		t.Fatalf("the child is not shown the system's view: %v", argv)
	}
	if !strings.Contains(joined, "--remount-ro / --") {
		t.Fatalf("the sandbox's own root is left writable: %v", argv)
	}
	// No network is not no socket: a socket on the file system the view
	// shows is in reach, and the filter is what refuses them.
	if !strings.Contains(joined, "--seccomp 3") || len(telemetry.Filter) == 0 {
		t.Fatalf("the child runs under no system-call filter, so a socket on the file system is a way out: %v", argv)
	}

	requirePrivateProc(t, argv)
}

func requirePrivateProc(t *testing.T, argv []string) {
	t.Helper()
	proc, end := -1, len(argv)
	for i, a := range argv {
		switch {
		case a == "--" && end == len(argv):
			end = i
		case a == "--proc" && i+1 < len(argv) && argv[i+1] == "/proc" && i < end:
			proc = i
		}
	}
	if proc < 0 {
		t.Fatalf("the child does not get its own /proc — the daemon's /proc reaches past every mask: %v", argv)
	}
	for i := 0; i+2 < end; i++ {
		switch argv[i] {
		case "--ro-bind", "--bind", "--dev-bind", "--ro-bind-try", "--bind-try":
			if dest := argv[i+2]; (dest == "/" || dest == "/proc" || strings.HasPrefix(dest, "/proc/")) && i > proc {
				t.Fatalf("a bind over /proc comes after the child's own /proc and covers it with the host's: %v", argv)
			}
		}
	}
}

func TestContainingNothingIsAnError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the no-op lane has nothing to refuse")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is not installed")
	}
	if _, _, err := containArgv(context.Background(), nil, nil, nativeFiles{}); err == nil {
		t.Fatal("containing an empty argv reported success")
	}
}

func TestContainmentRefusesRatherThanRunningBare(t *testing.T) {
	var tool string
	switch runtime.GOOS {
	case "linux":
		tool = "bwrap"
	case "darwin":
		tool = "sandbox-exec"
	default:
		t.Skip("this platform contains after spawn, or not at all")
	}
	if _, err := exec.LookPath(tool); err == nil {

		argv, _, err := containArgv(context.Background(), []string{"/tmp/artifact"}, nil, nativeFiles{})
		if err != nil {
			if argv != nil {
				t.Fatalf("containment refused but returned an argv anyway: %v (%v)", argv, err)
			}
			return
		}
		if len(argv) == 1 {
			t.Fatal("the argv was returned unwrapped while the mechanism was available")
		}
		return
	}

	argv, _, err := containArgv(context.Background(), []string{"/tmp/artifact"}, nil, nativeFiles{})
	if err == nil {
		t.Fatalf("%s is missing and containment returned %v instead of refusing — native code would run with no wall at all", tool, argv)
	}
	if argv != nil {
		t.Fatal("a refusal returned an argv anyway; a caller ignoring the error would run it")
	}
}
