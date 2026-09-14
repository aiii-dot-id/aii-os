package pluginhost

// .
// .
// .
// .

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
type audioPair struct {
	wmu    sync.Mutex
	in     io.Writer
	out    io.ReadCloser
	frames chan audio.Frame
	stray  atomic.Uint64
	ended  chan struct{}
}

const pairBuffer = 256

func newAudioPair(in io.Writer, out io.ReadCloser) *audioPair {
	p := &audioPair{in: in, out: out, frames: make(chan audio.Frame, pairBuffer), ended: make(chan struct{})}
	go p.read()
	return p
}

func (p *audioPair) read() {
	defer close(p.ended)
	for {
		fr, err := audio.ReadFrame(p.out)
		if err != nil {
			return
		}
		select {
		case p.frames <- fr:
		default:
			p.stray.Add(1)
		}
	}
}

// .
// .
// .
// .
type sessionChannel struct {
	pair *audioPair
	ctx  context.Context
}

func (c *sessionChannel) WriteInput(fr audio.Frame) error {
	c.pair.wmu.Lock()
	defer c.pair.wmu.Unlock()
	return audio.WriteFrame(c.pair.in, fr)
}

func (c *sessionChannel) ReadOutput() (audio.Frame, error) {
	select {
	case fr := <-c.pair.frames:
		return fr, nil
	case <-c.pair.ended:
		// .
		select {
		case fr := <-c.pair.frames:
			return fr, nil
		default:
			return audio.Frame{}, io.EOF
		}
	case <-c.ctx.Done():
		return audio.Frame{}, io.EOF
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
	args["input_handle"] = b.InputHandle
	args["output_handle"] = b.OutputHandle
	// .
	// .
	// .
	args["audio"] = map[string]any{"format": "s16le",
		"input":  map[string]any{"rate": b.InFormat.Rate, "channels": b.InFormat.Channels},
		"output": map[string]any{"rate": b.OutFormat.Rate, "channels": b.OutFormat.Channels}}
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
		in, out, ferr := engineFormats(v.openResult)
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
		pctx, cancel := context.WithCancel(context.Background())
		p := audio.NewPump(b, &sessionChannel{pair: v.pair, ctx: pctx})
		p.Stream = uint32(v.inst)
		p.EngineIn, p.EngineOut = in, out
		v.pump, v.pumpCancel = p, cancel
		go p.Run(pctx)
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
func engineFormats(result json.RawMessage) (in, out audio.Format, err error) {
	if len(result) == 0 {
		return audio.Format{}, audio.Format{}, fmt.Errorf("voicesession: the engine admitted the open without naming its audio formats — the host will not guess sample rates")
	}
	var res struct {
		Audio struct {
			Input  *struct{ Rate, Channels int } `json:"input"`
			Output *struct{ Rate, Channels int } `json:"output"`
		} `json:"audio"`
	}
	if jerr := json.Unmarshal(result, &res); jerr != nil {
		return audio.Format{}, audio.Format{}, fmt.Errorf("voicesession: the engine's open admission does not carry readable audio formats: %w", jerr)
	}
	if res.Audio.Input == nil || res.Audio.Output == nil {
		return audio.Format{}, audio.Format{}, fmt.Errorf("voicesession: the engine named only part of its audio formats — both input and output are required before audio flows")
	}
	in = audio.Format{Rate: res.Audio.Input.Rate, Channels: res.Audio.Input.Channels}
	out = audio.Format{Rate: res.Audio.Output.Rate, Channels: res.Audio.Output.Channels}
	check := func(name string, f audio.Format) error {
		if f.Rate < 8000 || f.Rate > 192000 || f.Channels < 1 || f.Channels > 2 {
			return fmt.Errorf("voicesession: the engine answered with an %s format that is not audio: %s", name, f)
		}
		return nil
	}
	if err := check("input", in); err != nil {
		return in, out, err
	}
	if err := check("output", out); err != nil {
		return in, out, err
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
	end := p.Cutoff(p.Delivered())
	return end, v.FinishInputFor(ctx, sid, streamID, end)
}

// .
func (v *VoiceSession) Stray() uint64 {
	if v.pair == nil {
		return 0
	}
	return v.pair.stray.Load()
}
