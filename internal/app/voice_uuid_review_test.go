package app

import (
	"encoding/json"
	"testing"
)

// .
// .
func TestReviewUUIDMetadataSurvivesPageMapping(t *testing.T) {
	e := observationRaw("review", 2, map[string]any{"refers_to": 1, "track_id": "a",
		"start_sample": 0, "end_sample": 16000, "revision": 1, "decision": "uncertain",
		"speaker_uuid": "76d3a1b4-4df8-421c-a08f-821912ff103a", "registry_revision": "1", "continuity": "matched"})
	ve, ok := voiceEventFor(e, false)
	if !ok {
		t.Fatal("observation disappeared")
	}
	raw, err := json.Marshal(ve)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err = json.Unmarshal(raw, &row); err != nil {
		t.Fatal(err)
	}
	if row["speaker_uuid"] != "76d3a1b4-4df8-421c-a08f-821912ff103a" || row["track_id"] != "a" {
		t.Fatalf("speaker UUID/segment discarded: %s", raw)
	}
}
