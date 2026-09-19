package dashboard

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

// .
// .
// .
func TestASaveRefreshesTheStatusThePageReadsTheMicrophoneFrom(t *testing.T) {
	var state atomic.Value
	state.Store("setup")
	h := &WSHandler{
		GetStats:      func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		VoiceStatus:   func() (string, string, string) { return state.Load().(string), "", "" },
		HearUtterance: func(context.Context, []byte, int, int, bool) error { return nil },
		SetConfig: func(changes map[string]interface{}) (*ConfigState, error) {
			for k := range changes {
				if strings.HasPrefix(k, "llm.") {
					state.Store("unreachable")
					return &ConfigState{}, nil
				}
			}
			state.Store("cloud")
			return &ConfigState{}, nil
		},
		SetSpeechService: func(string, string, string) error { state.Store("setup"); return nil },
		GetProviders:     func() ProviderDirectory { return ProviderDirectory{} },
		GetConfig:        func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	if m := drainUntil(t, conn, "status"); m.Stats.VoiceState != "setup" {
		t.Fatalf("connect status %+v", m.Stats)
	}

	for _, tc := range []struct {
		why  string
		msg  ClientMessage
		want string
	}{
		{"a setting saved", ClientMessage{RequestID: "c1", Type: "config_set", Config: map[string]interface{}{"speech.stt.provider": "Cartesia"}}, "cloud"},
		{"a substrate change, checked off the read loop", ClientMessage{RequestID: "c2", Type: "config_set", Config: map[string]interface{}{"llm.provider": "Other"}}, "unreachable"},
		{"a speech service's key", ClientMessage{RequestID: "p1", Type: "speech_service", Provider: "Cartesia", APIKey: "k"}, "setup"},
	} {
		sendMsg(t, conn, tc.msg)
		if m := drainUntil(t, conn, "status"); m.Stats.VoiceState != tc.want {
			t.Errorf("%s: the page was left with voice_state %q, want %q", tc.why, m.Stats.VoiceState, tc.want)
		}
	}
}
