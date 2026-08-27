package pluginhost

import (
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
	if runtime.GOOS == "linux" {
		if _, lerr := exec.LookPath("bwrap"); lerr != nil {
			t.Skip("bubblewrap is absent; containment refuses, which the fail-closed test covers")
		}
	}
	argv, telemetry, err := containArgv([]string{"/tmp/artifact"})
	if err != nil {
		t.Fatalf("containment refused a plain argv: %v", err)
	}
	if telemetry == "" {
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
		for _, want := range []string{"deny network*", "deny file-write*"} {
			if !strings.Contains(joined, want) {
				t.Errorf("the Seatbelt profile does not %q — it must match what Linux delivers: %v", want, argv)
			}
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
		if !strings.Contains(telemetry, "not installed") {
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
	if !strings.Contains(joined, "--ro-bind / /") {
		t.Fatalf("the child can write the daemon's filesystem: %v", argv)
	}
	// Mounting a fresh procfs fails when the daemon is itself sandboxed,
	// and the two halves of the containment story have to compose.
	if strings.Contains(joined, "--proc") {
		t.Fatalf("--proc breaks nesting inside a contained runtime: %v", argv)
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
	if _, _, err := containArgv(nil); err == nil {
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
		// without removing it. What IS provable is that the contained
		// path never returns the bare argv.
		argv, _, err := containArgv([]string{"/tmp/artifact"})
		if err != nil {
			t.Fatalf("containment refused with the mechanism present: %v", err)
		}
		if len(argv) == 1 {
			t.Fatal("the argv was returned unwrapped while the mechanism was available")
		}
		return
	}
	// The mechanism is missing: this must be a refusal and not an argv.
	argv, _, err := containArgv([]string{"/tmp/artifact"})
	if err == nil {
		t.Fatalf("%s is missing and containment returned %v instead of refusing — native code would run with no wall at all", tool, argv)
	}
	if argv != nil {
		t.Fatal("a refusal returned an argv anyway; a caller ignoring the error would run it")
	}
}
