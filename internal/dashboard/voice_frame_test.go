package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// .
// .

func refuses(t *testing.T, name string, frame []byte) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		h := newVoiceHarness()
		s := New("127.0.0.1", 0, h.handler())
		addr, _ := s.Start(t.TempDir())
		defer s.Shutdown(context.Background())

		conn := dialWS(t, addr)
		if err := conn.Write(context.Background(), websocket.MessageBinary, frame); err != nil {
			t.Fatalf("write: %v", err)
		}
		select {
		case <-h.heard:
			t.Fatal("the frame was passed to the host as audio")
		case <-time.After(300 * time.Millisecond):
		}
	})
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestAVoiceFrameWithAReservedByteSetIsRefused(t *testing.T) {
	frame := voiceFrame(voiceFrameVersion, 1, 16000, 100)
	frame[3] = 1
	refuses(t, "reserved byte 3", frame)
}

// .
// .
// .
func TestAVoiceFrameWithAnUnknownModeIsRefused(t *testing.T) {
	frame := voiceFrame(voiceFrameVersion, 1, 16000, 100)
	frame[2] = 2
	refuses(t, "mode 2", frame)
}

// .
// .
// .
// .
func TestTheVoiceModeByteReachesTheHost(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode byte
		want bool
	}{
		{"meeting", 0, false},
		{"conversation", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newVoiceHarness()
			s := New("127.0.0.1", 0, h.handler())
			addr, _ := s.Start(t.TempDir())
			defer s.Shutdown(context.Background())

			conn := dialWS(t, addr)
			frame := voiceFrame(voiceFrameVersion, 1, 16000, 100)
			frame[2] = tc.mode
			if err := conn.Write(context.Background(), websocket.MessageBinary, frame); err != nil {
				t.Fatalf("write: %v", err)
			}
			select {
			case <-h.heard:
				h.mu.Lock()
				got := h.answer
				h.mu.Unlock()
				if got != tc.want {
					t.Fatalf("mode byte %d arrived as answer=%v, want %v — the operator's choice did not reach the host",
						tc.mode, got, tc.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the frame never reached the host")
			}
		})
	}
}

// .
// .
// .
// .
func TestAVoiceFrameThatIsNotWholeSamplesIsRefused(t *testing.T) {
	odd := append(voiceFrame(voiceFrameVersion, 1, 16000, 100), 0x7f)
	refuses(t, "mono with a trailing byte", odd)

	// .
	// .
	stereo := append(voiceFrame(voiceFrameVersion, 2, 16000, 100), 0x00, 0x00)
	refuses(t, "stereo missing half a frame", stereo)
}

// .
// .
func TestAWellFormedStereoFrameIsStillAccepted(t *testing.T) {
	h := newVoiceHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	// .
	// .
	if err := conn.Write(context.Background(), websocket.MessageBinary,
		voiceFrame(voiceFrameVersion, 2, 48000, 200)); err != nil {
		t.Fatalf("write: %v", err)
	}
	h.waitHeard(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ch != 2 || h.rate != 48000 {
		t.Fatalf("stereo frame arrived as %d ch / %d Hz", h.ch, h.rate)
	}
}

// .
// .
// .
// .
func TestStatusCarriesTheUtteranceCeiling(t *testing.T) {
	h := newVoiceHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	got := waitForStatus(t, dialWS(t, addr))
	if !got.Voice {
		t.Fatal("fixture: voice should be configured here")
	}
	if got.VoiceMaxFrameBytes != maxVoiceFrameBytes {
		t.Fatalf("ceiling reported as %d, want %d — the page cannot stop where the server stops",
			got.VoiceMaxFrameBytes, maxVoiceFrameBytes)
	}

	// .
	h2 := newVoiceHarness()
	h2.present = false
	s2 := New("127.0.0.1", 0, h2.handler())
	addr2, _ := s2.Start(t.TempDir())
	defer s2.Shutdown(context.Background())
	if got := waitForStatus(t, dialWS(t, addr2)); got.VoiceMaxFrameBytes != 0 {
		t.Fatalf("an identity with no microphone stated a ceiling of %d", got.VoiceMaxFrameBytes)
	}
}
