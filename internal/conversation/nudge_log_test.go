package conversation

import (
	"bytes"
	"log"
	"strings"
	"sync"
	"testing"
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
	var mu sync.Mutex
	buf := &bytes.Buffer{}
	old := log.Writer()
	log.SetOutput(&syncWriter{w: buf, mu: &mu})
	defer log.SetOutput(old)
	_, _ = runBreadthLoop(t, reply, cfg)
	mu.Lock()
	defer mu.Unlock()
	return buf.String()
}

// .
// .
// .
type syncWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
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
