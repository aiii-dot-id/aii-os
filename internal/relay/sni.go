package relay

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"
)

var errPeeked = errors.New("relay: peeked")

// .
// .
// .
// .
func PeekServerName(c net.Conn, within time.Duration) (string, net.Conn, error) {
	_ = c.SetReadDeadline(time.Now().Add(within))
	rec := &recorder{r: c}
	var sni string
	_ = tls.Server(readOnly{rec}, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			sni = h.ServerName
			return nil, errPeeked
		},
	}).Handshake()
	_ = c.SetReadDeadline(time.Time{})
	if sni == "" {
		return "", nil, errors.New("relay: no server name in the client hello")
	}
	return sni, &replayConn{Conn: c, head: bytes.NewReader(rec.buf.Bytes())}, nil
}

type recorder struct {
	r   io.Reader
	buf bytes.Buffer
}

func (r *recorder) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.buf.Write(p[:n])
	return n, err
}

// .
// .
type readOnly struct{ r io.Reader }

func (c readOnly) Read(p []byte) (int, error)         { return c.r.Read(p) }
func (c readOnly) Write(p []byte) (int, error)        { return len(p), nil }
func (c readOnly) Close() error                       { return nil }
func (c readOnly) LocalAddr() net.Addr                { return nil }
func (c readOnly) RemoteAddr() net.Addr               { return nil }
func (c readOnly) SetDeadline(t time.Time) error      { return nil }
func (c readOnly) SetReadDeadline(t time.Time) error  { return nil }
func (c readOnly) SetWriteDeadline(t time.Time) error { return nil }

// .
type replayConn struct {
	net.Conn
	head *bytes.Reader
}

func (c *replayConn) Read(p []byte) (int, error) {
	if c.head.Len() > 0 {
		return c.head.Read(p)
	}
	return c.Conn.Read(p)
}
