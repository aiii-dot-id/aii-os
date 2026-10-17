//go:build (linux && !android) || (darwin && !ios) || windows

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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// A model served from memory: the fetcher honors the offset, records
// its calls and the offsets it saw, can stop short once, can fail every
// time, and can hold a call until released or cancelled.
type modelFixture struct {
	decl ModelDecl
	data []byte

	mu          sync.Mutex
	calls       int
	offsets     []int64
	firstAt     time.Time
	cutOnce     int64         // the first call writes this many bytes then fails; 0 = never
	failWith    error         // every call fails with this
	block       chan struct{} // when set, a call waits here (or for its context)
	cancelledAt time.Time     // a blocked call saw its context end
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
		return int64(n), &NetworkError{Cause: errors.New("the connection dropped")}
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

// modelServer dispatches one fetcher over several models by URL.
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

func sinkLogf(t *testing.T) (func(string, ...any), *logSink) {
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

// The time seam: a wait that passes at once, and one that never passes.
func nowAfter(time.Duration) <-chan time.Time {
	c := make(chan time.Time, 1)
	c <- time.Now()
	return c
}
func neverAfter(time.Duration) <-chan time.Time { return nil }

func TestAcquirerEndCauseBelongsToTheCurrentUnretriedJob(t *testing.T) {
	for _, cause := range []error{
		&DownloadError{Cause: &FetchStatusError{Status: 404}},
		&MaterialEnded{Cause: "the writer ended the download"},
		&MaterialRefused{Cause: "the package must change"},
		fmt.Errorf("wrapped: %w", &WorkError{Entry: "v/example", Detail: "invalid declaration"}),
	} {
		t.Run(fmt.Sprintf("%T", cause), func(t *testing.T) {
			q := NewAcquirer(AcquirerConfig{})
			m := Material{PluginID: "org.example.ended"}
			q.Want(m) // no Attach: the job is held, no goroutine or disk work
			job := q.jobs[m.PluginID]
			job.phase, job.lastErr = PhaseWaiting, cause
			if got := q.EndCause(m.PluginID); got != cause {
				t.Fatalf("lost the typed terminal cause: got %v, want %v", got, cause)
			}
			q.Retry(m.PluginID)
			if job.phase != PhaseWaiting || q.EndCause(m.PluginID) != nil {
				t.Fatal("retry must clear the end before the worker leaves waiting")
			}
			for _, phase := range []string{PhaseFetching, PhaseReady} {
				job.phase, job.lastErr = phase, cause
				if q.EndCause(m.PluginID) != nil {
					t.Fatalf("%s reported an end", phase)
				}
			}
			job.phase, job.stopping = PhaseWaiting, true
			if q.EndCause(m.PluginID) != nil || q.EndCause("absent") != nil {
				t.Fatal("retiring or absent job reported an end")
			}
			job.stopping = false
			m.Version = "replacement"
			q.Want(m)
			if q.EndCause(m.PluginID) != nil {
				t.Fatal("the replacement inherited its predecessor's end")
			}
		})
	}
	q := NewAcquirer(AcquirerConfig{})
	q.Want(Material{PluginID: "org.example.waiting"})
	job := q.jobs["org.example.waiting"]
	job.phase = PhaseWaiting
	for _, cause := range []error{nil, &NetworkError{Cause: errors.New("offline")}, &MaterialWait{Cause: "no room", Remedy: "free space"}} {
		job.lastErr = cause
		if q.EndCause("org.example.waiting") != nil {
			t.Fatalf("a momentary wait reported an end: %v", cause)
		}
	}
}

func TestAcquirerBringsModelsAndRuntimeThenSaysReady(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 50000)
	rt := newRuntimeFixture(t)
	var rl readyLog
	logf, _ := sinkLogf(t)
	w := openStore(t)
	q := newAttachedAcquirer(w, byURL(map[string]ModelFetcher{fx.decl.URL: fx.fetch, rt.decl.URL: rt.fetch}), AcquirerConfig{Ready: rl.ready, After: nowAfter, Logf: logf})
	m := runtimeMaterial(t, w, "id.example.voice", "0.1.0", rt, fx.decl)
	q.Want(m)
	waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
	if got, err := os.ReadFile(filepath.Join(m.ModelsDir, "stt.bin")); err != nil || !bytes.Equal(got, fx.data) {
		t.Fatalf("the model is not on disk as declared: %v", err)
	}
	if _, err := os.Stat(m.runtimeRoot()); err != nil {
		t.Fatalf("the runtime root is not published: %v", err)
	}
	st, ok := q.Status(m.PluginID)
	if !ok || st.Phase != PhaseReady || st.FilesPresent != 2 || st.BytesPresent != 50000+rt.decl.Size || !st.RuntimePresent || st.LastError != "" {
		t.Fatalf("status after ready: %+v", st)
	}
	if s := st.Summary(); !strings.Contains(s, "models present (1 files, 48 KB)") || !strings.Contains(s, "runtime present") {
		t.Fatalf("summary: %s", s)
	}
	// A ready job stays visible — the page says "activating" — until the
	// activation settles and forgets it.
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
	w := openStore(t)
	q := newAttachedAcquirer(w, fx.fetch, AcquirerConfig{Ready: rl.ready, After: nowAfter, Logf: logf})
	m := modelMaterial(w, "id.example.cut", "1", fx.decl)
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
	fx.failWith = &FetchStatusError{Status: 503}
	var changes int
	var cmu sync.Mutex
	logf, _ := sinkLogf(t)
	w := openStore(t)
	q := newAttachedAcquirer(w, fx.fetch, AcquirerConfig{After: neverAfter, Logf: logf, Changed: func() { cmu.Lock(); changes++; cmu.Unlock() }})
	m := modelMaterial(w, "id.example.wait", "1", fx.decl)
	q.Want(m)
	waitFor(t, "waiting", func() bool { st, _ := q.Status(m.PluginID); return st.Phase == PhaseWaiting })
	st, _ := q.Status(m.PluginID)
	if st.Attempt != 1 || !strings.Contains(st.LastError, "the server answered 503") || !st.RetryAt.After(time.Now()) || st.Remedy != "" {
		t.Fatalf("waiting status: %+v", st)
	}
	if st.BytesPresent != 0 || st.BytesTotal != 1000 || st.FilesTotal != 1 || st.FilesPresent != 0 {
		t.Fatalf("measured from disk: %+v", st)
	}
	// WAIT FOR THE WATCHER, do not assert it has already run. The phase
	// reaching "waiting" and the watcher being told about it are two
	// separate events, so checking the counter the instant the phase
	// moves is a race the loaded gate wins — the same race this test
	// already documents for the attempt number, one assertion further
	// down, left unfixed here.
	waitFor(t, "the watcher to be told of both phase moves", func() bool {
		cmu.Lock()
		defer cmu.Unlock()
		return changes >= 2
	})
	// The same want again is ONE job, woken: the second attempt runs now,
	// not in an hour.
	// Wait for the FETCH, not for the counter: the attempt number is
	// recorded before the attempt runs, so watching it would let this
	// assertion win a race the wake has not finished yet — which is
	// exactly how it failed under a loaded gate.
	q.Want(m)
	waitFor(t, "the second attempt to reach the server", func() bool { c, _ := fx.seen(); return c == 2 })
	if st, _ := q.Status(m.PluginID); st.Attempt != 2 {
		t.Fatalf("the woken attempt was not counted: %+v", st)
	}
	if len(q.Snapshot()) != 1 {
		t.Fatal("two wants made two jobs")
	}
	// The server recovers; the next attempt succeeds.
	fx.mu.Lock()
	fx.failWith = nil
	fx.mu.Unlock()
	q.Want(m)
	waitFor(t, "ready", func() bool { st, _ := q.Status(m.PluginID); return st.Phase == PhaseReady })
}

// DAMAGE IS REPORTED TO THE STORE'S WRITER ONCE (a defect of 0.1.16's
// store, found by L2's engine link). A verification that found an entry damaged hands its material to the
// Acquirer naming it: the job's first ask of the writer reports it, and no
// later ask does — the writer, told once, repairs and answers pending while
// it does. Reported on every ask, each one woke a repair again, and the
// machine service answers a report of damage pending whatever it did
// before: the job waited for good on an entry already mended. A new job,
// handed damage found again, reports it again.
func TestAcquirerReportsDamageToTheWriterOnce(t *testing.T) {
	w := openStore(t)
	fx := newModelFixture(t, "stt.bin", 100)
	m := modelMaterial(w, "id.example.damaged", "1", fx.decl)
	m.damaged = []string{"v/" + m.viewKey()}
	var mu sync.Mutex
	var asks [][]string
	var rl readyLog
	q := NewAcquirer(AcquirerConfig{After: nowAfter, Ready: rl.ready, Logf: func(string, ...any) {}, Materialize: func(_ context.Context, m Material, _ ModelFetcher) error {
		mu.Lock()
		defer mu.Unlock()
		asks = append(asks, m.Damaged())
		if len(asks)%3 != 0 {
			return &MaterialWait{Cause: "the writer is repairing it", Fetching: true, After: time.Millisecond}
		}
		return nil
	}})
	q.Attach(t.Context())
	said := func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return append([][]string(nil), asks...)
	}
	q.Want(m)
	waitFor(t, "the first job to be ready", func() bool { return len(rl.list()) == 1 })
	got := said()
	if len(got) != 3 || !slices.Equal(got[0], m.damaged) || got[1] != nil || got[2] != nil {
		t.Fatalf("the writer was asked %q; want the damage on the first ask alone", got)
	}
	// Found damaged again by the next verification: a new job of the same
	// material, which reports it on its first ask.
	q.Want(m)
	waitFor(t, "the second job to be ready", func() bool { return len(rl.list()) == 2 })
	if got := said(); len(got) != 6 || !slices.Equal(got[3], m.damaged) || got[4] != nil || got[5] != nil {
		t.Fatalf("the second job asked %q; want the damage on its first ask alone", got[3:])
	}
}

func TestAcquirerKeepStopsWhatIsNoLongerWanted(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 1000)
	fx.block = make(chan struct{})
	var rl readyLog
	logf, _ := sinkLogf(t)
	w := openStore(t)
	q := newAttachedAcquirer(w, fx.fetch, AcquirerConfig{Ready: rl.ready, After: nowAfter, Logf: logf})
	m := modelMaterial(w, "id.example.gone", "1", fx.decl)
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
	w := openStore(t)
	q := newAttachedAcquirer(w, srv.fetch, AcquirerConfig{Ready: rl.ready, After: nowAfter, Logf: logf})
	q.Want(modelMaterial(w, "id.example.rel", "1", old.decl))
	waitFor(t, "the old fetch to start", func() bool { c, _ := old.seen(); return c == 1 })
	q.Want(modelMaterial(w, "id.example.rel", "2", fresh.decl))
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
	w := openStore(t)
	blob, _ := w.Blob(fx.decl.SHA256)
	partial := blob + ".partial"
	if err := os.WriteFile(partial, fx.data[:250], 0o600); err != nil {
		t.Fatal(err)
	}
	var rl readyLog
	logf, _ := sinkLogf(t)
	// No download path on this host: the job says so, measures the
	// partial, and keeps looking.
	q := newAttachedAcquirer(w, nil, AcquirerConfig{Ready: rl.ready, After: neverAfter, Logf: logf})
	m := modelMaterial(w, "id.example.offline", "1", fx.decl)
	slot := t.TempDir()
	m.adopt = Adoption{ModelDirs: []string{slot}}
	q.Want(m)
	waitFor(t, "waiting", func() bool { st, _ := q.Status(m.PluginID); return st.Phase == PhaseWaiting })
	st, _ := q.Status(m.PluginID)
	if st.BytesPresent != 250 || st.BytesTotal != 1000 || st.FilesPresent != 0 || !strings.Contains(st.LastError, "place the files") {
		t.Fatalf("%+v", st)
	}
	if s := st.Summary(); s != "models 250 B of 1000 B present (0 of 1 files)" {
		t.Fatalf("summary: %s", s)
	}
	// NO DOWNLOAD PATH IS AN ANSWER, not a moment's trouble (the download
	// rule): the job stops with its remedy, and a want asked again does not
	// reopen it.
	if st.RetryAt != (time.Time{}) || !strings.Contains(st.Remedy, "place the files") {
		t.Fatalf("a host with no download path stopped without its remedy: %+v", st)
	}
	// The operator places the file in the identity's own models directory
	// and says Try again. The attempt adopts it, by hash.
	if err := os.WriteFile(filepath.Join(slot, "stt.bin"), fx.data, 0o600); err != nil {
		t.Fatal(err)
	}
	q.Want(m)
	time.Sleep(50 * time.Millisecond)
	if len(rl.list()) != 0 {
		t.Fatal("a want asked again reopened a stopped download")
	}
	q.Retry(m.PluginID)
	waitFor(t, "ready", func() bool { return len(rl.list()) == 1 })
}

func TestAcquirerStartsNothingWhileTheHostStops(t *testing.T) {
	fx := newModelFixture(t, "stt.bin", 1000)
	logf, _ := sinkLogf(t)
	w := openStore(t)
	q := newAttachedAcquirer(w, fx.fetch, AcquirerConfig{Logf: logf, Spawn: func(func()) bool { return false }})
	q.Want(modelMaterial(w, "id.example.late", "1", fx.decl))
	if len(q.Snapshot()) != 0 {
		t.Fatal("a job that never ran claims to")
	}
	if c, _ := fx.seen(); c != 0 {
		t.Fatal("a fetch ran while the host stops")
	}
}

func TestAcquireStatusSummaryAndTheTypedRefusal(t *testing.T) {
	// The counts are every declared file, the runtime archive one of them
	// (G2); the summary still says the models and the runtime apart.
	st := AcquireStatus{FilesTotal: 25, FilesPresent: 14, BytesTotal: 3311000000 + 22340309, BytesPresent: 1770000000, RuntimeDeclared: true, RuntimeBytes: 22340309}
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
	if d := downloadWait(1); d != 5*time.Second {
		t.Fatalf("first wait %s", d)
	}
	if d := downloadWait(4); d != 40*time.Second {
		t.Fatalf("fourth wait %s", d)
	}
	if d := downloadWait(20); d != time.Minute {
		t.Fatalf("capped wait %s", d)
	}
	for i := 0; i < 100; i++ {
		if d := spread(time.Minute); d < 45*time.Second || d > time.Minute {
			t.Fatalf("a spread wait left its last quarter: %s", d)
		}
	}
}

// newAttachedAcquirer is one whose wants run at once, under a context
// nothing cancels — the tests above ask about the jobs, not the hold —
// writing w through fetch, as the machine service's does.
func newAttachedAcquirer(w *plugstore.Writer, fetch ModelFetcher, cfg AcquirerConfig) *Acquirer {
	q := storeAcquirer(w, fetch, cfg)
	q.Attach(context.Background())
	return q
}

// A want before Attach is held: the boot pass activates before the
// host's background plane exists, and nothing runs behind that sequence.
// Held wants can be kept or dropped like running ones; Attach starts
// what is still wanted, under the host's context.
func TestAcquirerHoldsWantsUntilAttachedAndStartsWhatIsKept(t *testing.T) {
	kept := newModelFixture(t, "kept.bin", 1000)
	ghost := newModelFixture(t, "ghost.bin", 1000)
	srv := &modelServer{byURL: map[string]*modelFixture{kept.decl.URL: kept, ghost.decl.URL: ghost}}
	var rl readyLog
	logf, _ := sinkLogf(t)
	w := openStore(t)
	q := storeAcquirer(w, srv.fetch, AcquirerConfig{Ready: rl.ready, After: nowAfter, Logf: logf})
	q.Want(modelMaterial(w, "id.example.kept", "1", kept.decl))
	q.Want(modelMaterial(w, "id.example.ghost", "1", ghost.decl))
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
	// A want after Attach runs at once, under the attached context: when
	// that ends, so does the job.
	late := newModelFixture(t, "late.bin", 1000)
	late.block = make(chan struct{})
	srv.byURL[late.decl.URL] = late
	q.Want(modelMaterial(w, "id.example.late", "1", late.decl))
	waitFor(t, "the late want to start", func() bool { c, _ := late.seen(); return c == 1 })
	cancel()
	waitFor(t, "the host's end to end the job", func() bool { _, c := late.cancelled(); return c })
	waitFor(t, "the ended job to leave", func() bool { _, ok := q.Status("id.example.late"); return !ok })
}

// logSink is a race-safe host-log capture.
type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *logSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
