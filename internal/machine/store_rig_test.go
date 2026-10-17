//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// THE STORE'S RIG: a machine service with its plugin store, in the test's
// process, each identity linked over an in-memory pipe — so the service
// is driven through its own wire and every path it makes lies under the
// test's own temporary directory (no socket, whose address a long path
// would overflow).

// storeRig is one service and its store.
type storeRig struct {
	t     *testing.T
	srv   *Server
	dir   string
	f     *fetches
	clock *clock
	ctx   context.Context
	stop  context.CancelFunc
}

// clock is a time the test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// newStoreRig starts a service whose directory is dir ("" makes one) with
// these startup slots, its store opened as Serve opens it.
func newStoreRig(t *testing.T, dir string, f *fetches, opts Options) *storeRig {
	t.Helper()
	if dir == "" {
		dir = filepath.Join(t.TempDir(), "machine")
	}
	if err := ensureOwnerOnlyDir(dir); err != nil {
		t.Fatal(err)
	}
	// The service's clock starts at the real time: what it compares with
	// a file's modification time must agree with the file system's.
	c := &clock{now: time.Now()}
	opts.Dir = dir
	opts.now = c.Now
	opts.store.fetch = f.fetch
	if opts.store.settleWait == 0 {
		opts.store.settleWait = -1 // no pass the test does not run itself
	}
	srv := NewServer(opts)
	st, err := loadState(srv.statePath)
	if err != nil {
		t.Fatal(err)
	}
	srv.state = st
	ctx, cancel := context.WithCancel(context.Background())
	srv.openStore(ctx)
	r := &storeRig{t: t, srv: srv, dir: dir, f: f, clock: c, ctx: ctx, stop: cancel}
	t.Cleanup(r.close)
	return r
}

// close stops the service's store as Serve does at its end: its jobs
// stopped, its lock let go.
func (r *storeRig) close() {
	r.stop()
	r.srv.closeStore()
}

// holdFirstStoreEnd replaces the rig's idle acquirer before its first need.
// Its existing Ended seam holds the first terminal status before the machine
// owner receives it; all fetching, persistence and wire handling stay real.
func holdFirstStoreEnd(r *storeRig) (<-chan struct{}, func(), <-chan struct{}) {
	arrived, release, delivered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var first, once sync.Once
	ss := r.srv.store
	ss.acq = pluginhost.NewAcquirer(pluginhost.AcquirerConfig{
		Materialize: ss.attempt,
		Fetcher:     r.f.fetch,
		Spawn:       ss.spawn,
		Ready:       ss.published,
		Logf:        func(string, ...any) {},
		Ended: func(entry string, cause error) {
			held := false
			first.Do(func() {
				held = true
				close(arrived)
				select {
				case <-release:
				case <-ss.ctx.Done():
				}
			})
			ss.jobEnded(entry, cause)
			if held {
				close(delivered)
			}
		},
	})
	ss.acq.Attach(ss.ctx)
	return arrived, func() { once.Do(func() { close(release) }) }, delivered
}

func awaitStoreStep(t *testing.T, step string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("the store did not reach %s", step)
	}
}

// storeDir is where the rig's store lies.
func (r *storeRig) storeDir() string { return r.srv.store.w.Dir }

// link proves kp for slot over a pipe and answers the linked connection.
func (r *storeRig) link(kp *crypto.KeyPair, slot string) *rawLink {
	r.t.Helper()
	a, b := net.Pipe()
	go r.srv.serveConn(r.ctx, a)
	r.t.Cleanup(func() { b.Close() })
	_ = b.SetDeadline(time.Now().Add(10 * time.Second))
	m, err := readMessage(b)
	if err != nil {
		r.t.Fatal(err)
	}
	var h helloParams
	if m.Method != MsgHello || json.Unmarshal(m.Params, &h) != nil {
		r.t.Fatalf("the link did not begin with hello: %+v", m)
	}
	l := &rawLink{t: r.t, conn: b, hello: h}
	if _, err := l.ask(MsgHandshake, l.signed(kp, slot, true)); err != nil {
		r.t.Fatalf("the handshake: %v", err)
	}
	return l
}

// diskState is state.json as it is on disk now.
func (r *storeRig) diskState() State {
	r.t.Helper()
	st, err := loadState(filepath.Join(r.dir, stateFileName))
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

// need sends one need and answers the result.
func (l *rawLink) need(p NeedParams) (NeedResult, error) {
	l.t.Helper()
	raw, err := l.ask(MsgNeed, p)
	if err != nil {
		return NeedResult{}, err
	}
	var res NeedResult
	if err := json.Unmarshal(raw, &res); err != nil {
		l.t.Fatal(err)
	}
	return res, nil
}

// needUntil asks again until the material stands as want, and answers the
// last answer.
func (l *rawLink) needUntil(p NeedParams, want string, within time.Duration) NeedResult {
	l.t.Helper()
	deadline := time.Now().Add(within)
	for {
		res, err := l.need(p)
		if err != nil {
			l.t.Fatalf("need: %v", err)
		}
		if res.State == want {
			return res
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("need: want %s within %s, the last answer %+v", want, within, res)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// fetches is the service's download path in the test: each address its
// own bytes, every call counted.
type fetches struct {
	mu      sync.Mutex
	bodies  map[string][]byte
	calls   map[string]int
	offsets map[string][]int64
	fail    map[string]error
	// before, when set, runs before each call's bytes; an error ends the
	// call with it.
	before func(ctx context.Context, url string, offset int64) error
	// half, when set, writes half of the address's bytes and then waits
	// for the call's end.
	half map[string]bool
}

func newFetches() *fetches {
	return &fetches{bodies: map[string][]byte{}, calls: map[string]int{}, offsets: map[string][]int64{}, fail: map[string]error{}, half: map[string]bool{}}
}

func (f *fetches) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.mu.Lock()
	f.calls[url]++
	f.offsets[url] = append(f.offsets[url], offset)
	body, ok := f.bodies[url]
	fail := f.fail[url]
	half := f.half[url]
	before := f.before
	f.mu.Unlock()
	if before != nil {
		if err := before(ctx, url, offset); err != nil {
			return 0, err
		}
	}
	if fail != nil {
		return 0, fail
	}
	if !ok {
		return 0, &pluginhost.FetchStatusError{Status: 404}
	}
	rest := body[offset:]
	if half {
		n, err := w.Write(rest[:len(rest)/2])
		if err != nil {
			return int64(n), err
		}
		<-ctx.Done()
		return int64(n), ctx.Err()
	}
	n, err := w.Write(rest)
	return int64(n), err
}

func (f *fetches) count(url string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[url]
}

func (f *fetches) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

// model declares body as a model named name, served by f.
func (f *fetches) model(name string, body []byte) pluginhost.ModelDecl {
	sum := sha256.Sum256(body)
	url := "https://models.example/" + name
	f.mu.Lock()
	f.bodies[url] = body
	f.mu.Unlock()
	return pluginhost.ModelDecl{Name: name, URL: url, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
}

// modelsDigest is the declaring member's digest the rig's views are made
// of.
var modelsDigest = "sha256:" + strings.Repeat("2", 64)

// viewWork is the work of a view holding these models, every one named.
func viewWork(models ...pluginhost.ModelDecl) pluginhost.StoreWork {
	var names []string
	for _, d := range models {
		names = append(names, d.Name)
	}
	return pluginhost.StoreWork{Entry: "v/" + plugstore.ViewKey(modelsDigest, names), View: &pluginhost.ViewWork{ModelsSHA256: modelsDigest, Names: names, Models: models}}
}

// archiveRef is an archive kept by reference.
func archiveRef(sha string) pluginhost.StoreWork {
	return pluginhost.StoreWork{Entry: "a/" + sha}
}

// ensure publishes work in the rig's store through its writer, as a job
// would, for a test that builds a store before it collects.
func (r *storeRig) ensure(work pluginhost.StoreWork) {
	r.t.Helper()
	if err := pluginhost.EnsureEntry(r.t.Context(), r.srv.store.w, work, r.f.fetch, pluginhost.Adoption{}, nil); err != nil {
		r.t.Fatal(err)
	}
}

// exists says whether the store holds the entry's path.
func (r *storeRig) exists(entry string) bool {
	e, err := plugstore.ParseEntry(entry)
	if err != nil {
		r.t.Fatal(err)
	}
	p, err := plugstore.Store{Dir: r.storeDir()}.Path(e)
	if err != nil {
		r.t.Fatal(err)
	}
	_, err = os.Lstat(p)
	return err == nil
}

// published says the store holds the root's or view's record.
func (r *storeRig) published(entry string) bool {
	_, err := os.Lstat(filepath.Join(r.storeDir(), entry+plugstore.RecordSuffix))
	return err == nil
}

// blobEntry is a model's blob as an entry.
func blobEntry(d pluginhost.ModelDecl) string { return "b/" + d.SHA256 }

// slotIn makes a slot directory for an identity under root.
func slotIn(t *testing.T, root, name string) string {
	t.Helper()
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(d); err == nil {
		d = real
	}
	return d
}

// isRefusal says err is the service's refusal for reason.
func isRefusal(err error, reason string) bool {
	var r *Refusal
	return errors.As(err, &r) && r.Reason == reason
}
