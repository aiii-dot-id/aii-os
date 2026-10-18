package pluginhost

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

// readOpSession reads the next control frame, checks its operation, and
// returns its id and the session the request NAMES — so a test can prove
// which session an operation was dispatched for, not only that one was.
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

// req 1 (corrected): an operation names the session its caller captured.
// After the driver has moved to a replacement session on the same reusable
// driver, every operation bound to the old session — synthesize, interrupt,
// finish_input, close — is refused HERE, before any frame is sent; an
// operation bound to the current session is dispatched naming that session.
func TestReviewOldHandleIsRefusedAfterReopenOnTheSameDriver(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	// s1 ends on the engine's word; the same driver reopens as s2.
	p.event("session_end", "s1", 1)
	p.observed()
	p.open("s2")
	ctx := p.ctx
	// s2's own pump (the test stands in for OpenWithAudio): nothing bound to
	// s1 may touch it.
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

	// Nothing above reached the engine: the very next frame is the current
	// session's synthesis, and it names s2.
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
