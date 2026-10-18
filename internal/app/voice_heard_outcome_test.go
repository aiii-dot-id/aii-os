package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// WHAT WAS HEARD IS NOT REPORTED LOST WHEN ONLY ITS ANSWER FAILED (audit
// H9). A wake records the words before it thinks, so a turn that fails at
// the model has already put them in the record; the log said "the engine's
// transcript was not recorded" for every failure alike, and an operator
// reading it would say the words again. The log now says which half is
// missing. A wake that fails BEFORE the record — here the real one, on an
// identity that is not live — still says the words were not recorded.
func TestAFailedAnswerSaysTheTranscriptWasRecorded(t *testing.T) {
	ctx := context.Background()
	heard := func(t *testing.T, a *App, id string) {
		t.Helper()
		h, _ := newTrackedSession(a, id, true)
		if err := h.Finish(ctx, 320); err != nil {
			t.Fatal(err)
		}
		a.voiceEngineEvent(nil, finalEvent(id, "goodbye"), dropEvent)
		a.voiceEngineEvent(nil, finishedEvent(id, 320), dropEvent)
		awaitDrained(t, h)
	}

	t.Run("the answer failed after the record", func(t *testing.T) {
		logs := logsink.CaptureForTest(t)
		a := newVoiceApp(t)
		stubWake(t, func() (string, error) { return "", errors.New("the model timed out") })
		heard(t, a, "vs-h9-answer")
		if logs.Contains("transcript was not recorded") || !logs.Contains("transcript was recorded; answering it failed: the model timed out") {
			t.Fatalf("the log does not say the words were kept and only the answer failed:\n%s", logs.String())
		}
	})
	t.Run("a participant's answer failed after the record", func(t *testing.T) {
		logs := logsink.CaptureForTest(t)
		a := newVoiceApp(t)
		stubWake(t, func() (string, error) { return "", errors.New("the model timed out") })
		a.handleHeard(ctx, heardUtterance{Source: "speech engine", Text: "hello", Answer: true})
		if !logs.Contains("transcript was recorded; answering it failed: the model timed out") {
			t.Fatalf("the participant's path does not say the words were kept:\n%s", logs.String())
		}
	})
	t.Run("the wake failed before the record", func(t *testing.T) {
		logs := logsink.CaptureForTest(t)
		a := newVoiceApp(t) // the real wake: no conversation, so not live
		heard(t, a, "vs-h9-unrecorded")
		if !logs.Contains("transcript was not recorded: the identity is not live") {
			t.Fatalf("a wake that never recorded the words must say so:\n%s", logs.String())
		}
		if strings.Contains(logs.String(), "transcript was recorded") {
			t.Fatalf("a failure before the record was reported as recorded:\n%s", logs.String())
		}
	})
	t.Run("a participant's wake failed before the record", func(t *testing.T) {
		logs := logsink.CaptureForTest(t)
		a := newVoiceApp(t)
		a.handleHeard(ctx, heardUtterance{Source: "speech engine", Text: "hello", Answer: true})
		if !logs.Contains("transcript was not recorded: the identity is not live") || strings.Contains(logs.String(), "transcript was recorded") {
			t.Fatalf("a participant's wake that never recorded the words must say so:\n%s", logs.String())
		}
	})
}
