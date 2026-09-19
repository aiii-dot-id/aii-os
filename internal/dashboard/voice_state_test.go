package dashboard

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
// .
// .
// .
func TestTheStatusCarriesTheVoiceModeInForce(t *testing.T) {
	stats := func() (*StatsResponse, error) { return &StatsResponse{}, nil }
	h := &WSHandler{GetStats: stats, VoiceMode: func() (string, string, uint64) { return "meeting", "off", 3 }}
	msg, ok := New("127.0.0.1", 0, h).statusMessage(h)
	if !ok {
		t.Fatal("no status")
	}
	if msg.Stats.VoiceListen != "meeting" || msg.Stats.VoiceSpeak != "off" || msg.Stats.VoiceModeRevision != 3 {
		t.Fatalf("the frame does not carry the mode: listen=%q speak=%q rev=%d", msg.Stats.VoiceListen, msg.Stats.VoiceSpeak, msg.Stats.VoiceModeRevision)
	}
	none := &WSHandler{GetStats: stats}
	msg, ok = New("127.0.0.1", 0, none).statusMessage(none)
	if !ok || msg.Stats.VoiceListen != "" || msg.Stats.VoiceSpeak != "" {
		t.Fatalf("a server with no mode hook invented one: %+v", msg.Stats)
	}
}

func TestTheStatusNamesOneMicrophoneThisServerCanServe(t *testing.T) {
	for _, tc := range []struct {
		why, host, want string
		engine, ears    bool
	}{
		{"nothing set up", "setup", "setup", false, true},
		{"a cloud service", "cloud", "cloud", false, true},
		{"a cloud service with no door for utterances", "cloud", "setup", false, false},
		{"a plugin conversation", "plugin", "plugin", true, true},
		{"a plugin this server cannot open", "plugin", "setup", false, true},
		{"SAFE", "safe", "safe", true, true},
	} {
		h := &WSHandler{
			GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
			VoiceStatus: func() (string, string, string) { return tc.host, "why", "what listens" },
		}
		if tc.ears {
			h.HearUtterance = func(context.Context, []byte, int, int, bool) error { return nil }
		}
		if tc.engine {
			h.VoiceEngine = func() bool { return true }
			h.AudioPlane = func() *audio.Plane { return nil }
			h.VoiceSessionOpen = func(context.Context, string, string, string) (VoiceSession, error) { return nil, nil }
		}
		msg, ok := New("127.0.0.1", 0, h).statusMessage(h)
		if !ok {
			t.Fatalf("%s: no status", tc.why)
		}
		if got := msg.Stats.VoiceState; got != tc.want {
			t.Errorf("%s: voice_state %q, want %q", tc.why, got, tc.want)
		}
		if tc.want == "setup" && tc.host != "setup" && (msg.Stats.VoiceSource != "" || msg.Stats.VoiceReason != "") {
			t.Errorf("%s: a microphone this server cannot serve kept its source %q", tc.why, msg.Stats.VoiceSource)
		}
	}
}
