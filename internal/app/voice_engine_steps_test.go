package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A FINAL AFTER THE INPUT'S COMPLETION BREAKS THE ENGINE'S CONTRACT: it is
// handled and logged (voiceEngineEvent's last step, answerFinal). The
// words are still answered.
func TestAFinalAfterTheInputsCompletionIsHandledAndLogged(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	a := newVoiceApp(t)
	entered := make(chan struct{})
	stubWake(t, func() (string, error) { close(entered); return "late answer", nil })
	h, _ := newTrackedSession(a, "vs-late", true)
	h.inputDone.Store(true)
	a.voiceEngineEvent(nil, finalEvent("vs-late", "one more thing"), dropEvent)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("a final after the completion was not handled")
	}
	if !logs.Contains("AFTER input_finished on vs-late") || !strings.Contains(logs.String(), "voice.refusal") {
		t.Errorf("the broken contract was not logged as a refusal:\n%s", logs.String())
	}
}
