package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

// .
type replayChannel struct {
	out  []audio.Frame
	next int
	hold chan struct{}
}

func (c *replayChannel) WriteInput(audio.Frame) error { return nil }
func (c *replayChannel) ReadOutput() (audio.Frame, error) {
	if c.next < len(c.out) {
		fr := c.out[c.next]
		c.next++
		return fr, nil
	}
	<-c.hold
	return audio.Frame{}, io.EOF
}
func (c *replayChannel) Close() error { return nil }

func readReport(p *epochProbe) (json.RawMessage, map[string]any) {
	p.t.Helper()
	frame, err := bbb.ReadFrame(p.reader, bbb.MaxControlFrameBytes)
	if err != nil {
		p.t.Fatal(err)
	}
	var req struct {
		ID     json.RawMessage
		Params struct {
			Operation string
			Arguments map[string]any
		}
	}
	if err := json.Unmarshal(frame, &req); err != nil {
		p.t.Fatal(err)
	}
	if req.Params.Operation != "speech.session.playback_report" {
		p.t.Fatalf("got %s, want speech.session.playback_report", req.Params.Operation)
	}
	return req.ID, req.Params.Arguments
}

// .
// .
// .
// .
// .
// .
func TestReviewPlaybackReportIsBoundValidatedMappedAndForwarded(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	eng, spk := audio.Format{Rate: 24000, Channels: 1}, audio.Format{Rate: 48000, Channels: 1}
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: &stillSource{f: spk}}); err != nil {
		t.Fatal(err)
	}
	sink := audio.NewCaptureSink(spk)
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: sink}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	pcm := func(n int) []byte { return make([]byte, 2*n) }
	ch := &replayChannel{hold: make(chan struct{}), out: []audio.Frame{
		{Kind: audio.KindPCM, Stream: 3, Seq: 1, Start: 0, PCM: pcm(2400)},
		{Kind: audio.KindEnd, Stream: 3, Seq: 2, Start: 2400},
		{Kind: audio.KindPCM, Stream: 4, Seq: 1, Start: 0, PCM: pcm(1000)},
	}}
	pump := audio.NewPump(b, ch)
	pump.EngineIn, pump.EngineOut = eng, eng
	pctx, pcancel := context.WithCancel(context.Background())
	t.Cleanup(func() { close(ch.hold); pcancel() })
	go pump.Run(pctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := pump.OutputStream(4); ok && st.Written > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	p.v.mu.Lock()
	p.v.binding, p.v.pump = b, pump
	p.v.mu.Unlock()
	// .
	probeEvent(p, `{"type":"synthesis_start","session_id":"s1","sequence":1,"synthesis_id":"syn-a","output_stream":3}`)
	p.observed()
	probeEvent(p, `{"type":"synthesis_start","session_id":"s1","sequence":2,"synthesis_id":"syn-b","output_stream":4}`)
	p.observed()
	ctx := p.ctx

	refused := func(r PlaybackReport, want string) {
		t.Helper()
		err := p.v.PlaybackReportFor(ctx, "s1", r)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("report %+v must be refused with %q, got %v", r, want, err)
		}
	}
	refused(PlaybackReport{Stream: 3, Rendered: 100, Rate: 44100, Channels: 1}, "speaker")
	refused(PlaybackReport{Stream: 9, Rendered: 0, Rate: 48000, Channels: 1}, "never delivered")
	refused(PlaybackReport{Stream: 3, Rendered: 4801, Rate: 48000, Channels: 1}, "exceeds")
	refused(PlaybackReport{Stream: 3, Rendered: 4000, Rate: 48000, Channels: 1, Terminal: true, Outcome: "drained"}, "every sample rendered")
	refused(PlaybackReport{Stream: 4, Rendered: 0, Rate: 48000, Channels: 1, Terminal: true, Outcome: "drained"}, "END")
	refused(PlaybackReport{Stream: 3, Rendered: 100, Rate: 48000, Channels: 1, Terminal: true, Outcome: "progress"}, "names its outcome")
	refused(PlaybackReport{Stream: 3, Rendered: 100, Rate: 48000, Channels: 1, Outcome: "vanished"}, "not drained, stopped or progress")
	if err := p.v.PlaybackReportFor(ctx, "s0", PlaybackReport{Stream: 3, Rendered: 0, Rate: 48000, Channels: 1}); err == nil {
		t.Fatal("a report bound to another session is refused")
	}

	// .
	done := make(chan error, 1)
	go func() {
		done <- p.v.PlaybackReportFor(ctx, "s1", PlaybackReport{Stream: 3, Rendered: 2400, Rate: 48000, Channels: 1, Outcome: "progress"})
	}()
	id, args := readReport(p)
	if args["synthesis_id"] != "syn-a" || args["output_stream"] != float64(3) || args["rendered_samples"] != float64(1200) || args["terminal"] != false || args["session_id"] != "s1" {
		t.Fatalf("the control carries the bound synthesis and the ENGINE-clock count: %v", args)
	}
	p.reply(id, `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// .
	refused(PlaybackReport{Stream: 3, Rendered: 2399, Rate: 48000, Channels: 1}, "backward")
	// .
	go func() {
		done <- p.v.PlaybackReportFor(ctx, "s1", PlaybackReport{Stream: 3, Rendered: 4800, Rate: 48000, Channels: 1, Terminal: true, Outcome: "drained"})
	}()
	id, args = readReport(p)
	if args["rendered_samples"] != float64(2400) || args["terminal"] != true {
		t.Fatalf("a complete rendering is the engine's exact count: %v", args)
	}
	p.reply(id, `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// .
	go func() {
		done <- p.v.PlaybackReportFor(ctx, "s1", PlaybackReport{Stream: 4, Rendered: 500, Rate: 48000, Channels: 1, Terminal: true, Outcome: "stopped"})
	}()
	id, _ = readReport(p)
	p.frames <- []byte(`{"jsonrpc":"2.0","id":` + string(id) + `,"error":{"code":-32000,"message":"PLAYBACK_REPORT: output still live"}}`)
	if err := <-done; err == nil || !strings.Contains(err.Error(), "output still live") {
		t.Fatalf("the engine's refusal is relayed: %v", err)
	}
}

// .
type stillSource struct{ f audio.Format }

func (s *stillSource) Format() audio.Format                      { return s.f }
func (s *stillSource) Read(context.Context) (audio.Frame, error) { return audio.Frame{}, io.EOF }

// .
type dropPCMSink struct{ f audio.Format }

func (s *dropPCMSink) Format() audio.Format { return s.f }
func (s *dropPCMSink) Close() error         { return nil }
func (s *dropPCMSink) Write(_ context.Context, fr audio.Frame) error {
	if fr.Kind == audio.KindPCM {
		return errors.New("the speaker refused this frame")
	}
	return nil
}

// .
// .
// .
// .
// .
// .
func TestReviewRefusedDeliveryCannotBeDrained(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	f := audio.Format{Rate: 24000, Channels: 1}
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: &stillSource{f: f}}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: &dropPCMSink{f: f}}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	ch := &replayChannel{hold: make(chan struct{}), out: []audio.Frame{
		{Kind: audio.KindPCM, Stream: 3, Seq: 1, Start: 0, PCM: make([]byte, 480)},
		{Kind: audio.KindEnd, Stream: 3, Seq: 2, Start: 240},
	}}
	pump := audio.NewPump(b, ch)
	pump.EngineIn, pump.EngineOut = f, f
	pctx, pcancel := context.WithCancel(context.Background())
	t.Cleanup(func() { close(ch.hold); pcancel() })
	go pump.Run(pctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := pump.OutputStream(3); ok && st.Ended {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	st, _ := pump.OutputStream(3)
	if st.Written != 0 || st.Refused != 1 || !st.Ended {
		t.Fatalf("the refusal is accounted: %+v", st)
	}
	p.v.mu.Lock()
	p.v.binding, p.v.pump = b, pump
	p.v.mu.Unlock()
	probeEvent(p, `{"type":"synthesis_start","session_id":"s1","sequence":1,"synthesis_id":"syn-a","output_stream":3}`)
	p.observed()
	err = p.v.PlaybackReportFor(p.ctx, "s1", PlaybackReport{Stream: 3, Rendered: 0, Rate: 24000, Channels: 1, Terminal: true, Outcome: "drained"})
	if err == nil || !strings.Contains(err.Error(), "never delivered whole") {
		t.Fatalf("a reply the speaker refused cannot be drained by a zero report: %v", err)
	}
	// .
	// .
	done := make(chan error, 1)
	go func() {
		done <- p.v.PlaybackReportFor(p.ctx, "s1", PlaybackReport{Stream: 3, Rendered: 0, Rate: 24000, Channels: 1, Outcome: "progress"})
	}()
	id, args := readReport(p)
	if args["rendered_samples"] != float64(0) || args["terminal"] != false {
		t.Fatalf("the progress report is what reaches the engine: %v", args)
	}
	p.reply(id, `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
