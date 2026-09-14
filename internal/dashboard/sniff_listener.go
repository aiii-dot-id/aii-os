package dashboard

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
type sniffListener struct {
	net.Listener
	target func(local net.Addr) string
}

func newSniffListener(ln net.Listener, target func(net.Addr) string) net.Listener {
	return &sniffListener{Listener: ln, target: target}
}

// .
// .
const sniffWait = 2 * time.Second

func (l *sniffListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		_ = c.SetReadDeadline(time.Now().Add(sniffWait))
		var first [1]byte
		n, rerr := c.Read(first[:])
		_ = c.SetReadDeadline(time.Time{})
		if n == 0 {
			if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
				return c, nil
			}
			c.Close()
			continue
		}
		pc := &peekedConn{Conn: c, head: first[:n]}
		if first[0] == 0x16 {
			return pc, nil
		}
		go l.bounce(pc)
	}
}

// .
// .
// .
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

// .
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
