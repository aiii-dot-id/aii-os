package pluginhost

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func TestReviewCloseAdmissionMustRetainPin(t *testing.T) {
	h2gr, h2gw, _ := os.Pipe()
	g2hr, g2hw, _ := os.Pipe()
	t.Cleanup(func() { h2gr.Close(); h2gw.Close(); g2hr.Close(); g2hw.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := supervisor.NewSessionClient(g2hr, h2gw, nopDispatcher{}, 8)
	go c.Run(ctx)
	eng := &mockEngine{}
	eng.statusSeq.Store(1)
	eng.playback.Store("playing")
	go eng.serve(h2gr, g2hw)
	ap := pluginWithTestVoice(&ActivePlugin{ID: "review.voice"}, NewVoiceSession(c))
	if err := ap.Voice.Load().Open(ctx, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().FinishInput(ctx, "mic", 48000); err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().Close(ctx, "drain", "done"); err != nil {
		t.Fatal(err)
	}
	snap, err := ap.Voice.Load().Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ap.Pinned() {
		t.Errorf("close ADMISSION released pin while engine reports lifecycle=%s playback=%s", snap.Lifecycle, snap.Playback.State)
	}
	if ap.Voice.Load().Label() == "Closed" {
		t.Error("close ADMISSION fabricated Closed label while engine is still playing")
	}
}

func TestReviewVoiceCandidateDoesNotNeedToolOperations(t *testing.T) {
	// A voice package need declare nothing but its session interface,
	// whose controls never become tools; Voice carries that surface.
	// The health gate must evaluate it rather than count operations.
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "review.voice", Argv: []string{fakechildBin, "session"}, SessionMode: true}, nopDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	defer sup.Close()
	ap := &ActivePlugin{ID: "review.voice", sup: sup}
	if err := ap.bindVoiceSession(sup); err != nil {
		t.Fatal(err)
	}
	defer ap.sessionCancel()
	if err := ap.Health(context.Background()); err != nil {
		t.Fatalf("voice candidate rejected solely for having no tool operations: %v", err)
	}
}
