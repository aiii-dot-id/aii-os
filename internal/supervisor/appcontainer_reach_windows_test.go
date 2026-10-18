//go:build windows

package supervisor

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestAppContainerChildOpensNoServicePipeAndStartsNothingThroughTheShell(t *testing.T) {
	probe := buildWallProbe(t)
	granted := filepath.Dir(probe)
	outside := t.TempDir()
	marker := filepath.Join(outside, "started-outside.txt")
	script := filepath.Join(granted, "start.cmd")
	if err := os.WriteFile(script, []byte("@echo off\r\necho started> \""+marker+"\"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := func(wait time.Duration) bool {
		deadline := time.Now().Add(wait)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return false
	}

	name := fmt.Sprintf(`\\.\pipe\aiios-wall-%d`, os.Getpid())
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := windows.CreateNamedPipe(namePtr, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT, 1, 512, 512, 0, nil)
	if err != nil {
		t.Fatalf("serve a pipe: %v", err)
	}
	var connected atomic.Int32
	var closing atomic.Bool
	served := make(chan struct{})
	go func() {
		defer close(served)
		for {
			err := windows.ConnectNamedPipe(pipe, nil)
			if closing.Load() {
				return
			}
			if err != nil && err != windows.ERROR_PIPE_CONNECTED {
				return
			}
			connected.Add(1)
			_ = windows.DisconnectNamedPipe(pipe)
		}
	}()
	t.Cleanup(func() {
		// A blocked ConnectNamedPipe is woken by a client, not by closing
		// the handle under it.
		closing.Store(true)
		if f, err := os.OpenFile(name, os.O_RDWR, 0); err == nil {
			f.Close()
		}
		<-served
		windows.CloseHandle(pipe)
	})
	waitConnected := func(want int32) bool {
		deadline := time.Now().Add(2 * time.Second)
		for connected.Load() < want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		return connected.Load() == want
	}

	// The controls: the probe itself, uncontained.
	control := exec.Command(probe, "reach")
	control.Env = append(os.Environ(), "WALL_PIPE="+name, "WALL_SHELL_SCRIPT="+script)
	out, err := control.CombinedOutput()
	if err != nil {
		t.Fatalf("rig: the uncontained probe did not run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "pipe=ok") || !waitConnected(1) {
		t.Fatalf("rig: uncontained, the probe must open the pipe, or its refusal below proves nothing:\n%s", out)
	}
	if !strings.Contains(string(out), "shell-start=asked") || !started(15*time.Second) {
		t.Skipf("rig: uncontained, the shell started nothing in this session, so its refusal cannot be shown:\n%s", out)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(probe)
	cmd.Env = []string{"WALL_PIPE=" + name, "WALL_SHELL_SCRIPT=" + script}
	l, err := launchContained(cmd, &AppContainer{Profile: "aiios.test.wall.reach", GrantRead: []string{granted}}, 0)
	if err != nil {
		t.Fatalf("launch under the wall: %v", err)
	}
	defer func() {
		if err := l.contained(); err != nil {
			t.Error(err)
		}
	}()
	l.stdin.Close()
	answers := map[string]string{}
	sc := bufio.NewScanner(l.stdout)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			answers[k] = v
		}
	}
	errOut, _ := readAllString(l.stderr)
	if state, err := cmd.Process.Wait(); err != nil || !state.Success() {
		t.Fatalf("the probe must exit cleanly: %v %v\nstderr: %s\nanswers: %v", err, state, errOut, answers)
	}
	if answers["token"] != "appcontainer" {
		t.Fatalf("the probe did not run inside the wall: %v", answers)
	}
	if answers["pipe"] != "denied" {
		t.Errorf("a contained child opened a pipe a service of the same user serves: pipe=%q", answers["pipe"])
	}
	if connected.Load() != 1 {
		t.Errorf("the pipe's server saw %d connections; only the control's is expected", connected.Load())
	}
	if started(5 * time.Second) {
		t.Errorf("a program ran outside the wall at the contained child's request (the shell answered %q)", answers["shell-start"])
	}
	t.Logf("under the wall: pipe=%s shell-start=%s", answers["pipe"], answers["shell-start"])
	// THE SHELL'S HALF IS ANSWERED ONLY WHERE THE SHELL WAS ASKED. Where the
	// contained child could not load the shell's library at all, no request
	// reached the shell, and a test that passed there would be read as the
	// wall having refused one. Nothing ran outside (the marker, above) and
	// the pipe was refused; the shell's refusal is shown where the library
	// loads, which is the desktop's own session, where an identity runs.
	if why, ok := strings.CutPrefix(answers["shell-start"], "unavailable:"); ok {
		t.Skipf("inside the wall the shell's library did not load in this session (%s), so no request reached the shell and its refusal is not shown here; the pipe was refused and nothing ran outside. Run this test in the desktop's own session to put the question", why)
	}
}
