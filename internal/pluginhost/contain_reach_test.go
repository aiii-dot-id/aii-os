//go:build linux || darwin

package pluginhost

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// THE WAYS OUT THAT ARE NOT THE NETWORK, TRIED FROM INSIDE.
// TestAContainedNativeChildCanNeitherConnectNorWrite tries a TCP
// connection and a write. A wall that refuses both still left a contained
// child able to act on the machine around it: on Linux through any socket
// on the file system the account may use (the session bus, the user's
// service manager and a container engine each start a process outside the
// wall on request), on macOS through LaunchServices, Apple events, launchd
// and a plain signal. Each test here runs the same child twice, as the
// wall test does: uncontained, where the attempt must succeed or its
// refusal proves nothing, and under the platform's real containment.

// requireWall skips where the platform's containment cannot be built, as
// the wall test does.
func requireWall(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bubblewrap is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
		}
		skipWhereTheSandboxCannotBeEstablished(t)
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
		}
	}
}

// runContained runs a contained argv to completion with what its wall
// reads at the spawn (Linux: the system-call filter), as the supervisor
// hands it, and returns what it printed.
func runContained(t *testing.T, argv []string, wall supervisor.Containment) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	release, err := wall.Attach(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the contained probe did not run: %v\n%s%s", err, out, stderr.String())
	}
	return string(out)
}

// shortDir is a directory whose paths fit a socket address (104 bytes on
// macOS, where the default temporary directory is already longer).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "aiiw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir
}

// A socket on the file system needs no network and no write to connect
// to. The listener is the test's own, standing in for the session bus.
func TestAContainedNativeChildReachesNoSocketOnTheFileSystem(t *testing.T) {
	requireWall(t)
	path := filepath.Join(shortDir(t), "service.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	accepted := func() bool {
		t.Helper()
		if err := ln.(*net.UnixListener).SetDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		c, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return false
			}
			t.Fatalf("listener: %v", err)
		}
		c.Close()
		return true
	}

	out := runWallProbe(t, []string{fakechildBin, "reach", "unix=" + path})
	if !strings.Contains(out, "unix=connected") || !accepted() {
		t.Fatalf("rig: uncontained, the probe must connect, or its refusal below proves nothing:\n%s", out)
	}
	argv, wall, err := containArgv(context.Background(), []string{fakechildBin, "reach", "unix=" + path}, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	out = runContained(t, argv, wall)
	if !strings.Contains(out, "unix=refused") {
		t.Errorf("the contained child connected to a socket on the file system:\n%s", out)
	}
	if accepted() {
		t.Error("the listener accepted a connection from inside the wall")
	}
	t.Logf("inside the wall: %s", strings.TrimSpace(out))
}

// A contained child signals nothing outside its wall: not the host that
// supervises it, and not, through a service that signals on request,
// anything else of the account's. Signal 0 asks the question and sends
// nothing. On Linux the child's process namespace has no such process; on
// macOS the profile refuses.
func TestAContainedNativeChildSignalsNothingOutside(t *testing.T) {
	requireWall(t)
	me := "signal=" + strconv.Itoa(os.Getpid())
	out := runWallProbe(t, []string{fakechildBin, "reach", me})
	if !strings.Contains(out, "signal=allowed") {
		t.Fatalf("rig: uncontained, the probe may signal the test, or its refusal below proves nothing:\n%s", out)
	}
	argv, wall, err := containArgv(context.Background(), []string{fakechildBin, "reach", me}, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	out = runContained(t, argv, wall)
	want := map[string]string{"linux": "signal=refused ESRCH", "darwin": "signal=refused EPERM"}[runtime.GOOS]
	if !strings.Contains(out, want) {
		t.Errorf("the contained child may signal a process outside the wall (want %q):\n%s", want, out)
	}
}

// A CONTAINED CHILD STILL ENDS ITS OWN CHILDREN. A real engine is a carrier
// and a worker, and the carrier ends a worker whose request ran out of
// time by a signal to the worker's process group. The wall that refuses a
// signal to anything outside must not refuse that one, or a stuck worker
// could only be ended by ending the whole plugin.
func TestAContainedNativeChildSignalsItsOwnChildren(t *testing.T) {
	requireWall(t)
	out := runWallProbe(t, []string{fakechildBin, "reach", "ownsignal"})
	if !strings.Contains(out, "ownsignal=delivered") {
		t.Fatalf("rig: uncontained, the probe must end its own child:\n%s", out)
	}
	argv, wall, err := containArgv(context.Background(), []string{fakechildBin, "reach", "ownsignal"}, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	if out = runContained(t, argv, wall); !strings.Contains(out, "ownsignal=delivered") {
		t.Errorf("inside the wall a child could not end its own child's process group:\n%s", out)
	}
}

// THE NULL DEVICE TAKES A WRITE INSIDE THE WALL, ON BOTH PLATFORMS. Nothing
// written there is kept, so allowing it gives a child nothing; refusing it
// broke an ordinary thing. A program that starts a child with one of its
// streams unset has the null device opened for writing on its behalf, and
// under a profile that refused every write that open failed and the child
// was never started: a carrier that ran on Linux, where the wall's own
// /dev has a writable null device, failed on macOS for no reason of its
// own. The second half is that sequence itself: the contained child starts
// its own program with nowhere for that program's output to go.
func TestAContainedNativeChildMayWriteToTheNullDevice(t *testing.T) {
	requireWall(t)
	own := t.TempDir()
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(own, "child")
	if err := os.WriteFile(child, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	asks := []string{child, "reach", "null", "quiet=" + child + "|reach|null"}
	argv, wall, err := containArgv(context.Background(), asks, nil, nativeFiles{own: []string{own}})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	out := runContained(t, argv, wall)
	if strings.Join(strings.Fields(out), " ") != "null=written quiet=ran" {
		t.Errorf("inside the wall a write to the null device, and a child started with its output discarded, must both work:\n%s", out)
	}
}
