package relay

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
// .
type Client struct {
	Relay      string
	Name       string
	IdentityID string
	Key        witness.IdentityKey
	Env        *witness.PublicKeyEnvelope
	Envelope   []byte
	Local      string
	TLS        *tls.Config
	Now        func() time.Time
	Nonce      func() (string, error)

	mu        sync.Mutex
	connected bool
	since     time.Time
	lastErr   string
	sessions  int
}

// .
type State struct {
	Relay     string
	Connected bool
	Since     time.Time
	LastError string
}

// .
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return State{Relay: c.Relay, Connected: c.connected, Since: c.since, LastError: c.lastErr}
}

// .
// .
// .
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for {
		err := c.session(ctx)
		c.mu.Lock()
		c.connected = false
		if err != nil {
			c.lastErr = err.Error()
		}
		c.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Printf("relay: %s at %s: %v — reconnecting in %s", c.Name, c.Relay, err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

// .
// .
// .
func (c *Client) relayName() string {
	if c.TLS != nil && c.TLS.ServerName != "" {
		return c.TLS.ServerName
	}
	host, _, err := net.SplitHostPort(c.Relay)
	if err != nil {
		return c.Relay
	}
	return host
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.TLS != nil {
		cfg = c.TLS.Clone()
	}
	cfg.ServerName = c.relayName()
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 15 * time.Second}, Config: cfg}
	return d.DialContext(ctx, "tcp", c.Relay)
}

// .
// .
func (c *Client) session(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	now, nonce := c.Now, c.Nonce
	if now == nil {
		now = time.Now
	}
	if nonce == nil {
		nonce = randomNonce
	}
	n, err := nonce()
	if err != nil {
		return err
	}
	expires := now().Add(RegisterLifetime).UTC().Format(time.RFC3339)
	sig, err := witness.SignInput(c.Key, c.Env, RegisterInput(c.relayName(), c.Name, c.IdentityID, n, expires))
	if err != nil {
		return err
	}
	if err := WriteMessage(conn, Message{Type: "register", Name: c.Name, IdentityID: c.IdentityID, IdentityPublicKey: c.Envelope, Nonce: n, ExpiresAt: expires, IdentitySignature: &sig}); err != nil {
		return err
	}
	r := NewReader(conn)
	_ = conn.SetReadDeadline(now().Add(15 * time.Second))
	reply, err := ReadMessage(r)
	if err != nil {
		return fmt.Errorf("registration unanswered: %w", err)
	}
	switch reply.Type {
	case "registered":
	case "refused":
		return fmt.Errorf("registration refused: %s", reply.Reason)
	default:
		return fmt.Errorf("registration answered with %q", reply.Type)
	}
	c.mu.Lock()
	c.connected, c.since, c.lastErr = true, now(), ""
	c.sessions++
	c.mu.Unlock()
	log.Printf("relay: %s registered at %s", c.Name, c.Relay)

	var wmu sync.Mutex
	write := func(m Message) error {
		wmu.Lock()
		defer wmu.Unlock()
		return WriteMessage(conn, m)
	}
	pings := time.NewTicker(PingEvery)
	defer pings.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pings.C:
				if err := write(Message{Type: "ping"}); err != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	for {
		_ = conn.SetReadDeadline(now().Add(SilentFor))
		m, err := ReadMessage(r)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("control connection ended: %w", err)
		}
		switch m.Type {
		case "open":
			go c.attach(ctx, m.Conn)
		case "ping":
			_ = write(Message{Type: "pong"})
		case "pong":
		case "refused":
			return fmt.Errorf("the relay dropped the registration: %s", m.Reason)
		}
	}
}

// .
// .
func (c *Client) attach(ctx context.Context, id string) {
	up, err := c.dial(ctx)
	if err != nil {
		log.Printf("relay: attach %s: %v", id, err)
		return
	}
	defer up.Close()
	if err := WriteMessage(up, Message{Type: "attach", Conn: id, Name: c.Name}); err != nil {
		return
	}
	r := NewReader(up)
	_ = up.SetReadDeadline(time.Now().Add(AttachWithin))
	m, err := ReadMessage(r)
	if err != nil || m.Type != "attached" {
		log.Printf("relay: attach %s not accepted: %v %s", id, err, m.Reason)
		return
	}
	_ = up.SetReadDeadline(time.Time{})
	down, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", c.Local)
	if err != nil {
		log.Printf("relay: attach %s: the dashboard at %s: %v", id, c.Local, err)
		return
	}
	defer down.Close()
	splice(ctx, &bufferedConn{Conn: up, r: r}, down)
}

// .
// .
// .
func Splice(ctx context.Context, a, b net.Conn) { splice(ctx, a, b) }

// .
// .
func Carrier(c net.Conn, r io.Reader) net.Conn { return &bufferedConn{Conn: c, r: r} }

// .
func splice(ctx context.Context, a, b net.Conn) {
	stop := context.AfterFunc(ctx, func() { a.Close(); b.Close() })
	defer stop()
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); closeWrite(a); done <- struct{}{} }()
	go func() { io.Copy(b, a); closeWrite(b); done <- struct{}{} }()
	<-done
	<-done
}

func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	if bc, ok := c.(*bufferedConn); ok {
		if cw, ok := bc.Conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
			return
		}
	}
	_ = c.Close()
}

// .
type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func randomNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// .
var ErrNoRelay = errors.New("relay: none configured")
