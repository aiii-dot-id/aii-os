package dashboard

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// voice_frame_test.go — the header says what the payload is, so the
// header is checked before the payload is believed.

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

func TestAVoiceFrameWithAReservedByteSetIsRefused(t *testing.T) {
	frame := voiceFrame(voiceFrameVersion, 1, 16000, 100)
	frame[3] = 1
	refuses(t, "reserved byte 3", frame)
}

// AN UNKNOWN MODE IS REFUSED, NOT ROUNDED. A future page that means
// something by mode 2 must be told this host does not understand it,
// rather than have it quietly treated as a meeting.
func TestAVoiceFrameWithAnUnknownModeIsRefused(t *testing.T) {
	frame := voiceFrame(voiceFrameVersion, 1, 16000, 100)
	frame[2] = 2
	refuses(t, "mode 2", frame)
}

// THE MODE REACHES THE HOST, which is the whole point of the byte.
// Conversation mode was unreachable for its entire existence because
// the flag lived on a session nothing initialised; this pins the wire
// that replaced it.
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

// A WHOLE NUMBER OF FRAMES, OR IT IS NOT THE AUDIO IT CLAIMS TO BE.
// A trailing half-sample means the page truncated or the channel count
// is wrong, and every sample after the error is misaligned — the engine
// would transcribe the noise rather than report it.
func TestAVoiceFrameThatIsNotWholeSamplesIsRefused(t *testing.T) {
	odd := append(voiceFrame(voiceFrameVersion, 1, 16000, 100), 0x7f)
	refuses(t, "mono with a trailing byte", odd)

	// Stereo needs four bytes per frame, so two spare bytes is still
	// half a frame — the case a mono-shaped check would wave through.
	stereo := append(voiceFrame(voiceFrameVersion, 2, 16000, 100), 0x00, 0x00)
	refuses(t, "stereo missing half a frame", stereo)
}

// And a well-formed stereo frame still gets through, so the check above
// is rejecting misalignment rather than rejecting stereo.
func TestAWellFormedStereoFrameIsStillAccepted(t *testing.T) {
	h := newVoiceHarness()
	s := New("127.0.0.1", 0, h.handler())
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	// voiceFrame writes samples*2 bytes; for 2 channels that is 100
	// interleaved frames' worth only if the count is even.
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

// THE PAGE IS TOLD THE CEILING RATHER THAN LEFT TO DISCOVER IT. Past
// this many bytes the socket's read limit closes the connection, so a
// browser that did not know the number met it as a mystery disconnect
// mid-sentence with the words lost.
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

	// With no microphone there is no ceiling to state.
	h2 := newVoiceHarness()
	h2.present = false
	s2 := New("127.0.0.1", 0, h2.handler())
	addr2, _ := s2.Start(t.TempDir())
	defer s2.Shutdown(context.Background())
	if got := waitForStatus(t, dialWS(t, addr2)); got.VoiceMaxFrameBytes != 0 {
		t.Fatalf("an identity with no microphone stated a ceiling of %d", got.VoiceMaxFrameBytes)
	}
}
