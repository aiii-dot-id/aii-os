//go:build windows

package supervisor_test

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// An external test: the machine service imports the plugin host, which
// imports this package, so a test that takes the service's own link
// cannot be one of this package's own.

func TestAppContainerChildCannotReachTheMachineLink(t *testing.T) {
	probe := supervisor.BuildWallProbeForTest(t)
	granted := filepath.Dir(probe)

	dir := filepath.Join(t.TempDir(), "machine")
	h, err := machine.Listen(dir)
	if err != nil {
		t.Fatalf("the service's own link: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			c, err := h.Listener().Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			c.Close()
		}
	}()
	if ok, err := fileperm.IsOwnerOnlyDir(dir); err != nil || !ok {
		t.Fatalf("the service's directory must be owner-only: %v, %v", ok, err)
	}

	short, err := os.MkdirTemp(os.Getenv("USERPROFILE"), "u")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	unixDir := filepath.Join(short, "owner-only")
	if err := fileperm.MkdirOwnerOnly(unixDir); err != nil {
		t.Fatal(err)
	}
	unixPath := filepath.Join(unixDir, "link")
	uln, err := net.Listen("unix", unixPath)
	if err != nil {
		t.Fatalf("an AF_UNIX listener: %v", err)
	}
	t.Cleanup(func() { uln.Close() })
	var unixAccepted atomic.Int32
	go func() {
		for {
			c, err := uln.Accept()
			if err != nil {
				return
			}
			unixAccepted.Add(1)
			c.Close()
		}
	}()

	env := []string{"WALL_LINK=" + h.Path(), "WALL_UNIX=" + unixPath}
	control := exec.Command(probe, "link")
	control.Env = append(os.Environ(), env...)
	out, err := control.CombinedOutput()
	if err != nil {
		t.Fatalf("rig: the uncontained probe did not run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "link=ok") || !strings.Contains(string(out), "unix=ok") {
		t.Fatalf("rig: uncontained, the probe must open the link and the socket, or their refusal below proves nothing:\n%s", out)
	}
	waitCount := func(n *atomic.Int32, want int32) {
		deadline := time.Now().Add(2 * time.Second)
		for n.Load() < want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitCount(&accepted, 1)
	waitCount(&unixAccepted, 1)

	cmd := exec.Command(probe, "link")
	cmd.Env = env
	l, err := supervisor.LaunchContainedForTest(cmd, &supervisor.AppContainer{Profile: "aiios.test.wall.machine", GrantRead: []string{granted}})
	if err != nil {
		t.Fatalf("launch under the wall: %v", err)
	}
	defer func() {
		if err := l.Contained(); err != nil {
			t.Error(err)
		}
	}()
	l.Stdin.Close()
	answers := map[string]string{}
	sc := bufio.NewScanner(l.Stdout)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			answers[k] = v
		}
	}
	errOut, _ := supervisor.ReadAllStringForTest(l.Stderr)
	if state, err := cmd.Process.Wait(); err != nil || !state.Success() {
		t.Fatalf("the probe must exit cleanly: %v %v\nstderr: %s\nanswers: %v", err, state, errOut, answers)
	}
	if answers["link"] != "denied" {
		t.Errorf("a contained child opened the machine service's link: link=%q", answers["link"])
	}
	time.Sleep(200 * time.Millisecond)
	if accepted.Load() != 1 {
		t.Errorf("the link accepted %d connections; only the control's is expected", accepted.Load())
	}
	t.Logf("R9, measured: from inside the wall an AF_UNIX connect to a socket in an owner-only directory answered %q, and its listener saw %d connections (the control's is one)", answers["unix"], unixAccepted.Load())
}
