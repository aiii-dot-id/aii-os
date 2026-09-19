package audio

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .

type scriptedSource struct {
	read func(ctx context.Context) (Frame, error)
}

func (s scriptedSource) Format() Format                          { return Format{Rate: 16000, Channels: 1} }
func (s scriptedSource) Close() error                            { return nil }
func (s scriptedSource) Read(ctx context.Context) (Frame, error) { return s.read(ctx) }

type quietChannel struct {
	mu       sync.Mutex
	writeErr error
	closed   chan struct{}
	once     sync.Once
}

func (c *quietChannel) WriteInput(Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeErr
}
func (c *quietChannel) ReadOutput() (Frame, error) { <-c.closed; return Frame{}, io.EOF }
func (c *quietChannel) Close() error               { c.once.Do(func() { close(c.closed) }); return nil }

func pumpWith(t *testing.T, src Source, ch *quietChannel) (*Pump, context.CancelFunc) {
	t.Helper()
	plane := NewPlane()
	if err := plane.Register(&Endpoint{ID: "mic", Source: src}); err != nil {
		t.Fatal(err)
	}
	if err := plane.Register(&Endpoint{ID: "spk", Sink: NewCaptureSink(Format{Rate: 16000, Channels: 1})}); err != nil {
		t.Fatal(err)
	}
	b, err := plane.Bind("s", "mic", "spk", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := NewPump(b, ch)
	go p.Run(ctx)
	t.Cleanup(func() { cancel(); ch.Close(); <-p.Done() })
	return p, cancel
}

func failedSoon(p *Pump) bool {
	select {
	case <-p.Failed():
		return true
	case <-time.After(150 * time.Millisecond):
		return false
	}
}

func TestThePumpsOwnRetirementIsNotAFailure(t *testing.T) {
	entered := make(chan struct{})
	var once sync.Once
	src := scriptedSource{read: func(ctx context.Context) (Frame, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return Frame{}, ctx.Err()
	}}
	ch := &quietChannel{closed: make(chan struct{})}
	p, cancel := pumpWith(t, src, ch)
	<-entered
	cancel()
	ch.Close()
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the pump did not retire")
	}
	if _, in, out := p.Errors(); in != nil || out != nil {
		t.Errorf("a clean retirement left an error behind: in=%v out=%v", in, out)
	}
	select {
	case <-p.Failed():
		t.Error("a clean retirement was signalled as a failure")
	default:
	}
}

func TestACancellationThatIsNotThePumpsOwnIsStillAFault(t *testing.T) {
	// .
	// .
	// .
	src := scriptedSource{read: func(context.Context) (Frame, error) { return Frame{}, context.Canceled }}
	p, _ := pumpWith(t, src, &quietChannel{closed: make(chan struct{})})
	if !failedSoon(p) {
		t.Fatal("a source that lost its own upstream was not signalled as a failure")
	}
	if _, in, _ := p.Errors(); !errors.Is(in, context.Canceled) {
		t.Errorf("the failure lost its cause: %v", in)
	}
}

func TestAnOrdinaryTransportErrorIsAFaultAtOnce(t *testing.T) {
	src := scriptedSource{read: func(context.Context) (Frame, error) { return Frame{}, errors.New("device unplugged") }}
	p, _ := pumpWith(t, src, &quietChannel{closed: make(chan struct{})})
	if !failedSoon(p) {
		t.Fatal("a broken source was not signalled")
	}
}

func TestAWriteThatFailsBecauseThePumpWasRetiredIsNotAFault(t *testing.T) {
	gate := make(chan struct{})
	var first sync.Once
	src := scriptedSource{read: func(ctx context.Context) (Frame, error) {
		var fr Frame
		handed := false
		first.Do(func() {
			// .
			// .
			<-gate
			fr, handed = Frame{Kind: KindPCM, PCM: make([]byte, 320)}, true
		})
		if handed {
			return fr, nil
		}
		<-ctx.Done()
		return Frame{}, ctx.Err()
	}}
	ch := &quietChannel{closed: make(chan struct{})}
	p, cancel := pumpWith(t, src, ch)
	// .
	cancel()
	ch.mu.Lock()
	ch.writeErr = errors.New("the channel is closed")
	ch.mu.Unlock()
	close(gate)
	ch.Close()
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the pump did not retire")
	}
	if _, in, _ := p.Errors(); in != nil {
		t.Errorf("a write refused by the pump's own retirement was kept as a fault: %v", in)
	}
	select {
	case <-p.Failed():
		t.Error("and signalled as one")
	default:
	}
}

func TestAWriteThatFailsWhileThePumpIsWantedIsAFaultAtOnce(t *testing.T) {
	src := scriptedSource{read: func(ctx context.Context) (Frame, error) {
		return Frame{Kind: KindPCM, PCM: make([]byte, 320)}, nil
	}}
	ch := &quietChannel{closed: make(chan struct{}), writeErr: errors.New("broken pipe")}
	p, _ := pumpWith(t, src, ch)
	if !failedSoon(p) {
		t.Fatal("a microphone that can no longer reach the engine was not signalled")
	}
}
