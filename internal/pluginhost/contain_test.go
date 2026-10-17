package pluginhost

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

// T0-T2 run as wasm under wazero, where a guest cannot perform I/O
// unless the host hands it a capability. Native T3 is the deliberate
// exception to that model, and it was an exception to the containment
// too: plain exec, the daemon's filesystem, and nothing stopping it
// opening a socket.

func TestTheNativeChildIsWrappedNotRunBare(t *testing.T) {
	skipWhereNativeWallCannotBeEstablished(t)
	argv, telemetry, err := containArgv(context.Background(), []string{"/tmp/artifact"}, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused a plain argv: %v", err)
	}
	if telemetry.Description == "" {
		t.Fatal("containment said nothing — the operator cannot tell whether it happened")
	}

	// THREE SHAPES, and every platform is one of them. Linux and macOS
	// wrap argv — bubblewrap and sandbox-exec are programs you run your
	// program under. Windows contains the process instead, after spawn,
	// so there is nothing to wrap here and the job object lives in the
	// supervisor. Android and iOS never spawn a native child at all.
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec is absent; containment refuses, which the fail-closed test covers")
		}
		if !strings.HasSuffix(argv[0], "sandbox-exec") {
			t.Fatalf("the native child was not run under Seatbelt: %v", argv)
		}
		joined := strings.Join(argv, " ")
		// Each is a route out that was open under allow-default until it
		// was denied by name; the from-inside tests try the routes, and
		// this holds every rule in place on its own.
		for _, want := range []string{"deny network*", "deny file-write*", "deny mach-lookup", "deny iokit-open", "deny ipc-posix-shm*",
			`(deny file-read* (regex #"^/dev/(tty|pty|ptmx|console)"))`, "deny lsopen", "deny appleevent-send",
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
		// checked below
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

	// The artifact must still be what actually runs, last.
	if argv[len(argv)-1] != "/tmp/artifact" {
		t.Fatalf("the artifact is not the command being run: %v", argv)
	}
	if !strings.HasSuffix(argv[0], "bwrap") {
		t.Fatalf("the child is not wrapped: %v", argv)
	}

	joined := strings.Join(argv, " ")
	// "a grant is not a socket" (broker.go) was a policy sentence a
	// native plugin could ignore. This is the sentence with a kernel
	// behind it.
	if !strings.Contains(joined, "--unshare-all") {
		t.Fatalf("the child keeps its own network — net.outbound means the broker, not a socket it opens: %v", argv)
	}
	if !strings.Contains(joined, "--die-with-parent") {
		t.Fatalf("the child could outlive its supervisor: %v", argv)
	}
	if !strings.Contains(joined, "--new-session") {
		t.Fatalf("the child shares the host's session, and with it the terminal the host was started from: %v", argv)
	}
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("the child can write the daemon's filesystem: %v", argv)
	}
	// No network is not no socket: every socket on the file system is in
	// the read-only root's view, and the filter is what refuses them.
	if !strings.Contains(joined, "--seccomp 3") || len(telemetry.Filter) == 0 {
		t.Fatalf("the child runs under no system-call filter, so a socket on the file system is a way out: %v", argv)
	}
	// The host's /proc is a door around every mask: /proc/<daemon
	// pid>/root is the daemon's own view of its private files.
	requirePrivateProc(t, argv)
}

// requirePrivateProc holds argv to mounting the child's own procfs, and
// to doing it AFTER the read-only root bind, which would otherwise cover
// it with the host's.
func requirePrivateProc(t *testing.T, argv []string) {
	t.Helper()
	root, proc, end := -1, -1, len(argv)
	for i, a := range argv {
		switch {
		case a == "--" && end == len(argv):
			end = i
		case a == "--ro-bind" && i+2 < len(argv) && argv[i+1] == "/" && argv[i+2] == "/" && root < 0:
			root = i
		case a == "--proc" && i+1 < len(argv) && argv[i+1] == "/proc":
			proc = i
		}
	}
	if root < 0 || proc < root || proc > end {
		t.Fatalf("the child does not get its own /proc after the root bind — the daemon's /proc reaches past every mask: %v", argv)
	}
}

// An empty argv is a caller bug, not something to wrap silently.
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

// CONTAINMENT FAILS CLOSED. It used to return the bare argv with a
// telemetry line when the mechanism was missing, on the reasoning that
// "no mechanism is not a mechanism refusing". That is true of a PLATFORM
// with none and false of a platform that has one and has not installed
// it — and it meant a host missing bwrap ran signed native code with the
// daemon's whole filesystem and an open network, while
// PLUGIN_FRAMEWORK promised containment is fail-closed.
//
// A plugin that does not run, with a reason the operator can act on,
// beats a plugin that runs with none of the wall they were told about.
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
		// The mechanism is here, so the refusal cannot be provoked
		// without removing it. A confined runner can still refuse because
		// the mechanism cannot establish its sandbox; that is safe only if
		// it returns no argv at all.
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
	// The mechanism is missing: this must be a refusal and not an argv.
	argv, _, err := containArgv(context.Background(), []string{"/tmp/artifact"}, nil, nativeFiles{})
	if err == nil {
		t.Fatalf("%s is missing and containment returned %v instead of refusing — native code would run with no wall at all", tool, argv)
	}
	if argv != nil {
		t.Fatal("a refusal returned an argv anyway; a caller ignoring the error would run it")
	}
}
