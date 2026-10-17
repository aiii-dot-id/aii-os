package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// shortDir is a directory whose paths fit a socket address: 104 bytes on
// macOS, where the default temporary directory is already long. On Linux
// the link lies in the runtime directory, which the test points here too.
func shortDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "aiim")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	if runtime.GOOS == "linux" {
		rt := filepath.Join(dir, "run")
		if err := os.Mkdir(rt, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_RUNTIME_DIR", rt)
	}
	return dir
}

func newKey(t *testing.T) *crypto.KeyPair {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

// slotDir makes a slot directory under root.
func slotDir(t *testing.T, root, name string) string {
	t.Helper()
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

// running is a service a test started, and how it ended.
type running struct {
	srv   *Server
	ended chan struct{} // closed when Serve returned
	err   error         // Serve's answer, once ended
	stop  context.CancelFunc
}

// startAsync starts a service and returns at once; the test's end stops it.
func startAsync(t *testing.T, opts Options) *running {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{srv: NewServer(opts), ended: make(chan struct{}), stop: cancel}
	go func() { r.err = r.srv.Serve(ctx); close(r.ended) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.ended:
		case <-time.After(10 * time.Second):
			t.Error("a service did not stop within 10s")
		}
	})
	return r
}

// done says whether Serve has returned.
func (r *running) done() bool {
	select {
	case <-r.ended:
		return true
	default:
		return false
	}
}

// waitEnd waits for Serve to return and answers how.
func (r *running) waitEnd(t *testing.T, within time.Duration) error {
	t.Helper()
	select {
	case <-r.ended:
		return r.err
	case <-time.After(within):
		t.Fatalf("the service did not end within %s", within)
		return nil
	}
}

// serve starts a service and waits until its link answers.
func serve(t *testing.T, opts Options) *running {
	t.Helper()
	r := startAsync(t, opts)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		path, _ := LinkPath(opts.Dir)
		if c, err := dialLink(context.Background(), path, ""); err == nil {
			c.Close()
			return r
		}
		if r.done() {
			t.Fatalf("the service ended before it listened: %v", r.err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the service never listened")
	return nil
}

// rawLink is a connection a test drives frame by frame.
type rawLink struct {
	t     *testing.T
	conn  net.Conn
	hello helloParams
	id    int
}

func dialRaw(t *testing.T, dir string) *rawLink {
	t.Helper()
	path, err := LinkPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := dialLink(context.Background(), path, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	m, err := readMessage(c)
	if err != nil {
		t.Fatal(err)
	}
	var h helloParams
	if m.Method != MsgHello || json.Unmarshal(m.Params, &h) != nil {
		t.Fatalf("the link did not begin with hello: %+v", m)
	}
	return &rawLink{t: t, conn: c, hello: h}
}

// signed is a handshake by kp for slot, signed over this link's hello.
func (l *rawLink) signed(kp *crypto.KeyPair, slot string, register bool) handshakeParams {
	l.t.Helper()
	fp := kp.Fingerprint()
	sig, err := crypto.Sign(kp, linkInput(l.hello.Service, l.hello.Nonce, fp))
	if err != nil {
		l.t.Fatal(err)
	}
	return handshakeParams{Identity: fp, PublicKey: kp.PublicKeyB64(), Slot: slot, Nonce: l.hello.Nonce,
		Signature: base64.StdEncoding.EncodeToString(sig), Speaks: speaks(), Build: Self(), Register: register}
}

// ask sends one request and answers its result or the service's refusal.
func (l *rawLink) ask(method string, params any) (json.RawMessage, error) {
	l.t.Helper()
	l.id++
	return call(l.conn, l.id, method, params)
}

// refusal is err's reason, or "" when err is not the service's refusal.
func refusal(err error) string {
	var r *Refusal
	if errors.As(err, &r) {
		return r.Reason
	}
	return ""
}

// startupSlots is a fixed set of startup registrations.
func startupSlots(dirs ...string) func() ([]string, error) {
	return func() ([]string, error) { return dirs, nil }
}

// gate holds requests whose method it names until released.
type gate struct {
	method  string
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newGate(method string) *gate {
	return &gate{method: method, entered: make(chan struct{}), release: make(chan struct{})}
}

// hold is an Options.hold: the first request of the gate's method waits.
func (g *gate) hold(method string) {
	if method != g.method {
		return
	}
	first := false
	g.once.Do(func() { first = true })
	if !first {
		return
	}
	close(g.entered)
	<-g.release
}
