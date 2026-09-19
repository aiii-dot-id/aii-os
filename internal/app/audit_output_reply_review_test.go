package app

// .
// .
// .
// .
// .

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"path/filepath"
	"testing"
)

func auditOutputApp(t *testing.T) (*App, *voiceHandle, *fakeEngineSession) {
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	voiceModeDoor(t, a, listenOff, speakOn)
	h, f := outputHandle(t, a, "vs-output-audit")
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}
	return a, h, f
}

func TestAuditSilentTurnDoesNotCancelPriorTypedReply(t *testing.T) {
	a, _, f := auditOutputApp(t)
	a.settleVoice(context.Background(), "first answer still being spoken")
	first := f.producingNow()
	if first == "" {
		t.Fatal("fixture has no first synthesis")
	}
	a.settleVoice(context.Background(), " \n ")
	if f.producingNow() != first {
		t.Fatalf("a turn with no new reply cancelled the existing spoken answer; operations=%v", f.opsSeen())
	}
}
