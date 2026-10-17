package app

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// joinApp is newVoiceApp with the store's path kept, so a test can read
// what reached the store after Shutdown has closed it.
func joinApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.db")
	st, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newBareApp(t, &Config{SourcePath: filepath.Join(dir, "config.json")})
	a.store = st
	a.engine = identity.NewEngine(st, nil, ring.NewManager(), nil, identity.Ports{})
	return a, path
}

// recordedAfter opens the closed store again and returns its turns.
func recordedAfter(t *testing.T, path string) []string {
	t.Helper()
	st, err := store.NewForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	turns, err := st.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, turn := range turns {
		out = append(out, turn.Content)
	}
	return out
}

// engineWire is an engine handle over the real framed transport: what the
// engine says goes in as frames, and closing them is its lane ending —
// what the plugin teardown does to a running engine.
type engineWire struct {
	frames chan []byte
	v      *pluginhost.VoiceSession
	once   sync.Once
}

func newEngineWire(t *testing.T) *engineWire {
	t.Helper()
	frames := make(chan []byte, 16)
	c := pluginhosttest.SessionClientFrames(frames, io.Discard, 32)
	w := &engineWire{frames: frames, v: pluginhost.NewVoiceSession(c)}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = c.Run(ctx) }()
	t.Cleanup(func() {
		w.end()
		cancel()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
			t.Error("the wire reader did not retire")
		}
	})
	return w
}

func (w *engineWire) say(t *testing.T, fields map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session.event", "params": fields})
	if err != nil {
		t.Fatal(err)
	}
	w.frames <- raw
}

func (w *engineWire) end() { w.once.Do(func() { close(w.frames) }) }

// lineHold parks the goroutine that logs a line holding its text until
// let is called: a hold inside work that has no hold of its own.
type lineHold struct {
	slog.Handler
	text    string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	letOnce sync.Once
}

func holdAtLine(t *testing.T, text string) *lineHold {
	t.Helper()
	prev, prevWriter, prevFlags := slog.Default(), log.Writer(), log.Flags()
	// Lines go nowhere while it holds: the default handler writes through
	// the log package, which SetDefault points back at this one.
	h := &lineHold{Handler: slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}),
		text: text, entered: make(chan struct{}), release: make(chan struct{})}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() {
		h.let()
		slog.SetDefault(prev)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
	return h
}

func (h *lineHold) Handle(ctx context.Context, r slog.Record) error {
	if strings.Contains(r.Message, h.text) {
		h.once.Do(func() {
			close(h.entered)
			<-h.release
		})
	}
	return h.Handler.Handle(ctx, r)
}

func (h *lineHold) let() { h.letOnce.Do(func() { close(h.release) }) }

// shutdownHeldBy starts Shutdown and requires it to be still waiting a
// moment later — on the engine-bound goroutine the case holds — then lets
// that goroutine go and requires Shutdown to return, cleanly, after it.
func shutdownHeldBy(t *testing.T, a *App, what string, release func()) {
	t.Helper()
	stopped := make(chan error, 1)
	go func() { stopped <- a.Shutdown(context.Background()) }()
	select {
	case err := <-stopped:
		release()
		t.Fatalf("Shutdown returned (%v) with %s still running: nothing joined it, and the store closed under it", err, what)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("teardown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Shutdown never returned after %s ended", what)
	}
}

func TestEngineBoundWorkIsJoinedBeforeTheStoreCloses(t *testing.T) {
	t.Run("the observer of an engine, and the heard words it starts", func(t *testing.T) {
		a, path := joinApp(t)
		w := newEngineWire(t)
		a.noteVoiceOpened("vs-gone", w.v) // opened here; its handle already let go
		a.ensureVoiceObserver(nil, w.v)
		// The engine's last final arrives as its lane ends: the observer
		// hands it to its heard words, which record the room's words.
		shutdownHeldBy(t, a, "the engine's observer", func() {
			w.say(t, map[string]any{"type": "transcript_final", "session_id": "vs-gone", "sequence": 1, "text": "the last words before the close", "speaker": "quinn"})
			w.end()
		})
		if got := recordedAfter(t, path); len(got) != 1 || !strings.Contains(got[0], "the last words before the close") {
			t.Fatalf("the words the engine heard before its close are not in the store: %q", got)
		}
	})

	t.Run("heard words the observer started for no session it holds", func(t *testing.T) {
		a, path := joinApp(t)
		w := newEngineWire(t)
		a.noteVoiceOpened("vs-gone", w.v) // opened here; its handle already let go
		a.ensureVoiceObserver(nil, w.v)
		held := holdAtLine(t, "proposes an utterance")
		w.say(t, map[string]any{"type": "transcript_final", "session_id": "vs-gone", "sequence": 1, "text": "words still being recorded", "speaker": "quinn"})
		select {
		case <-held.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the observer never handed the final to its heard words")
		}
		w.end() // the lane ends; the observer retires, its heard words do not yet
		shutdownHeldBy(t, a, "the heard words", held.let)
		if got := recordedAfter(t, path); len(got) != 1 || !strings.Contains(got[0], "words still being recorded") {
			t.Fatalf("the heard words were not recorded before the store closed: %q", got)
		}
	})

	t.Run("heard words waiting behind the words ahead of them", func(t *testing.T) {
		a, path := joinApp(t)
		h, _ := heldSession(a, "vs-order", true)
		ahead := make(chan struct{})
		h.heardTail = ahead // words heard earlier, still waiting for their admission
		h.work.Add(1)
		a.admitHeldInOrder(h, &heldFinal{heard: heardUtterance{Source: "speech engine", Text: "words in their turn", SessionID: h.id, Sequence: 2, Operator: true}})
		shutdownHeldBy(t, a, "the heard words", func() { close(ahead) })
		if got := recordedAfter(t, path); len(got) != 1 || !strings.Contains(got[0], "words in their turn") {
			t.Fatalf("the heard words were not recorded before the store closed: %q", got)
		}
	})

	t.Run("the drain after an input's completion", func(t *testing.T) {
		a, _ := joinApp(t)
		h, _ := heldSession(a, "vs-drain", true)
		h.work.Add(1) // a heard utterance still being answered
		a.voiceInputFinished(h.id)
		shutdownHeldBy(t, a, "the drain after the input", func() { h.work.Done() })
		select {
		case <-h.drained:
		default:
			t.Fatal("Shutdown returned before the drain after the input resolved")
		}
	})

	t.Run("the watcher of a session's end", func(t *testing.T) {
		a, _ := joinApp(t)
		h, _ := heldSession(a, "vs-end", true)
		ended := make(chan struct{})
		h.done = ended
		a.watchSessionEnd(h)
		// The engine's close ends the session.
		shutdownHeldBy(t, a, "the session's end watcher", func() { close(ended) })
		if a.voiceHandle(h.id) != nil {
			t.Fatal("Shutdown returned with the ended session still held")
		}
	})

	t.Run("the pinned predecessor's watcher", func(t *testing.T) {
		a, _ := joinApp(t)
		w := newEngineWire(t)
		ap := &pluginhost.ActivePlugin{ID: "id.test.engine", Version: "1.0.0"}
		ap.Voice.Store(w.v)
		a.watchPinnedPredecessor(&pluginhost.Activation{ID: ap.ID, Version: ap.Version, Plugin: ap}, w.v)
		// The child's reap: the teardown stopped it, and its lane ends.
		shutdownHeldBy(t, a, "the pinned predecessor's watcher", w.end)
	})
}

func TestAWedgedEngineGoroutineIsBoundedAndNamed(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	// Not newBareApp: its cleanup's stopApp fails a test whose teardown
	// fails, and this one's is meant to.
	dir := t.TempDir()
	st, err := store.NewForTest(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.store = st
	a.engine = identity.NewEngine(st, nil, ring.NewManager(), nil, identity.Ports{})
	h := &voiceHandle{id: "vs-wedged", v: &fakeEngineSession{}, b: &audio.Binding{InputHandle: "in-vs-wedged", Contained: true}, done: make(chan struct{}), drained: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	h.work.Add(1) // never done while Shutdown runs
	defer h.work.Done()
	a.voiceInputFinished(h.id)

	began := time.Now()
	stopped := make(chan error, 1)
	go func() { stopped <- a.Shutdown(context.Background()) }()
	select {
	case err = <-stopped:
	case <-time.After(engineJoinBound + 5*time.Second):
		t.Fatal("a wedged engine goroutine held Shutdown past its bound")
	}
	if took := time.Since(began); took < engineJoinBound {
		t.Fatalf("Shutdown returned after %s, before the join's bound (%s): the drain was never waited for", took, engineJoinBound)
	}
	if err == nil || !strings.Contains(err.Error(), "the drain after vs-wedged's input") {
		t.Fatalf("a teardown that left an engine goroutine running returned %v — it must fail, naming what it left", err)
	}
	if !logs.Contains("the drain after vs-wedged's input") || !logs.Contains("plugins.error") {
		t.Fatalf("the join did not log what it left running:\n%s", logs.String())
	}
	// Asked again, the result stands.
	if again := a.Shutdown(context.Background()); again == nil || again.Error() != err.Error() {
		t.Fatalf("a second Shutdown erased the failed teardown: %v", again)
	}
}

// awaitAppWork waits, as Shutdown's two joins do, for the application's
// background jobs (bgWG) and its engine work — a fence of another engine's
// synthesis, a SAFE abort — to settle; a job of either may start one of
// the other, so the background is waited for again after.
func awaitAppWork(t testing.TB, a *App) {
	t.Helper()
	a.bgWG.Wait()
	if left := a.engines.join(engineJoinBound); len(left) > 0 {
		t.Fatalf("engine work still running after %s: %v", engineJoinBound, left)
	}
	a.bgWG.Wait()
}
