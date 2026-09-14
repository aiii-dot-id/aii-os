package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
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
	ap := &ActivePlugin{ID: "review.voice", Voice: NewVoiceSession(c)}
	if err := ap.Voice.Open(ctx, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.FinishInput(ctx, "mic", 48000); err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Close(ctx, "drain", "done"); err != nil {
		t.Fatal(err)
	}
	snap, err := ap.Voice.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ap.Pinned() {
		t.Errorf("close ADMISSION released pin while engine reports lifecycle=%s playback=%s", snap.Lifecycle, snap.Playback.State)
	}
	if ap.Voice.Label() == "Closed" {
		t.Error("close ADMISSION fabricated Closed label while engine is still playing")
	}
}

func TestReviewVoiceCandidateDoesNotNeedToolOperations(t *testing.T) {
	// .
	// .
	// .
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "review.voice", Argv: []string{buildFakechild(t), "session"}, SessionMode: true}, nopDispatcher{})
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

// .
func buildFakechild(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakechild"+exeSuffix)
	if out, err := exec.Command("go", "build", "-o", bin, "../supervisor/testdata/fakechild").CombinedOutput(); err != nil {
		t.Skipf("cannot build fakechild here: %v\n%s", err, out)
	}
	return bin
}
