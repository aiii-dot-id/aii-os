package pluginhost

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
func TestBindVoiceSessionDrivesARealChild(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "fakechild"+exeSuffix)
	if out, err := exec.Command("go", "build", "-o", bin, "../supervisor/testdata/fakechild").CombinedOutput(); err != nil {
		t.Skipf("cannot build fakechild here: %v\n%s", err, out)
	}
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "com.example.voice", Argv: []string{bin, "session"}, SessionMode: true}, nopDispatcher{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ap := &ActivePlugin{ID: "com.example.voice", sup: sup}
	if err := ap.bindVoiceSession(sup); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if ap.Voice == nil {
		t.Fatal("a voice plugin carries its session driver")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ap.Voice.Open(ctx, "s1", nil); err != nil {
		t.Fatalf("open through the real child: %v", err)
	}
	if err := ap.Voice.Synthesize(ctx, "x", "hello"); err != nil {
		t.Fatalf("synthesize admitted by the real child: %v", err)
	}
	if err := ap.CloseQuiet(ctx); err != nil {
		t.Logf("close: %v", err)
	}
}
