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

// .
// .
// .
type fakeEngineSession struct {
	mu           sync.Mutex
	current      string
	closed       []string
	synths       []string
	ops          []string
	onSynthesize func()
	// .
	// .
	// .
	// .
	// .
	entered       chan struct{}
	enteredOnce   sync.Once
	ackGate       chan struct{}
	producing     string
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
		f.producing = ""
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

// .
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

// .
// .
// .
// .
func (f *fakeEngineSession) SynthesizeForEnqueue(_ context.Context, sid, id, text string) (func(context.Context) error, error) {
	if err := f.admit("synthesize", sid); err != nil {
		return nil, err
	}
	// .
	// .
	// .
	// .
	f.mu.Lock()
	f.synths = append(f.synths, id+"|"+text)
	f.producing = id
	gate := f.ackGate
	f.mu.Unlock()
	if f.entered != nil {
		f.enteredOnce.Do(func() { close(f.entered) })
	}
	if f.onSynthesize != nil {
		f.onSynthesize()
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

// .
// .
// .
// .
// .
// .
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

	// .
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
