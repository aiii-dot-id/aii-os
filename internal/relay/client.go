package relay

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	mathrand "math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/witness"
)

type Client struct {
	Relay      string
	Name       string
	IdentityID string
	Key        witness.IdentityKey
	Env        *witness.PublicKeyEnvelope
	Envelope   []byte

	Deliver func(net.Conn) bool
	TLS     *tls.Config

	Tune      func() Tuning
	Now       func() time.Time
	Nonce     func() (string, error)
	dialRelay func(context.Context) (net.Conn, error)
	random    func() float64

	mu        sync.Mutex
	connected bool
	since     time.Time
	lastErr   string
	refused   string
	servers   []SessionServer
	serversTo time.Time
	sessions  int
	wake      chan struct{}
	gate      carryGate
}

func (c *Client) Wake() {
	select {
	case c.woken() <- struct{}{}:
	default:
	}
}

func (c *Client) woken() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.wake == nil {
		c.wake = make(chan struct{}, 1)
	}
	return c.wake
}

type Tuning struct {
	PingSeconds int

	BackoffBase, BackoffMax time.Duration

	Answer time.Duration

	Carried, AttachesPerSecond, AttachBurst int
}

func (c *Client) tuning() Tuning {
	var t Tuning
	if c.Tune != nil {
		t = c.Tune()
	}
	if t.BackoffBase <= 0 {
		t.BackoffBase = BackoffBase
	}
	if t.BackoffMax <= 0 {
		t.BackoffMax = BackoffMax
	}
	if t.BackoffMax < t.BackoffBase {
		t.BackoffMax = t.BackoffBase
	}
	if t.Answer <= 0 {
		t.Answer = AnswerWithin
	}
	if t.Carried <= 0 {
		t.Carried = CarriedAtOnce
	}
	if t.AttachesPerSecond <= 0 {
		t.AttachesPerSecond = AttachesPerSecond
	}
	if t.AttachBurst <= 0 {
		t.AttachBurst = AttachBurst
	}
	return t
}

type carryGate struct {
	mu       sync.Mutex
	carrying int
	tokens   float64
	at       time.Time
	declined uint64
	saying   bool
}

func (c *Client) admitOpen(now time.Time) bool {
	t := c.tuning()
	g := &c.gate
	g.mu.Lock()
	defer g.mu.Unlock()
	burst := float64(t.AttachBurst)
	if g.at.IsZero() {
		g.tokens = burst
	} else if since := now.Sub(g.at); since > 0 {
		g.tokens += since.Seconds() * float64(t.AttachesPerSecond)
	}
	g.tokens = min(g.tokens, burst)
	g.at = now
	if g.carrying < t.Carried && g.tokens >= 1 {
		g.tokens--
		g.carrying++
		return true
	}
	g.declined++
	if !g.saying {
		g.saying = true
		time.AfterFunc(DeclinedSaidEvery, c.sayDeclined)
	}
	return false
}

func (c *Client) sayDeclined() {
	t := c.tuning()
	g := &c.gate
	g.mu.Lock()
	n := g.declined
	g.declined, g.saying = 0, false
	g.mu.Unlock()
	logsink.Warn("relay.refusal", "%s at %s: %d browser connection(s) were not carried in the last %s: more arrived than this identity takes at once (certificate.relay_carried_max, %d) or answers in a second (certificate.relay_attaches_per_second, %d, in bursts of %d)",
		c.Name, c.Relay, n, DeclinedSaidEvery, t.Carried, t.AttachesPerSecond, t.AttachBurst)
}

func (c *Client) carriedDone() {
	c.gate.mu.Lock()
	c.gate.carrying--
	c.gate.mu.Unlock()
}

func (c *Client) draw() float64 {
	if c.random != nil {
		return c.random()
	}
	return mathrand.Float64()
}

func pingWait(interval time.Duration, draw float64) time.Duration {
	return interval*4/5 + time.Duration(draw*float64(interval/5))
}

func pingInterval(set int, stated, silent time.Duration) (interval time.Duration, clamped bool) {
	interval = stated
	if interval <= 0 {
		interval = PingEvery
	}
	if set > 0 {
		interval = time.Duration(set) * time.Second
	}
	if half := silent / 2; half > 0 && interval > half {
		return half, set > 0
	}
	return interval, false
}

func heldStated(ping, silent time.Duration) (time.Duration, time.Duration) {
	if ping > 0 {
		ping = min(max(ping, StatedPingLeast), StatedPingMost)
	}
	if silent > 0 {
		silent = min(max(silent, 2*StatedPingLeast), 2*StatedPingMost)
	}
	return ping, silent
}

func relaysWords(s string, most int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= most {
			b.WriteString("…")
			break
		}
		if r < 0x20 || r == 0x7f || !utf8.ValidRune(r) || r == utf8.RuneError {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

func backoffWait(base, max time.Duration, failures int, draw float64) time.Duration {
	wait := base
	for i := 0; i < failures && wait < max; i++ {
		wait *= 2
	}
	if wait > max {
		wait = max
	}
	return wait/2 + time.Duration(draw*float64(wait/2))
}

type Refused struct {
	Code       string
	Reason     string
	RetryAfter time.Duration
}

var refusalWords = map[string]string{
	"not_assigned":       "the relay does not hold this name: its route has lapsed, was withdrawn, or leads to another relay",
	"owner":              "the relay holds this name for another key",
	"window":             "this machine's clock and the relay's disagree by more than a registration allows",
	"nonce":              "the relay had already seen this registration",
	"signature":          "the relay could not verify the registration's signature",
	"changed":            "the name's owner changed while it was being registered",
	"capacity":           "the relay is at its limit and asked for a later attempt",
	"unknown_connection": "the relay no longer holds the connection it asked to be carried",
	"malformed":          "the relay could not read the registration",
}

func (e *Refused) Error() string {
	words, ok := refusalWords[e.Code]
	switch {
	case ok && e.Reason != "":
		return fmt.Sprintf("%s (%s)", words, e.Reason)
	case ok:
		return words
	case e.Reason != "":
		return "the relay refused: " + e.Reason
	}
	return "the relay refused and gave no reason"
}

func refusal(m Message) *Refused {
	e := &Refused{Code: relaysWords(m.Code, 40), Reason: relaysWords(m.Reason, 200)}
	if m.RetryAfterSeconds > 0 {
		e.RetryAfter = time.Duration(m.RetryAfterSeconds) * time.Second
	}
	return e
}

type State struct {
	Relay     string
	Connected bool
	Since     time.Time
	LastError string

	Refused string
}

func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return State{Relay: c.Relay, Connected: c.connected, Since: c.since, LastError: c.lastErr, Refused: c.refused}
}

func (c *Client) SessionServers() ([]SessionServer, time.Time) {
	now := c.Now
	if now == nil {
		now = time.Now
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.serversTo.IsZero() && !now().Before(c.serversTo) {
		return nil, c.serversTo
	}
	return append([]SessionServer(nil), c.servers...), c.serversTo
}

func (c *Client) holdServers(m Message) {
	to, _ := time.Parse(time.RFC3339, m.SessionServersExpireAt)
	c.mu.Lock()
	c.servers, c.serversTo = m.SessionServers, to
	c.mu.Unlock()
}

func (c *Client) Run(ctx context.Context) {

	var carried sync.WaitGroup
	defer carried.Wait()
	failures := 0
	for {
		proven, err := c.session(ctx, &carried)
		var refused *Refused
		c.mu.Lock()
		c.connected = false
		if err != nil {
			c.lastErr, c.refused = err.Error(), ""
			if errors.As(err, &refused) {
				c.refused = refused.Code
			}
		}
		c.mu.Unlock()
		if ctx.Err() != nil {
			return
		}

		if proven {
			failures = 0
		}
		t := c.tuning()
		wait := backoffWait(t.BackoffBase, t.BackoffMax, failures, c.draw())

		if refused != nil && refused.RetryAfter > 0 {
			wait += min(refused.RetryAfter, t.BackoffMax)
		}
		failures++
		if err != nil {
			logsink.Warn("relay.error", "%s at %s: %v — reconnecting in %s", c.Name, c.Relay, err, wait)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-c.woken():

		}
	}
}

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
	if c.dialRelay != nil {
		return c.dialRelay(ctx)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.TLS != nil {
		cfg = c.TLS.Clone()
	}
	cfg.ServerName = c.relayName()
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: c.tuning().Answer}, Config: cfg}
	return d.DialContext(ctx, "tcp", c.Relay)
}

func (c *Client) session(ctx context.Context, carried *sync.WaitGroup) (proven bool, err error) {
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	conn, err := c.dial(ctx)
	if err != nil {
		return false, err
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
		return false, err
	}

	expires := now().Add(RegisterLifetime).UTC().Format(time.RFC3339)
	sig, err := witness.SignInput(c.Key, c.Env, RegisterInput(c.relayName(), c.Name, c.IdentityID, n, expires))
	if err != nil {
		return false, err
	}
	if err := WriteMessage(conn, Message{Type: "register", Name: c.Name, IdentityID: c.IdentityID, IdentityPublicKey: c.Envelope, Nonce: n, ExpiresAt: expires, IdentitySignature: &sig}); err != nil {
		return false, err
	}
	r := NewReader(conn)
	_ = conn.SetReadDeadline(now().Add(c.tuning().Answer))
	reply, err := ReadMessage(r)
	if err != nil {
		return false, fmt.Errorf("registration unanswered: %w", err)
	}
	switch reply.Type {
	case "registered":
	case "refused":
		return false, refusal(reply)
	default:
		return false, fmt.Errorf("registration answered with %q", reply.Type)
	}

	stated, silent := heldStated(time.Duration(reply.PingSeconds)*time.Second, time.Duration(reply.SilentSeconds)*time.Second)
	if silent <= 0 {
		silent = SilentFor
	}
	c.mu.Lock()
	c.connected, c.since, c.lastErr, c.refused = true, now(), "", ""
	c.sessions++
	c.mu.Unlock()

	c.holdServers(reply)
	logsink.Info("relay.start", "%s registered at %s", c.Name, c.Relay)

	var wmu sync.Mutex
	write := func(m Message) error {
		wmu.Lock()
		defer wmu.Unlock()
		return WriteMessage(conn, m)
	}
	workers.Go(func() {
		told := false
		for {

			interval, clamped := pingInterval(c.tuning().PingSeconds, stated, silent)
			if clamped && !told {
				told = true
				logsink.Warn("relay.refusal", "%s at %s: certificate.relay_ping_seconds is longer than half the %s this relay waits before dropping a silent connection; pinging every %s instead", c.Name, c.Relay, silent, interval)
			}
			wait := time.NewTimer(pingWait(interval, c.draw()))
			select {
			case <-ctx.Done():
				wait.Stop()
				return
			case <-c.woken():

				wait.Stop()
				_ = conn.SetReadDeadline(now().Add(c.tuning().Answer))
				if err := write(Message{Type: "ping"}); err != nil {
					conn.Close()
					return
				}
			case <-wait.C:
				if err := write(Message{Type: "ping"}); err != nil {
					conn.Close()
					return
				}
			}
		}
	})
	for {
		_ = conn.SetReadDeadline(now().Add(silent))
		m, err := ReadMessage(r)
		if err != nil {
			if ctx.Err() != nil {
				return proven, nil
			}
			return proven, fmt.Errorf("control connection ended: %w", err)
		}
		switch m.Type {
		case "open":

			if !c.admitOpen(now()) {
				continue
			}
			carried.Go(func() {
				defer c.carriedDone()
				c.attach(parent, m.Conn)
			})
		case "ping":
			_ = write(Message{Type: "pong"})
		case "pong":
			proven = true
		case "servers":
			c.holdServers(m)
		case "refused":
			return proven, refusal(m)
		}
	}
}

func (c *Client) attach(ctx context.Context, id string) {
	up, err := c.dial(ctx)
	if err != nil {
		logsink.Warn("relay.error", "attach %s: %v", relaysWords(id, 64), err)
		return
	}
	defer up.Close()
	stop := context.AfterFunc(ctx, func() { up.Close() })
	defer stop()
	if err := WriteMessage(up, Message{Type: "attach", Conn: id, Name: c.Name}); err != nil {
		return
	}
	r := NewReader(up)
	_ = up.SetReadDeadline(time.Now().Add(c.tuning().Answer))
	m, err := ReadMessage(r)
	if err != nil || m.Type != "attached" {
		logsink.Warn("relay.refusal", "attach %s not accepted: %v %s", relaysWords(id, 64), err, relaysWords(m.Reason, 200))
		return
	}
	_ = up.SetReadDeadline(time.Time{})

	carried := &carriedConn{Conn: &bufferedConn{Conn: up, r: r}, ended: make(chan struct{})}
	if c.Deliver == nil || !c.Deliver(carried) {
		logsink.Warn("relay.error", "attach %s: the dashboard is not taking carried sessions", relaysWords(id, 64))
		return
	}
	select {
	case <-carried.ended:
	case <-ctx.Done():
		carried.Close()
	}
}

type carriedConn struct {
	net.Conn
	ended chan struct{}
	once  sync.Once
}

func (c *carriedConn) Close() error {
	c.once.Do(func() { close(c.ended) })
	return c.Conn.Close()
}

func Splice(ctx context.Context, a, b net.Conn) { splice(ctx, a, b) }

func Carrier(c net.Conn, r io.Reader) net.Conn { return &bufferedConn{Conn: c, r: r} }

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

var ErrNoRelay = errors.New("relay: none configured")
