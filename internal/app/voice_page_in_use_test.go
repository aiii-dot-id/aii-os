package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestAnOutputSessionAskedForByTheOperatorsHandTakesTheEngineFromAnotherPages(t *testing.T) {
	a := speechRouteApp(t, "", "id.engine.first")
	engine, _ := runningEngine(t, a, "id.engine.first")
	opensReach(t)

	// A page that is only open holds the engine's output session.
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "held-spk", Sink: audio.NewCaptureSink(mono16k)}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.BindOutput("vs-held", "held-spk", true)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeEngineSession{}
	ended := make(chan struct{})
	held := &voiceHandle{id: "vs-held", v: f, b: b, done: ended, engine: engine}
	a.voiceSessions.Store(held.id, held)
	a.voiceModes.Store(held.id, false)
	closes := func() []string {
		f.mu.Lock()
		defer f.mu.Unlock()
		return append([]string(nil), f.closed...)
	}

	// Another page asks because the mode changed under it: nothing is taken.
	follower, err := a.OpenVoiceSession(context.Background(), "", "spk-2", voiceOutputMode, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := closes(); len(got) != 0 {
		t.Fatalf("a page that only follows the mode took the engine from the page that held it: %v", got)
	}
	fh := follower.(*voiceHandle)
	fh.v = &fakeEngineSession{} // a double stands where the engine admits its close
	if err := fh.Close(context.Background(), "abort"); err != nil {
		t.Fatal(err)
	}
	a.voiceSessions.Delete(fh.id)

	// The operator's hand on another page asks: the held session is asked to
	// abort, and the open waits for the engine's word that it is over.
	opened := make(chan error, 1)
	go func() {
		_, err := a.OpenVoiceSession(dashboard.WithOperatorGesture(context.Background()), "", "spk-3", voiceOutputMode, nil)
		opened <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(closes()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := closes(); len(got) != 1 || !strings.HasPrefix(got[0], "abort") {
		t.Fatalf("the output session another page held was not asked to give the engine up: %v", got)
	}
	select {
	case err := <-opened:
		t.Fatalf("the open went ahead before the engine said the other page's session was over: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(ended) // the engine's word
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("the page the operator is using was refused the voice: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the open never proceeded after the engine's word")
	}
}

// ONE OPEN AT A TIME, FROM BEFORE IT MAKES ROOM FOR ITSELF UNTIL THE ENGINE
// HAS ANSWERED IT. The page whose output session was taken asks for another
// the moment it is told, and nothing ordered that open against the one that
// took the engine: it could reach the engine first and take the voice back.
// So an open waits for the one being answered, and reaches the engine after
// it; and an open that is given up while it waits reaches no engine at all.
func TestAVoiceOpenWaitsForTheOneBeingAnswered(t *testing.T) {
	a := speechRouteApp(t, "", "id.engine.first")
	runningEngine(t, a, "id.engine.first")
	reached, answer := make(chan string, 4), make(chan error)
	prev := voiceEngineOpen
	t.Cleanup(func() { voiceEngineOpen = prev })
	voiceEngineOpen = func(_ *pluginhost.VoiceSession, _ context.Context, id string, _ *audio.Binding, _ map[string]any) error {
		reached <- id
		return <-answer
	}
	type result struct {
		s   dashboard.VoiceSession
		err error
	}
	open := func(ctx context.Context, speaker string) chan result {
		done := make(chan result, 1)
		go func() {
			s, err := a.OpenVoiceSession(ctx, "", speaker, voiceOutputMode, nil)
			done <- result{s, err}
		}()
		return done
	}
	at := func() string {
		t.Helper()
		select {
		case id := <-reached:
			return id
		case <-time.After(5 * time.Second):
			t.Fatal("no open reached the engine")
			return ""
		}
	}
	quiet := func(why string) {
		t.Helper()
		select {
		case id := <-reached:
			t.Fatalf("%s: open %s reached the engine", why, id)
		case <-time.After(100 * time.Millisecond):
		}
	}

	first := open(context.Background(), "spk")
	firstID := at()
	// A second page asks while the first open is with the engine, and a
	// third asks and goes away: neither reaches the engine yet.
	second := open(context.Background(), "spk-2")
	gone, leave := context.WithCancelCause(context.Background())
	third := open(gone, "spk-3")
	quiet("while the first open was still being answered")
	left := errors.New("the page went away")
	leave(left)
	select {
	case r := <-third:
		if r.err == nil || !errors.Is(r.err, left) || !strings.Contains(r.err.Error(), "given up while another was still being answered") {
			t.Fatalf("an open given up while it waited: %v", r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an open given up while it waited never returned")
	}
	quiet("after a waiting open was given up")

	// The engine answers the first: only now does the second reach it.
	answer <- nil
	r := <-first
	if r.err != nil || r.s == nil || r.s.ID() != firstID {
		t.Fatalf("the first open: %+v", r)
	}
	if secondID := at(); secondID == firstID {
		t.Fatalf("the engine was reached twice by open %s", firstID)
	}
	// It is refused, as an engine refuses an open beside a live session;
	// the turn is given back all the same.
	answer <- errors.New("BUSY: a session holds the engine (1 of 1)")
	if r := <-second; r.err == nil || !errors.Is(r.err, dashboard.ErrVoiceEngineRefused) {
		t.Fatalf("the second open: %+v", r)
	}
	// On the speaker the open that was given up never bound: the stand-in for
	// the engine's open does not give a refused open's binding back, as the
	// engine's own does.
	fourth := open(context.Background(), "spk-3")
	at()
	answer <- errors.New("BUSY: a session holds the engine (1 of 1)")
	if r := <-fourth; r.err == nil {
		t.Fatalf("an open after a refused one: %+v", r)
	}
}
