// .
// .
// .
// .
// .

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"io"
	"testing"
	"time"
)

func TestAuditHistoricalStatusCompletionStillReachesAppDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	frames := make(chan []byte, 16)
	rd, wr := io.Pipe()
	c := supervisor.NewSessionClientFrames(frames, wr, nil, 16)
	v := pluginhost.NewVoiceSession(c)
	clientDone, engineDone, observerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a := &App{}
	laterSeen := make(chan struct{})
	go func() { defer close(clientDone); _ = c.Run(ctx) }()
	go func() {
		defer close(observerDone)
		for {
			select {
			case ev := <-v.Observe():
				a.voiceObserved(ev)
				if ev.Type == "playback_progress" {
					close(laterSeen)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		defer close(engineDone)
		for {
			raw, err := bbb.ReadFrame(rd, bbb.MaxControlFrameBytes)
			if err != nil {
				return
			}
			var req struct {
				ID     json.RawMessage
				Params struct{ Operation string }
			}
			if json.Unmarshal(raw, &req) != nil {
				return
			}
			result := `{"accepted":true}`
			if req.Params.Operation == "speech.session.status" {
				result = `{"session_id":"status-only","state_sequence":4,"lifecycle":"open","input_completion":{"stream_id":"in","end_sample":320,"processed_end_sample":320,"sequence":3,"reason":"capture_limit"}}`
			}
			select {
			case frames <- []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%s}`, req.ID, result)):
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = rd.Close()
		_ = wr.Close()
		for _, done := range []chan struct{}{clientDone, engineDone, observerDone} {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("review fixture worker did not join")
			}
		}
	})
	if err := v.Open(ctx, "status-only", nil); err != nil {
		t.Fatal(err)
	}
	h := &voiceHandle{id: "status-only", v: v, done: v.Done(), drained: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	// .
	// .
	frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"playback_progress","session_id":"status-only","sequence":4}}`)
	select {
	case <-laterSeen:
	case <-ctx.Done():
		t.Fatal("later wire event not observed")
	}

	if _, err := v.Status(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.InputClosed():
	default:
		t.Fatal("positive control: status did not publish input completion")
	}
	if why := h.InputCompletionReason(); why != "capture_limit" {
		t.Fatalf("completion reason=%q", why)
	}
	select {
	case <-h.drained:
	case <-time.After(200 * time.Millisecond):
		t.Errorf("historical completion seq3 recovered after wire seq4 closed browser input, but App inputDone=%v drainOnce=%v and no drain was requested", h.inputDone.Load(), h.drainOnce.Load())
	}
	// .
	// .
	frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"input_finished","session_id":"status-only","sequence":5,"stream_id":"in","end_sample":320,"processed_end_sample":320,"reason":"capture_limit"}}`)
	awaitDrained(t, h)
	if !h.drainOnce.Load() {
		t.Fatal("event positive control failed to request drain")
	}
}
