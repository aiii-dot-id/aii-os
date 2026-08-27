package dashboard

import (
	"net"
	"sync"
)

// maxConcurrentConns bounds accepted TCP connections (D48, Sev
// 2026-08-26). The dashboard is an operator surface — a handful of
// tabs and their assets — so this is generous headroom, not a tuning
// knob; a flood stops at the accept gate instead of exhausting the
// daemon's file descriptors.
const maxConcurrentConns = 256

// limitListener admits at most cap concurrent connections. A parked
// Accept (cap reached) unparks when the listener closes — without
// that, shutdown would strand the serve goroutine on the slot send.
type limitListener struct {
	net.Listener
	slots     chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newLimitListener(ln net.Listener, n int) net.Listener {
	return &limitListener{Listener: ln, slots: make(chan struct{}, n), done: make(chan struct{})}
}

func (l *limitListener) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.done:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitConn{Conn: c, release: l.slots}, nil
}

func (l *limitListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return l.Listener.Close()
}

// limitConn returns its slot exactly once, when closed. http.Server
// closes every connection it accepted — hijacked WebSockets included,
// through the ws library's own close paths.
type limitConn struct {
	net.Conn
	once    sync.Once
	release chan struct{}
}

func (c *limitConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { <-c.release })
	return err
}
