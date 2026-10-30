//go:build linux && !android

package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// startRig is the home, its fake systemd and the slot processes it ran.
type startRig struct {
	t           *testing.T
	root, state string
}

func newStartRig(t *testing.T, mode string) *startRig {
	t.Helper()
	home := managerHome(t) // PATH is its directory of fakes alone
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(t.TempDir(), "aii") // named as the program, so the slot's process reads as an aii -dir <slot>
	if err := os.Symlink(exe, child); err != nil {
		t.Fatal(err)
	}
	r := &startRig{t: t, root: filepath.Join(home, install.Dir), state: t.TempDir()}
	r.mode(mode)
	script := fmt.Sprintf(`#!/bin/sh
S=%q; C=%q; R=%q
echo "systemctl $*" >> "$S/calls"
case "$*" in *enable*--now*aii-os@*) ;; *) exit 0 ;; esac
for a in "$@"; do case "$a" in aii-os@*) u=$a ;; esac; done
s=${u#aii-os@}; s=${s%%.service}
: > "$S/starting"
while [ -e "$S/hold" ]; do /bin/sleep 0.05; done
AII_TEST_SLOT_CHILD=$(/bin/cat "$S/mode") "$C" -dir "$R/$s" >> "$S/child.log" 2>&1 &
echo $! >> "$S/pids"
exit 0
`, r.state, child, r.root)
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.stopChildren)
	saved, savedWait := firstPort, servingWait
	t.Cleanup(func() { firstPort, servingWait = saved, savedWait })
	return r
}

// mode is what the next slot process does: "serve" or "idle".
func (r *startRig) mode(m string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.state, "mode"), []byte(m), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *startRig) stopChildren() {
	raw, _ := os.ReadFile(filepath.Join(r.state, "pids"))
	for _, f := range strings.Fields(string(raw)) {
		if pid, err := strconv.Atoi(f); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// slot is slot n's directory.
func (r *startRig) slot(n int) string { return filepath.Join(r.root, install.SlotName(n)) }

// startFrom has a new slot's search for a port begin at port.
func startFrom(port int) { firstPort = func(int) int { return port } }

// freePort is a port nothing holds this moment, chosen by the system.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// stranger is another program on port: an HTTP server that answers every
// request, the serving challenge too, with words of its own.
func stranger(t *testing.T, port int) func() {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, strings.Repeat("ab", 32))
	})}
	go srv.Serve(ln)
	var once sync.Once
	stop := func() { once.Do(func() { srv.Close() }) }
	t.Cleanup(stop)
	return stop
}

// A PORT ANOTHER PROGRAM HOLDS IS NOT WRITTEN FOR A NEW SLOT: the port the
// slot is offered is held by the test's own listener, and init writes the
// next one nothing holds, and says that one.
func TestInitPassesOverAPortAnotherProgramHolds(t *testing.T) {
	tempHome(t)
	saved := firstPort
	t.Cleanup(func() { firstPort = saved })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	held := ln.Addr().(*net.TCPAddr).Port
	startFrom(held)
	var stdout, stderr bytes.Buffer
	if code := runInit([]string{"--no-start"}, &stdout, &stderr); code != 0 {
		t.Fatalf("init exited %d: %s", code, stderr.String())
	}
	root, _ := install.Root()
	got := install.ConfiguredPort(filepath.Join(root, install.SlotName(0)), 0)
	if got == held || got <= held || !strings.Contains(stdout.String(), fmt.Sprintf("(dashboard port %d)", got)) {
		t.Fatalf("the held port %d: the slot was written on %d, and init said %q", held, got, stdout.String())
	}
}

// whenStarted is init's stdout, which asks, at the moment "Started" is
// written, whether the slot already answers with its own proof.
type whenStarted struct {
	bytes.Buffer
	port   int
	dir    string
	proved bool
}

func (w *whenStarted) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("Started ")) {
		w.proved = install.WaitServing(w.port, w.dir, 0) == nil
	}
	return w.Buffer.Write(p)
}

// THE HAPPY PATH: "Started" and the address are said once the slot's own
// process answers there with its proof, and not before.
func TestInitSaysStartedOnlyOnceThisIdentityAnswers(t *testing.T) {
	r := newStartRig(t, "serve")
	port := freePort(t)
	startFrom(port)
	out := &whenStarted{port: port, dir: r.slot(0)}
	var stderr bytes.Buffer
	if code := runInit(nil, out, &stderr); code != 0 {
		t.Fatalf("init exited %d: %s", code, stderr.String())
	}
	if !out.proved || !strings.Contains(out.String(), "Started "+install.Unit+"identity-0") || !strings.Contains(out.String(), fmt.Sprintf("Open http://127.0.0.1:%d to meet them.", port)) {
		t.Fatalf("started (answered with its proof when said: %v):\n%s", out.proved, out.String())
	}
}

// A PORT FREE AT THE SEARCH AND TAKEN BEFORE THE START, WHILE THIS
// IDENTITY'S PROCESS RUNS: the port answers and the slot's process is up,
// which is all the launcher used to ask; but what answers has not this
// identity's proof, and init says another program holds the port, how to
// move, and exits ExitNotServing — never "Started".
func TestInitSaysAPortTakenBeforeTheStartIsNotThisIdentitys(t *testing.T) {
	r := newStartRig(t, "idle")
	port := freePort(t)
	startFrom(port)
	servingWait = func(string) time.Duration { return 2 * time.Second }
	hold := filepath.Join(r.state, "hold")
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- runInit(nil, &stdout, &stderr) }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(r.state, "starting")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake systemd was never asked to start the slot")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stranger(t, port)
	if err := os.Remove(hold); err != nil {
		t.Fatal(err)
	}
	code := <-done
	if running, err := install.SlotRunning(r.slot(0)); err != nil || !running {
		t.Fatalf("rig: the slot's process is not running (%v, %v)", running, err)
	}
	words := stderr.String()
	if code != install.ExitNotServing || strings.Contains(stdout.String(), "Started") ||
		!strings.Contains(words, fmt.Sprintf("port %d is answered without this identity's proof: another program holds the port", port)) ||
		!strings.Contains(words, "aii dashboard-port -dir "+r.slot(0)+" <port>") || !strings.Contains(words, "aii register identity-0") {
		t.Fatalf("a port another program took before the start: exit %d\nstdout %s\nstderr %s", code, stdout.String(), words)
	}
}

// A START WHOSE PROCESS NEVER ANSWERS is said not to serve within the
// slot's wait, with its log and what to run next, and the typed status.
func TestInitSaysAStartThatNeverAnswersIsNotStarted(t *testing.T) {
	r := newStartRig(t, "idle")
	port := freePort(t)
	startFrom(port)
	servingWait = func(string) time.Duration { return time.Second }
	var stdout, stderr bytes.Buffer
	began := time.Now()
	code := runInit(nil, &stdout, &stderr)
	took := time.Since(began)
	want := fmt.Sprintf("init: the service manager started %sidentity-0, but this identity did not answer on port %d within 1s.\nIts log is %s. Once what it names is mended, start it again with:\n  aii register identity-0\n",
		install.Unit, port, filepath.Join(r.slot(0), "log", "aii.log"))
	if code != install.ExitNotServing || strings.Contains(stdout.String(), "Started") || !strings.HasSuffix(stderr.String(), want) || took > 15*time.Second {
		t.Fatalf("a start that never answers: exit %d after %s\nstdout %s\nstderr %s", code, took, stdout.String(), stderr.String())
	}
}

// AII REGISTER WAITS FOR THE SAME ANSWER. With another program on the
// slot's port, the slot's process cannot bind it, and register says so and
// exits ExitNotServing; with the port free again, it says "Started" once
// the slot answers with its proof.
func TestRegisterSaysStartedOnlyOnceThisIdentityAnswers(t *testing.T) {
	r := newStartRig(t, "serve")
	port := freePort(t)
	startFrom(port)
	if code := runInit([]string{"--no-start"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("init --no-start exited %d", code)
	}
	if got := install.ConfiguredPort(r.slot(0), 0); got != port {
		t.Fatalf("rig: the slot was written on %d, not %d", got, port)
	}
	servingWait = func(string) time.Duration { return 2 * time.Second }
	stop := stranger(t, port)
	var stdout, stderr bytes.Buffer
	if code := runRegister([]string{"identity-0"}, &stdout, &stderr); code != install.ExitNotServing || strings.Contains(stdout.String(), "Started") ||
		!strings.Contains(stderr.String(), fmt.Sprintf("register: the service manager started %sidentity-0, but port %d is answered without this identity's proof", install.Unit, port)) {
		t.Fatalf("register beside another program on the port: exit %d\nstdout %s\nstderr %s", code, stdout.String(), stderr.String())
	}
	stop()
	servingWait = install.ConfiguredStartWait
	stdout.Reset()
	stderr.Reset()
	if code := runRegister([]string{"identity-0"}, &stdout, &stderr); code != 0 || stdout.String() != "Started "+install.Unit+"identity-0\n" {
		t.Fatalf("register with the port free: exit %d\nstdout %s\nstderr %s", code, stdout.String(), stderr.String())
	}
}
