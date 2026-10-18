package pluginhost

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// A FINISHED REPLY IS NOT REMEMBERED FOR EVER. Three tables were keyed
// per output stream — the pump's record of what it wrote, and the
// session's synthesis id and rendered count — and none of them were
// ever emptied except by a session OPENING. One entry per spoken reply,
// reclaimed only when the session ended.
//
// That was invisible while every session ended. The operator may now
// set a capture limit of zero, which means it does not end, so a table
// bounded only by sessions ending is bounded by nothing. A terminal
// playback report is the last thing that can legitimately name a
// stream, and it is where the record goes.
func TestATerminalPlaybackReportRetiresTheStream(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	eng, spk := audio.Format{Rate: 24000, Channels: 1}, audio.Format{Rate: 48000, Channels: 1}
	plane := audio.NewPlane()
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: &stillSource{f: spk}}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: audio.NewCaptureSink(spk)}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	ch := &replayChannel{hold: make(chan struct{}), out: []audio.Frame{
		{Kind: audio.KindPCM, Stream: 3, Seq: 1, Start: 0, PCM: make([]byte, 2*2400)},
		{Kind: audio.KindEnd, Stream: 3, Seq: 2, Start: 2400},
	}}
	pump := audio.NewPump(b, ch)
	pump.EngineIn, pump.EngineOut = eng, eng
	pctx, pcancel := context.WithCancel(context.Background())
	t.Cleanup(func() { close(ch.hold); pcancel() })
	go pump.Run(pctx)

	deadline := time.Now().Add(5 * time.Second)
	var st audio.OutputStream
	for time.Now().Before(deadline) {
		if got, ok := pump.OutputStream(3); ok && got.Ended {
			st = got
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !st.Ended {
		t.Fatal("the reply never finished being written to the speaker")
	}

	p.v.mu.Lock()
	p.v.binding, p.v.pump = b, pump
	p.v.mu.Unlock()
	probeEvent(p, `{"type":"synthesis_start","session_id":"s1","sequence":1,"synthesis_id":"syn-a","output_stream":3}`)
	p.observed()

	p.v.mu.Lock()
	remembered := len(p.v.streams)
	p.v.mu.Unlock()
	if remembered == 0 {
		t.Fatal("precondition: the session never learnt which synthesis owns the stream")
	}

	// The page reports the reply played whole. That is the last word on
	// this stream. The report is forwarded to the engine and waits for
	// its answer, so it is driven the way a real one is.
	done := make(chan error, 1)
	go func() {
		done <- p.v.PlaybackReportFor(context.Background(), "s1", PlaybackReport{
			Stream: 3, Rendered: st.Written, Rate: spk.Rate, Channels: spk.Channels,
			Terminal: true, Outcome: "drained"})
	}()
	id, args := readReport(p)
	if args["terminal"] != true {
		t.Fatalf("the forwarded control is not terminal: %v", args)
	}
	p.reply(id, `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatalf("a drained report on a finished reply: %v", err)
	}

	p.v.mu.Lock()
	streams, reported := len(p.v.streams), len(p.v.reported)
	p.v.mu.Unlock()
	if streams != 0 || reported != 0 {
		t.Fatalf("the session still remembers a finished reply: streams=%d reported=%d", streams, reported)
	}
	if _, ok := pump.OutputStream(3); ok {
		t.Fatal("the pump still remembers a finished reply")
	}

	// And a second report for it is refused BEFORE any control reaches
	// the engine, saying which of the two things it is rather than
	// asserting the one it cannot tell apart.
	err = p.v.PlaybackReportFor(context.Background(), "s1", PlaybackReport{
		Stream: 3, Rendered: st.Written, Rate: spk.Rate, Channels: spk.Channels,
		Terminal: true, Outcome: "drained"})
	if err == nil {
		t.Fatal("a stream reported terminal must not accept another report")
	}
	if !strings.Contains(err.Error(), "not open") || !strings.Contains(err.Error(), "already reported terminal") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}
