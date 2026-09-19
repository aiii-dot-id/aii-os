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

// .
// .
// .
// .
// .
// .
// .
// .
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
	argv, _, err := containArgv(context.Background(), []string{bin}, nil)
	if err != nil {
		t.Skipf("this host cannot contain a native child: %v", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "TMPDIR=/nonexistent"}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 2 {
		t.Skipf("no Metal device on this host: %s", text)
	}
	if err != nil {
		t.Fatalf("the contained child could not run its kernel on the GPU: %v — %s", err, text)
	}
	if !strings.HasPrefix(text, "GPU ") || !strings.Contains(text, "out[7]=49.0") || !strings.Contains(text, "out[99]=9801.0") || !strings.Contains(text, "status=4") {
		t.Fatalf("the kernel's result is not the GPU's: %s", text)
	}
	t.Logf("%s (under the host's containment, scrubbed environment, nothing writable)", text)
}
