package pluginhost

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// The voice wiring seam (V-W): a session-mode child bound to an
// activation drives a VoiceSession — admission comes back from the real
// process, and closing the activation ends the session.
func TestBindVoiceSessionDrivesARealChild(t *testing.T) {
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "com.example.voice", Argv: []string{fakechildBin, "session"}, SessionMode: true}, nopDispatcher{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ap := &ActivePlugin{ID: "com.example.voice", sup: sup}
	if err := ap.bindVoiceSession(sup); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if ap.Voice.Load() == nil {
		t.Fatal("a voice plugin carries its session driver")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ap.Voice.Load().Open(ctx, "s1", nil); err != nil {
		t.Fatalf("open through the real child: %v", err)
	}
	if err := ap.Voice.Load().Synthesize(ctx, "x", "hello"); err != nil {
		t.Fatalf("synthesize admitted by the real child: %v", err)
	}
	if err := ap.CloseQuiet(ctx); err != nil {
		t.Logf("close: %v", err)
	}
}
