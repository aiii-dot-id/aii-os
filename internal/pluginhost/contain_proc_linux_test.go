//go:build linux

package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// EVERY PROFILE, THE SAME /proc. The private procfs is not a compute
// accommodation: CUDA needed it first, but the reason every child needs
// it is the daemon's own files. Each backend is built here as the host
// would build it; one whose compute nodes this host lacks is refused
// before any wall exists, and is skipped saying so.
func TestEveryContainedProfileGetsItsOwnProc(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
	}
	for _, backend := range []string{"", "cpu", "vulkan", "cpu+vulkan", "vulkan+cpu", "cuda", "cuda+vulkan", "vulkan+cuda"} {
		name := backend
		if name == "" {
			name = "no-profile"
		}
		t.Run(name, func(t *testing.T) {
			var profile *AcceleratorProfile
			if backend != "" {
				profile = &AcceleratorProfile{Backend: backend}
			}
			if _, err := linuxAcceleratorDevices(profile, os.DirFS("/dev")); err != nil {
				t.Skipf("this host has no %s compute nodes, so the profile is refused before a wall is built: %v", backend, err)
			}
			argv, wall, err := containArgv(context.Background(), []string{"/bin/true"}, profile, nativeFiles{})
			var unavailable *ContainmentUnavailableError
			if errors.As(err, &unavailable) {
				t.Skipf("this host cannot build the sandbox (user namespaces, or a private /proc inside them, unavailable): %v", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			requirePrivateProc(t, argv)
			if !strings.Contains(wall.Description, "private PID-namespace procfs") {
				t.Fatalf("the wall does not say the child has its own /proc: %q", wall.Description)
			}
		})
	}
}

// THE DAEMON'S /proc WAS A DOOR AROUND EVERY MASK. The private directories
// are hidden from a native child by mounts in the child's own namespace,
// and /proc/<daemon pid>/root is not in that namespace: it is the daemon's
// view of the filesystem, readable by a child of the same uid. Through the
// host's procfs, which the read-only root carried in, a contained child
// read data/credentials straight past the tmpfs laid over it.
//
// Here the test process stands in for the daemon. Uncontained, the probe
// reads the canary by its path and by the daemon's /proc — the control
// half: a probe that could not see it would prove nothing about a wall
// that hides it. Contained, it must read it neither way, and must still
// have a /proc of its own.
func TestAContainedChildCannotReachTheDaemonsMaskedFilesThroughProc(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
	}
	skipWhereTheSandboxCannotBeEstablished(t)

	data := filepath.Join(t.TempDir(), "data")
	secret := filepath.Join(data, "credentials", "key")
	if err := os.MkdirAll(filepath.Dir(secret), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("canary-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	via := fmt.Sprintf("/proc/%d/root%s", os.Getpid(), secret)
	const script = `for p in "$@"; do
  value=$(cat "$p") || value=
  case "$value" in canary-secret*) echo "LEAK:$p";; esac
done
test -r /proc/self/status && echo "own-proc"
echo checked`
	run := func(argv []string, wall supervisor.Containment) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := walled(t, exec.CommandContext(ctx, argv[0], argv[1:]...), wall).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "checked") {
			t.Fatalf("the probe did not run: %v\n%s", err, out)
		}
		return string(out)
	}

	probe := []string{"/bin/sh", "-c", script, "probe", secret, via}
	control := run(probe, supervisor.Containment{})
	if !strings.Contains(control, "LEAK:"+secret) {
		t.Fatalf("rig: uncontained, the probe must read the canary by its path, or its silence below proves nothing:\n%s", control)
	}
	if !strings.Contains(control, "LEAK:"+via) {
		t.Skipf("this host already keeps the daemon's /proc/<pid>/root from its own children, so there is no route here to close:\n%s", control)
	}

	argv, wall, err := containArgv(context.Background(), probe, nil, nativeFiles{places: []*firewall.Rule{nativePlace(data)}})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	inside := run(argv, wall)
	if strings.Contains(inside, "LEAK:"+secret) {
		t.Fatalf("rig: the masked directory is readable by its path inside the wall, so this probe is not testing the mask:\n%s", inside)
	}
	if strings.Contains(inside, "LEAK:") {
		t.Fatalf("a contained child read the daemon's masked file through the daemon's /proc:\n%s", inside)
	}
	if !strings.Contains(inside, "own-proc") {
		t.Fatalf("the contained child has no /proc of its own:\n%s", inside)
	}
}
