package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const (
	StateConnecting = "connecting"
	StateLinked     = "linked"
	StateRefused    = "refused"
	StateDown       = "not running"
)

const (
	NeederStatusLine = "the Plugins page's machine service line"
	NeederForce      = "the Plugins page's Update now"
)

var StoreMessages = []string{MsgNeed, MsgRelease}

func NeederPlugin(id string) string { return "plugin " + id }

const (
	defaultPollEvery     = 3 * time.Second
	defaultRelaunchAfter = 5 * time.Second
	backoffFirst         = 250 * time.Millisecond
	backoffMax           = 8 * time.Second

	refusedBackoff = 30 * time.Second
	dialTimeout    = 2 * time.Second
	callTimeout    = 10 * time.Second
)

var ErrNotLinked = errors.New("this identity is not linked to the machine service")

type ClientOptions struct {
	Dir string

	Slot string

	Key *crypto.KeyPair

	Build Build

	ServerImage func() string

	Relaunch      func() error
	RelaunchAfter time.Duration

	PollEvery time.Duration

	OnChange func()

	register bool
	budget   *budget

	dial func(context.Context) (net.Conn, error)
}

type Snapshot struct {
	State    string
	Service  Build
	Identity Build
	Linked   int
	Update   *Update
	Refusal  *Refusal

	Missing []*MissingError

	Error string

	Failing bool

	StartError string
	Since      time.Time

	Store string
	Refs  []LinkRef

	speaks Speaks
}

type Client struct {
	opts ClientOptions
	asks chan ask

	mu   sync.Mutex
	snap Snapshot
}

type ask struct {
	method string
	params any
	reply  chan answer
}

type answer struct {
	raw json.RawMessage
	err error
}

func NewClient(opts ClientOptions) *Client {
	if opts.Build == (Build{}) {
		opts.Build = Self()
	}
	if opts.PollEvery <= 0 {
		opts.PollEvery = defaultPollEvery
	}
	if opts.RelaunchAfter <= 0 {
		opts.RelaunchAfter = defaultRelaunchAfter
	}
	if opts.budget == nil {
		b := newBudget(opts.Dir)
		opts.budget = &b
	}
	return &Client{opts: opts, asks: make(chan ask), snap: Snapshot{State: StateConnecting, Identity: opts.Build, Since: time.Now()}}
}

func (c *Client) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap
}

func (c *Client) Covers(needer string, needs []string) error {
	s := c.Snapshot()
	if s.State != StateLinked {
		return ErrNotLinked
	}
	if m := covers(s.speaks, needer, needs, s.Service, s.Identity); m != nil {
		return m
	}
	return nil
}

func (c *Client) ForceUpdate(ctx context.Context) error {
	if err := c.Covers(NeederForce, []string{MsgUpdateForce}); err != nil {
		return err
	}
	_, err := c.request(ctx, MsgUpdateForce, nil)
	return err
}

func (c *Client) Need(ctx context.Context, p NeedParams) (NeedResult, error) {
	if err := c.Covers(NeederPlugin(p.Plugin), StoreMessages); err != nil {
		return NeedResult{}, err
	}
	raw, err := c.request(ctx, MsgNeed, p)
	if err != nil {
		return NeedResult{}, err
	}
	var res NeedResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return NeedResult{}, fmt.Errorf("the machine service's answer to a need does not read: %w", err)
	}
	return res, nil
}

func (c *Client) Release(ctx context.Context, refs []LinkRef) error {
	if err := c.Covers(NeederPlugin("references"), []string{MsgRelease}); err != nil {
		return err
	}
	_, err := c.request(ctx, MsgRelease, ReleaseParams{Refs: refs})
	return err
}

func (c *Client) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	a := ask{method: method, params: params, reply: make(chan answer, 1)}
	select {
	case c.asks <- a:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-a.reply:
		return r.raw, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) set(f func(*Snapshot)) {
	c.mu.Lock()
	before := c.snap
	f(&c.snap)
	if c.snap.State != before.State {
		c.snap.Since = time.Now()
	}
	changed := !sameSnapshot(before, c.snap)
	c.mu.Unlock()
	if changed && c.opts.OnChange != nil {
		c.opts.OnChange()
	}
}

func sameSnapshot(a, b Snapshot) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb) && a.State == b.State
}

func (c *Client) Run(ctx context.Context) {
	logsink.Info("machine.start", "linking to the machine service at %s", c.opts.Dir)
	backoff := backoffFirst
	lost := time.Now()
	for ctx.Err() == nil {
		conn, hello, err := c.dial(ctx)
		if err == nil {
			backoff = backoffFirst
			if c.opts.Relaunch != nil {
				if err := c.opts.budget.clear(); err != nil {
					logsink.Warn("machine.error", "the hosts' start record was not cleared: %v", err)
				}
			}
			c.serveLink(ctx, conn, hello)
			conn.Close()
			lost = time.Now()
			continue
		}
		var ref *Refusal
		if errors.As(err, &ref) {
			c.set(func(s *Snapshot) {
				*s = Snapshot{State: StateRefused, Identity: s.Identity, Refusal: ref, Since: s.Since}
			})
		} else {
			down := Snapshot{State: StateDown, Error: err.Error()}
			if c.opts.Relaunch != nil && time.Since(lost) >= c.opts.RelaunchAfter {
				down = c.relaunch(down)
			}
			c.set(func(s *Snapshot) { down.Identity, down.Since = s.Identity, s.Since; *s = down })
		}
		delay := backoff
		if ref != nil {
			delay = refusedBackoff
		}
		if !c.wait(ctx, delay) {
			return
		}
		backoff = min(backoff*2, backoffMax)
		if c.opts.Relaunch != nil {

			backoff = min(backoff, c.opts.RelaunchAfter)
		}
	}
}

func (c *Client) relaunch(down Snapshot) Snapshot {
	v, err := c.opts.budget.take()
	switch {
	case errors.Is(err, errNoServiceDir):
		down.StartError = err.Error()
	case err != nil:
		logsink.Warn("machine.error", "the hosts' start record does not read or write: %v", err)
		down.StartError = "the hosts' start record: " + err.Error()
	case v.exhausted:
		why := down.Error
		if f := readFailure(c.opts.Dir); f != nil {
			why = f.Error + " (" + f.At + ")"
		}
		down.Failing = true
		down.Error = fmt.Sprintf("the machine service keeps failing: started %d times in the last %s and never answered; no identity starts it again until that window passes. Its last failure: %s", v.starts, c.opts.budget.window, why)
	case v.start:
		logsink.Info("machine.decision", "the machine service's link is gone; starting it (start %d of %d in %s)", v.starts, c.opts.budget.limit, c.opts.budget.window)
		if rerr := c.opts.Relaunch(); rerr != nil {
			logsink.Warn("machine.error", "the machine service could not be started: %v", rerr)
			down.StartError = rerr.Error()
		}
	}
	return down
}

func (c *Client) wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		case a := <-c.asks:
			a.reply <- answer{err: ErrNotLinked}
		}
	}
}

func (c *Client) dial(ctx context.Context) (net.Conn, helloParams, error) {
	var conn net.Conn
	var err error
	if c.opts.dial != nil {
		conn, err = c.opts.dial(ctx)
	} else {
		var path string
		if path, err = LinkPath(c.opts.Dir); err != nil {
			return nil, helloParams{}, err
		}
		image := ""
		if c.opts.ServerImage != nil {
			image = c.opts.ServerImage()
		}
		conn, err = dialLink(ctx, path, image)
	}
	if err != nil {
		return nil, helloParams{}, err
	}
	hello, err := handshakeOn(conn, c.opts.Key, c.opts.Slot, c.opts.Build, c.opts.register)
	if err != nil {
		conn.Close()
		return nil, helloParams{}, err
	}
	return conn, hello, nil
}

func handshakeOn(conn net.Conn, key *crypto.KeyPair, slot string, build Build, register bool) (helloParams, error) {
	_ = conn.SetDeadline(time.Now().Add(callTimeout))
	defer conn.SetDeadline(time.Time{})
	m, err := readMessage(conn)
	if err != nil {
		return helloParams{}, fmt.Errorf("read the service's hello: %w", err)
	}
	var hello helloParams
	if m.Method != MsgHello || json.Unmarshal(m.Params, &hello) != nil || hello.Nonce == "" || hello.Service == "" {
		return helloParams{}, errors.New("the link did not begin with the service's hello")
	}
	fp := key.Fingerprint()
	sig, err := crypto.Sign(key, linkInput(hello.Service, hello.Nonce, fp))
	if err != nil {
		return helloParams{}, fmt.Errorf("sign the handshake: %w", err)
	}
	p := handshakeParams{Identity: fp, PublicKey: key.PublicKeyB64(), Slot: slot, Nonce: hello.Nonce,
		Signature: base64.StdEncoding.EncodeToString(sig), Speaks: speaks(), Build: build, Register: register}
	if _, err := call(conn, 1, MsgHandshake, p); err != nil {
		return helloParams{}, err
	}
	return hello, nil
}

func call(conn net.Conn, id int, method string, params any) (json.RawMessage, error) {
	_ = conn.SetDeadline(time.Now().Add(callTimeout))
	defer conn.SetDeadline(time.Time{})
	raw := json.RawMessage(strconv.Itoa(id))
	var p json.RawMessage
	if params != nil {
		p = mustJSON(params)
	}
	if err := writeMessage(conn, message{ID: raw, Method: method, Params: p}); err != nil {
		return nil, err
	}
	m, err := readMessage(conn)
	if err != nil {
		return nil, err
	}
	if string(m.ID) != string(raw) {
		return nil, fmt.Errorf("the service answered request %s with %s", raw, m.ID)
	}
	if m.Error != nil {
		if m.Error.Data != nil {
			return nil, m.Error.Data
		}
		return nil, &Refusal{Reason: ReasonBadRequest, Message: m.Error.Message}
	}
	return m.Result, nil
}

func (c *Client) serveLink(ctx context.Context, conn net.Conn, hello helloParams) {
	var missing []*MissingError
	for _, n := range []struct {
		needer string
		needs  []string
	}{{NeederStatusLine, []string{MsgStatus}}, {NeederForce, []string{MsgUpdateForce}}} {
		if m := covers(hello.Speaks, n.needer, n.needs, hello.Build, c.opts.Build); m != nil {
			missing = append(missing, m)
		}
	}
	c.set(func(s *Snapshot) {
		*s = Snapshot{State: StateLinked, Service: hello.Build, Identity: s.Identity, Linked: s.Linked, Update: s.Update, Missing: missing, Since: s.Since, speaks: hello.Speaks}
	})
	logsink.Info("machine.session", "linked to the machine service %s (%s)", hello.Service, hello.Build)
	defer logsink.Info("machine.session", "the link to the machine service %s ended", hello.Service)
	id := 1
	t := time.NewTicker(c.opts.PollEvery)
	defer t.Stop()
	for {
		id++
		if !c.observe(conn, hello, id) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case a := <-c.asks:
			id++
			raw, err := call(conn, id, a.method, a.params)
			a.reply <- answer{raw: raw, err: err}
			var ref *Refusal
			if err != nil && !errors.As(err, &ref) {
				return
			}
		}
	}
}

func (c *Client) observe(conn net.Conn, hello helloParams, id int) bool {
	if !hello.Speaks.has(MsgStatus) {
		_ = conn.SetReadDeadline(time.Now().Add(c.opts.PollEvery))
		var one [1]byte
		_, err := conn.Read(one[:])
		_ = conn.SetReadDeadline(time.Time{})
		var ne net.Error
		return errors.As(err, &ne) && ne.Timeout()
	}
	raw, err := call(conn, id, MsgStatus, nil)
	if err != nil {
		return false
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return false
	}
	c.set(func(s *Snapshot) {
		s.Service, s.Linked, s.Update, s.Store, s.Refs = st.Build, st.Linked, st.Update, st.Store, st.Refs
	})
	return true
}

func Register(ctx context.Context, dir, slot string, key *crypto.KeyPair, image string, wait time.Duration) (string, error) {
	path, err := LinkPath(dir)
	if err != nil {
		return "", err
	}
	deadline := time.Now().Add(wait)
	for {
		conn, err := dialLink(ctx, path, image)
		if err == nil {
			_, err = handshakeOn(conn, key, slot, Self(), true)
			conn.Close()
			if err == nil {
				return RecordedByRegister, nil
			}
			var ref *Refusal
			if errors.As(err, &ref) {
				return "", err
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return "", fmt.Errorf("the machine service did not answer at %s within %s: %w", path, wait, err)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}
