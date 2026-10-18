//go:build (linux && !android) || (darwin && !ios)

package machine

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// THE LINK SURVIVES A RESTART WHERE THE MANAGER KEEPS ITS LISTENER. A
// systemd socket unit passes the listener by systemd's protocol; the
// service adopts it, refuses one that is not the link, and a client that
// connects while no instance runs waits in the listener's queue instead of
// being refused. The child is this test binary, started as systemd starts
// a socket-activated service: the listener on descriptor 3, LISTEN_PID its
// own pid (the shell's, which exec keeps), LISTEN_FDS 1.
func TestAServiceAdoptsTheListenerItsManagerKeeps(t *testing.T) {
	if os.Getenv("AII_MACHINE_ADOPT_CHILD") != "" {
		adoptChild()
		return
	}
	root := shortDir(t)
	path := filepath.Join(root, "link")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	f, err := ln.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// A client connects BEFORE any service runs: the manager's listener
	// queues it.
	early, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("a client must connect while no service runs: %v", err)
	}
	defer early.Close()
	if _, err := early.Write([]byte("early\n")); err != nil {
		t.Fatal(err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(link string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `LISTEN_PID=$$ LISTEN_FDS=1 exec "$0" -test.run='^TestAServiceAdoptsTheListenerItsManagerKeeps$'`, self)
		cmd.Env = append(os.Environ(), "AII_MACHINE_ADOPT_CHILD=1", "AII_MACHINE_ADOPT_LINK="+link)
		cmd.ExtraFiles = []*os.File{f}
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if out := run(path); !strings.Contains(out, "adopted early") {
		t.Fatalf("the service did not adopt the passed listener and serve the queued client:\n%s", out)
	}
	if out := run(filepath.Join(root, "another")); !strings.Contains(out, "refused: the service manager passed") {
		t.Fatalf("a passed listener that is not the link must be refused:\n%s", out)
	}
}

func adoptChild() {
	ln, managed, err := systemdListener(os.Getenv("AII_MACHINE_ADOPT_LINK"))
	if err != nil {
		os.Stdout.WriteString("refused: " + err.Error() + "\n")
		return
	}
	if !managed || os.Getenv("LISTEN_FDS") != "" {
		os.Stdout.WriteString("not adopted, or the variables were left for children\n")
		return
	}
	c, err := ln.Accept()
	if err != nil {
		os.Stdout.WriteString("accept: " + err.Error() + "\n")
		return
	}
	line, _ := bufio.NewReader(c).ReadString('\n')
	os.Stdout.WriteString("adopted " + strings.TrimSpace(line) + "\n")
}

// A PROCESS THAT IS NOT THE MANAGER'S INSTANCE NEVER TAKES THE MANAGER'S
// LINK: it holds the lock (the manager's instance is not running) but
// finds a live listener at the path, and refuses rather than remove it.
// What a crash leaves — a socket file nothing listens on — it replaces.
func TestALinkTheManagerHoldsIsNeverReplaced(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	path, err := LinkPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := prepareLinkDir(path); err != nil {
		t.Fatal(err)
	}
	held, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(dir); !errors.Is(err, ErrManaged) {
		t.Fatalf("a live listener at the link: want ErrManaged, got %v", err)
	}
	// The manager's listener goes and leaves its file: a stale link.
	held.(*net.UnixListener).SetUnlinkOnClose(false)
	held.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("rig: the stale socket file must remain: %v", err)
	}
	h, err := Listen(dir)
	if err != nil {
		t.Fatalf("a stale link must be replaced: %v", err)
	}
	h.Close()
}
