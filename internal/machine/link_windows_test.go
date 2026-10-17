//go:build windows

package machine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// A NAME ANOTHER PROCESS HOLDS IS NEVER SERVED. Pipe names are global, so
// a process may make the link's pipe first; the service's first instance
// (FILE_FLAG_FIRST_PIPE_INSTANCE) is refused while it stands, and the
// service refuses to share the name rather than serve beside it.
func TestAPipeNameAnotherProcessHoldsIsNeverServed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "machine")
	path, err := LinkPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := windows.UTF16PtrFromString(path)
	squat, err := windows.CreateNamedPipe(n, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT, windows.PIPE_UNLIMITED_INSTANCES, 512, 512, 0, nil)
	if err != nil {
		t.Fatalf("rig: make the squatting pipe: %v", err)
	}
	defer windows.CloseHandle(squat)
	start := time.Now()
	if h, err := Listen(dir); err == nil {
		h.Close()
		t.Fatal("the service served a pipe name another process holds")
	} else if !strings.Contains(err.Error(), "another process holds this pipe name") {
		t.Fatalf("the refusal must say why: %v", err)
	}
	t.Logf("refused after %s", time.Since(start).Round(time.Millisecond))
}

// A SUCCESSOR TAKES THE PIPE ONCE ITS PREDECESSOR'S INSTANCES ARE GONE. A
// client still holding a connected instance of the old service keeps the
// name; the successor waits, and takes it when that client lets go.
func TestASuccessorTakesThePipeOnceItsPredecessorLetsGo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "machine")
	old, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := old.Listener().Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	client, err := dialLink(context.Background(), old.Path(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	took := make(chan error, 1)
	go func() {
		h, err := Listen(dir)
		if err == nil {
			h.Close()
		}
		took <- err
	}()
	select {
	case err := <-took:
		t.Logf("the successor took the name while the old client still held its instance: %v", err)
	case <-time.After(time.Second):
		client.Close()
		if err := <-took; err != nil {
			t.Fatalf("once the old client let go the successor must take the name: %v", err)
		}
		t.Log("the successor waited for the old client's instance and then took the name")
	}
}

// THE PIPE IS CLOSED TO EVERY OTHER ACCOUNT: its DACL grants this account
// and SYSTEM, nothing else, and is protected.
func TestThePipeGrantsOnlyThisAccount(t *testing.T) {
	sa, err := ownerOnlyPipeSecurity()
	if err != nil {
		t.Fatal(err)
	}
	s := sa.SecurityDescriptor.String()
	tu, _ := windows.GetCurrentProcessToken().GetTokenUser()
	me := tu.User.Sid.String()
	if !strings.Contains(s, "D:P") || strings.Count(s, "(A;") != 2 || !strings.Contains(s, me) || !strings.Contains(s, ";;;SY)") {
		t.Fatalf("the link's security descriptor is not owner-only: %s", s)
	}
	for _, open := range []string{";;;WD)", ";;;AC)", ";;;BU)", ";;;AU)"} {
		if strings.Contains(s, open) {
			t.Fatalf("the link's DACL grants %s: %s", open, s)
		}
	}
}

// A CLIENT SPEAKS ONLY TO THIS ACCOUNT'S SERVICE: before a byte is read it
// checks that the pipe's server runs as this account and from the program
// it was told to expect. Here the server is this test, so the check
// passes for this program and refuses any other.
func TestTheLinkChecksWhoServesItBeforeSpeaking(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "machine")
	srv := NewServer(Options{Dir: dir})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	kp := newKey(t)
	slot := t.TempDir()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, lastErr = Register(ctx, dir, slot, kp, self, time.Second); lastErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("a client expecting this program must link: %v", lastErr)
	}
	if _, err := Register(ctx, dir, slot, kp, filepath.Join(filepath.Dir(self), "not-the-service.exe"), time.Second); err == nil || !strings.Contains(err.Error(), "not served by this account's machine service") {
		t.Fatalf("a client expecting another program must refuse the server before speaking: %v", err)
	}
}

func TestAClientThatLeftBeforeAcceptDoesNotStopTheLink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "machine")
	h, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	early, err := dialLink(context.Background(), h.Path(), "")
	if err != nil {
		t.Fatal(err)
	}
	early.Close()
	accepted := make(chan error, 1)
	go func() {
		c, err := h.Listener().Accept()
		if err == nil {
			c.Close()
		}
		accepted <- err
	}()
	time.Sleep(200 * time.Millisecond)
	next, err := dialLink(context.Background(), h.Path(), "")
	if err != nil {
		t.Fatalf("the client after one that left was not served: %v", err)
	}
	defer next.Close()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("Accept after a client that left: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Accept never answered the next client")
	}
}
