package app

import (
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// req 5: the page observation built for one engine event, and whether it
// may be fanned. Every accepted event is evidence for the page EXCEPT what
// was HEARD under SAFE (a transcript, provisional or final), which reaches
// no page as it reaches no record. A final transcript is marked final; a
// failure names itself; an unknown/turn type passes through generically.
func TestVoiceEventForSafeAndMapping(t *testing.T) {
	ev := func(typ, raw string) pluginhost.Event {
		return pluginhost.Event{Type: typ, SessionID: "vs1", Sequence: 3, Raw: json.RawMessage(raw)}
	}

	if ve, ok := voiceEventFor(decodeVoiceFrame(ev("transcript_final", `{"text":"hello","speaker":"Ada"}`)), false); !ok || !ve.Final || ve.Text != "hello" || ve.Speaker != "Ada" || ve.Type != "transcript_final" || ve.Sequence != 3 {
		t.Fatalf("a final transcript is fanned as final evidence: %+v ok=%v", ve, ok)
	}
	if ve, ok := voiceEventFor(decodeVoiceFrame(ev("transcript_partial", `{"text":"hel"}`)), false); !ok || ve.Final || ve.Text != "hel" {
		t.Fatalf("a provisional transcript is fanned, not final: %+v ok=%v", ve, ok)
	}
	if _, ok := voiceEventFor(decodeVoiceFrame(ev("transcript_final", `{"text":"secret"}`)), true); ok {
		t.Fatal("SAFE withholds a final transcript from the page")
	}
	if _, ok := voiceEventFor(decodeVoiceFrame(ev("transcript_partial", `{"text":"sec"}`)), true); ok {
		t.Fatal("SAFE withholds a provisional transcript from the page")
	}
	if ve, ok := voiceEventFor(decodeVoiceFrame(ev("failure", `{}`)), true); !ok || ve.Type != "failure" || ve.Reason == "" {
		t.Fatalf("a failure is fanned with a reason, even under SAFE: %+v ok=%v", ve, ok)
	}
	if ve, ok := voiceEventFor(decodeVoiceFrame(ev("turn_started", `{}`)), true); !ok || ve.Type != "turn_started" {
		t.Fatalf("an unknown/turn event passes through generically, even under SAFE: %+v ok=%v", ve, ok)
	}
}
