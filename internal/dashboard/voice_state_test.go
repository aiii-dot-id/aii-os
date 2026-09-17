package dashboard

import (
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
// .
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
