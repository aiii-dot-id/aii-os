package audio

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// .
// .
type recordingChannel struct {
	wrote  chan Frame
	closed chan struct{}
}

func (c *recordingChannel) WriteInput(fr Frame) error { c.wrote <- fr; return nil }
func (c *recordingChannel) ReadOutput() (Frame, error) {
	<-c.closed
	return Frame{}, io.EOF
}
func (c *recordingChannel) Close() error { return nil }

// .
// .
// .
// .
func TestBindOutputHoldsOnlyTheSpeaker(t *testing.T) {
	format := Format{Rate: 16000, Channels: 1}
	plane := NewPlane()
	_ = plane.Register(&Endpoint{ID: "mic", Source: &silentSource{f: format}})
	_ = plane.Register(&Endpoint{ID: "spk", Sink: NewCaptureSink(format)})
	_ = plane.Register(&Endpoint{ID: "call", Remote: true, Sink: NewCaptureSink(format)})

	b, err := plane.BindOutput("typed", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	if b.HasInput() || b.Source != nil || b.InputHandle != "" || b.InputID != "" || b.InFormat != (Format{}) {
		t.Fatalf("an output-only binding carries no input of any kind: %+v", b)
	}
	if b.OutputHandle == "" || b.OutFormat != format || b.Sink == nil {
		t.Fatalf("the output half is the ordinary one: %+v", b)
	}
	for _, ep := range plane.Endpoints() {
		if ep.ID == "mic" && ep.BoundTo != "" {
			t.Fatalf("the microphone is held by a session that does not listen: %q", ep.BoundTo)
		}
	}
	if _, err := plane.Bind("talk", "mic", "spk", true); !errors.Is(err, ErrEndpointBusy) {
		t.Fatalf("the speaker is this session's while it is bound: %v", err)
	}
	if _, err := plane.BindOutput("x", "mic", true); !errors.Is(err, ErrNotASink) {
		t.Fatalf("a microphone is not an output: %v", err)
	}
	b.Release()
	if _, err := plane.Bind("talk", "mic", "spk", true); err != nil {
		t.Fatalf("after release the speaker is free, and the microphone always was: %v", err)
	}

	plane.SafeMode = func() (string, bool) { return "integrity unverified", true }
	if _, err := plane.BindOutput("typed2", "call", true); !errors.Is(err, ErrSafe) {
		t.Fatalf("SAFE binds no remote endpoint, output-only or not: %v", err)
	}
	if _, err := plane.BindOutput("typed3", "call", false); !errors.Is(err, ErrSafe) {
		t.Fatalf("SAFE binds no uncontained engine: %v", err)
	}
}

// .
// .
// .
// .
func TestAnOutputOnlyPumpWritesNothingToTheEngine(t *testing.T) {
	format := Format{Rate: 16000, Channels: 1}
	plane := NewPlane()
	_ = plane.Register(&Endpoint{ID: "spk", Sink: NewCaptureSink(format)})
	b, err := plane.BindOutput("typed", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	ch := &recordingChannel{wrote: make(chan Frame, 4), closed: make(chan struct{})}
	p := NewPump(b, ch)
	ctx, cancel := context.WithCancel(context.Background())
	go p.Run(ctx)
	select {
	case fr := <-ch.wrote:
		t.Fatalf("an output-only pump wrote input to the engine: %+v", fr)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	close(ch.closed)
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the pump did not end with its context")
	}
	if _, inErr, outErr := p.Errors(); inErr != nil || outErr != nil {
		t.Fatalf("ending is not failing: in=%v out=%v", inErr, outErr)
	}
}
