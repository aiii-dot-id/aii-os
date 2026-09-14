package audio

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
func TestOutputPumpCarriesRepliesAcrossPerStreamEnds(t *testing.T) {
	p := NewPlane()
	src, _ := NewFileSource(bytes.NewReader(pcm(320)), mono16k, 320)
	sink := NewCaptureSink(mono16k)
	_ = p.Register(&Endpoint{ID: "mic", Source: src})
	_ = p.Register(&Endpoint{ID: "spk", Sink: sink})
	b, err := p.Bind("s1", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	pump := NewPump(b, newTwoReplies())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pump.Run(ctx)

	streams := sink.Streams()
	if len(streams) != 2 {
		t.Fatalf("both replies must reach the sink under distinct stream ids, got %v", streams)
	}
	if !bytes.Equal(sink.StreamPCM(7), pcm(160)) || sink.StreamEnd(7) != 160 {
		t.Fatalf("reply one: %d samples, end %d", len(sink.StreamPCM(7))/2, sink.StreamEnd(7))
	}
	if !bytes.Equal(sink.StreamPCM(8), pcm(240)) || sink.StreamEnd(8) != 240 {
		t.Fatalf("reply two (after reply one's END): %d samples, end %d", len(sink.StreamPCM(8))/2, sink.StreamEnd(8))
	}
}

// .
// .
// .
type twoReplies struct{ out chan Frame }

func newTwoReplies() *twoReplies {
	r := &twoReplies{out: make(chan Frame, 8)}
	r.out <- Frame{Kind: KindPCM, Stream: 7, Seq: 1, Start: 0, PCM: pcm(160)}
	r.out <- Frame{Kind: KindEnd, Stream: 7, Seq: 2, Start: 160}
	r.out <- Frame{Kind: KindPCM, Stream: 8, Seq: 1, Start: 0, PCM: pcm(240)}
	r.out <- Frame{Kind: KindEnd, Stream: 8, Seq: 2, Start: 240}
	close(r.out)
	return r
}

func (r *twoReplies) WriteInput(Frame) error { return nil }
func (r *twoReplies) ReadOutput() (Frame, error) {
	fr, ok := <-r.out
	if !ok {
		return Frame{}, io.EOF
	}
	return fr, nil
}
func (r *twoReplies) Close() error { return nil }
