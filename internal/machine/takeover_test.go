package machine

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// programFile stands in for the service's program: a copy of this test
// binary, a Go binary with a build record, at a path the test replaces as
// an updater or a package manager does (a new file renamed over the old).
func programFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "aii")
	copyExecutable(t, path)
	return path
}

func replaceProgram(t *testing.T, path string) {
	t.Helper()
	copyExecutable(t, path+".new")
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

func copyExecutable(t *testing.T, to string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

func (r *running) waiting() bool {
	r.srv.mu.Lock()
	defer r.srv.mu.Unlock()
	return r.srv.update != nil
}

func (r *running) handingOver() bool {
	select {
	case <-r.srv.handover:
		return true
	default:
		return false
	}
}

// linked proves kp for slot on a new raw link.
func linked(t *testing.T, dir, slot string) *rawLink {
	t.Helper()
	l := dialRaw(t, dir)
	if _, err := l.ask(MsgHandshake, l.signed(newKey(t), slot, false)); err != nil {
		t.Fatal(err)
	}
	return l
}

// A NEWER PROGRAM TAKES OVER WHEN THE SERVICE IS IDLE, never mid-request.
// While a request is in flight the service keeps serving and says the
// update waits; once the last request ends it hands over.
func TestTheTakeoverWaitsForIdle(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	a, b := slotDir(t, root, "identity-0"), slotDir(t, root, "identity-1")
	prog := programFile(t, root)
	g := newGate(MsgStatus)
	r := serve(t, Options{Dir: dir, Program: prog, StartupSlots: startupSlots(a, b), watchEvery: 20 * time.Millisecond, hold: g.hold})

	held := linked(t, dir, a)
	answered := make(chan error, 1)
	go func() { _, err := call(held.conn, 99, MsgStatus, nil); answered <- err }()
	<-g.entered
	replaceProgram(t, prog)
	waitFor(t, "the service notices the newer program", r.waiting)

	raw, err := linked(t, dir, b).ask(MsgStatus, nil)
	if err != nil {
		t.Fatalf("a waiting service must keep serving: %v", err)
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil || st.Update == nil || st.Update.Since == "" {
		t.Fatalf("the status must say the update waits: %s, %v", raw, err)
	}
	time.Sleep(200 * time.Millisecond)
	if r.done() || r.handingOver() {
		t.Fatal("the service handed over with a request in flight")
	}
	close(g.release)
	if err := <-answered; err != nil {
		t.Fatalf("the request in flight was not answered: %v", err)
	}
	if err := r.waitEnd(t, 5*time.Second); !errors.Is(err, ErrHandover) {
		t.Fatalf("once idle the service must hand over: %v", err)
	}
}

// THE OPERATOR FORCES A WAITING TAKEOVER: with a request still in flight,
// update.force begins the handover at once. With nothing waiting there is
// nothing to force, and the service says so.
func TestTheOperatorForcesAWaitingTakeover(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	a, b := slotDir(t, root, "identity-0"), slotDir(t, root, "identity-1")
	prog := programFile(t, root)
	g := newGate(MsgStatus)
	r := serve(t, Options{Dir: dir, Program: prog, StartupSlots: startupSlots(a, b), watchEvery: 20 * time.Millisecond, hold: g.hold})

	operator := linked(t, dir, b)
	if _, err := operator.ask(MsgUpdateForce, nil); refusal(err) != ReasonNoUpdate {
		t.Fatalf("forcing with nothing waiting: want %s, got %v", ReasonNoUpdate, err)
	}
	held := linked(t, dir, a)
	answered := make(chan error, 1)
	go func() { _, err := call(held.conn, 99, MsgStatus, nil); answered <- err }()
	<-g.entered
	replaceProgram(t, prog)
	waitFor(t, "the service notices the newer program", r.waiting)
	if r.handingOver() {
		t.Fatal("rig: the handover began before it was forced")
	}
	if _, err := operator.ask(MsgUpdateForce, nil); err != nil {
		t.Fatalf("update.force: %v", err)
	}
	waitFor(t, "the forced handover begins with a request in flight", r.handingOver)
	close(g.release)
	<-answered
	if err := r.waitEnd(t, 5*time.Second); !errors.Is(err, ErrHandover) {
		t.Fatalf("a forced handover ends the service: %v", err)
	}
}

// WHERE NO MANAGER RESTARTS THE SERVICE (Windows), IT STARTS ITS SUCCESSOR,
// having let the link go first, and exits once the successor holds it; an
// identity's link moves to the successor. The platform's own start is the
// seam; this is the same path the Windows build takes.
func TestAHandoverStartsItsSuccessorWhereNoManagerDoes(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	prog := programFile(t, root)
	var mu sync.Mutex
	var successor *running
	start := func() error {
		mu.Lock()
		defer mu.Unlock()
		successor = startAsync(t, Options{Dir: dir, StartupSlots: startupSlots(slot)})
		return nil
	}
	r := serve(t, Options{Dir: dir, Program: prog, StartupSlots: startupSlots(slot), Successor: start, watchEvery: 20 * time.Millisecond})
	c := runClient(t, ClientOptions{Dir: dir, Slot: slot, Key: newKey(t)})
	waitState(t, c, StateLinked)
	old := r.srv.instance

	replaceProgram(t, prog)
	if err := r.waitEnd(t, 20*time.Second); !errors.Is(err, ErrHandover) {
		t.Fatalf("the old service must hand over: %v", err)
	}
	mu.Lock()
	next := successor
	mu.Unlock()
	if next == nil || next.done() {
		t.Fatalf("no successor holds the link")
	}
	if l := dialRaw(t, dir); l.hello.Service == old {
		t.Fatal("the link is still the old instance's")
	}
	waitFor(t, "the identity links to the successor", func() bool {
		s := c.Snapshot()
		return s.State == StateLinked && s.Linked == 1
	})
}
