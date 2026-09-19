// .
// .
// .
// .
// .
// .
// .

package pluginhost

import (
	"context"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
func TestAuditEarlyAnnouncementsCannotBypassQueueBudget(t *testing.T) {
	h := auditNewAudioHarness(t)
	sink := auditNewHeldSink()
	h.open(t, "s", sink)
	h.name("s", 1)
	ownFrame(t, h, 0, 1)
	auditAudioJoin(t, "speaker blocked", sink.started)
	stop := make(chan struct{})
	produced := make(chan error, 1)
	waitPending := func(want int) bool {
		for ownWaiting(h.v.pair) != want {
			select {
			case <-stop:
				return false
			case <-time.After(time.Millisecond):
			}
		}
		return true
	}
	go func() {
		for batch := uint32(0); batch < 12; batch++ {
			stream := 100 + batch
			for i := uint32(0); i < 63; i++ {
				if err := h.frame(i, stream); err != nil {
					produced <- err
					return
				}
			}
			if err := audio.WriteFrame(h.audioOut, audio.Frame{Kind: audio.KindEnd, Stream: stream, Seq: 63, Start: 63}); err != nil {
				produced <- err
				return
			}
			if !waitPending(64) {
				produced <- nil
				return
			}
			h.name("s", stream)
			if !waitPending(0) {
				produced <- nil
				return
			}
		}
		produced <- nil
	}()
	finished := false
	defer func() {
		close(stop)
		_ = h.audioOut.Close()
		if !finished {
			select {
			case <-produced:
			case <-time.After(3 * time.Second):
				t.Error("producer did not join")
			}
		}
	}()
	// .
	select {
	case err := <-produced:
		finished = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
	}
	// .
	controlCtx, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	if _, err := h.v.Status(controlCtx); err != nil {
		t.Errorf("status stalled behind audio: %v", err)
	}

	got := auditQueued(h.v.pair)
	t.Logf("queued=%d, pairBuffer=%d, pendingFrames=%d, unnamed=%d, stray=%d", got, pairBuffer, pendingFrames, h.v.Unnamed(), h.v.Stray())
	if got > pairBuffer+pendingFrames {
		t.Fatalf("bounded staging repeatedly bypassed backpressure: queued=%d exceeds combined budget=%d", got, pairBuffer+pendingFrames)
	}
}

// .
// .
func TestAuditEarlyAudioAfterENDCannotReachSpeaker(t *testing.T) {
	h := auditNewAudioHarness(t)
	sink := audio.NewCaptureSink(mono16k)
	h.open(t, "s", sink)
	ownFrame(t, h, 0, 5)
	if err := audio.WriteFrame(h.audioOut, audio.Frame{Kind: audio.KindEnd, Stream: 5, Seq: 1, Start: 1}); err != nil {
		t.Fatal(err)
	}
	ownFrame(t, h, 2, 5)
	auditAudioWait(t, "all early frames waiting", func() bool { return ownWaiting(h.v.pair) == 3 })
	h.name("s", 5)
	h.name("s", 6)
	ownFrame(t, h, 0, 6)
	auditAudioWait(t, "subsequent stream reaches speaker", func() bool { return len(ownHeard(sink, 6)) == 1 })
	for _, fr := range sink.Frames() {
		if fr.Stream == 5 && fr.Kind == audio.KindPCM && fr.Seq == 2 {
			t.Fatalf("speaker received PCM after stream END: stream=%d seq=%d, stray=%d", fr.Stream, fr.Seq, h.v.Stray())
		}
	}
	if got := h.v.Stray(); got != 1 {
		t.Fatalf("post-END frame must be counted: stray=%d", got)
	}
}
