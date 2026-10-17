//go:build linux

package pluginhost

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// A CONTAINED CHILD MAKES NO SOCKET. The wall's namespaces close the
// network and leave every socket on the file system in view, so the child
// runs under a system-call filter (syscall_filter_linux.go). Here the
// child makes each call itself and reports the kernel's answer:
// uncontained, a socket of every family, a pair, a datagram sent to a
// socket the test holds by its path, and an io_uring where the host has
// them; contained, each refused with the filter's own error, and nothing
// arrives at the test's socket. Typing into a terminal is tried on a
// descriptor that is not one: the kernel says ENOTTY, the filter EPERM
// before the kernel looks.
func TestAContainedNativeChildMakesNoSocket(t *testing.T) {
	requireWall(t)
	path := filepath.Join(shortDir(t), "journal.sock")
	addr, err := net.ResolveUnixAddr("unixgram", path)
	if err != nil {
		t.Fatal(err)
	}
	sock, err := net.ListenUnixgram("unixgram", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sock.Close() })
	arrived := func() bool {
		t.Helper()
		if err := sock.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		_, err := sock.Read(make([]byte, 64))
		return err == nil
	}
	asks := []string{"socket", "pair", "dgram=" + path, "uring", "tiocsti", "keyring"}

	out := runWallProbe(t, append([]string{fakechildBin, "reach"}, asks...))
	for _, want := range []string{"socket.unix=made", "socket.inet=made", "socket.inet6=made", "socket.netlink=made", "pair=made", "dgram=sent", "tiocsti=refused ENOTTY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rig: uncontained, the probe must report %q, or its refusal below proves nothing:\n%s", want, out)
		}
	}
	if !arrived() {
		t.Fatal("rig: uncontained, the probe's datagram must arrive at the test's socket")
	}
	// A host may forbid rings itself (kernel.io_uring_disabled); then the
	// filter's refusal of them cannot be told from the host's.
	rings := strings.Contains(out, "uring=made")
	// And so may a host forbid the keyrings (a container's own filter).
	keys := strings.Contains(out, "keyring=reached") && strings.Contains(out, "addkey=made")

	argv, wall, err := containArgv(context.Background(), append([]string{fakechildBin, "reach"}, asks...), nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	out = runContained(t, argv, wall)
	for _, want := range []string{"socket.unix=refused EACCES", "socket.inet=refused EACCES", "socket.inet6=refused EACCES", "socket.netlink=refused EACCES",
		"pair=refused EACCES", "dgram=refused EACCES", "tiocsti=refused EPERM"} {
		if !strings.Contains(out, want) {
			t.Errorf("inside the wall the probe must report %q:\n%s", want, out)
		}
	}
	if rings && !strings.Contains(out, "uring=refused ENOSYS") {
		t.Errorf("the contained child set up an io_uring, which makes sockets without calling socket():\n%s", out)
	}
	if arrived() {
		t.Error("a datagram from inside the wall arrived at a socket on the file system")
	}
	// The kernel's keyrings are divided by no namespace the wall makes.
	if keys && (!strings.Contains(out, "keyring=refused ENOSYS") || !strings.Contains(out, "addkey=refused ENOSYS")) {
		t.Errorf("the contained child reached the kernel's keyrings:\n%s", out)
	}
	if !keys {
		t.Logf("this host forbids the keyring calls itself, so the filter's refusal of them was not shown here")
	}
	if !rings {
		t.Logf("this host forbids io_uring itself, so the filter's refusal of it was not shown here")
	}
	t.Logf("inside the wall: %s", strings.Join(strings.Fields(out), " "))
}

// THE ACCOUNT'S RUNTIME DIRECTORY IS NOT IN THE CHILD'S VIEW, and the
// package's own material is when that is where it is kept. The directory
// here is the test's, named the way a session names its own; it holds a
// file that stands for a session's token and the child's program under a
// directory of its own.
func TestAContainedNativeChildDoesNotSeeTheRuntimeDirectory(t *testing.T) {
	requireWall(t)
	runtimeDir := shortDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	if err := os.WriteFile(filepath.Join(runtimeDir, "session-token"), []byte("canary"), 0o600); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(runtimeDir, "package")
	if err := os.Mkdir(own, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(own, "child")
	if err := os.WriteFile(child, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	asks := []string{"reach", "list=" + runtimeDir, "list=" + own}

	out := runWallProbe(t, append([]string{child}, asks...))
	if strings.Join(strings.Fields(out), " ") != "list=2 list=1" {
		t.Fatalf("rig: uncontained, the probe must see the token and its own directory:\n%s", out)
	}
	// The empty directory that stands in its place is not somewhere to
	// write, and neither is the package's own directory under it.
	asks = append(asks, "write="+filepath.Join(runtimeDir, "left-behind"), "write="+filepath.Join(own, "left-behind"))
	argv, wall, err := containArgv(context.Background(), append([]string{child}, asks...), nil, nativeFiles{own: []string{own}})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	if !strings.Contains(wall.Description, "runtime directory hidden") {
		t.Errorf("the wall does not say the runtime directory is hidden: %s", wall.Description)
	}
	out = runContained(t, argv, wall)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || lines[0] != "list=1" || lines[1] != "list=1" {
		t.Fatalf("inside the wall the runtime directory must hold the package's own directory and nothing else:\n%s", out)
	}
	for _, line := range lines[2:] {
		if !strings.HasPrefix(line, "write=refused") {
			t.Errorf("the contained child wrote under the hidden runtime directory: %s", line)
		}
	}
	for _, left := range []string{filepath.Join(runtimeDir, "left-behind"), filepath.Join(own, "left-behind")} {
		if _, err := os.Lstat(left); err == nil {
			t.Errorf("a file the contained child created is on the host: %s", left)
		}
	}
}

// A CONTAINED CHILD HAS NO TERMINAL OF THE HOST'S. A host started from a
// terminal has it as its controlling terminal, and so had every child:
// /dev/tty opened it, to read what the operator types there. The wall's
// /dev has pseudo-terminals of its own, so that was the one terminal of
// the host's in reach, and the child now runs in a session of its own
// with none. The terminal here is a pseudo-terminal the test makes and
// starts each run under, as a shell starts a program.
func TestAContainedNativeChildHasNoTerminalOfTheHosts(t *testing.T) {
	requireWall(t)
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("this host makes no pseudo-terminal: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("this host makes no pseudo-terminal: %v", err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("this host makes no pseudo-terminal: %v", err)
	}
	name := "/dev/pts/" + strconv.Itoa(n)
	run := func(argv []string, wall supervisor.Containment) string {
		t.Helper()
		terminal, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer terminal.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		// A session of its own whose controlling terminal is the test's:
		// what a program started from a shell has.
		cmd.Stdin = terminal
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		release, err := wall.Attach(cmd)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("the probe did not run: %v\n%s", err, out)
		}
		return strings.Join(strings.Fields(string(out)), " ")
	}
	asks := []string{fakechildBin, "reach", "open=/dev/tty", "open=" + name}
	if out := run(asks, supervisor.Containment{}); out != "open=opened open=opened" {
		t.Fatalf("rig: uncontained, the probe must open its controlling terminal and the terminal by its path, or its refusal below proves nothing: %s", out)
	}
	argv, wall, err := containArgv(context.Background(), asks, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	if out := run(argv, wall); out != "open=refused ENXIO open=refused ENOENT" {
		t.Errorf("inside the wall there must be no controlling terminal and no terminal of the host's by its path: %s", out)
	}
}

// A CUDA CHILD MAKES ITS OWN PACKET-SEQUENCED SOCKET AND STILL REACHES NO
// SERVICE. CUDA does not start without that socket, and the filter that
// admits it admits nothing else. The child here does what CUDA does at
// start (the socket, a bind to an abstract name, a listen) and then tries
// the rest: a stream, a pair, and a connection to a stream listener the
// test holds on the file system, standing for the bus. Under everyone's
// filter all of it is refused; under a CUDA child's the first works and
// the rest is refused as before. Both run on the same wall, so the only
// thing that differs is the filter the backend selects.
func TestACUDAChildMakesItsPacketSocketAndReachesNoService(t *testing.T) {
	requireWall(t)
	path := filepath.Join(shortDir(t), "bus.sock")
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
			return false
		}
		c.Close()
		return true
	}
	asks := []string{fakechildBin, "reach", "packets", "socket", "pair", "unix=" + path}
	if out := runWallProbe(t, asks); !strings.Contains(out, "packets=listening") || !strings.Contains(out, "unix=connected") || !accepted() {
		t.Fatalf("rig: uncontained, the probe must listen on its packet socket and connect to the test's stream listener:\n%s", out)
	}
	argv, wall, err := containArgv(context.Background(), asks, nil, nativeFiles{})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	if out := runContained(t, argv, wall); !strings.Contains(out, "packets=refused EACCES") {
		t.Errorf("a child with no CUDA backend made a packet-sequenced socket:\n%s", out)
	}
	if wall.Filter, err = syscallFilter(true); err != nil {
		t.Fatal(err)
	}
	out := runContained(t, argv, wall)
	if !strings.Contains(out, "packets=listening") {
		t.Errorf("under a CUDA child's filter the probe could not do what CUDA does at start:\n%s", out)
	}
	for _, want := range []string{"socket.unix=refused EACCES", "socket.inet=refused EACCES", "socket.netlink=refused EACCES", "pair=refused EACCES", "unix=refused"} {
		if !strings.Contains(out, want) {
			t.Errorf("under a CUDA child's filter the probe must still report %q:\n%s", want, out)
		}
	}
	if accepted() {
		t.Error("a stream listener on the file system accepted a connection from a CUDA child")
	}
	t.Logf("under a CUDA child's filter: %s", strings.Join(strings.Fields(out), " "))

	// The launch itself selects that filter from the signed backend, and
	// says on the containment line what it admits. This half needs a host
	// with CUDA's device nodes, or the profile is refused before a wall
	// is built.
	cuda := &AcceleratorProfile{Backend: "cuda"}
	if _, err := linuxAcceleratorDevices(cuda, os.DirFS("/dev")); err != nil {
		t.Logf("this host has no CUDA compute nodes, so the launch's own selection was not shown here: %v", err)
		return
	}
	_, selected, err := containArgv(context.Background(), asks, cuda, nativeFiles{})
	if err != nil {
		t.Fatal(err)
	}
	if string(selected.Filter) != string(wall.Filter) {
		t.Error("a launch with a CUDA backend did not select the filter that admits CUDA's socket")
	}
	if strings.Contains(selected.Description, "no socket of any kind") || !strings.Contains(selected.Description, "packet-sequenced local ones cuda cannot start without") {
		t.Errorf("the containment line promises a CUDA child what it does not get: %s", selected.Description)
	}
	_, plain, _ := containArgv(context.Background(), asks, nil, nativeFiles{})
	if !strings.Contains(plain.Description, "no socket of any kind") {
		t.Errorf("a child with no backend is not told it has no socket: %s", plain.Description)
	}
}
