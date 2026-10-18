package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// fakeEngineSession is a driver stand-in. `current` is the session the
// driver holds ("" = whatever it is asked for); like the real driver it
// refuses a handle naming another session before doing anything.
type fakeEngineSession struct {
	mu           sync.Mutex
	malformed    uint64 // what MalformedFor answers
	current      string
	closed       []string
	synths       []string
	ops          []string // every operation with the session it named
	onSynthesize func()   // fires inside SynthesizeFor, after admission is decided
	// entered is closed when SynthesizeFor is first entered; ackGate, when
	// set, holds the acknowledgement until closed — a delayed admission with
	// the engine already holding the text. producing is the synthesis the
	// engine is making audio for until a fence names it (or the current
	// generation); failInterrupt makes every fence refuse.
	entered     chan struct{}
	enteredOnce sync.Once
	ackGate     chan struct{}
	// inputClosed / inputWhy stand in for the engine ending the input of
	// its own accord; nil until endInput arms them.
	inputClosed chan struct{}
	inputWhy    string
	producing   string
	// streams is what the engine named for each output stream, as
	// synthesis_start would have said it.
	streams       map[uint32]string
	failInterrupt error
	reports       []pluginhost.PlaybackReport
}

func (f *fakeEngineSession) admit(op, sid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops = append(f.ops, op+":"+sid)
	if f.current != "" && sid != f.current {
		return pluginhost.ErrStaleSession
	}
	return nil
}
func (f *fakeEngineSession) FinishInputFor(_ context.Context, sid, _ string, _ int64) error {
	return f.admit("finish", sid)
}
func (f *fakeEngineSession) CloseFor(_ context.Context, sid, mode, reason string) error {
	if err := f.admit("close", sid); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, mode+": "+reason)
	return nil
}
func (f *fakeEngineSession) InterruptFor(_ context.Context, sid, synthesisID, _ string) error {
	if err := f.admit("interrupt:"+synthesisID, sid); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failInterrupt != nil {
		return f.failInterrupt
	}
	if synthesisID == "" || synthesisID == f.producing {
		f.producing = "" // the fence names the current generation, or this one
	}
	return nil
}

func (f *fakeEngineSession) PlaybackReportFor(_ context.Context, sid string, r pluginhost.PlaybackReport) error {
	if err := f.admit("playback", sid); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, r)
	return nil
}

// producingNow is the synthesis the engine is still making audio for.
// SynthesisFor is the engine's stream to synthesis map as the fixture
// keeps it: a test that files a receipt names the stream its synthesis
// was given, so the host settles the reply the report is for.
func (f *fakeEngineSession) SynthesisFor(stream uint32) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.streams[stream]
}

func (f *fakeEngineSession) producingNow() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.producing
}
func (f *fakeEngineSession) opsSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ops...)
}

// SynthesizeForEnqueue is the two-phase admission: the ENQUEUE records the
// operation and starts production at once — the engine has the text and
// is making audio BEFORE any acknowledgement — and the returned waiter
// holds the acknowledgement while ackGate is set, until it is closed.
func (f *fakeEngineSession) SynthesizeForEnqueue(_ context.Context, sid, id, text string) (func(context.Context) error, error) {
	if err := f.admit("synthesize", sid); err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.synths = append(f.synths, id+"|"+text)
	f.producing = id // enqueued: audio is being made until a fence names it
	gate := f.ackGate
	f.mu.Unlock()
	if f.entered != nil {
		f.enteredOnce.Do(func() { close(f.entered) })
	}
	if f.onSynthesize != nil {
		f.onSynthesize() // the race: the engine's word lands while the acknowledgement is pending
	}
	return func(ctx context.Context) error {
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return errors.New("acknowledgement withheld past the bound")
			}
		}
		return nil
	}, nil
}

func (f *fakeEngineSession) Label() string { return "Listening" }

// MalformedFor answers for the session the fake holds, as the driver does.
func (f *fakeEngineSession) MalformedFor(sid string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if sid == "" || sid != f.current {
		return 0
	}
	return f.malformed
}

// THE ENGINE'S OWN END OF THE INPUT. A fake that no test has armed never
// stops listening, which is the ordinary case: a nil channel blocks
// forever, so the watcher simply waits on the session's end instead.
func (f *fakeEngineSession) InputClosed() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inputClosed
}
func (f *fakeEngineSession) InputCompletionReason() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inputWhy
}

// endInput is the engine deciding on its own that it has heard enough.
func (f *fakeEngineSession) endInput(why string) {
	f.mu.Lock()
	if f.inputClosed == nil {
		f.inputClosed = make(chan struct{})
	}
	f.inputWhy = why
	ch := f.inputClosed
	f.mu.Unlock()
	close(ch)
}
func (f *fakeEngineSession) closes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.closed...)
}

func (f *fakeEngineSession) synthed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.synths...)
}

func TestSafeBeginningMidSessionAbortsWhatEntryWouldHaveRefused(t *testing.T) {
	a := &App{}
	plane := a.AudioPlane()
	src := func() audio.Source { return &liveSource{f: mono16k, every: 50 * time.Millisecond, total: 16000} }
	for _, ep := range []*audio.Endpoint{
		{ID: "mic", Source: src()}, {ID: "spk", Sink: audio.NewCaptureSink(mono16k)},
		{ID: "mic2", Source: src()}, {ID: "spk2", Sink: audio.NewCaptureSink(mono16k)},
		{ID: "call-in", Remote: true, Source: src()}, {ID: "call-out", Remote: true, Sink: audio.NewCaptureSink(mono16k)},
		{ID: "mic3", Source: src()}, {ID: "spk3", Sink: audio.NewCaptureSink(mono16k)},
		{ID: "call2-in", Remote: true, Source: src()}, {ID: "call2-out", Remote: true, Sink: audio.NewCaptureSink(mono16k)},
	} {
		if err := plane.Register(ep); err != nil {
			t.Fatal(err)
		}
	}
	hold := func(id, in, out string, contained bool) *fakeEngineSession {
		t.Helper()
		b, err := plane.Bind(id, in, out, contained)
		if err != nil {
			t.Fatalf("in NORMAL every binding is legitimate: %s: %v", id, err)
		}
		f := &fakeEngineSession{}
		a.voiceSessions.Store(id, &voiceHandle{id: id, v: f, b: b, done: make(chan struct{})})
		return f
	}
	kept := hold("host", "mic", "spk", true)
	remote := hold("remote", "call-in", "call-out", true)
	uncontained := hold("trusted-native", "mic2", "spk2", false)

	a.enterSafe("the record cannot be trusted")
	defer a.resetModeForTest()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (len(remote.closes()) == 0 || len(uncontained.closes()) == 0) {
		time.Sleep(10 * time.Millisecond)
	}
	for name, f := range map[string]*fakeEngineSession{"remote": remote, "trusted-native": uncontained} {
		if c := f.closes(); len(c) != 1 || c[0] != "abort: SAFE: the record cannot be trusted" {
			t.Fatalf("SAFE must abort the %s session on the engine's own path, got %q", name, c)
		}
	}
	if c := kept.closes(); len(c) != 0 {
		t.Fatalf("the contained engine on the host's endpoints keeps its session, got %q", c)
	}

	// Entry under SAFE, on the application's plane.
	if _, err := plane.Bind("r2", "call2-in", "call2-out", true); !errors.Is(err, audio.ErrSafe) {
		t.Fatalf("a remote endpoint is refused at entry under SAFE: %v", err)
	}
	if _, err := plane.Bind("u2", "mic3", "spk3", false); !errors.Is(err, audio.ErrSafe) {
		t.Fatalf("an engine that is not contained is refused at entry under SAFE: %v", err)
	}
	b, err := plane.Bind("h2", "mic3", "spk3", true)
	if err != nil {
		t.Fatalf("a contained engine on the host's endpoints is allowed under SAFE: %v", err)
	}
	if !b.Contained || b.Remote {
		t.Fatalf("the binding carries what SAFE decided on: %+v", b)
	}
}
