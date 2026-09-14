package audio

import (
	"context"
	"errors"
	"io"
	"testing"
)

// .
type scriptedChannel struct {
	out  []Frame
	next int
}

func (c *scriptedChannel) WriteInput(Frame) error { return nil }
func (c *scriptedChannel) ReadOutput() (Frame, error) {
	if c.next >= len(c.out) {
		return Frame{}, io.EOF
	}
	fr := c.out[c.next]
	c.next++
	return fr, nil
}
func (c *scriptedChannel) Close() error { return nil }

// .
// .
// .
// .
// .
// .
func TestOutputAccountingAndEngineRenderedMapping(t *testing.T) {
	eng, spk := Format{Rate: 24000, Channels: 1}, Format{Rate: 48000, Channels: 1}
	plane := NewPlane()
	if err := plane.Register(&Endpoint{ID: "mic", Source: &silentSource{f: spk}}); err != nil {
		t.Fatal(err)
	}
	sink := NewCaptureSink(spk)
	if err := plane.Register(&Endpoint{ID: "spk", Sink: sink}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	pcm := func(n int) []byte { return make([]byte, 2*n) }
	ch := &scriptedChannel{out: []Frame{
		{Kind: KindPCM, Stream: 7, Seq: 1, Start: 0, PCM: pcm(240)},
		{Kind: KindPCM, Stream: 7, Seq: 2, Start: 240, PCM: pcm(241)},
		{Kind: KindEnd, Stream: 7, Seq: 3, Start: 481},
		{Kind: KindPCM, Stream: 8, Seq: 1, Start: 0, PCM: pcm(100)},
	}}
	p := NewPump(b, ch)
	p.EngineIn, p.EngineOut = eng, eng
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	<-p.Done()

	st7, ok := p.OutputStream(7)
	if !ok || st7.EngineReceived != 481 || !st7.Ended {
		t.Fatalf("stream 7 as the engine sent it: %+v ok=%v", st7, ok)
	}
	if got := int64(len(sink.StreamPCM(7)) / 2); got != st7.Written || got < 960 || got > 963 {
		t.Fatalf("written is what reached the sink: written=%d sink=%d", st7.Written, got)
	}
	if _, ok := p.OutputStream(9); ok {
		t.Fatal("a stream never delivered is not accounted")
	}
	// .
	if n, err := p.EngineRendered(7, 480); err != nil || n != 240 {
		t.Fatalf("480 sink samples on a 2:1 ratio = 240 engine samples, got %d (%v)", n, err)
	}
	// .
	// .
	if n, err := p.EngineRendered(7, st7.Written); err != nil || n != 481 {
		t.Fatalf("a complete rendering is the engine's own count 481, got %d (%v)", n, err)
	}
	// .
	if _, err := p.EngineRendered(7, st7.Written+1); err == nil {
		t.Fatal("rendered beyond written must be refused")
	}
	st8, _ := p.OutputStream(8)
	if st8.Ended || st8.EngineReceived != 100 {
		t.Fatalf("stream 8 is open: %+v", st8)
	}
	if n, err := p.EngineRendered(8, st8.Written); err != nil || n != st8.Written*24000/48000 || n > 100 {
		t.Fatalf("an open stream maps by floor and never beyond what the engine sent: %d of written %d (%v)", n, st8.Written, err)
	}
	if _, err := p.EngineRendered(9, 0); err == nil {
		t.Fatal("a stream never delivered has no mapping")
	}
}

// .
// .
type dropPCMSink struct{ f Format }

func (s *dropPCMSink) Format() Format { return s.f }
func (s *dropPCMSink) Close() error   { return nil }
func (s *dropPCMSink) Write(_ context.Context, fr Frame) error {
	if fr.Kind == KindPCM {
		return errors.New("the speaker refused this frame")
	}
	return nil
}

// .
// .
// .
// .
func TestRefusedOutputIsNeverCompletePlayback(t *testing.T) {
	f := Format{Rate: 24000, Channels: 1}
	plane := NewPlane()
	if err := plane.Register(&Endpoint{ID: "mic", Source: &silentSource{f: f}}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&Endpoint{ID: "spk", Sink: &dropPCMSink{f: f}}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPump(b, &scriptedChannel{out: []Frame{
		{Kind: KindPCM, Stream: 7, Seq: 1, Start: 0, PCM: make([]byte, 480)},
		{Kind: KindEnd, Stream: 7, Seq: 2, Start: 240},
	}})
	p.EngineIn, p.EngineOut = f, f
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	<-p.Done()

	st, ok := p.OutputStream(7)
	if !ok || st.Written != 0 || st.Refused != 1 || !st.Ended || st.EngineReceived != 240 {
		t.Fatalf("the refusal is accounted, not hidden: %+v ok=%v", st, ok)
	}
	if n, err := p.EngineRendered(7, 0); err != nil || n != 0 {
		t.Fatalf("zero rendered after a refused write must certify nothing, got %d (%v)", n, err)
	}
	if dropped, _, _ := p.Errors(); dropped != 1 {
		t.Fatalf("the refused frame is counted: %d", dropped)
	}
}

// .
type silentSource struct{ f Format }

func (s *silentSource) Format() Format                      { return s.f }
func (s *silentSource) Read(context.Context) (Frame, error) { return Frame{}, io.EOF }
