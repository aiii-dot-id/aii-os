package conversation

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
// .
// .
// .
// .

// .
// .
func captureNudgeLog(t *testing.T, reply string, cfg Config) string {
	t.Helper()
	// .
	// .
	buf := logsink.CaptureForTest(t)
	_, _ = runBreadthLoop(t, reply, cfg)
	return buf.String()
}

func TestNudgeLogLineNamesItsKind(t *testing.T) {
	got := captureNudgeLog(t, multiStepReadOnlyAnnouncement(), Config{HeuristicNudges: true})
	if !strings.Contains(got, "NUDGE breadth sent") {
		t.Fatalf("breadth nudge fired without its log line. Log:\n%s", got)
	}
}

func TestNudgeLogSerialWhenGateOff(t *testing.T) {
	off := false
	got := captureNudgeLog(t, multiStepReadOnlyAnnouncement(), Config{HeuristicNudges: true, BreadthNudge: &off})
	if !strings.Contains(got, "NUDGE serial sent") {
		t.Fatalf("gate off: serial nudge must log its kind. Log:\n%s", got)
	}
}

func TestNudgeLogSilentWhenNoNudge(t *testing.T) {
	got := captureNudgeLog(t, "Let me know if you need anything else. 1. I will read your notes. 2. I will check them twice.", Config{HeuristicNudges: true})
	if strings.Contains(got, "NUDGE") {
		t.Fatalf("goodbye closer drew a nudge log line. Log:\n%s", got)
	}
}
