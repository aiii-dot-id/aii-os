package pluginhost

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

// .
// .
type nullChannel struct{}

func (nullChannel) WriteInput(audio.Frame) error     { return nil }
func (nullChannel) ReadOutput() (audio.Frame, error) { return audio.Frame{}, io.EOF }
func (nullChannel) Close() error                     { return nil }

// .
// .
// .
// .
func serviceControls(p *epochProbe) {
	go func() {
		for {
			frame, err := bbb.ReadFrame(p.reader, bbb.MaxControlFrameBytes)
			if err != nil {
				return
			}
			var req struct {
				ID     json.RawMessage
				Params struct{ Operation string }
			}
			if json.Unmarshal(frame, &req) != nil {
				return
			}
			var reply string
			if req.Params.Operation == "speech.session.open" {
				reply = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"accepted":true}}`, req.ID)
			} else {
				reply = fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"STALE_SESSION"}}`, req.ID)
			}
			select {
			case p.frames <- []byte(reply):
			case <-p.ctx.Done():
				return
			}
		}
	}()
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestReviewBoundFinishNeverBorrowsAReplacementPump(t *testing.T) {
	p := newEpochProbe(t)
	serviceControls(p)
	f := audio.Format{Rate: 16000, Channels: 1}
	newPump := func() *audio.Pump {
		return audio.NewPump(&audio.Binding{InFormat: f, OutFormat: f}, nullChannel{})
	}
	install := func(pm *audio.Pump) {
		p.v.mu.Lock()
		p.v.pump = pm
		p.v.mu.Unlock()
	}
	if err := p.v.Open(p.ctx, "s0", nil); err != nil {
		t.Fatal(err)
	}
	install(newPump())
	for k := 1; k <= 200; k++ {
		old, next := fmt.Sprintf("s%d", k-1), fmt.Sprintf("s%d", k)
		p.event("session_end", old, 1)
		p.observed()
		replacement := newPump()
		var wg sync.WaitGroup
		var finErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			finErr = p.v.FinishInputFor(p.ctx, old, "in", 100)
		}()
		go func() {
			defer wg.Done()
			if err := p.v.Open(p.ctx, next, nil); err != nil {
				t.Error(err)
				return
			}
			install(replacement)
		}()
		wg.Wait()
		if finErr == nil {
			t.Fatalf("round %d: a Finish bound to the replaced session %s was admitted", k, old)
		}
		if c := replacement.CutoffAt(); c != -1 {
			t.Fatalf("round %d: the Finish bound to %s set the REPLACEMENT %s's cutoff to %d", k, old, next, c)
		}
	}
}
