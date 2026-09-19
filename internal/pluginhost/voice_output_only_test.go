package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

// .
// .
// .
// .
// .
// .

// .
// .
type outputOnlyProbe struct {
	*epochProbe
	plane *audio.Plane
	b     *audio.Binding
	sink  *audio.CaptureSink
	pcm   *io.PipeWriter
}

func newOutputOnlyProbe(t *testing.T) *outputOnlyProbe {
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
	sink := audio.NewCaptureSink(mono16k)
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: &stillSource{f: mono16k}}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: sink}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.BindOutput("typed", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	return &outputOnlyProbe{epochProbe: p, plane: plane, b: b, sink: sink, pcm: w}
}

// .
func (p *outputOnlyProbe) request() (id json.RawMessage, op string, args map[string]any) {
	p.t.Helper()
	frame, err := bbb.ReadFrame(p.reader, bbb.MaxControlFrameBytes)
	if err != nil {
		p.t.Fatal(err)
	}
	var req struct {
		ID     json.RawMessage `json:"id"`
		Params struct {
			Operation string         `json:"operation"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(frame, &req); err != nil {
		p.t.Fatal(err)
	}
	return req.ID, req.Params.Operation, req.Params.Arguments
}

// .
func (p *outputOnlyProbe) open() (json.RawMessage, map[string]any, <-chan error) {
	p.t.Helper()
	opened := make(chan error, 1)
	go func() { opened <- p.v.OpenWithAudio(p.ctx, "typed", p.b, nil) }()
	id, op, args := p.request()
	if op != "speech.session.open" {
		p.t.Fatalf("the first control is %s", op)
	}
	return id, args, opened
}

const outputOnlyAdmission = `{"accepted":true,"audio":{"input":null,"output":{"rate":16000,"channels":1}}}`

func TestTheOutputOnlyOpenSaysSoInTheContractsWordsAndInventsNoInput(t *testing.T) {
	p := newOutputOnlyProbe(t)
	id, args, opened := p.open()

	if _, has := args["input_handle"]; has {
		t.Fatalf("an output-only open carries no input_handle at all — not null, not empty: %v", args["input_handle"])
	}
	if args["output_handle"] != p.b.OutputHandle || p.b.OutputHandle == "" {
		t.Fatalf("the output handle is required and is the binding's: %v", args["output_handle"])
	}
	au, _ := args["audio"].(map[string]any)
	in, present := au["input"]
	if !present || in != nil {
		t.Fatalf("audio.input must be PRESENT and null — omission is not the encoding: present=%v value=%v", present, in)
	}
	out, _ := au["output"].(map[string]any)
	if out["rate"] != float64(16000) || out["channels"] != float64(1) || au["format"] != "s16le" {
		t.Fatalf("the output format is the speaker's, as ever: %v", au)
	}

	p.reply(id, outputOnlyAdmission)
	if err := <-opened; err != nil {
		t.Fatalf("a confirmed output-only open: %v", err)
	}
	_, pump := p.v.Audio()
	if pump == nil || !p.v.OutputOnly() {
		t.Fatalf("an admitted output-only session runs its output pump: pump=%v outputOnly=%v", pump != nil, p.v.OutputOnly())
	}
	for _, ep := range p.plane.Endpoints() {
		if ep.ID == "mic" && ep.BoundTo != "" {
			t.Fatalf("an output-only session holds the microphone: bound to %q", ep.BoundTo)
		}
	}

	// .
	// .
	if err := p.v.FinishInput(p.ctx, "in:typed:mic", 0); !errors.Is(err, ErrNoInput) {
		t.Fatalf("finish_input on a session with no input: %v", err)
	}
	if _, err := p.v.FinishInputNow(p.ctx, "in:typed:mic"); !errors.Is(err, ErrNoInput) {
		t.Fatalf("FinishInputNow on a session with no input: %v", err)
	}
	status := make(chan error, 1)
	go func() { _, err := p.v.Status(p.ctx); status <- err }()
	sid, op, _ := p.request()
	if op != "speech.session.status" {
		t.Fatalf("a refused finish reached the engine anyway: the next control was %s", op)
	}
	p.reply(sid, `{"session_id":"typed","state_sequence":1,"lifecycle":"open","input":{"state":"absent"},"recognition":{"state":"inactive"},"input_completion":null}`)
	if err := <-status; err != nil {
		t.Fatal(err)
	}
	p.v.mu.Lock()
	snap := p.v.lastSnapshot
	p.v.mu.Unlock()
	if snap.Input.State != "absent" || snap.Recognition.State != "inactive" || snap.InputCompletion != nil {
		t.Fatalf("the absent-input status did not survive the driver: %+v", snap)
	}
	if p.v.Faulted() {
		t.Fatalf("an honest output-only session is not faulted: %s", p.v.FaultReason())
	}

	// .
	probeEvent(p.epochProbe, `{"type":"synthesis_start","session_id":"typed","synthesis_id":"reply-1","output_stream":7}`)
	for _, fr := range []audio.Frame{
		{Kind: audio.KindPCM, Stream: 7, Seq: 1, Start: 0, PCM: []byte{1, 2, 3, 4}},
		{Kind: audio.KindEnd, Stream: 7, Seq: 2, Start: 2},
	} {
		if err := audio.WriteFrame(p.pcm, fr); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(p.sink.Frames()) != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := p.sink.Frames(); len(got) != 2 || got[1].Kind != audio.KindEnd {
		t.Fatalf("the typed reply did not reach the speaker whole: %+v", got)
	}
}

// .
// .
// .
// .
// .
func TestAnUnconfirmedTopologyIsRefusedAndCustodyHolds(t *testing.T) {
	cases := []struct{ name, admission, says string }{
		{"the key left out", `{"accepted":true,"audio":{"output":{"rate":16000,"channels":1}}}`, "omission is not confirmation"},
		{"a microphone path nobody asked for", `{"accepted":true,"audio":{"input":{"rate":16000,"channels":1},"output":{"rate":16000,"channels":1}}}`, "did not bind"},
		{"no output format", `{"accepted":true,"audio":{"input":null}}`, "output format is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newOutputOnlyProbe(t)
			id, _, opened := p.open()
			p.reply(id, tc.admission)
			err := <-opened
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Fatalf("the open must be refused and say why (%q): %v", tc.says, err)
			}
			if _, pump := p.v.Audio(); pump != nil {
				t.Fatal("audio was pumped on an unconfirmed topology")
			}
			if !p.v.Faulted() {
				t.Fatal("the instance must be faulted")
			}
			// .
			cid, op, args := p.request()
			if op != "speech.session.close" || args["mode"] != "abort" {
				t.Fatalf("the host must ask for an abort, got %s %v", op, args)
			}
			p.reply(cid, `{"accepted":true}`)
			// .
			select {
			case <-p.b.Released():
				t.Fatal("custody was released on a refusal alone")
			case <-time.After(50 * time.Millisecond):
			}
			p.event("session_end", "typed", 1)
			select {
			case <-p.b.Released():
			case <-time.After(3 * time.Second):
				t.Fatal("the engine's terminal word must give the speaker back")
			}
		})
	}
}

// .
// .
func TestADuplexOpenAnsweredAsOutputOnlyIsRefused(t *testing.T) {
	if _, _, err := engineFormats(json.RawMessage(outputOnlyAdmission), true); err == nil || !strings.Contains(err.Error(), "both input and output") {
		t.Fatalf("a duplex open needs both formats: %v", err)
	}
	if in, out, err := engineFormats(json.RawMessage(outputOnlyAdmission), false); err != nil || in != (audio.Format{}) || out != mono16k {
		t.Fatalf("the confirmed output-only admission: in=%s out=%s err=%v", in, out, err)
	}
}

// .
// .
// .
func TestAnInputCompletionOnAnOutputOnlySessionIsAContradiction(t *testing.T) {
	for _, carrier := range []string{"event", "status"} {
		t.Run(carrier, func(t *testing.T) {
			p := newOutputOnlyProbe(t)
			id, _, opened := p.open()
			p.reply(id, outputOnlyAdmission)
			if err := <-opened; err != nil {
				t.Fatal(err)
			}
			if carrier == "event" {
				probeEvent(p.epochProbe, `{"type":"input_finished","session_id":"typed","sequence":1,"stream_id":"in:typed:mic","end_sample":0,"processed_end_sample":0}`)
			} else {
				status := make(chan error, 1)
				go func() { _, err := p.v.Status(p.ctx); status <- err }()
				sid, _, _ := p.request()
				p.reply(sid, `{"session_id":"typed","state_sequence":1,"lifecycle":"open","input_completion":{"stream_id":"in:typed:mic","end_sample":0,"processed_end_sample":0,"sequence":1}}`)
				<-status
			}
			deadline := time.Now().Add(3 * time.Second)
			for !p.v.Faulted() && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if !p.v.Faulted() || !strings.Contains(p.v.FaultReason(), "output-only") {
				t.Fatalf("the contradiction must fault the session and say so: faulted=%v %q", p.v.Faulted(), p.v.FaultReason())
			}
			if done, _ := p.v.InputFinished(); done {
				t.Fatal("an input that does not exist was recorded as finished")
			}
		})
	}
}

// .
// .
// .
// .
// .
func TestAnOutputOnlySessionSpeaksThroughARealEngineAndDrainsWithoutAMicrophone(t *testing.T) {
	ap := audioVoicePlugin(t, []string{buildFakechild(t), "session-audio"}, "FAKE_SPEAK=1000")
	ctx := context.Background()
	plane := audio.NewPlane()
	sink := audio.NewCaptureSink(mono16k)
	_ = plane.Register(&audio.Endpoint{ID: "spk", Label: "capture", Sink: sink})
	b, err := plane.BindOutput("typed", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.OpenWithAudio(ctx, "typed", b, nil); err != nil {
		t.Fatalf("the output-only open: %v", err)
	}
	if !ap.Voice.OutputOnly() {
		t.Fatal("the session must know it has no input")
	}
	if err := ap.Voice.Synthesize(ctx, "reply-1", "typed words"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for sink.End() != 1000 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if sink.End() != 1000 || !bytes.Equal(sink.PCM(), pcmRamp(1000)) {
		t.Fatalf("the typed reply did not arrive whole: end=%d faulted=%v (%s) stray=%d unnamed=%d",
			sink.End(), ap.Voice.Faulted(), ap.Voice.FaultReason(), ap.Voice.Stray(), ap.Voice.Unnamed())
	}
	_, pump := ap.Voice.Audio()
	if err := ap.Voice.Close(ctx, "drain", "said"); err != nil {
		t.Fatalf("a drain close with no input boundary: %v", err)
	}
	if !fired(pump.Done(), 5*time.Second) || !fired(b.Released(), 5*time.Second) {
		t.Fatal("the engine's terminal word must end the pump and give the speaker back")
	}
	if ap.Voice.Faulted() {
		t.Fatalf("nothing about this session was a fault: %s", ap.Voice.FaultReason())
	}
}

// .
// .
func TestARealEngineThatOmitsTheTopologyIsRefused(t *testing.T) {
	for _, env := range []string{"FAKE_OMIT_INPUT=1", "FAKE_DUPLEX_ANYWAY=1"} {
		t.Run(env, func(t *testing.T) {
			ap := audioVoicePlugin(t, []string{buildFakechild(t), "session-audio"}, env)
			plane := audio.NewPlane()
			_ = plane.Register(&audio.Endpoint{ID: "spk", Sink: audio.NewCaptureSink(mono16k)})
			b, err := plane.BindOutput("typed", "spk", true)
			if err != nil {
				t.Fatal(err)
			}
			if err := ap.Voice.OpenWithAudio(context.Background(), "typed", b, nil); err == nil {
				t.Fatal("an unconfirmed topology was accepted")
			}
			if _, pump := ap.Voice.Audio(); pump != nil {
				t.Fatal("audio was pumped on an unconfirmed topology")
			}
		})
	}
}

// .
// .
// .
// .
func TestANamingWordWithoutASessionBindsNothing(t *testing.T) {
	h := auditNewAudioHarness(t)
	sink := audio.NewCaptureSink(mono16k)
	h.open(t, "s", sink)
	h.frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"synthesis_start","synthesis_id":"anon","output_stream":9}}`)
	auditAudioWait(t, "the session is told", h.v.Faulted)
	if why := h.v.FaultReason(); !strings.Contains(why, "output stream 9") || !strings.Contains(why, "session_id") {
		t.Fatalf("the fault does not say what was missing: %q", why)
	}
	ownFrame(t, h, 0, 9)
	auditAudioWait(t, "its audio waits for a name it never got", func() bool { return ownWaiting(h.v.pair) == 1 })
	if n := len(sink.Frames()); n != 0 {
		t.Fatalf("audio named by nobody in particular reached the speaker: %d", n)
	}
}

// .
// .
// .
// .
// .
// .
func TestAnOutputOnlySessionRefusesWhatItCannotHear(t *testing.T) {
	for _, ev := range []string{
		`{"type":"transcript_final","session_id":"typed","sequence":1,"text":"words without a microphone"}`,
		`{"type":"speaker_observation","session_id":"typed","sequence":2,"refers_to":1,"speaker":"nobody","decision":"known"}`,
		`{"type":"input_finished","session_id":"typed","sequence":3,"cutoff":{"samples":0}}`,
	} {
		p := newOutputOnlyProbe(t)
		id, _, opened := p.open()
		p.reply(id, outputOnlyAdmission)
		if err := <-opened; err != nil {
			t.Fatal(err)
		}
		probeEvent(p.epochProbe, ev)
		var typ struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(ev), &typ)
		select {
		case got := <-p.v.Observe():
			if got.Type == typ.Type {
				t.Fatalf("the driver forwarded %s on a session opened with no input; faulted=%v", typ.Type, p.v.Faulted())
			}
		case <-time.After(300 * time.Millisecond):
		}
		if !p.v.Faulted() {
			t.Fatalf("%s on a session opened with no input was neither refused nor faulted", typ.Type)
		}
	}
}

// .
// .
// .
// .
func TestTheAdmissionRefusesAnyEncodingButS16LE(t *testing.T) {
	for _, format := range []string{`"f32le"`, `"s24le"`, `null`, `16`, `""`} {
		raw := json.RawMessage(fmt.Sprintf(`{"accepted":true,"audio":{"format":%s,"input":null,"output":{"rate":16000,"channels":1}}}`, format))
		if _, _, err := engineFormats(raw, false); err == nil || !strings.Contains(err.Error(), "not s16le") {
			t.Fatalf("audio.format=%s was admitted, or refused for another reason: %v", format, err)
		}
	}
	for _, ok := range []string{`{"accepted":true,"audio":{"format":"s16le","input":null,"output":{"rate":16000,"channels":1}}}`, outputOnlyAdmission} {
		if _, _, err := engineFormats(json.RawMessage(ok), false); err != nil {
			t.Fatalf("s16le, named or meant, must be confirmed: %v", err)
		}
	}
}
