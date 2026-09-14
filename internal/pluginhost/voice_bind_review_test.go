package pluginhost

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

// .
// .
// .
func readOpSession(p *epochProbe, op string) (json.RawMessage, string) {
	p.t.Helper()
	frame, err := bbb.ReadFrame(p.reader, bbb.MaxControlFrameBytes)
	if err != nil {
		p.t.Fatal(err)
	}
	var req struct {
		ID     json.RawMessage
		Params struct {
			Operation string
			Arguments struct {
				SessionID string `json:"session_id"`
			}
		}
	}
	if err := json.Unmarshal(frame, &req); err != nil {
		p.t.Fatal(err)
	}
	if req.Params.Operation != op {
		p.t.Fatalf("got %s, want %s", req.Params.Operation, op)
	}
	return req.ID, req.Params.Arguments.SessionID
}

// .
// .
// .
// .
// .
func TestReviewOldHandleIsRefusedAfterReopenOnTheSameDriver(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	// .
	p.event("session_end", "s1", 1)
	p.observed()
	p.open("s2")
	ctx := p.ctx
	// .
	// .
	f := audio.Format{Rate: 16000, Channels: 1}
	pm := audio.NewPump(&audio.Binding{InFormat: f, OutFormat: f}, nullChannel{})
	p.v.mu.Lock()
	p.v.pump = pm
	p.v.mu.Unlock()

	if err := p.v.SynthesizeFor(ctx, "s1", "syn-old", "old answer"); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("synthesize on the old session must be refused as stale, got %v", err)
	}
	if err := p.v.InterruptFor(ctx, "s1", "", "operator"); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("interrupt on the old session must be refused as stale, got %v", err)
	}
	if err := p.v.FinishInputFor(ctx, "s1", "in-1", 320); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("finish_input on the old session must be refused as stale, got %v", err)
	}
	if err := p.v.CloseFor(ctx, "s1", "abort", "old"); !errors.Is(err, ErrStaleSession) {
		t.Fatalf("close on the old session must be refused as stale, got %v", err)
	}
	if c := pm.CutoffAt(); c != -1 {
		t.Fatalf("the old Finish touched the replacement's pump: cutoff %d", c)
	}

	// .
	// .
	done := make(chan error, 1)
	go func() { done <- p.v.SynthesizeFor(ctx, "s2", "syn-new", "fresh answer") }()
	id, sid := readOpSession(p, "speech.session.synthesize")
	if sid != "s2" {
		t.Fatalf("the dispatched synthesis must name the current session s2, got %q", sid)
	}
	p.reply(id, `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
