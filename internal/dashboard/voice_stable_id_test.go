package dashboard

import (
	"context"
	"encoding/json"
	"testing"
)

func TestStableSpeakerIDCrossesSocket(t *testing.T) {
	s := New("127.0.0.1", 0, newVoiceHarness().handler())
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	// A socket the server has accepted is not yet in its connection set; a
	// broadcast in that window never reaches it and the read below waits
	// out its deadline (one gate run in six under browser-test load).
	waitForConns(t, s, 1)
	for _, id := range []string{"quinn-one", "quinn-two", ""} {
		// Use JSON so the identical test compiles against a host without
		// the new field and proves that the real wire loses the ID there.
		raw, _ := json.Marshal(map[string]any{"type": "speaker_observation", "session_id": "uid-wire", "sequence": 12,
			"refers_to": 7, "speaker": "Quinn", "speaker_id": id})
		var ev VoiceEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		s.BroadcastVoiceEvent(ev)
		got := drainUntil(t, conn, "voice_event")
		wire, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var frame struct {
			Event map[string]any `json:"voice_event"`
		}
		if err := json.Unmarshal(wire, &frame); err != nil {
			t.Fatal(err)
		}
		gotID, _ := frame.Event["speaker_id"].(string)
		if gotID != id || frame.Event["session_id"] != "uid-wire" || frame.Event["refers_to"] != float64(7) {
			t.Fatalf("socket lost identity or attribution reference: %s; want ID %q", wire, id)
		}
	}
}
