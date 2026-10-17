package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// Characterization, not a new approval of legacy evidence: exact modern
// evidence removes an ignored row; missing or mismatched segment evidence
// does not acquire permission to erase a possibly different speaker's words.
func TestF011QualificationKeepsTheExactEvidenceBoundary(t *testing.T) {
	for _, kind := range []string{"legacy missing span", "exact span", "wrong track"} {
		t.Run(kind, func(t *testing.T) {
			a := newVoiceApp(t)
			a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}}
			v := dashboard.VoiceEvent{}
			start, end := int64(0), int64(16000)
			body := map[string]any{"refers_to": 1, "speaker_id": "visitor", "decision": "known", "revision": 1}
			if kind != "legacy missing span" {
				v.TrackID, v.StartSample, v.EndSample = "track-a", &start, &end
				body["track_id"], body["start_sample"], body["end_sample"] = "track-a", start, end
				if kind == "wrong track" {
					body["track_id"] = "another-track"
				}
			}
			a.retainHeard("qualification", 1, "earlier unresolved speech", v, "withheld", "pending", nil)
			a.amendHeard(decodeVoiceFrame(observationRaw("qualification", 2, body)))
			rows := historyRows(t, a, "earlier unresolved speech")
			want := 1
			if kind == "exact span" {
				want = 0
			}
			if len(rows) != want {
				t.Fatalf("%s: retained %d rows, want %d under the current exact-evidence rule", kind, len(rows), want)
			}
		})
	}
}
