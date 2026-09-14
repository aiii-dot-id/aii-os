package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
type epochProbe struct {
	t      *testing.T
	ctx    context.Context
	frames chan []byte
	reader *io.PipeReader
	c      *supervisor.SessionClient
	v      *VoiceSession
}

func newEpochProbe(t *testing.T) *epochProbe {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	r, w := io.Pipe()
	p := &epochProbe{t: t, ctx: ctx, frames: make(chan []byte, 8), reader: r}
	p.c = supervisor.NewSessionClientFrames(p.frames, w, nopDispatcher{}, 8)
	p.v = NewVoiceSession(p.c)
	done := make(chan error, 1)
	go func() { done <- p.c.Run(ctx) }()
	t.Cleanup(func() { cancel(); r.Close(); w.Close(); <-done })
	return p
}

func (p *epochProbe) read(op string) json.RawMessage {
	p.t.Helper()
	frame, err := bbb.ReadFrame(p.reader, bbb.MaxControlFrameBytes)
	if err != nil {
		p.t.Fatal(err)
	}
	var req struct {
		ID     json.RawMessage
		Params struct{ Operation string }
	}
	if err = json.Unmarshal(frame, &req); err != nil {
		p.t.Fatal(err)
	}
	if req.Params.Operation != op {
		p.t.Fatalf("got %s, want %s", req.Params.Operation, op)
	}
	return req.ID
}
func (p *epochProbe) reply(id json.RawMessage, result string) {
	p.frames <- []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, id, result))
}
func (p *epochProbe) event(typ, sid string, seq int) {
	p.frames <- []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":%q,"session_id":%q,"sequence":%d}}`, typ, sid, seq))
}
func (p *epochProbe) observed() {
	p.t.Helper()
	select {
	case <-p.v.Observe():
	case <-p.ctx.Done():
		p.t.Fatal("no observation")
	}
}
func (p *epochProbe) open(sid string) {
	p.t.Helper()
	done := make(chan error, 1)
	go func() { done <- p.v.Open(p.ctx, sid, nil) }()
	id := p.read("speech.session.open")
	p.reply(id, `{"accepted":true}`)
	if err := <-done; err != nil {
		p.t.Fatal(err)
	}
}

func TestReviewOpenDoesNotEraseEarlyFailure(t *testing.T) {
	p := newEpochProbe(t)
	done := make(chan error, 1)
	go func() { done <- p.v.Open(p.ctx, "s1", nil) }()
	id := p.read("speech.session.open")
	p.event("failure", "s1", 1)
	p.observed()
	p.reply(id, `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !p.v.Faulted() {
		t.Fatal("open admission erased the already-observed engine failure")
	}
}

func TestReviewOldStatusCannotCloseNewSession(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	done := make(chan error, 1)
	go func() { _, err := p.v.Status(p.ctx); done <- err }()
	oldID := p.read("speech.session.status")
	p.event("session_end", "s1", 1)
	p.observed()
	p.open("s2")
	p.reply(oldID, `{"session_id":"s1","state_sequence":99,"lifecycle":"closed"}`)
	<-done
	if !p.v.IsOpen() {
		t.Fatalf("late s1 status closed live s2: label=%s", p.v.Label())
	}
}

func TestReviewUnknownOpenRetainsActivationPin(t *testing.T) {
	p := newEpochProbe(t)
	ctx, cancel := context.WithCancel(p.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.v.Open(ctx, "s1", nil) }()
	p.read("speech.session.open")
	cancel()
	if err := <-done; !errors.Is(err, supervisor.ErrAdmissionUnknown) {
		t.Fatalf("wanted unknown, got %v", err)
	}
	ap := &ActivePlugin{Voice: p.v}
	if !ap.Pinned() {
		t.Fatal("open reached engine but lost acknowledgement leaves activation unpinned")
	}
}

func TestReviewObserverFaultCannotReleaseLivePin(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	ap := &ActivePlugin{Voice: p.v}
	released := ap.PinReleased()
	// .
	// .
	for seq := 1; seq <= 1025; seq++ {
		p.event("transcript_final", "s1", seq)
		for {
			p.v.mu.Lock()
			seen := p.v.lastSeq
			p.v.mu.Unlock()
			if seen >= int64(seq) {
				break
			}
			select {
			case <-p.ctx.Done():
				t.Fatal("watermark timeout")
			case <-time.After(time.Millisecond):
			}
		}
	}
	select {
	case <-released:
		select {
		case <-p.c.Done():
			t.Fatal("bad probe: transport ended")
		default:
		}
		t.Fatal("observer overflow released the live pin without an engine terminal event or transport end")
	case <-time.After(100 * time.Millisecond):
	}
}
