package pluginhost

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// checkProbe is a driver over synthetic control and audio lanes with a
// plane of a microphone and a speaker, opened as a check.
type checkProbe struct {
	*epochProbe
	b *audio.Binding
}

func newCheckProbe(t *testing.T, id string) *checkProbe {
	t.Helper()
	p := newEpochProbe(t)
	r, w := io.Pipe()
	p.v.pair = newAudioPair(io.Discard, r)
	t.Cleanup(func() {
		p.v.mu.Lock()
		p.v.releaseAudioLocked()
		p.v.mu.Unlock()
		_ = w.Close()
		_ = r.Close()
	})
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: &stillSource{f: mono16k}}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: audio.NewCaptureSink(mono16k)}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind(id, "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	return &checkProbe{epochProbe: p, b: b}
}

const duplexAdmission = `{"accepted":true,"audio":{"input":{"rate":16000,"channels":1},"output":{"rate":16000,"channels":1}}}`

// openCheck opens the check's instance and returns its events and what
// reached the engine.
func (p *checkProbe) openCheck(id string) (<-chan Event, map[string]any) {
	p.t.Helper()
	type opened struct {
		events <-chan Event
		err    error
	}
	done := make(chan opened, 1)
	go func() {
		events, err := p.v.OpenCheck(p.ctx, id, p.b)
		done <- opened{events, err}
	}()
	reqID, op, args := (&outputOnlyProbe{epochProbe: p.epochProbe}).request()
	if op != "speech.session.open" {
		p.t.Fatalf("the first control is %s", op)
	}
	p.reply(reqID, duplexAdmission)
	o := <-done
	if o.err != nil || o.events == nil {
		p.t.Fatalf("the check's open: %v %v", o.events, o.err)
	}
	return o.events, args
}

func (p *checkProbe) final(sid string, seq int, text string) {
	p.frames <- []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"transcript_final","session_id":%q,"sequence":%d,"text":%q}}`, sid, seq, text))
}

// nothingObserved fails, naming everything, if the application's observer
// or telemetry got anything within the wait.
func (p *checkProbe) nothingObserved(what string) {
	p.t.Helper()
	var got []string
	wait := time.After(300 * time.Millisecond)
	for {
		select {
		case ev := <-p.v.Observe():
			got = append(got, fmt.Sprintf("observer: %s of %s %s", ev.Type, ev.SessionID, ev.Raw))
			continue
		case ev := <-p.v.Telemetry():
			got = append(got, fmt.Sprintf("telemetry: %s of %s", ev.Type, ev.SessionID))
			continue
		case <-wait:
		}
		break
	}
	if len(got) != 0 {
		p.t.Fatalf("%s reached the application: %s", what, strings.Join(got, "; "))
	}
}

func expectCheckEvent(t *testing.T, events <-chan Event, typ string) Event {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatalf("the check's events ended before %s", typ)
		}
		if ev.Type != typ {
			t.Fatalf("the check got %s, want %s", ev.Type, typ)
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatalf("the check never got %s", typ)
	}
	return Event{}
}

// A CHECK'S TRANSCRIPT NEVER REACHES THE OBSERVER — before its session
// ended and after — nor its telemetry; the check hears all of it. The open
// says it is a check in the one word the engine keeps nothing for.
// Afterwards an operator's session is observed as ever, and the check's
// late words, now another session's, reach no one.
func TestACheckSessionsEventsReachTheCheckAndNeverTheObserver(t *testing.T) {
	p := newCheckProbe(t, "check-1")
	events, args := p.openCheck("check-1")
	if args["check"] != true || args["session_id"] != "check-1" {
		t.Fatalf("a check's open says so, and names its session: %v", args)
	}
	p.event("session_ready", "check-1", 1)
	p.frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"vad_probability","session_id":"check-1","sequence":2,"probability":0.9}}`)
	p.final("check-1", 3, "cobalt lantern")
	p.event("session_end", "check-1", 4)
	p.final("check-1", 5, "after the end")
	p.nothingObserved("the check's session")
	expectCheckEvent(t, events, "session_ready")
	if ev := expectCheckEvent(t, events, "transcript_final"); string(ev.Raw) == "" {
		t.Fatal("the check hears the words")
	}
	expectCheckEvent(t, events, "session_end")
	expectCheckEvent(t, events, "transcript_final")

	// The operator's session afterwards: its own words are observed; the
	// check's events end with its instance, and its late words reach no
	// one.
	p.open("vs-1")
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("the check's events must end when its instance is replaced")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the check's events were never closed")
	}
	p.final("check-1", 6, "a late word of the check's")
	p.final("vs-1", 1, "the operator speaking")
	select {
	case ev := <-p.v.Observe():
		if ev.SessionID != "vs-1" {
			t.Fatalf("the observer received %s of %s", ev.Type, ev.SessionID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the operator's own words must be observed")
	}
}

// A CHECK'S SESSION NEVER PINS ITS ACTIVATION. Stopping a candidate whose
// check's session the engine has not ended is established — the child is
// stopped, which resolves the session — never handed to the pinned watch.
func TestACheckSessionNeverPinsItsActivation(t *testing.T) {
	p := newCheckProbe(t, "check-1")
	p.openCheck("check-1") // open: the engine admitted it, nothing closed it
	v := p.v
	ap := pluginWithTestVoice(&ActivePlugin{ID: "id.test.engine", Version: "1.0.0"}, v)
	act := &Activation{ID: ap.ID, Version: ap.Version, Plugin: ap}
	h := NewRuntime(RuntimeWires{Pinned: func(*Activation, *VoiceSession) { t.Error("a check's session armed the pinned watch") }})
	if ap.Pinned() || !v.IsOpen() {
		t.Fatalf("an open check session: open %v (want true), pins its activation %v (want false)", v.IsOpen(), ap.Pinned())
	}
	ret, err := h.Stop(t.Context(), act)
	if err != nil || !ret.Established || len(ret.Residue) != 0 {
		t.Fatalf("stopping a candidate with an open check session: %+v, %v — want established", ret, err)
	}
}

// AN ENGINE THAT ADMITS A CHECK WITH FORMATS THE HOST CANNOT CARRY IS SAID
// SO, TYPED: the check judges it, and the session is held and aborted as
// any such open is.
func TestACheckOpenWithFormatsTheHostCannotCarryIsTyped(t *testing.T) {
	p := newCheckProbe(t, "check-2")
	done := make(chan error, 1)
	go func() {
		_, err := p.v.OpenCheck(p.ctx, "check-2", p.b)
		done <- err
	}()
	id := p.read("speech.session.open")
	p.reply(id, `{"accepted":true}`)
	var refused *FormatRefusedError
	if err := <-done; !errors.As(err, &refused) {
		t.Fatalf("an open admitted without formats is a typed refusal: %v", err)
	}
	p.read("speech.session.close")
}
