package app

import (
	"encoding/json"
	"testing"
)

func TestTheTurnKeepsWhatTheRecognizerHeard(t *testing.T) {
	a := newVoiceApp(t)
	note := func(text string, b *voiceBinding) map[string]any {
		t.Helper()
		seq, err := a.store.AddConversationTurnSeq(roleOperator, voiceMarker+text)
		if err != nil {
			t.Fatal(err)
		}
		a.annotateVoiceTurn(seq, b)
		got, err := a.store.TurnAnnotations(annotationVoice, []uint64{seq})
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(got[seq]), &payload); err != nil {
			t.Fatalf("the turn's voice annotation does not read: %q: %v", got[seq], err)
		}
		return payload
	}

	corrected := note("ask Quinn about the plan", &voiceBinding{session: "vs-1", seq: 7, text: "ask Quinn about the plan", recognized: "ask Queen about the plan", corrections: 1})
	if corrected["recognized_text"] != "ask Queen about the plan" || corrected["corrections"] != float64(1) {
		t.Fatalf("the turn does not keep what was heard and the count: %v", corrected)
	}
	if corrected["session"] != "vs-1" || corrected["sequence"] != float64(7) {
		t.Fatalf("the voice reference changed: %v", corrected)
	}

	plain := note("ask Quinn about the plan", &voiceBinding{session: "vs-1", seq: 8, text: "ask Quinn about the plan"})
	if _, has := plain["recognized_text"]; has {
		t.Fatalf("a transcript no rule changed carries recognized words: %v", plain)
	}
	if _, has := plain["corrections"]; has {
		t.Fatalf("a transcript no rule changed carries a count: %v", plain)
	}

	// One of the pair without the other is not a correction.
	half := note("ask Quinn", &voiceBinding{session: "vs-1", seq: 9, text: "ask Quinn", recognized: "ask Queen"})
	if _, has := half["recognized_text"]; has {
		t.Fatalf("recognized words with no count were kept as a correction: %v", half)
	}
}
