package relay

import (
	"context"
	"net"
	"testing"
	"time"
)

// attachTo runs one attach against a relay the test plays, with deliver as
// the dashboard. It returns the relay's end of the carried connection and a
// channel closed when the attach has returned.
func attachTo(t *testing.T, ctx context.Context, deliver func(net.Conn) bool) (relaySide net.Conn, ended chan struct{}) {
	t.Helper()
	up, relaySide := net.Pipe()
	t.Cleanup(func() { relaySide.Close() })
	c := &Client{Relay: "fixture.test:443", Name: "fixture.example.test", Deliver: deliver}
	c.dialRelay = func(context.Context) (net.Conn, error) { return up, nil }
	ended = make(chan struct{})
	go func() { defer close(ended); c.attach(ctx, "fixture") }()
	if m, err := ReadMessage(NewReader(relaySide)); err != nil || m.Type != "attach" {
		t.Fatalf("the attach line: %+v %v", m, err)
	}
	if err := WriteMessage(relaySide, Message{Type: "attached"}); err != nil {
		t.Fatal(err)
	}
	return relaySide, ended
}

func within(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("waiting for %s", what)
	}
}

// THE SESSION IS HANDED TO THE DASHBOARD, WHOLE, AND HELD UNTIL IT ENDS. The
// client dialled the dashboard's own TLS port and spliced the bytes there,
// which made a loopback identity turn HTTPS on and restart to be reached.
// The dashboard now serves the connection itself: it receives the browser's
// bytes from the first one, the attach lasts as long as the dashboard holds
// the connection, and its address is the relay's, not this machine's.
func TestTheClientHandsTheSessionToTheDashboardAndHoldsItUntilItEnds(t *testing.T) {
	handed := make(chan net.Conn, 1)
	relaySide, ended := attachTo(t, context.Background(), func(c net.Conn) bool { handed <- c; return true })
	go func() { _, _ = relaySide.Write([]byte("\x16\x03\x01hello")) }()
	var session net.Conn
	select {
	case session = <-handed:
	case <-time.After(10 * time.Second):
		t.Fatal("the session was not handed to the dashboard")
	}
	buf := make([]byte, 8)
	_ = session.SetReadDeadline(time.Now().Add(5 * time.Second))
	if n, err := session.Read(buf); err != nil || string(buf[:n]) != "\x16\x03\x01hello" {
		t.Fatalf("the dashboard did not receive the browser's bytes from the first one: %q %v", buf[:n], err)
	}
	select {
	case <-ended:
		t.Fatal("the attach returned while the dashboard still held the session")
	case <-time.After(50 * time.Millisecond):
	}
	session.Close() // the dashboard's way of saying the session is over
	within(t, "the attach to return once the dashboard closed the session", ended)
}

// A stop ends a session the dashboard still holds, and a dashboard that is
// not taking sessions gets none: in both, the carried connection is closed.
func TestACarriedSessionEndsWithAStopAndIsNotLeftWithADashboardThatRefusesIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handed := make(chan net.Conn, 1)
	relaySide, ended := attachTo(t, ctx, func(c net.Conn) bool { handed <- c; return true })
	session := <-handed
	cancel()
	within(t, "the attach to return at the stop", ended)
	_ = session.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := session.Read(make([]byte, 1)); err == nil {
		t.Fatal("the dashboard's session outlived the stop")
	}
	_ = relaySide

	refused, gone := attachTo(t, context.Background(), func(net.Conn) bool { return false })
	within(t, "the attach to return when the dashboard takes no sessions", gone)
	_ = refused.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := refused.Read(make([]byte, 1)); err == nil {
		t.Fatal("a session the dashboard refused was left open at the relay")
	}
	nobody, left := attachTo(t, context.Background(), nil)
	within(t, "the attach to return with no dashboard at all", left)
	_ = nobody
}
