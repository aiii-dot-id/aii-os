// .
// .
// .
// .
// .
// .
// .
// .
// .

package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type reviewFailedInputChannel struct{ reason error }

func (c reviewFailedInputChannel) WriteInput(Frame) error     { return c.reason }
func (c reviewFailedInputChannel) ReadOutput() (Frame, error) { return Frame{}, io.EOF }
func (c reviewFailedInputChannel) Close() error               { return nil }

// .
// .
func TestReviewInputWriteFailureSignalsBeforeOutputFinishes(t *testing.T) {
	for _, n := range []int{0, 8} {
		t.Run(map[bool]string{true: "PCM", false: "END"}[n > 0], func(t *testing.T) {
			boom := errors.New("review: engine input pipe broke")
			src, err := NewFileSource(bytes.NewReader(make([]byte, n*2)), mono16k, 320)
			if err != nil {
				t.Fatal(err)
			}
			p := NewPump(&Binding{Source: src, Sink: NewCaptureSink(mono16k), InFormat: mono16k, OutFormat: mono16k}, reviewFailedInputChannel{boom})
			p.runInput(context.Background())
			_, inErr, _ := p.Errors()
			if !errors.Is(inErr, boom) {
				t.Fatalf("error not recorded: %v", inErr)
			}
			select {
			case <-p.Failed():
			default:
				t.Fatal("input write failed and input pump returned, but Failed is not signaled while output remains live")
			}
		})
	}
}

// .
// .
// .
// .
// .
// .
func TestInputWriteFailureFaultsALivePump(t *testing.T) {
	boom := errors.New("engine input pipe broke")
	src, err := NewFileSource(bytes.NewReader(make([]byte, 16*2)), mono16k, 320)
	if err != nil {
		t.Fatal(err)
	}
	ch := &liveOutputFailedInput{reason: boom, out: make(chan Frame, 4)}
	p := NewPump(&Binding{Source: src, Sink: NewCaptureSink(mono16k), InFormat: mono16k, OutFormat: mono16k}, ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	// .
	ch.out <- Frame{Kind: KindPCM, PCM: make([]byte, 64)}
	select {
	case <-p.Failed():
	case <-time.After(5 * time.Second):
		t.Fatal("a microphone that can no longer reach the engine must surface while the engine is still speaking")
	}
	select {
	case <-p.Done():
		t.Fatal("Done cannot close while the output direction is alive — which is exactly why Failed has to carry this")
	default:
	}
	if _, inErr, _ := p.Errors(); !errors.Is(inErr, boom) {
		t.Fatalf("the recorded reason is the write's own: %v", inErr)
	}
	close(ch.out)
}

// .
// .
// .
type liveOutputFailedInput struct {
	reason error
	out    chan Frame
}

func (c *liveOutputFailedInput) WriteInput(Frame) error { return c.reason }
func (c *liveOutputFailedInput) ReadOutput() (Frame, error) {
	fr, ok := <-c.out
	if !ok {
		return Frame{}, io.EOF
	}
	return fr, nil
}
func (c *liveOutputFailedInput) Close() error { return nil }
