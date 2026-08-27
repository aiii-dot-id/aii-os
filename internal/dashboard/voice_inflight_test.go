package dashboard

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// voice_inflight_test.go — two utterances cannot be in the air at once.
//
// handleVoiceFrame launched one goroutine per frame and bounded nothing.
// Memory was the smaller half of that: each holds up to
// maxVoiceFrameBytes. The larger half is that transcription takes as
// long as it takes, so the SECOND thing said can reach AdmitParticipant
// before the first — a conversation stored out of the order it happened
// in, which no later reader can detect or repair.

// waitVoiceFree blocks until the in-flight slot comes back. Reaching
// into the field is deliberate: the release is a defer inside a
// goroutine, so a test that inferred it from the outside would be
// racing the thing it is trying to prove.
func waitVoiceFree(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.voiceMu.Lock()
		busy := s.voiceBusy
		s.voiceMu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("THE IN-FLIGHT SLOT WAS NEVER RELEASED — the identity has gone permanently deaf")
}

// SPEAK TWICE, QUICKLY. The second must be refused rather than run
// alongside the first, and the operator must be told which of the two
// things happened.
func TestASecondUtteranceIsRefusedWhileTheFirstIsInFlight(t *testing.T) {
	h := newVoiceHarness()
	block := make(chan struct{})
	started := make(chan struct{})
	var calls int32

	hd := h.handler()
	hd.HearUtterance = func(context.Context, []byte, int, int) error {
		if atomic.AddInt32(&calls, 1) == 1 {
			close(started)
		}
		<-block
		return nil
	}
	s := New("127.0.0.1", 0, hd)
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())
	defer close(block)

	conn := dialWS(t, addr)
	if err := conn.Write(context.Background(), websocket.MessageBinary,
		voiceFrame(voiceFrameVersion, 1, 48000, 800)); err != nil {
		t.Fatalf("write first: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first utterance never reached the host")
	}

	// With the first still inside the endpoint, speak again.
	if err := conn.Write(context.Background(), websocket.MessageBinary,
		voiceFrame(voiceFrameVersion, 1, 48000, 800)); err != nil {
		t.Fatalf("write second: %v", err)
	}

	var told bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !told {
		if m := readMsgOrNil(conn); m != nil && m.Type == "error" &&
			strings.Contains(m.Message, "still hearing") {
			told = true
		}
	}
	if !told {
		t.Fatal("the second utterance was neither run nor refused — the operator was told nothing")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("TWO UTTERANCES WERE IN FLIGHT AT ONCE (%d) — they can land in the record out of the order they were spoken", n)
	}
}

// AND THE SLOT COMES BACK.
//
// A bound that never releases is an identity that goes deaf after one
// sentence — and from the operator's side that is indistinguishable from
// an endpoint that has stopped answering, which is the worst kind of bug
// to ship behind a refusal message that sounds reasonable.
func TestTheMicrophoneIsFreeAgainAfterAnUtteranceLands(t *testing.T) {
	h := newVoiceHarness()
	var calls int32
	done := make(chan struct{}, 4)

	hd := h.handler()
	hd.HearUtterance = func(context.Context, []byte, int, int) error {
		atomic.AddInt32(&calls, 1)
		done <- struct{}{}
		return nil
	}
	s := New("127.0.0.1", 0, hd)
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	for i := 0; i < 3; i++ {
		if err := conn.Write(context.Background(), websocket.MessageBinary,
			voiceFrame(voiceFrameVersion, 1, 48000, 400)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("utterance %d never reached the host", i)
		}
		waitVoiceFree(t, s)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("three utterances spoken in sequence, %d heard", n)
	}
}
