package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func correctedRaw(typ, session string, seq int64, fields map[string]any) pluginhost.Event {
	body := map[string]any{"type": typ, "session_id": session, "sequence": seq}
	for k, v := range fields {
		body[k] = v
	}
	raw, _ := json.Marshal(body)
	return pluginhost.Event{Type: typ, SessionID: session, Sequence: seq, Raw: raw}
}

func TestTheTwoMembersTravelTogetherOrNotAtAll(t *testing.T) {
	for name, c := range map[string]struct {
		fields      map[string]any
		recognized  string
		corrections int64
	}{
		"corrected":                 {map[string]any{"text": "Quinn is here", "recognized_text": "Queen is here", "corrections": 1}, "Queen is here", 1},
		"several corrections":       {map[string]any{"text": "Quinn and Taylor", "recognized_text": "Queen and Roman", "corrections": 2}, "Queen and Roman", 2},
		"uncorrected":               {map[string]any{"text": "Queen is here"}, "", 0},
		"recognized without count":  {map[string]any{"text": "Quinn is here", "recognized_text": "Queen is here"}, "", 0},
		"count without recognized":  {map[string]any{"text": "Quinn is here", "corrections": 1}, "", 0},
		"count of zero":             {map[string]any{"text": "Quinn is here", "recognized_text": "Queen is here", "corrections": 0}, "", 0},
		"negative count":            {map[string]any{"text": "Quinn is here", "recognized_text": "Queen is here", "corrections": -1}, "", 0},
		"recognized equal to text":  {map[string]any{"text": "Quinn is here", "recognized_text": "Quinn is here", "corrections": 1}, "", 0},
		"empty recognized, counted": {map[string]any{"text": "Quinn is here", "recognized_text": "", "corrections": 3}, "", 0},
	} {
		for _, typ := range []string{"transcript_final", "transcript_partial"} {
			f := decodeVoiceFrame(correctedRaw(typ, "vs", 4, c.fields))
			if r, n := f.corrected(); r != c.recognized || n != c.corrections {
				t.Errorf("%s %s: corrected() = %q, %d; want %q, %d", name, typ, r, n, c.recognized, c.corrections)
			}
			ve, shown := voiceEventFor(f, false)
			if !shown || ve.Text != c.fields["text"] || ve.RecognizedText != c.recognized || ve.Corrections != c.corrections {
				t.Errorf("%s %s: the page is told %+v (shown %v)", name, typ, ve, shown)
			}
			wire, _ := json.Marshal(ve)
			var onWire map[string]any
			_ = json.Unmarshal(wire, &onWire)
			_, hasText := onWire["recognized_text"]
			_, hasCount := onWire["corrections"]
			if hasText != (c.recognized != "") || hasCount != (c.corrections != 0) {
				t.Errorf("%s %s: the page's frame carries the members without a correction, or not with one: %s", name, typ, wire)
			}
		}
	}
	// Under SAFE the recognizer's words are withheld with the transcript.
	if _, shown := voiceEventFor(decodeVoiceFrame(correctedRaw("transcript_final", "vs", 4, map[string]any{"text": "Quinn", "recognized_text": "Queen", "corrections": 1})), true); shown {
		t.Fatal("SAFE let a corrected transcript through to the page")
	}
	// A transcript with either member mistyped carries no words at all, as
	// any transcript with a mistyped field (DecodeVoiceFrame).
	for _, fields := range []map[string]any{
		{"text": "Quinn", "recognized_text": 7, "corrections": 1},
		{"text": "Quinn", "recognized_text": "Queen", "corrections": "one"},
	} {
		f := decodeVoiceFrame(correctedRaw("transcript_final", "vs", 4, fields))
		if _, shown := voiceEventFor(f, false); f.err == nil || shown {
			t.Fatalf("a transcript with a mistyped correction member was applied: %v", fields)
		}
	}
	// Only transcripts carry them: another event's stray members go nowhere.
	if ve, _ := voiceEventFor(decodeVoiceFrame(correctedRaw("turn_started", "vs", 4, map[string]any{"recognized_text": "Queen", "corrections": 1})), false); ve.RecognizedText != "" || ve.Corrections != 0 {
		t.Fatalf("an event that is no transcript carried recognized words to the page: %+v", ve)
	}
}

// The whole path of one corrected final through the host's event consumer:
// the page's event, the words the turn is opened with, and the binding the
// recording turn reads.
func TestACorrectedFinalIsAnsweredAsCorrectedAndKeepsWhatWasHeard(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "corrected", true)
	page, fanned := &pageLog{}, &pageLog{}
	a.voiceEventSink = fanned.enqueue
	var mu sync.Mutex
	var woke []string
	saved := voiceWake
	voiceWake = func(_ *App, _ context.Context, _ string, text string) (string, error) {
		mu.Lock()
		woke = append(woke, text)
		mu.Unlock()
		return "reply", nil
	}
	t.Cleanup(func() { voiceWake = saved })

	a.voiceEngineEvent(nil, correctedRaw("transcript_final", h.id, 7, map[string]any{"text": "Quinn is here", "recognized_text": "Queen is here", "corrections": 1}), page.enqueue)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(woke)
		mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(woke) != 1 || woke[0] != voiceMarker+"Quinn is here" {
		t.Fatalf("the turn was opened with %q, want the corrected words once", woke)
	}
	var final *dashboard.VoiceEvent
	page.mu.Lock()
	for i := range page.evs {
		if page.evs[i].Type == "transcript_final" {
			final = &page.evs[i]
		}
	}
	page.mu.Unlock()
	if final == nil || final.Text != "Quinn is here" || final.RecognizedText != "Queen is here" || final.Corrections != 1 || !final.Operator {
		t.Fatalf("the page's final: %+v", final)
	}

	b := a.voiceBindingFor(heardUtterance{SessionID: h.id, Sequence: 7, Text: "Quinn is here", Recognized: "Queen is here", Corrections: 1})
	if b == nil || b.text != "Quinn is here" || b.recognized != "Queen is here" || b.corrections != 1 || b.seq != 7 {
		t.Fatalf("the binding the recording turn reads: %+v", b)
	}
	if plain := a.voiceBindingFor(heardUtterance{SessionID: h.id, Sequence: 8, Text: "nothing changed"}); plain == nil || plain.recognized != "" || plain.corrections != 0 {
		t.Fatalf("an uncorrected utterance's binding claims a correction: %+v", plain)
	}
}
