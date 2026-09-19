// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
)

// .
// .
type Format struct {
	Rate     int
	Channels int
}

// .
func (f Format) BytesPerSample() int { return 2 * f.Channels }

func (f Format) String() string { return fmt.Sprintf("s16le/%d/%d", f.Rate, f.Channels) }

// .
type Kind uint8

const (
	// .
	// .
	KindPCM Kind = 1
	// .
	// .
	// .
	KindDiscontinuity Kind = 2
	// .
	// .
	KindEnd Kind = 3
)

// .
type Frame struct {
	Kind   Kind
	Stream uint32
	Seq    uint32
	Start  int64
	PCM    []byte
}

// .
func (fr Frame) Samples(f Format) int64 { return int64(len(fr.PCM) / f.BytesPerSample()) }

// .
func (fr Frame) End(f Format) int64 {
	if fr.Kind == KindPCM {
		return fr.Start + fr.Samples(f)
	}
	return fr.Start
}

// .
// .
type Source interface {
	Format() Format
	Read(ctx context.Context) (Frame, error)
}

// .
type Sink interface {
	Format() Format
	Write(ctx context.Context, fr Frame) error
	Close() error
}

// .
// .
// .
type Endpoint struct {
	ID     string
	Label  string
	Remote bool
	Source Source
	Sink   Sink
}

var (
	ErrEndpointUnknown = errors.New("audio: no such endpoint")
	ErrEndpointBusy    = errors.New("audio: the endpoint is bound to another session")
	ErrNotASource      = errors.New("audio: the endpoint has no input")
	ErrNotASink        = errors.New("audio: the endpoint has no output")
	// .
	// .
	ErrSafe = errors.New("audio: no audio leaves the host while SAFE holds")
)

// .
type Plane struct {
	mu        sync.Mutex
	endpoints map[string]*Endpoint
	bound     map[string]string
	// .
	SafeMode func() (reason string, safe bool)
}

// .
func NewPlane() *Plane {
	return &Plane{endpoints: map[string]*Endpoint{}, bound: map[string]string{}}
}

// .
func (p *Plane) Register(ep *Endpoint) error {
	if ep == nil || ep.ID == "" {
		return errors.New("audio: an endpoint needs an id")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, dup := p.endpoints[ep.ID]; dup {
		return fmt.Errorf("audio: endpoint %q is already registered", ep.ID)
	}
	p.endpoints[ep.ID] = ep
	return nil
}

// .
func (p *Plane) Unregister(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, busy := p.bound[id]; busy {
		return fmt.Errorf("%w (session %s)", ErrEndpointBusy, s)
	}
	delete(p.endpoints, id)
	return nil
}

// .
// .
func (p *Plane) Endpoints() []EndpointState {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]EndpointState, 0, len(p.endpoints))
	for id, ep := range p.endpoints {
		out = append(out, EndpointState{ID: id, Label: ep.Label, Remote: ep.Remote, Input: ep.Source != nil, Output: ep.Sink != nil, BoundTo: p.bound[id]})
	}
	return out
}

// .
type EndpointState struct {
	ID, Label     string
	Remote        bool
	Input, Output bool
	BoundTo       string
}

// .
// .
// .
// .
// .
// .
type Binding struct {
	SessionID    string
	InputID      string
	OutputID     string
	InputHandle  string
	OutputHandle string
	// .
	// .
	// .
	// .
	InFormat  Format
	OutFormat Format
	Source    Source
	Sink      Sink
	// .
	// .
	// .
	Contained bool
	Remote    bool

	plane    *Plane
	once     sync.Once
	released chan struct{}
}

// .
// .
// .
// .
func (p *Plane) Bind(sessionID, inputID, outputID string, contained bool) (*Binding, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	in, ok := p.endpoints[inputID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrEndpointUnknown, inputID)
	}
	out, ok := p.endpoints[outputID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrEndpointUnknown, outputID)
	}
	if in.Source == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotASource, inputID)
	}
	if out.Sink == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotASink, outputID)
	}
	if p.SafeMode != nil {
		if reason, safe := p.SafeMode(); safe {
			if !contained {
				return nil, fmt.Errorf("%w: the engine is not contained (%s)", ErrSafe, reason)
			}
			if in.Remote || out.Remote {
				return nil, fmt.Errorf("%w: a remote endpoint (%s)", ErrSafe, reason)
			}
		}
	}
	for _, id := range []string{inputID, outputID} {
		if s, busy := p.bound[id]; busy && s != sessionID {
			return nil, fmt.Errorf("%w: %s is held by session %s", ErrEndpointBusy, id, s)
		}
	}
	p.bound[inputID] = sessionID
	p.bound[outputID] = sessionID
	b := &Binding{
		SessionID: sessionID, InputID: inputID, OutputID: outputID,
		InputHandle: "in:" + sessionID + ":" + inputID, OutputHandle: "out:" + sessionID + ":" + outputID,
		InFormat: in.Source.Format(), OutFormat: out.Sink.Format(), Source: in.Source, Sink: out.Sink,
		Contained: contained, Remote: in.Remote || out.Remote,
		plane: p, released: make(chan struct{}),
	}
	return b, nil
}

// .
func (b *Binding) HasInput() bool { return b.Source != nil }

// .
// .
// .
// .
func (p *Plane) BindOutput(sessionID, outputID string, contained bool) (*Binding, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out, ok := p.endpoints[outputID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrEndpointUnknown, outputID)
	}
	if out.Sink == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotASink, outputID)
	}
	if p.SafeMode != nil {
		if reason, safe := p.SafeMode(); safe {
			if !contained {
				return nil, fmt.Errorf("%w: the engine is not contained (%s)", ErrSafe, reason)
			}
			if out.Remote {
				return nil, fmt.Errorf("%w: a remote endpoint (%s)", ErrSafe, reason)
			}
		}
	}
	if s, busy := p.bound[outputID]; busy && s != sessionID {
		return nil, fmt.Errorf("%w: %s is held by session %s", ErrEndpointBusy, outputID, s)
	}
	p.bound[outputID] = sessionID
	return &Binding{
		SessionID: sessionID, OutputID: outputID,
		OutputHandle: "out:" + sessionID + ":" + outputID,
		OutFormat:    out.Sink.Format(), Sink: out.Sink,
		Contained: contained, Remote: out.Remote,
		plane: p, released: make(chan struct{}),
	}, nil
}

// .
// .
// .
func (b *Binding) Release() {
	b.once.Do(func() {
		b.plane.mu.Lock()
		if b.InputID != "" && b.plane.bound[b.InputID] == b.SessionID {
			delete(b.plane.bound, b.InputID)
		}
		if b.plane.bound[b.OutputID] == b.SessionID {
			delete(b.plane.bound, b.OutputID)
		}
		b.plane.mu.Unlock()
		close(b.released)
	})
}

// .
func (b *Binding) Released() <-chan struct{} { return b.released }

// .
// .
// .
type Channel interface {
	WriteInput(fr Frame) error
	ReadOutput() (Frame, error)
	Close() error
}

// .
// .
// .
// .
// .
// .
type Pump struct {
	// .
	// .
	runCtx context.Context
	b      *Binding
	ch     Channel
	// .
	// .
	Stream uint32
	// .
	// .
	// .
	// .
	// .
	EngineIn  Format
	EngineOut Format

	mu        sync.Mutex
	inSeq     uint32
	delivered int64
	received  int64
	cutoff    int64
	inDone    bool
	inErr     error
	outErr    error
	failed    chan struct{}
	failOnce  sync.Once
	dropped   uint64
	done      chan struct{}
	// .
	// .
	// .
	// .
	outputs map[uint32]*OutputStream
}

// .
type OutputStream struct {
	EngineReceived int64
	Written        int64
	Refused        int64
	Ended          bool
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (p *Pump) RetireOutputStream(stream uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.outputs, stream)
}

func (p *Pump) OutputStream(stream uint32) (OutputStream, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.outputs[stream]
	if st == nil {
		return OutputStream{}, false
	}
	return *st, true
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (p *Pump) EngineRendered(stream uint32, rendered int64) (int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.outputs[stream]
	if st == nil {
		return 0, fmt.Errorf("audio: output stream %d was never delivered on this session", stream)
	}
	if rendered < 0 || rendered > st.Written {
		return 0, fmt.Errorf("audio: rendered %d exceeds the %d sample groups written on stream %d", rendered, st.Written, stream)
	}
	if st.Ended && st.Refused == 0 && rendered == st.Written {
		return st.EngineReceived, nil
	}
	eng, out := p.engineOut(), p.b.OutFormat
	n := rendered
	if eng.Rate != out.Rate {
		n = rendered * int64(eng.Rate) / int64(out.Rate)
	}
	if n > st.EngineReceived {
		n = st.EngineReceived
	}
	return n, nil
}

// .
func (p *Pump) outputLocked(stream uint32) *OutputStream {
	st := p.outputs[stream]
	if st == nil {
		st = &OutputStream{}
		p.outputs[stream] = st
	}
	return st
}

// .
func NewPump(b *Binding, ch Channel) *Pump {
	return &Pump{b: b, ch: ch, cutoff: -1, done: make(chan struct{}), failed: make(chan struct{}), outputs: map[uint32]*OutputStream{}}
}

// .
// .
// .
// .
func (p *Pump) Run(ctx context.Context) {
	p.mu.Lock()
	p.runCtx = ctx
	p.mu.Unlock()
	defer close(p.done)
	defer p.ch.Close()
	var wg sync.WaitGroup
	// .
	// .
	// .
	if p.b.HasInput() {
		wg.Add(1)
		go func() { defer wg.Done(); p.runInput(ctx) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); p.runOutput(ctx) }()
	wg.Wait()
}

// .
func (p *Pump) Done() <-chan struct{} { return p.done }

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (p *Pump) Failed() <-chan struct{} { return p.failed }

// .
func (p *Pump) markFailed() { p.failOnce.Do(func() { close(p.failed) }) }

func (p *Pump) engineIn() Format {
	if p.EngineIn.Rate == 0 {
		return p.b.InFormat
	}
	return p.EngineIn
}

func (p *Pump) engineOut() Format {
	if p.EngineOut.Rate == 0 {
		return p.b.OutFormat
	}
	return p.EngineOut
}

// .
// .
// .
// .
func (p *Pump) EngineCutoff(endSample int64) int64 {
	return NewResampler(p.b.InFormat, p.engineIn()).TargetPos(endSample)
}

// .
// .
func (p *Pump) writeInput(fr Frame) error {
	p.mu.Lock()
	p.inSeq++
	fr.Seq = p.inSeq
	if p.Stream != 0 {
		fr.Stream = p.Stream
	}
	p.mu.Unlock()
	if err := p.ch.WriteInput(fr); err != nil {
		p.mu.Lock()
		retired := p.runCtx != nil && p.runCtx.Err() != nil
		p.inDone = true
		if !retired {
			p.inErr = err
		}
		p.mu.Unlock()
		if retired {
			// .
			// .
			return err
		}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		p.markFailed()
		return err
	}
	return nil
}

func (p *Pump) runInput(ctx context.Context) {
	f, eng := p.b.InFormat, p.engineIn()
	rs := NewResampler(f, eng)
	convert := f != eng
	for {
		fr, err := p.b.Source.Read(ctx)
		if err != nil {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			broken := err != io.EOF && ctx.Err() == nil
			p.mu.Lock()
			if broken {
				p.inErr = err
			}
			p.inDone = true
			p.mu.Unlock()
			if broken {
				p.markFailed()
			}
			return
		}
		p.mu.Lock()
		cutoff := p.cutoff
		p.mu.Unlock()
		if cutoff >= 0 && fr.Kind == KindPCM {
			// .
			// .
			// .
			if fr.Start >= cutoff {
				p.endInput(cutoff, rs, convert)
				return
			}
			if fr.End(f) > cutoff {
				fr.PCM = fr.PCM[:int(cutoff-fr.Start)*f.BytesPerSample()]
			}
		}
		if fr.Kind == KindEnd {
			p.endInput(fr.Start, rs, convert)
			return
		}
		// .
		// .
		p.mu.Lock()
		if fr.Kind == KindPCM {
			p.delivered = fr.End(f)
		} else {
			p.delivered = fr.Start
		}
		p.mu.Unlock()
		if convert {
			switch fr.Kind {
			case KindPCM:
				start := rs.Position()
				rs.Feed(fr.PCM)
				pcm := rs.Take()
				if len(pcm) == 0 {
					continue
				}
				fr.PCM, fr.Start = pcm, start
			case KindDiscontinuity:
				// .
				// .
				if tail := rs.Finish(); len(tail) > 0 {
					if err := p.writeInput(Frame{Kind: KindPCM, Start: rs.Position() - int64(len(tail)/eng.BytesPerSample()), PCM: tail}); err != nil {
						return
					}
				}
				rs.Reset(fr.Start)
				fr.Start = rs.Position()
			}
		}
		if err := p.writeInput(fr); err != nil {
			return
		}
		if cutoff >= 0 && fr.Kind == KindPCM && p.Delivered() == cutoff {
			p.endInput(cutoff, rs, convert)
			return
		}
	}
}

// .
// .
func (p *Pump) endInput(end int64, rs *Resampler, convert bool) {
	p.mu.Lock()
	p.delivered = end
	p.inDone = true
	stream := p.Stream
	p.mu.Unlock()
	if stream == 0 {
		stream = 1
	}
	engineEnd := end
	if convert {
		eng := p.engineIn()
		if tail := rs.Finish(); len(tail) > 0 {
			if err := p.writeInput(Frame{Kind: KindPCM, Stream: stream, Start: rs.Position() - int64(len(tail)/eng.BytesPerSample()), PCM: tail}); err != nil {
				return
			}
		}
		engineEnd = rs.TargetPos(end)
	}
	_ = p.writeInput(Frame{Kind: KindEnd, Stream: stream, Start: engineEnd})
}

func (p *Pump) runOutput(ctx context.Context) {
	defer p.b.Sink.Close()
	eng, out := p.engineOut(), p.b.OutFormat
	convert := eng != out
	// .
	rss := map[uint32]*Resampler{}
	seqs := map[uint32]uint32{}
	write := func(fr Frame) {
		if convert {
			seqs[fr.Stream]++
			fr.Seq = seqs[fr.Stream]
		}
		if err := p.b.Sink.Write(ctx, fr); err != nil {
			// .
			// .
			p.mu.Lock()
			p.dropped++
			p.outputLocked(fr.Stream).Refused++
			p.mu.Unlock()
			return
		}
		// .
		// .
		// .
		p.mu.Lock()
		st := p.outputLocked(fr.Stream)
		switch fr.Kind {
		case KindPCM:
			st.Written += fr.Samples(out)
		case KindEnd:
			st.Ended = true
		}
		p.mu.Unlock()
	}
	for {
		fr, err := p.ch.ReadOutput()
		if err != nil {
			p.mu.Lock()
			if err != io.EOF {
				p.outErr = err
			}
			p.mu.Unlock()
			if err != io.EOF {
				p.markFailed()
			}
			return
		}
		// .
		// .
		p.mu.Lock()
		switch fr.Kind {
		case KindPCM:
			p.received = fr.End(eng)
			p.outputLocked(fr.Stream).EngineReceived = fr.End(eng)
		case KindEnd:
			p.outputLocked(fr.Stream).EngineReceived = fr.Start
		}
		p.mu.Unlock()
		if convert {
			rs := rss[fr.Stream]
			if rs == nil {
				rs = NewResampler(eng, out)
				rs.Reset(fr.Start)
				rss[fr.Stream] = rs
			}
			switch fr.Kind {
			case KindPCM:
				start := rs.Position()
				rs.Feed(fr.PCM)
				pcm := rs.Take()
				if len(pcm) == 0 {
					continue
				}
				fr.PCM, fr.Start = pcm, start
			case KindDiscontinuity, KindEnd:
				if tail := rs.Finish(); len(tail) > 0 {
					write(Frame{Kind: KindPCM, Stream: fr.Stream, Start: rs.Position() - int64(len(tail)/out.BytesPerSample()), PCM: tail})
				}
				pos := rs.TargetPos(fr.Start)
				if fr.Kind == KindEnd {
					delete(rss, fr.Stream)
				} else {
					rs.Reset(fr.Start)
				}
				fr.Start = pos
			}
		}
		write(fr)
		// .
		// .
		if fr.Kind == KindEnd {
			delete(seqs, fr.Stream)
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// .
// .
// .
// .
func (p *Pump) Cutoff(endSample int64) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inDone {
		return p.delivered
	}
	if endSample < p.delivered {
		endSample = p.delivered
	}
	p.cutoff = endSample
	return endSample
}

// .
// .
func (p *Pump) CutoffAt() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cutoff
}

// .
// .
func (p *Pump) Delivered() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.delivered
}

// .
func (p *Pump) Received() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.received
}

// .
// .
func (p *Pump) Errors() (dropped uint64, in, out error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dropped, p.inErr, p.outErr
}
