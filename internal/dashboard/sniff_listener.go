package dashboard

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

type sniffListener struct {
	net.Listener
	target func(local net.Addr) string

	start  sync.Once
	stop   sync.Once
	ready  chan net.Conn
	failed chan error
	done   chan struct{}
}

func newSniffListener(ln net.Listener, target func(net.Addr) string) net.Listener {
	return &sniffListener{Listener: ln, target: target,
		ready: make(chan net.Conn), failed: make(chan error), done: make(chan struct{})}
}

const sniffWait = 2 * time.Second

func (l *sniffListener) Accept() (net.Conn, error) {
	l.start.Do(func() { go l.accept() })
	select {
	case c := <-l.ready:
		return c, nil
	case err := <-l.failed:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *sniffListener) accept() {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			select {
			case l.failed <- err:
				continue
			case <-l.done:
				return
			}
		}
		go l.sniff(c)
	}
}

func (l *sniffListener) sniff(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(sniffWait))
	var first [1]byte
	n, rerr := c.Read(first[:])
	_ = c.SetReadDeadline(time.Time{})
	switch {
	case n == 0:
		if ne, ok := rerr.(net.Error); !ok || !ne.Timeout() {
			c.Close()
			return
		}

	case first[0] == 0x16:
		c = &peekedConn{Conn: c, head: first[:n]}
	default:
		l.bounce(&peekedConn{Conn: c, head: first[:n]})
		return
	}
	select {
	case l.ready <- c:
	case <-l.done:
		c.Close()
	}
}

func (l *sniffListener) Close() error {
	l.stop.Do(func() { close(l.done) })
	return l.Listener.Close()
}

func (l *sniffListener) bounce(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	req, err := http.ReadRequest(bufio.NewReader(c))
	if err != nil {
		return
	}
	target := l.target(c.LocalAddr()) + req.URL.RequestURI()
	_, _ = io.WriteString(c, "HTTP/1.1 308 Permanent Redirect\r\nLocation: "+target+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
}

type peekedConn struct {
	net.Conn
	mu   sync.Mutex
	head []byte
}

func (p *peekedConn) Read(b []byte) (int, error) {
	p.mu.Lock()
	if len(p.head) > 0 {
		n := copy(b, p.head)
		p.head = p.head[n:]
		p.mu.Unlock()
		return n, nil
	}
	p.mu.Unlock()
	return p.Conn.Read(b)
}
