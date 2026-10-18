package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

type endingSource struct{ err error }

func (s endingSource) Format() Format                      { return mono16k }
func (s endingSource) Read(context.Context) (Frame, error) { return Frame{}, s.err }

type endingChannel struct{ err error }

func (c endingChannel) WriteInput(Frame) error     { return nil }
func (c endingChannel) ReadOutput() (Frame, error) { return Frame{}, c.err }
func (c endingChannel) Close() error               { return nil }

type endingReader struct{ err error }

func (r endingReader) Read([]byte) (int, error) { return 0, r.err }

// A WRAPPED io.EOF IS THE END IT WRAPS, asked with errors.Is: a source or
// an engine channel that ends with one ends the pump cleanly; a file that
// ends with one ends its stream; and a frame cut short by one is a
// truncated frame, never a clean end at a boundary. The control half: an
// error that is not an end is still a failure, said at once.
func TestAWrappedEOFIsTheEndItWraps(t *testing.T) {
	for _, c := range []struct {
		err   error
		clean bool
	}{{fmt.Errorf("the page went away: %w", io.EOF), true}, {errors.New("the page broke"), false}} {
		p := NewPlane()
		_ = p.Register(&Endpoint{ID: "mic", Source: endingSource{c.err}})
		_ = p.Register(&Endpoint{ID: "spk", Sink: NewCaptureSink(mono16k)})
		b, err := p.Bind("s", "mic", "spk", true)
		if err != nil {
			t.Fatal(err)
		}
		pump := NewPump(b, endingChannel{c.err})
		pump.Run(context.Background())
		_, in, out := pump.Errors()
		failed := false
		select {
		case <-pump.Failed():
			failed = true
		default:
		}
		if c.clean != (in == nil && out == nil && !failed) {
			t.Fatalf("%v: input %v, output %v, failed %v", c.err, in, out, failed)
		}

		src, _ := NewFileSource(endingReader{c.err}, mono16k, 0)
		fr, err := src.Read(context.Background())
		if c.clean != (err == nil && fr.Kind == KindEnd) {
			t.Fatalf("%v: a file source read %+v, %v", c.err, fr, err)
		}
	}
	var h bytes.Buffer
	_ = WriteFrame(&h, Frame{Kind: KindPCM, PCM: []byte{1, 2}})
	cut := io.MultiReader(bytes.NewReader(h.Bytes()[:headerBytes]), endingReader{fmt.Errorf("the pipe: %w", io.EOF)})
	if _, err := ReadFrame(cut); err != io.ErrUnexpectedEOF {
		t.Fatalf("a frame cut short after its header: %v", err)
	}
}
