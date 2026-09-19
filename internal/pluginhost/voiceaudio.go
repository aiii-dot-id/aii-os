package pluginhost

// .
// .
// .
// .

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

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
// .
// .
type audioPair struct {
	wmu   sync.Mutex
	in    io.Writer
	out   io.ReadCloser
	stray atomic.Uint64
	// .
	// .
	// .
	// .
	unnamed atomic.Uint64
	// .
	// .
	// .
	readErr atomic.Pointer[error]
	ended   chan struct{}

	// .
	// .
	omu   sync.Mutex
	bound map[uint32]*streamBinding
	// .
	// .
	current *audioTaker
	// .
	// .
	// .
	// .
	retired map[uint32]struct{}
	floor   int64
	// .
	// .
	evicted map[uint32]int
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
	held         []heldFrame
	heldBytes    int
	waiting      int
	waitingBytes int
	// .
	changed  chan struct{}
	patience time.Duration
}

// .
type heldFrame struct {
	fr    audio.Frame
	owner *audioTaker
}

// .
type streamBinding struct {
	owner     *audioTaker
	synthesis string
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
type audioTaker struct {
	done <-chan struct{}
	// .
	queued int
	ready  chan struct{}
}

const (
	// .
	// .
	pairBuffer = 256
	// .
	// .
	// .
	pendingFrames = 256
	pendingBytes  = 8 << 20
	// .
	// .
	// .
	// .
	storeFrames = pairBuffer + pendingFrames
	storeBytes  = pairBuffer*audio.MaxFramePayload + pendingBytes
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	pendingPatience = 2 * time.Second
	// .
	// .
	// .
	maxBoundStreams = 256
	// .
	retiredKept = 4096
)

func newAudioPair(in io.Writer, out io.ReadCloser) *audioPair {
	p := &audioPair{in: in, out: out, ended: make(chan struct{}),
		bound: map[uint32]*streamBinding{}, retired: map[uint32]struct{}{}, evicted: map[uint32]int{},
		floor: -1, changed: make(chan struct{}, 1), patience: pendingPatience}
	go p.read()
	return p
}

func nudge(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
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
func (p *audioPair) attach(ctx context.Context) *audioTaker {
	t := &audioTaker{done: ctx.Done(), ready: make(chan struct{}, 1)}
	p.omu.Lock()
	p.current = t
	n := p.turnOverLocked()
	p.omu.Unlock()
	p.stray.Add(uint64(n))
	go func() {
		<-ctx.Done()
		p.release(t)
	}()
	return t
}

// .
// .
// .
func (p *audioPair) discardLocked(drop func(heldFrame) bool) int {
	keep, n := p.held[:0], 0
	for _, h := range p.held {
		if !drop(h) {
			keep = append(keep, h)
			continue
		}
		n++
		p.heldBytes -= len(h.fr.PCM)
		if h.owner == nil {
			p.waiting--
			p.waitingBytes -= len(h.fr.PCM)
		} else {
			h.owner.queued--
		}
	}
	for i := len(keep); i < len(p.held); i++ {
		p.held[i] = heldFrame{}
	}
	p.held = keep
	if n > 0 {
		nudge(p.changed)
	}
	return n
}

// .
// .
func (p *audioPair) turnOverLocked() int {
	n := p.discardLocked(func(h heldFrame) bool {
		if h.owner == nil {
			p.markRetiredLocked(h.fr.Stream)
			return true
		}
		return false
	})
	nudge(p.changed)
	return n
}

// .
// .
// .
// .
// .
func (p *audioPair) release(t *audioTaker) {
	p.omu.Lock()
	for s, b := range p.bound {
		if b.owner == t {
			delete(p.bound, s)
			p.markRetiredLocked(s)
		}
	}
	p.discardLocked(func(h heldFrame) bool { return h.owner == t })
	n := 0
	if !p.aliveLocked() {
		n = p.turnOverLocked()
	}
	nudge(p.changed)
	p.omu.Unlock()
	p.stray.Add(uint64(n))
}

// .
// .
func (p *audioPair) aliveLocked() bool {
	return p.current != nil && !p.current.over()
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
// .
func (p *audioPair) bind(stream uint32, t *audioTaker, synthesis string) error {
	p.omu.Lock()
	defer p.omu.Unlock()
	if b := p.bound[stream]; b != nil {
		if b.owner == t && b.synthesis == synthesis {
			return nil
		}
		if b.owner == t {
			return fmt.Errorf("voicesession: output stream %d is synthesis %q's and was named again for %q — a stream carries one reply", stream, b.synthesis, synthesis)
		}
		return fmt.Errorf("voicesession: output stream %d belongs to another session and was named again — a stream id is used once per engine process", stream)
	}
	if p.retiredLocked(stream) {
		if n, ok := p.evicted[stream]; ok {
			return fmt.Errorf("voicesession: output stream %d was named after %d frame(s) of it had waited unnamed past the host's bound and were discarded — the reply cannot be delivered whole", stream, n)
		}
		return fmt.Errorf("voicesession: output stream %d was named after it was retired — a stream id is used once per engine process", stream)
	}
	if t == nil || t.over() {
		p.stray.Add(uint64(p.retireLocked(stream)))
		return nil
	}
	if len(p.bound) >= maxBoundStreams {
		return fmt.Errorf("voicesession: %d output streams are open and none has ended — the host will not track another (stream %d)", len(p.bound), stream)
	}
	ended, claimed := false, 0
	for i := range p.held {
		h := &p.held[i]
		if h.owner != nil || h.fr.Stream != stream || ended {
			continue
		}
		h.owner = t
		t.queued++
		p.waiting--
		p.waitingBytes -= len(h.fr.PCM)
		claimed++
		if h.fr.Kind == audio.KindEnd {
			ended = true
		}
	}
	if ended {
		// .
		// .
		p.stray.Add(uint64(p.discardLocked(func(h heldFrame) bool { return h.owner == nil && h.fr.Stream == stream })))
		p.markRetiredLocked(stream)
	} else {
		p.bound[stream] = &streamBinding{owner: t, synthesis: synthesis}
	}
	if claimed > 0 {
		nudge(t.ready)
	}
	nudge(p.changed)
	return nil
}

// .
// .
// .
// .
// .
func (p *audioPair) retire(stream uint32) {
	p.omu.Lock()
	n := p.retireLocked(stream)
	p.omu.Unlock()
	p.stray.Add(uint64(n))
}

func (p *audioPair) retireLocked(stream uint32) int {
	delete(p.bound, stream)
	n := p.discardLocked(func(h heldFrame) bool { return h.fr.Stream == stream })
	p.markRetiredLocked(stream)
	nudge(p.changed)
	return n
}

// .
// .
// .
func (p *audioPair) disown(stream uint32) error {
	p.omu.Lock()
	if b := p.bound[stream]; b != nil && !b.owner.over() {
		p.omu.Unlock()
		return fmt.Errorf("voicesession: output stream %d is this session's and an ended session named it too — a stream id is used once per engine process", stream)
	}
	n := p.retireLocked(stream)
	p.omu.Unlock()
	p.stray.Add(uint64(n))
	return nil
}

func (p *audioPair) retiredLocked(stream uint32) bool {
	if int64(stream) <= p.floor {
		return true
	}
	_, ok := p.retired[stream]
	return ok
}

func (p *audioPair) markRetiredLocked(stream uint32) {
	if p.retiredLocked(stream) {
		return
	}
	p.retired[stream] = struct{}{}
	for p.floor < int64(^uint32(0)) {
		next := uint32(p.floor + 1)
		if _, ok := p.retired[next]; !ok {
			break
		}
		delete(p.retired, next)
		delete(p.evicted, next)
		p.floor++
	}
	if len(p.retired) <= retiredKept {
		return
	}
	// .
	// .
	// .
	ids := make([]uint32, 0, len(p.retired))
	for s := range p.retired {
		ids = append(ids, s)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	p.floor = int64(ids[len(ids)/2])
	for _, s := range ids[:len(ids)/2+1] {
		delete(p.retired, s)
		delete(p.evicted, s)
	}
}

// .
func (p *audioPair) roomLocked(fr audio.Frame) bool {
	return len(p.held) < storeFrames && p.heldBytes+len(fr.PCM) <= storeBytes
}

// .
// .
func (p *audioPair) holdLocked(fr audio.Frame, owner *audioTaker) {
	p.held = append(p.held, heldFrame{fr: fr, owner: owner})
	p.heldBytes += len(fr.PCM)
	if owner == nil {
		p.waiting++
		p.waitingBytes += len(fr.PCM)
		return
	}
	owner.queued++
	nudge(owner.ready)
}

// .
// .
func (p *audioPair) giveUpOldest() {
	p.omu.Lock()
	n := 0
	for _, h := range p.held {
		if h.owner != nil {
			continue
		}
		s := h.fr.Stream
		n = p.discardLocked(func(h heldFrame) bool { return h.owner == nil && h.fr.Stream == s })
		p.markRetiredLocked(s)
		if len(p.evicted) < 64 {
			p.evicted[s] = n
		}
		break
	}
	p.omu.Unlock()
	p.unnamed.Add(uint64(n))
}

func (p *audioPair) read() {
	defer close(p.ended)
	for {
		fr, err := audio.ReadFrame(p.out)
		if err != nil {
			// .
			// .
			// .
			// .
			// .
			// .
			if !cleanEnd(err) {
				p.readErr.CompareAndSwap(nil, &err)
			}
			return
		}
		p.route(fr)
	}
}

// .
// .
// .
// .
// .
// .
// .
func (p *audioPair) route(fr audio.Frame) {
	var wait *time.Timer
	defer func() {
		if wait != nil {
			wait.Stop()
		}
	}()
	for {
		var owner *audioTaker
		patient := false
		p.omu.Lock()
		switch b := p.bound[fr.Stream]; {
		case b != nil:
			owner = b.owner
			// .
			// .
			// .
			if owner.over() {
				p.omu.Unlock()
				p.stray.Add(1)
				return
			}
			if owner.queued < pairBuffer && p.roomLocked(fr) {
				p.holdLocked(fr, owner)
				if fr.Kind == audio.KindEnd {
					// .
					delete(p.bound, fr.Stream)
					p.markRetiredLocked(fr.Stream)
				}
				p.omu.Unlock()
				return
			}
		case p.retiredLocked(fr.Stream):
			// .
			// .
			p.omu.Unlock()
			p.stray.Add(1)
			return
		case !p.aliveLocked():
			// .
			// .
			// .
			p.markRetiredLocked(fr.Stream)
			p.omu.Unlock()
			p.stray.Add(1)
			return
		default:
			full := p.waiting >= pendingFrames || p.waitingBytes+len(fr.PCM) > pendingBytes
			if !full && p.roomLocked(fr) {
				p.holdLocked(fr, nil)
				p.omu.Unlock()
				return
			}
			// .
			// .
			// .
			// .
			patient = full
		}
		// .
		// .
		select {
		case <-p.changed:
		default:
		}
		p.omu.Unlock()
		var done <-chan struct{}
		if owner != nil {
			done = owner.done
		}
		var timeout <-chan time.Time
		if patient {
			if wait == nil {
				wait = time.NewTimer(p.patience)
			}
			timeout = wait.C
		}
		select {
		case <-p.changed:
		case <-done:
		case <-timeout:
			p.giveUpOldest()
			wait = nil
		}
	}
}

func (t *audioTaker) over() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

// .
func (p *audioPair) take(t *audioTaker) (audio.Frame, bool) {
	p.omu.Lock()
	defer p.omu.Unlock()
	if t.queued == 0 {
		return audio.Frame{}, false
	}
	for i, h := range p.held {
		if h.owner != t {
			continue
		}
		copy(p.held[i:], p.held[i+1:])
		p.held[len(p.held)-1] = heldFrame{}
		p.held = p.held[:len(p.held)-1]
		p.heldBytes -= len(h.fr.PCM)
		t.queued--
		nudge(p.changed)
		return h.fr, true
	}
	return audio.Frame{}, false
}

// .
func (p *audioPair) queuedFor(t *audioTaker) int {
	p.omu.Lock()
	defer p.omu.Unlock()
	return t.queued
}

// .
func (p *audioPair) unnamedHeld() int {
	p.omu.Lock()
	defer p.omu.Unlock()
	return p.waiting
}

// .
// .
// .
// .
// .
type sessionChannel struct {
	pair  *audioPair
	taker *audioTaker
	ctx   context.Context
}

// .
// .
// .
// .
func (p *audioPair) channel(t *audioTaker, ctx context.Context) *sessionChannel {
	return &sessionChannel{pair: p, taker: t, ctx: ctx}
}

// .
// .
func newSessionChannel(p *audioPair, ctx context.Context) *sessionChannel {
	return p.channel(p.attach(ctx), ctx)
}

func (c *sessionChannel) WriteInput(fr audio.Frame) error {
	c.pair.wmu.Lock()
	defer c.pair.wmu.Unlock()
	return audio.WriteFrame(c.pair.in, fr)
}

// .
// .
func cleanEnd(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed)
}

// .
// .
func (p *audioPair) endReason() error {
	if e := p.readErr.Load(); e != nil {
		return *e
	}
	return io.EOF
}

func (c *sessionChannel) ReadOutput() (audio.Frame, error) {
	for {
		select {
		case <-c.ctx.Done():
			return audio.Frame{}, io.EOF
		default:
		}
		if fr, ok := c.pair.take(c.taker); ok {
			return fr, nil
		}
		select {
		case <-c.taker.ready:
		case <-c.pair.ended:
			// .
			if fr, ok := c.pair.take(c.taker); ok {
				return fr, nil
			}
			return audio.Frame{}, c.pair.endReason()
		case <-c.ctx.Done():
			return audio.Frame{}, io.EOF
		}
	}
}

func (c *sessionChannel) Close() error { return nil }

// .
// .
// .
var ErrNoAudioPair = errors.New("voicesession: this child has no audio pair")

// .
// .
// .
// .
// .
// .
// .
func (v *VoiceSession) OpenWithAudio(ctx context.Context, sessionID string, b *audio.Binding, args map[string]any) error {
	if b == nil {
		return errors.New("voicesession: OpenWithAudio needs a binding")
	}
	if v.pair == nil {
		b.Release()
		return ErrNoAudioPair
	}
	if args == nil {
		args = map[string]any{}
	}
	audioOpenArgs(args, b)
	err := v.open(ctx, sessionID, args, b)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.binding != b {
		return err
	}
	switch v.admission {
	case "admitted":
		// .
		// .
		// .
		in, out, ferr := engineFormats(v.openResult, b.HasInput())
		if ferr != nil {
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			v.markFaultLocked(ferr.Error())
			go func() {
				actx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = v.Close(actx, "abort", ferr.Error())
			}()
			return ferr
		}
		v.engineIn, v.engineOut = in, out
		// .
		// .
		pctx, cancel := context.WithCancel(v.audioCtx)
		p := audio.NewPump(b, v.pair.channel(v.owner, pctx))
		p.Stream = uint32(v.inst)
		p.EngineIn, p.EngineOut = in, out
		v.pump, v.pumpCancel = p, cancel
		go p.Run(pctx)
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		inst := v.inst
		go func() {
			select {
			case <-p.Failed():
			case <-p.Done():
			}
			_, inErr, outErr := p.Errors()
			why := outErr
			if why == nil {
				why = inErr
			}
			if why == nil {
				return
			}
			v.apply(inst, func() { v.markFaultLocked("audio transport: " + why.Error()) })
		}()
	case "unknown":
		// .
		// .
		// .
		// .
		// .
	}
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
// .
// .
func audioOpenArgs(args map[string]any, b *audio.Binding) {
	args["output_handle"] = b.OutputHandle
	output := map[string]any{"rate": b.OutFormat.Rate, "channels": b.OutFormat.Channels}
	if b.HasInput() {
		args["input_handle"] = b.InputHandle
		args["audio"] = map[string]any{"format": "s16le",
			"input": map[string]any{"rate": b.InFormat.Rate, "channels": b.InFormat.Channels}, "output": output}
		return
	}
	delete(args, "input_handle")
	args["audio"] = map[string]any{"format": "s16le", "input": nil, "output": output}
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
func engineFormats(result json.RawMessage, wantInput bool) (in, out audio.Format, err error) {
	if len(result) == 0 {
		return audio.Format{}, audio.Format{}, fmt.Errorf("voicesession: the engine admitted the open without naming its audio formats — the host will not guess sample rates")
	}
	var res struct {
		Audio map[string]json.RawMessage `json:"audio"`
	}
	if jerr := json.Unmarshal(result, &res); jerr != nil {
		return audio.Format{}, audio.Format{}, fmt.Errorf("voicesession: the engine's open admission does not carry readable audio formats: %w", jerr)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if raw, present := res.Audio["format"]; present {
		var enc string
		if jerr := json.Unmarshal(raw, &enc); jerr != nil || enc != "s16le" {
			return audio.Format{}, audio.Format{}, fmt.Errorf("voicesession: the engine's audio.format %s is not s16le — the pump decodes only s16le and will not read another encoding as it", string(bytes.TrimSpace(raw)))
		}
	}
	read := func(name string) (audio.Format, bool, error) {
		raw, present := res.Audio[name]
		if !present || string(bytes.TrimSpace(raw)) == "null" {
			return audio.Format{}, present, nil
		}
		var f struct{ Rate, Channels int }
		if jerr := json.Unmarshal(raw, &f); jerr != nil {
			return audio.Format{}, true, fmt.Errorf("voicesession: the engine's %s format is unreadable: %w", name, jerr)
		}
		got := audio.Format{Rate: f.Rate, Channels: f.Channels}
		if got.Rate < 8000 || got.Rate > 192000 || got.Channels < 1 || got.Channels > 2 {
			return got, true, fmt.Errorf("voicesession: the engine answered with an %s format that is not audio: %s", name, got)
		}
		return got, true, nil
	}
	in, inPresent, inErr := read("input")
	out, _, outErr := read("output")
	if inErr != nil {
		return in, out, inErr
	}
	if outErr != nil {
		return in, out, outErr
	}
	if out == (audio.Format{}) {
		return in, out, fmt.Errorf("voicesession: the engine named only part of its audio formats — the output format is required before audio flows")
	}
	switch {
	case wantInput && in == (audio.Format{}):
		return in, out, fmt.Errorf("voicesession: the engine named only part of its audio formats — both input and output are required before audio flows")
	case !wantInput && !inPresent:
		return in, out, fmt.Errorf("voicesession: the open asked for an output-only session and the engine's admission does not say audio.input is null — omission is not confirmation, and the host will not assume the engine opened no microphone path")
	case !wantInput && in != (audio.Format{}):
		return in, out, fmt.Errorf("voicesession: the open asked for an output-only session and the engine answered with an input format (%s) — it opened a direction the host did not bind", in)
	}
	return in, out, nil
}

// .
// .
func (v *VoiceSession) EngineFormats() (in, out audio.Format) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.engineIn, v.engineOut
}

// .
// .
func (v *VoiceSession) Audio() (*audio.Binding, *audio.Pump) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.binding, v.pump
}

// .
// .
// .
// .
func (v *VoiceSession) FinishInputNow(ctx context.Context, streamID string) (int64, error) {
	v.mu.Lock()
	sid, p := v.sessionID, v.pump
	v.mu.Unlock()
	if p == nil {
		return 0, fmt.Errorf("voicesession: no audio pump: finish_input needs a session opened with audio")
	}
	if v.OutputOnly() {
		return 0, ErrNoInput
	}
	end := p.Cutoff(p.Delivered())
	return end, v.FinishInputFor(ctx, sid, streamID, end)
}

// .
// .
// .
var ErrNoInput = errors.New("voicesession: this session has no input direction — it was opened output-only")

// .
// .
func (v *VoiceSession) OutputOnly() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.binding != nil && !v.binding.HasInput()
}

// .
func (v *VoiceSession) Stray() uint64 {
	if v.pair == nil {
		return 0
	}
	return v.pair.stray.Load()
}

// .
// .
// .
func (v *VoiceSession) Unnamed() uint64 {
	if v.pair == nil {
		return 0
	}
	return v.pair.unnamed.Load()
}
