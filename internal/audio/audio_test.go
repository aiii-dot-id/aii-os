package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"
)

var mono16k = Format{Rate: 16000, Channels: 1}

func pcm(n int) []byte {
	b := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(i))
	}
	return b
}

// .
// .
func TestCodecRoundTripAndRefusals(t *testing.T) {
	var buf bytes.Buffer
	frames := []Frame{
		{Kind: KindPCM, Stream: 1, Seq: 1, Start: 0, PCM: pcm(320)},
		{Kind: KindDiscontinuity, Stream: 1, Seq: 2, Start: 320},
		{Kind: KindPCM, Stream: 1, Seq: 3, Start: 480, PCM: pcm(10)},
		{Kind: KindEnd, Stream: 1, Seq: 4, Start: 490},
	}
	for _, fr := range frames {
		if err := WriteFrame(&buf, fr); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range frames {
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if got.Kind != want.Kind || got.Stream != want.Stream || got.Seq != want.Seq || got.Start != want.Start || !bytes.Equal(got.PCM, want.PCM) {
			t.Fatalf("frame %d: got %+v want %+v", i, got, want)
		}
	}
	if _, err := ReadFrame(&buf); err != io.EOF {
		t.Fatalf("after the last frame: %v", err)
	}
	if err := WriteFrame(&buf, Frame{Kind: KindEnd, PCM: []byte{1, 2}}); err == nil {
		t.Fatal("an end frame with a payload must be refused")
	}
	if err := WriteFrame(&buf, Frame{Kind: KindPCM, PCM: make([]byte, MaxFramePayload+2)}); !errors.Is(err, ErrFrameTooBig) {
		t.Fatalf("over the ceiling: %v", err)
	}
	if err := WriteFrame(&buf, Frame{Kind: 9}); !errors.Is(err, ErrBadKind) {
		t.Fatalf("bad kind: %v", err)
	}
	truncated := bytes.NewReader([]byte("AUD1\x01\x00\x00\x00"))
	if _, err := ReadFrame(truncated); err != io.ErrUnexpectedEOF {
		t.Fatalf("a truncated header: %v", err)
	}
}

// .
// .
func TestFileSourceKeepsTheSampleClock(t *testing.T) {
	src, err := NewFileSource(bytes.NewReader(pcm(1000)), mono16k, 320)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var next int64
	var last Frame
	for {
		fr, err := src.Read(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if fr.Kind == KindPCM {
			if fr.Start != next {
				t.Fatalf("span not contiguous: start %d, expected %d", fr.Start, next)
			}
			next = fr.End(mono16k)
		}
		last = fr
	}
	if last.Kind != KindEnd || last.Start != 1000 || next != 1000 {
		t.Fatalf("the stream must end at the exclusive sample 1000: last=%+v next=%d", last, next)
	}
	// .
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	binary.Write(&wav, binary.LittleEndian, uint32(36+8))
	wav.WriteString("WAVEfmt ")
	binary.Write(&wav, binary.LittleEndian, uint32(16))
	binary.Write(&wav, binary.LittleEndian, uint16(1))
	binary.Write(&wav, binary.LittleEndian, uint16(2))
	binary.Write(&wav, binary.LittleEndian, uint32(8000))
	binary.Write(&wav, binary.LittleEndian, uint32(8000*4))
	binary.Write(&wav, binary.LittleEndian, uint16(4))
	binary.Write(&wav, binary.LittleEndian, uint16(16))
	wav.WriteString("data")
	binary.Write(&wav, binary.LittleEndian, uint32(8))
	wav.Write(pcm(4))
	ws, err := NewFileSource(&wav, Format{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Format() != (Format{Rate: 8000, Channels: 2}) {
		t.Fatalf("wav format = %v", ws.Format())
	}
	fr, _ := ws.Read(ctx)
	if fr.Kind != KindPCM || fr.Samples(ws.Format()) != 2 {
		t.Fatalf("wav first frame: %+v", fr)
	}
}

// .
func TestPlaneExclusivityAndSafe(t *testing.T) {
	p := NewPlane()
	safe := false
	p.SafeMode = func() (string, bool) { return "test", safe }
	src, _ := NewFileSource(bytes.NewReader(pcm(10)), mono16k, 0)
	if err := p.Register(&Endpoint{ID: "mic", Label: "file", Source: src}); err != nil {
		t.Fatal(err)
	}
	if err := p.Register(&Endpoint{ID: "spk", Label: "capture", Sink: NewCaptureSink(mono16k)}); err != nil {
		t.Fatal(err)
	}
	if err := p.Register(&Endpoint{ID: "call", Label: "twilio", Remote: true, Sink: NewCaptureSink(mono16k)}); err != nil {
		t.Fatal(err)
	}
	b, err := p.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if b.InputHandle == "" || b.OutputHandle == "" || b.InFormat != mono16k || b.OutFormat != mono16k {
		t.Fatalf("binding: %+v", b)
	}
	if _, err := p.Bind("s2", "mic", "spk", true); !errors.Is(err, ErrEndpointBusy) {
		t.Fatalf("a second session on a held endpoint: %v", err)
	}
	if err := p.Unregister("mic"); !errors.Is(err, ErrEndpointBusy) {
		t.Fatalf("unregister while bound: %v", err)
	}
	if _, err := p.Bind("s1", "spk", "mic", true); !errors.Is(err, ErrNotASource) {
		t.Fatalf("an output-only endpoint as input: %v", err)
	}
	b.Release()
	b.Release()
	select {
	case <-b.Released():
	default:
		t.Fatal("Released must fire")
	}
	if _, err := p.Bind("s2", "mic", "spk", true); err != nil {
		t.Fatalf("after release the endpoints are free: %v", err)
	}
	// .
	// .
	safe = true
	p2 := NewPlane()
	p2.SafeMode = p.SafeMode
	src2, _ := NewFileSource(bytes.NewReader(pcm(10)), mono16k, 0)
	_ = p2.Register(&Endpoint{ID: "mic", Source: src2})
	_ = p2.Register(&Endpoint{ID: "spk", Sink: NewCaptureSink(mono16k)})
	_ = p2.Register(&Endpoint{ID: "call", Remote: true, Sink: NewCaptureSink(mono16k)})
	if _, err := p2.Bind("s", "mic", "spk", false); !errors.Is(err, ErrSafe) {
		t.Fatalf("uncontained under SAFE: %v", err)
	}
	if _, err := p2.Bind("s", "mic", "call", true); !errors.Is(err, ErrSafe) {
		t.Fatalf("remote endpoint under SAFE: %v", err)
	}
	if _, err := p2.Bind("s", "mic", "spk", true); err != nil {
		t.Fatalf("contained engine on host endpoints under SAFE: %v", err)
	}
}

// .
// .
type echoChannel struct {
	in  chan Frame
	out chan Frame
}

func newEcho() *echoChannel {
	e := &echoChannel{in: make(chan Frame, 64), out: make(chan Frame, 64)}
	go func() {
		for fr := range e.in {
			e.out <- fr
			if fr.Kind == KindEnd {
				close(e.out)
				return
			}
		}
		close(e.out)
	}()
	return e
}
func (e *echoChannel) WriteInput(fr Frame) error { e.in <- fr; return nil }
func (e *echoChannel) ReadOutput() (Frame, error) {
	fr, ok := <-e.out
	if !ok {
		return Frame{}, io.EOF
	}
	return fr, nil
}
func (e *echoChannel) Close() error { return nil }

// .
// .
// .
// .
func TestPumpsPreserveSpansAndHonorTheCutoffExactly(t *testing.T) {
	run := func(cutoff int64) (*CaptureSink, *Pump) {
		p := NewPlane()
		src, _ := NewFileSource(bytes.NewReader(pcm(1000)), mono16k, 320)
		sink := NewCaptureSink(mono16k)
		_ = p.Register(&Endpoint{ID: "mic", Source: src})
		_ = p.Register(&Endpoint{ID: "spk", Sink: sink})
		b, err := p.Bind("s1", "mic", "spk", true)
		if err != nil {
			t.Fatal(err)
		}
		pump := NewPump(b, newEcho())
		if cutoff >= 0 {
			if got := pump.Cutoff(cutoff); got != cutoff {
				t.Fatalf("cutoff honored as %d, asked %d", got, cutoff)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pump.Run(ctx)
		return sink, pump
	}
	// .
	sink, pump := run(-1)
	if !bytes.Equal(sink.PCM(), pcm(1000)) || sink.End() != 1000 || pump.Delivered() != 1000 || pump.Received() != 1000 {
		t.Fatalf("whole stream: %d bytes, end %d, delivered %d, received %d", len(sink.PCM()), sink.End(), pump.Delivered(), pump.Received())
	}
	var next int64
	for _, fr := range sink.Frames() {
		if fr.Kind == KindPCM {
			if fr.Start != next {
				t.Fatalf("output spans not contiguous at %d", fr.Start)
			}
			next = fr.End(mono16k)
		}
	}
	// .
	sink, pump = run(500)
	if !bytes.Equal(sink.PCM(), pcm(500)) || sink.End() != 500 || pump.Delivered() != 500 {
		t.Fatalf("cutoff 500: %d samples delivered, end %d, pump delivered %d", len(sink.PCM())/2, sink.End(), pump.Delivered())
	}
	// .
	sink, _ = run(640)
	if !bytes.Equal(sink.PCM(), pcm(640)) || sink.End() != 640 {
		t.Fatalf("cutoff 640: %d samples, end %d", len(sink.PCM())/2, sink.End())
	}
	// .
	p := NewPlane()
	src, _ := NewFileSource(bytes.NewReader(pcm(1000)), mono16k, 320)
	_ = p.Register(&Endpoint{ID: "mic", Source: src})
	_ = p.Register(&Endpoint{ID: "spk", Sink: NewCaptureSink(mono16k)})
	b, _ := p.Bind("s1", "mic", "spk", true)
	late := NewPump(b, newEcho())
	fr, _ := src.Read(context.Background())
	_ = late.ch.WriteInput(fr)
	late.mu.Lock()
	late.delivered = 320
	late.mu.Unlock()
	if got := late.Cutoff(100); got != 320 {
		t.Fatalf("a cutoff behind the delivered samples must be raised to them: %d", got)
	}
}

// .
// .
// .
// .
// .
func TestPumpsConvertAtTheHostsEndpoints(t *testing.T) {
	f48 := Format{Rate: 48000, Channels: 1}
	run := func(cutoff int64) (*CaptureSink, *Pump) {
		p := NewPlane()
		src, _ := NewFileSource(bytes.NewReader(pcm(3000)), f48, 320)
		sink := NewCaptureSink(f48)
		_ = p.Register(&Endpoint{ID: "mic", Source: src})
		_ = p.Register(&Endpoint{ID: "spk", Sink: sink})
		b, err := p.Bind("s1", "mic", "spk", true)
		if err != nil {
			t.Fatal(err)
		}
		pump := NewPump(b, newEcho())
		pump.EngineIn, pump.EngineOut = mono16k, mono16k
		if cutoff >= 0 {
			pump.Cutoff(cutoff)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pump.Run(ctx)
		return sink, pump
	}
	sink, pump := run(-1)
	if pump.Delivered() != 3000 || pump.Received() != 1000 || sink.End() != 3000 || len(sink.PCM())/2 != 3000 {
		t.Fatalf("whole stream: delivered %d (source clock), received %d (engine clock), sink end %d, %d sink samples", pump.Delivered(), pump.Received(), sink.End(), len(sink.PCM())/2)
	}
	if got := pump.EngineCutoff(1000); got != 334 {
		t.Fatalf("a cutoff at source 1000 is engine 334, got %d", got)
	}
	sink, pump = run(1000)
	if pump.Delivered() != 1000 || pump.Received() != 334 || sink.End() != 1002 || len(sink.PCM())/2 != 1002 {
		t.Fatalf("cutoff 1000: delivered %d, received %d, sink end %d, %d sink samples", pump.Delivered(), pump.Received(), sink.End(), len(sink.PCM())/2)
	}
}
