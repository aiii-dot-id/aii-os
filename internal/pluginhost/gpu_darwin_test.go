//go:build darwin

package pluginhost

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A CONTAINED CHILD REACHES THE GPU, WHEN ITS SIGNED PROFILE SAYS IT RUNS
// ON ONE. The engine builder's adapter runs its models on Metal inside the
// plugin's process tree, under the host's containment; this is the
// platform's word that it can: a compute kernel compiled at run time and
// executed on the GPU, from a child wrapped exactly as the host wraps a
// native T3 plugin whose profile names Metal, with a scrubbed environment
// and nowhere to write. Skips where the platform cannot build the probe
// (no Swift toolchain) or has no Metal device — capability absent, not
// product failure.
//
// The wall refuses every lookup of a system service and every connection
// to a device driver but the ones the backend cannot work without
// (seatbeltCompute), so the same probe is run again as a child with no
// such profile: it must find NO Metal device. That half is what keeps the
// allowances tied to the profile, and it is the evidence for the names on
// the two lists.
func TestAContainedChildReachesTheGPU(t *testing.T) {
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("no xcrun: the Swift probe cannot be built here")
	}
	src, err := filepath.Abs("testdata/gpuprobe/probe.swift")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "probe")
	if out, err := exec.Command("xcrun", "swiftc", "-O", "-o", bin, src).CombinedOutput(); err != nil {
		t.Skipf("the Swift probe does not build here (%v): %s", err, bytes.TrimSpace(out))
	}
	run := func(profile *AcceleratorProfile) (string, error) {
		t.Helper()
		argv, _, err := containArgv(context.Background(), []string{bin}, profile, nativeFiles{})
		if err != nil {
			t.Skipf("this host cannot contain a native child: %v", err)
		}
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "TMPDIR=/nonexistent"}
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	// The control: uncontained, on this host. "No Metal device" means the
	// host has none only if the probe says so with no wall around it; the
	// same words from inside the wall mean the wall hid the device, which
	// for a Metal profile is a failure and not a skip.
	control := exec.Command(bin)
	control.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "TMPDIR=/nonexistent"}
	if out, err := control.CombinedOutput(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
			t.Skipf("no Metal device on this host: %s", bytes.TrimSpace(out))
		}
		t.Skipf("the probe does not run on this host even uncontained (%v): %s", err, bytes.TrimSpace(out))
	}
	text, err := run(&AcceleratorProfile{Backend: "cpu+metal"})
	if err != nil {
		t.Fatalf("the contained child could not run its kernel on the GPU: %v — %s", err, text)
	}
	if !strings.HasPrefix(text, "GPU ") || !strings.Contains(text, "out[7]=49.0") || !strings.Contains(text, "out[99]=9801.0") || !strings.Contains(text, "status=4") {
		t.Fatalf("the kernel's result is not the GPU's: %s", text)
	}
	t.Logf("%s (under the host's containment, scrubbed environment, nothing writable)", text)

	for _, plain := range []*AcceleratorProfile{nil, {Backend: "cpu"}} {
		text, err := run(plain)
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 || !strings.Contains(text, "NO METAL DEVICE") {
			t.Errorf("a child whose profile names no Metal found a Metal device (profile %+v): %v — %s", plain, err, text)
		}
	}
}
