//go:build linux || darwin

package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// THE WALL, TRIED FROM INSIDE. TestTheNativeChildIsWrappedNotRunBare reads
// the argv the wall is built from; a flag in an argv is a promise, and the
// kernel is what keeps it. Here a child runs under the platform's real
// containment — bubblewrap on Linux, Seatbelt on macOS — and tries the two
// things that containment says it denies: a TCP connection to a listener
// the test holds on the host's loopback, and creating a file in a
// directory the test itself can write. The same child, uncontained, does
// both first: a probe that cannot connect or write proves nothing about a
// wall that refuses it.
func TestAContainedNativeChildCanNeitherConnectNorWrite(t *testing.T) {
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
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	addr := ln.Addr().String()
	dir := t.TempDir()

	control := filepath.Join(dir, "uncontained")
	out := runWallProbe(t, []string{fakechildBin, "wall", addr, control})
	if !strings.Contains(out, "dial=connected") || !strings.Contains(out, "write=written") || !listenerAccepted(t, ln) {
		t.Fatalf("rig: uncontained, the probe must connect and write, or its refusal below proves nothing:\n%s", out)
	}

	inside := filepath.Join(dir, "contained")
	argv, wall, err := containArgv(context.Background(), []string{fakechildBin, "wall", addr, inside}, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	if !wall.NetworkDenied || !wall.FilesystemRestricted {
		t.Fatalf("the wall does not claim what is tried here: %+v", wall)
	}
	out = runContained(t, argv, wall)
	if !strings.Contains(out, "dial=refused") {
		t.Errorf("the contained child opened a socket to the host's loopback — a grant is not a socket:\n%s", out)
	}
	if listenerAccepted(t, ln) {
		t.Error("the host's listener accepted a connection from inside the wall")
	}
	if !strings.Contains(out, "write=refused") {
		t.Errorf("the contained child created a file:\n%s", out)
	}
	if _, err := os.Lstat(inside); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file exists where the contained child tried to write: %v", err)
	}
	t.Logf("inside the wall (%s):\n%s", wall.Description, out)
}

// runWallProbe runs argv to completion, bounded, and returns its report.
func runWallProbe(t *testing.T, argv []string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the probe did not run: %v\n%s%s", err, out, stderr.Bytes())
	}
	return string(out)
}

// listenerAccepted reports whether the listener holds a connection. The
// probe has exited by the time this asks, so a connection it completed is
// already queued; the deadline only bounds the wait for one that never
// came.
func listenerAccepted(t *testing.T, ln net.Listener) bool {
	t.Helper()
	if err := ln.(*net.TCPListener).SetDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
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
