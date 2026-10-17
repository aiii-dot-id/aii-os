package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestTheHostsOwnStopReachesThePageScopedToItsStream(t *testing.T) {
	const fenced = "v-syn-1"
	cases := []struct {
		name  string
		typ   string
		event map[string]any
		want  string
	}{
		{"the stop_playback echo of the reply the host fenced", "interruption_requested",
			map[string]any{"synthesis_id": fenced, "reason": "stop_playback", "output_stream": 3},
			`{"session_id":"v","type":"interruption_requested","stream":3}`},
		{"the cancel_synthesis echo of the reply the host fenced", "interruption_requested",
			map[string]any{"synthesis_id": fenced, "reason": "cancel_synthesis", "output_stream": 3},
			`{"session_id":"v","type":"interruption_requested","stream":3}`},
		{"the engine's VAD fence: the operator speaking", "interruption_requested",
			map[string]any{"synthesis_id": fenced, "reason": "vad_speech", "output_stream": 3},
			`{"session_id":"v","type":"interruption_requested"}`},
		{"an interruption naming no reason", "interruption_requested",
			map[string]any{"synthesis_id": fenced, "output_stream": 3},
			`{"session_id":"v","type":"interruption_requested"}`},
		{"a stop this host never sent", "interruption_requested",
			map[string]any{"synthesis_id": "v-syn-never-fenced", "reason": "stop_playback", "output_stream": 4},
			`{"session_id":"v","type":"interruption_requested"}`},
		{"speech_start", "speech_start",
			map[string]any{"start_sample": 4800, "end_sample": 4800, "output_stream": 3},
			`{"session_id":"v","type":"speech_start"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &App{cfg: &Config{}}
			f := &fakeEngineSession{}
			h := &voiceHandle{id: "v", v: f}
			h.hush = func(string) {}
			a.voiceSessions.Store(h.id, h)
			defer a.bgWG.Wait()
			// The host's own fence of the reply a newer one replaced.
			if err := h.fence(context.Background(), fenced, "a newer reply took its place"); err != nil {
				t.Fatal(err)
			}
			var page []dashboard.VoiceEvent
			a.voiceEngineEvent(nil, nativeEvent(c.typ, "v", c.event), func(ev dashboard.VoiceEvent) { page = append(page, ev) })
			if len(page) != 1 {
				t.Fatalf("the page was handed %d events, want 1: %+v", len(page), page)
			}
			got, err := json.Marshal(page[0])
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("the page receives %s, want %s", got, c.want)
			}
		})
	}
}
