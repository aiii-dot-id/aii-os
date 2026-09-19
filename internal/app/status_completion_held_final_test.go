package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
// .
// .
// .
func TestStatusOnlyCompletionWaitsForTheHeldAnswerThenDrainsOnce(t *testing.T) {
	a := newVoiceApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	r, w := io.Pipe()
	frames := make(chan []byte, 8)
	c := supervisor.NewSessionClientFrames(frames, w, nil, 8)
	v := pluginhost.NewVoiceSession(c)
	clientDone, serverDone, observerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { defer close(clientDone); _ = c.Run(ctx) }()
	const completion = `{"stream_id":"in-held","end_sample":480,"processed_end_sample":480,"sequence":2,"reason":"capture_limit"}`
	var mu sync.Mutex
	var ops []string
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
			mu.Lock()
			ops = append(ops, strings.TrimPrefix(req.Params.Operation, "speech.session."))
			mu.Unlock()
			result := `{"accepted":true}`
			if req.Params.Operation == "speech.session.status" {
				result = `{"session_id":"held","state_sequence":1,"lifecycle":"open","input_completion":` + completion + `}`
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
	gate := make(chan struct{})
	stubWake(t, func() (string, error) { <-gate; return "the held answer", nil })
	h, _ := newTrackedSession(a, "held", true)
	h.v = v
	ap := &pluginhost.ActivePlugin{ID: "review.engine", Voice: v}
	go func() { defer close(observerDone); a.observeEngine(ap, v) }()
	if err := v.Open(ctx, h.id, nil); err != nil {
		t.Fatal(err)
	}
	// .
	frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":` + string(finalEvent("held", "goodbye").Raw) + `}`)
	// .
	if _, err := v.Status(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.InputClosed():
	case <-time.After(2 * time.Second):
		t.Fatal("status did not complete the input")
	}
	select {
	case <-h.drained:
		t.Fatal("the drain must wait for the held answer")
	case <-time.After(150 * time.Millisecond):
	}
	close(gate)
	awaitDrained(t, h)
	mu.Lock()
	got := strings.Join(ops, ",")
	mu.Unlock()
	if !strings.Contains(got, "synthesize,close") || strings.Count(got, "close") != 1 {
		t.Fatalf("the answer is admitted, then ONE drain-close: %s", got)
	}
}
