package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// .
// .
// .
type modelFixture struct {
	decl ModelDecl
	data []byte

	mu          sync.Mutex
	calls       int
	offsets     []int64
	firstAt     time.Time
	cutOnce     int64
	failWith    error
	block       chan struct{}
	cancelledAt time.Time
}

func newModelFixture(t *testing.T, name string, size int) *modelFixture {
	t.Helper()
	data := bytes.Repeat([]byte(name[:1]), size)
	sum := sha256.Sum256(data)
	return &modelFixture{data: data, decl: ModelDecl{Name: name, URL: "https://example.invalid/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(size)}}
}

func (f *modelFixture) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.mu.Lock()
	f.calls++
	if f.calls == 1 {
		f.firstAt = time.Now()
	}
	f.offsets = append(f.offsets, offset)
	block, cut, fail := f.block, f.cutOnce, f.failWith
	f.cutOnce = 0
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			f.mu.Lock()
			f.cancelledAt = time.Now()
			f.mu.Unlock()
			return 0, ctx.Err()
		}
	}
	if fail != nil {
		return 0, fail
	}
	if offset > int64(len(f.data)) {
		return 0, fmt.Errorf("offset past the end")
	}
	end := int64(len(f.data))
	if cut > 0 && offset+cut < end {
		end = offset + cut
	}
	n, err := w.Write(f.data[offset:end])
	if err != nil {
		return int64(n), err
	}
	if cut > 0 {
		return int64(n), errors.New("the connection dropped")
	}
	return int64(n), nil
}

func (f *modelFixture) seen() (int, []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]int64(nil), f.offsets...)
}

func (f *modelFixture) cancelled() (time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelledAt, !f.cancelledAt.IsZero()
}

// .
type modelServer struct{ byURL map[string]*modelFixture }

func (s *modelServer) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f := s.byURL[url]
	if f == nil {
		return 0, fmt.Errorf("no model at %s", url)
	}
	return f.fetch(ctx, url, offset, w)
}

type readyLog struct {
	mu  sync.Mutex
	ids []string
}

func (r *readyLog) ready(id string) {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
}

func (r *readyLog) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ids...)
}

func sinkLogf(t *testing.T) (func(string, ...interface{}), *logSink) {
	t.Helper()
	sink := &logSink{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(sink.String())
		}
	})
	return log.New(sink, "", 0).Printf, sink
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func noBackoff(int) time.Duration   { return 0 }
func longBackoff(int) time.Duration { return time.Hour }

func TestAcquirerBringsModelsAndRuntimeThenSaysReady(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 50000)
	rt := newRuntimeFixture(t)
	var rl readyLog
	logf, _ := sinkLogf(t)
	q := newAttachedAcquirer(AcquirerConfig{ModelFetcher: fx.fetch, RuntimeFetcher: rt.fetch, Ready: rl.ready, Backoff: noBackoff, Logf: logf})
	m := Material{PluginID: "id.example.voice", Version: "0.1.0", Models: []ModelDecl{fx.decl}, ModelsDir: filepath.Join(t.TempDir(), "models"),
		Runtime: &rt.decl, RuntimeDir: rt.dir, entry: rt.entry}
	q.Want(m)
	waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
	if got, err := os.ReadFile(filepath.Join(m.ModelsDir, "stt.bin")); err != nil || !bytes.Equal(got, fx.data) {
		t.Fatalf("the model is not on disk as declared: %v", err)
	}
	if _, err := os.Stat(m.runtimeRoot()); err != nil {
		t.Fatalf("the runtime root is not published: %v", err)
	}
	st, ok := q.Status(m.PluginID)
	if !ok || st.Phase != PhaseReady || st.FilesPresent != 1 || st.BytesPresent != 50000 || !st.RuntimePresent || st.LastError != "" {
		t.Fatalf("status after ready: %+v", st)
	}
	if s := st.Summary(); !strings.Contains(s, "models present (1 files, 48 KB)") || !strings.Contains(s, "runtime present") {
		t.Fatalf("summary: %s", s)
	}
	// .
	// .
	if len(q.Snapshot()) != 1 {
		t.Fatal("a ready job vanished before the activation settled")
	}
	q.Forget(m.PluginID)
	if len(q.Snapshot()) != 0 {
		t.Fatal("a forgotten job still shows")
	}
}

func TestAcquirerResumesACutDownloadOnTheNextAttempt(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 60000)
	fx.cutOnce = 20000
	var rl readyLog
	logf, _ := sinkLogf(t)
	q := newAttachedAcquirer(AcquirerConfig{ModelFetcher: fx.fetch, Ready: rl.ready, Backoff: noBackoff, Logf: logf})
	m := Material{PluginID: "id.example.cut", Version: "1", Models: []ModelDecl{fx.decl}, ModelsDir: t.TempDir()}
	q.Want(m)
	waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
	calls, offsets := fx.seen()
	if calls != 2 || offsets[0] != 0 || offsets[1] != 20000 {
		t.Fatalf("the second attempt must resume where the first stopped: calls %d offsets %v", calls, offsets)
	}
	if got, err := os.ReadFile(filepath.Join(m.ModelsDir, "stt.bin")); err != nil || !bytes.Equal(got, fx.data) {
		t.Fatalf("the resumed file is not the model: %v", err)
	}
}

func TestAcquirerWaitsOutABackoffAndTheSameWantWakesIt(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 1000)
	fx.failWith = errors.New("the model server answered 503")
	var changes int
	var cmu sync.Mutex
	logf, _ := sinkLogf(t)
	q := newAttachedAcquirer(AcquirerConfig{ModelFetcher: fx.fetch, Backoff: longBackoff, Logf: logf, Changed: func() { cmu.Lock(); changes++; cmu.Unlock() }})
	m := Material{PluginID: "id.example.wait", Version: "1", Models: []ModelDecl{fx.decl}, ModelsDir: t.TempDir()}
	q.Want(m)
	waitFor(t, "waiting", func() bool { st, _ := q.Status(m.PluginID); return st.Phase == PhaseWaiting })
	st, _ := q.Status(m.PluginID)
	if st.Attempt != 1 || !strings.Contains(st.LastError, "the model server answered 503") || st.RetryAt.Before(time.Now().Add(30*time.Minute)) {
		t.Fatalf("waiting status: %+v", st)
	}
	if st.BytesPresent != 0 || st.BytesTotal != 1000 || st.FilesTotal != 1 || st.FilesPresent != 0 {
		t.Fatalf("measured from disk: %+v", st)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	waitFor(t, "the watcher to be told of both phase moves", func() bool {
		cmu.Lock()
		defer cmu.Unlock()
		return changes >= 2
	})
	// .
	// .
	// .
	// .
	// .
	// .
	q.Want(m)
	waitFor(t, "the second attempt to reach the server", func() bool { c, _ := fx.seen(); return c == 2 })
	if st, _ := q.Status(m.PluginID); st.Attempt != 2 {
		t.Fatalf("the woken attempt was not counted: %+v", st)
	}
	if len(q.Snapshot()) != 1 {
		t.Fatal("two wants made two jobs")
	}
	// .
	fx.mu.Lock()
	fx.failWith = nil
	fx.mu.Unlock()
	q.Want(m)
	waitFor(t, "ready", func() bool { st, _ := q.Status(m.PluginID); return st.Phase == PhaseReady })
}

func TestAcquirerKeepStopsWhatIsNoLongerWanted(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 1000)
	fx.block = make(chan struct{})
	var rl readyLog
	logf, _ := sinkLogf(t)
	q := newAttachedAcquirer(AcquirerConfig{ModelFetcher: fx.fetch, Ready: rl.ready, Backoff: noBackoff, Logf: logf})
	m := Material{PluginID: "id.example.gone", Version: "1", Models: []ModelDecl{fx.decl}, ModelsDir: t.TempDir()}
	q.Want(m)
	waitFor(t, "the fetch to start", func() bool { c, _ := fx.seen(); return c == 1 })
	q.Keep(map[string]bool{"id.example.other": true})
	waitFor(t, "the fetch to be cancelled", func() bool { _, c := fx.cancelled(); return c })
	if len(q.Snapshot()) != 0 || len(rl.list()) != 0 {
		t.Fatal("an unwanted job still stands, or claimed ready")
	}
}

func TestAcquirerReplacesAJobWhoseMaterialChangedAfterItLeft(t *testing.T) {
	old := newModelFixture(t, "old.bin", 1000)
	old.block = make(chan struct{})
	fresh := newModelFixture(t, "new.bin", 1000)
	srv := &modelServer{byURL: map[string]*modelFixture{old.decl.URL: old, fresh.decl.URL: fresh}}
	var rl readyLog
	logf, _ := sinkLogf(t)
	q := newAttachedAcquirer(AcquirerConfig{ModelFetcher: srv.fetch, Ready: rl.ready, Backoff: noBackoff, Logf: logf})
	dir := t.TempDir()
	q.Want(Material{PluginID: "id.example.rel", Version: "1", Models: []ModelDecl{old.decl}, ModelsDir: dir})
	waitFor(t, "the old fetch to start", func() bool { c, _ := old.seen(); return c == 1 })
	q.Want(Material{PluginID: "id.example.rel", Version: "2", Models: []ModelDecl{fresh.decl}, ModelsDir: dir})
	waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
	left, ok := old.cancelled()
	if !ok {
		t.Fatal("the replaced job was not cancelled")
	}
	fresh.mu.Lock()
	started := fresh.firstAt
	fresh.mu.Unlock()
	if started.Before(left) {
		t.Fatal("the replacement touched the directory before the replaced job had left it")
	}
	st, _ := q.Status("id.example.rel")
	if st.Version != "2" || st.Phase != PhaseReady || len(q.Snapshot()) != 1 {
		t.Fatalf("status: %+v", st)
	}
}

func TestAcquirerCountsPartialBytesAndPicksUpAPlacedFile(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 1000)
	dir := t.TempDir()
	partial := filepath.Join(dir, "stt.bin.partial")
	if err := os.WriteFile(partial, fx.data[:250], 0o600); err != nil {
		t.Fatal(err)
	}
	var rl readyLog
	logf, _ := sinkLogf(t)
	// .
	// .
	q := newAttachedAcquirer(AcquirerConfig{Ready: rl.ready, Backoff: longBackoff, Logf: logf})
	m := Material{PluginID: "id.example.offline", Version: "1", Models: []ModelDecl{fx.decl}, ModelsDir: dir}
	q.Want(m)
	waitFor(t, "waiting", func() bool { st, _ := q.Status(m.PluginID); return st.Phase == PhaseWaiting })
	st, _ := q.Status(m.PluginID)
	if st.BytesPresent != 250 || st.BytesTotal != 1000 || st.FilesPresent != 0 || !strings.Contains(st.LastError, "place the files") {
		t.Fatalf("%+v", st)
	}
	if s := st.Summary(); s != "models 250 B of 1000 B present (0 of 1 files)" {
		t.Fatalf("summary: %s", s)
	}
	// .
	if err := os.Remove(partial); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stt.bin"), fx.data, 0o600); err != nil {
		t.Fatal(err)
	}
	q.Want(m)
	waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
}

func TestAcquirerStartsNothingWhileTheHostStops(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 1000)
	logf, _ := sinkLogf(t)
	q := newAttachedAcquirer(AcquirerConfig{ModelFetcher: fx.fetch, Logf: logf, Spawn: func(func()) bool { return false }})
	q.Want(Material{PluginID: "id.example.late", Version: "1", Models: []ModelDecl{fx.decl}, ModelsDir: t.TempDir()})
	if len(q.Snapshot()) != 0 {
		t.Fatal("a job that never ran claims to")
	}
	if c, _ := fx.seen(); c != 0 {
		t.Fatal("a fetch ran while the host stops")
	}
}

func TestAcquireStatusSummaryAndTheTypedRefusal(t *testing.T) {
	st := AcquireStatus{FilesTotal: 24, FilesPresent: 14, BytesTotal: 3311000000, BytesPresent: 1770000000, RuntimeDeclared: true, RuntimeBytes: 22340309}
	const want = "models 1.65 GB of 3.08 GB present (14 of 24 files); runtime absent (21.3 MB to fetch)"
	if got := st.Summary(); got != want {
		t.Fatalf("summary:\n got %s\nwant %s", got, want)
	}
	e := &AcquiringError{PluginID: "id.aiii.voice", Version: "0.1.0-beta.1", Status: st, Cause: errors.New("x")}
	if !strings.Contains(e.Error(), want) || !strings.Contains(e.Error(), "activation follows") || !errors.Is(e, e.Cause) {
		t.Fatalf("error: %v", e)
	}
	if !acquirable(&ModelsMissingError{}) || !acquirable(&RuntimeMissingError{}) || acquirable(&RuntimeError{}) || acquirable(errors.New("x")) {
		t.Fatal("acquirable answers wrong")
	}
	if d := defaultBackoff(1); d != 5*time.Second {
		t.Fatalf("first backoff %s", d)
	}
	if d := defaultBackoff(20); d != 5*time.Minute {
		t.Fatalf("capped backoff %s", d)
	}
}

// .
// .
func newAttachedAcquirer(cfg AcquirerConfig) *Acquirer {
	q := NewAcquirer(cfg)
	q.Attach(context.Background())
	return q
}

// .
// .
// .
// .
func TestAcquirerHoldsWantsUntilAttachedAndStartsWhatIsKept(t *testing.T) {
	kept := newModelFixture(t, "kept.bin", 1000)
	ghost := newModelFixture(t, "ghost.bin", 1000)
	srv := &modelServer{byURL: map[string]*modelFixture{kept.decl.URL: kept, ghost.decl.URL: ghost}}
	var rl readyLog
	logf, _ := sinkLogf(t)
	q := NewAcquirer(AcquirerConfig{ModelFetcher: srv.fetch, Ready: rl.ready, Backoff: noBackoff, Logf: logf})
	q.Want(Material{PluginID: "id.example.kept", Version: "1", Models: []ModelDecl{kept.decl}, ModelsDir: t.TempDir()})
	q.Want(Material{PluginID: "id.example.ghost", Version: "1", Models: []ModelDecl{ghost.decl}, ModelsDir: t.TempDir()})
	time.Sleep(150 * time.Millisecond)
	if c, _ := kept.seen(); c != 0 {
		t.Fatal("a want ran before the host's background plane existed")
	}
	st, ok := q.Status("id.example.kept")
	if !ok || st.Phase != PhaseFetching || st.Attempt != 0 || len(q.Snapshot()) != 2 {
		t.Fatalf("a held want is in hand, not yet attempted: %+v", st)
	}
	q.Keep(map[string]bool{"id.example.kept": true})
	if len(q.Snapshot()) != 1 {
		t.Fatal("a held want nobody wants still stands")
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.Attach(ctx)
	waitFor(t, "the kept want to run and finish", func() bool { return len(rl.list()) == 1 })
	if c, _ := ghost.seen(); c != 0 {
		t.Fatal("the dropped want ran")
	}
	// .
	// .
	late := newModelFixture(t, "late.bin", 1000)
	late.block = make(chan struct{})
	srv.byURL[late.decl.URL] = late
	q.Want(Material{PluginID: "id.example.late", Version: "1", Models: []ModelDecl{late.decl}, ModelsDir: t.TempDir()})
	waitFor(t, "the late want to start", func() bool { c, _ := late.seen(); return c == 1 })
	cancel()
	waitFor(t, "the host's end to end the job", func() bool { _, c := late.cancelled(); return c })
	waitFor(t, "the ended job to leave", func() bool { _, ok := q.Status("id.example.late"); return !ok })
}
