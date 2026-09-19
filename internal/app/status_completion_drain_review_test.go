// .
// .
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
	"io"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
func TestReviewStatusOnlyCompletionReachesExistingDrain(t *testing.T) {
	a := newVoiceApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	r, w := io.Pipe()
	frames := make(chan []byte, 8)
	c := supervisor.NewSessionClientFrames(frames, w, nil, 8)
	v := pluginhost.NewVoiceSession(c)
	clientDone, serverDone, observerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { defer close(clientDone); _ = c.Run(ctx) }()
	const completion = `{"stream_id":"in-review","end_sample":480,"processed_end_sample":480,"sequence":1,"reason":"capture_limit"}`
	go func() {
		defer close(serverDone)
		for {
			raw, err := bbb.ReadFrame(r, bbb.MaxControlFrameBytes)
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
				result = `{"session_id":"review","state_sequence":1,"lifecycle":"open","input_completion":` + completion + `}`
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
		r.Close()
		w.Close()
		<-clientDone
		<-serverDone
		<-observerDone
	})
	h, _ := newTrackedSession(a, "review", false)
	h.v = v
	ap := &pluginhost.ActivePlugin{ID: "review.engine", Voice: v}
	go func() { defer close(observerDone); a.observeEngine(ap, v) }()
	if err := v.Open(ctx, h.id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Status(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.InputClosed():
	default:
		t.Fatal("positive control: status did not complete input")
	}
	lost := false
	select {
	case <-h.drained:
	case <-time.After(250 * time.Millisecond):
		lost = true
	}
	// .
	// .
	frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"input_finished","session_id":"review",` + completion[1:] + `}`)
	awaitDrained(t, h)
	if lost {
		t.Fatal("status stopped input but did not reach the app drain; only delivering the missing event repaired the session")
	}
}
