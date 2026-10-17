//go:build windows

package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/install"
	"golang.org/x/sys/windows"
)

// Exercise the daemon's actual watcher in a disposable child, never App.Run or
// an installed identity. Its request handle must outlive request delivery and
// remain until process exit, including the shutdown work after delivery.
func TestWindowsStopWatcherKeepsItsMarkerUntilProcessExit(t *testing.T) {
	if os.Getenv("AII_STOP_WATCHER_HELPER") == "1" {
		stopped := watchStopRequest()
		if stopped == nil {
			t.Fatal("watcher unavailable")
		}
		fmt.Println("watcher-ready")
		<-stopped
		fmt.Println("request-received")
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Run("request-only", func(t *testing.T) { testStopWatcher(t, false) })
	t.Run("public-stop", func(t *testing.T) { testStopWatcher(t, true) })
}

func testStopWatcher(t *testing.T, publicStop bool) {
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("SUDO_USER", "")
	root, err := install.Root()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "identity-0")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsStopWatcherKeepsItsMarkerUntilProcessExit$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "AII_STOP_WATCHER_HELPER=1")
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	var stopping chan error
	stopJoined := false
	t.Cleanup(func() {
		in.Close()
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
		if stopping != nil && !stopJoined {
			<-stopping
		}
	})
	lines := bufio.NewScanner(out)
	expect := func(want string) {
		t.Helper()
		if !lines.Scan() || lines.Text() != want {
			t.Fatalf("child handshake wanted %q, got %q (%v)", want, lines.Text(), lines.Err())
		}
	}
	expect("watcher-ready")
	if publicStop {
		stopping = make(chan error, 1)
		go func() { stopping <- install.Stop("identity-0") }()
	} else {
		// With no polling stop caller, no observer can retain the event and
		// conceal a watcher that closes its handle prematurely.
		var handle windows.Handle
		for _, name := range install.StopChannelNames(dir) {
			n, err := windows.UTF16PtrFromString(name)
			if err != nil {
				t.Fatal(err)
			}
			if h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, n); err == nil {
				handle = h
				break
			}
		}
		if handle == 0 {
			t.Fatal("watcher event unavailable")
		}
		signalErr := windows.SetEvent(handle)
		closeErr := windows.CloseHandle(handle)
		if signalErr != nil || closeErr != nil {
			t.Fatalf("signal: %v close: %v", signalErr, closeErr)
		}
	}
	expect("request-received")
	if present, err := install.StopChannelPresent(dir); !present || err != nil {
		t.Fatalf("request delivery retired the still-running daemon's marker: %v %v", present, err)
	}
	if _, err := fmt.Fprintln(in, "finish shutdown"); err != nil {
		t.Fatal(err)
	}
	// Drain the Go test runner's own final PASS before joining the child.
	for lines.Scan() {
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatal(err)
	}
	if stopping != nil {
		if err := <-stopping; err != nil {
			stopJoined = true
			t.Fatal(err)
		}
	}
	stopJoined = true
	if present, err := install.StopChannelPresent(dir); present || err != nil {
		t.Fatalf("exited daemon's marker was retained: %v %v", present, err)
	}
}
