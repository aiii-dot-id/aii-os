package app

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestPlaybackControlsReachTheirPageWhileTheFanIsSaturated(t *testing.T) {
	a := newVoiceApp(t)
	release := make(chan struct{})
	var mu sync.Mutex
	var fanned, controls []dashboard.VoiceEvent
	first := true
	a.voiceEventSink = func(ev dashboard.VoiceEvent) {
		mu.Lock()
		fanned = append(fanned, ev)
		block := first
		first = false
		mu.Unlock()
		if block {
			<-release // a page that has stopped reading stalls the broadcast
		}
	}
	a.voiceControlSink = func(ev dashboard.VoiceEvent) error {
		mu.Lock()
		controls = append(controls, ev)
		mu.Unlock()
		return nil
	}
	send, _ := observerWire(t, a)
	for i := int64(1); a.voiceFanDropped.Load() == 0; i++ {
		send("vad_probability", i) // replaceable telemetry, until the fan drops
	}
	send("speech_start", 1001)
	send("interruption_requested", 1002)
	send("synthesis_start", 1003)
	holdWait(t, "the three playback controls, with the fan still blocked", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(controls) == 3
	})
	mu.Lock()
	order := []string{controls[0].Type, controls[1].Type, controls[2].Type}
	mu.Unlock()
	if order[0] != "speech_start" || order[1] != "interruption_requested" || order[2] != "synthesis_start" {
		t.Fatalf("the controls arrived out of order: %v", order)
	}
	send("EOF", 0)
	close(release)
	holdWait(t, "the observer's terminal word", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(fanned) > 0 && (fanned[len(fanned)-1].Type == "closed" || fanned[len(fanned)-1].Type == "failed")
	})
	mu.Lock()
	defer mu.Unlock()
	for _, ev := range fanned {
		if playbackControl(ev.Type) {
			t.Errorf("a playback control also went through the lossy fan: %+v", ev)
		}
	}
	if a.voiceFanDropped.Load() == 0 {
		t.Error("telemetry was not lossy: the fan dropped nothing")
	}
}

// A control for a session no page holds has no audio to stop: it is offered
// to the pages (the dashboard counts it), and nothing waits.
func TestAPlaybackControlNoPageHoldsIsCounted(t *testing.T) {
	a := newVoiceApp(t)
	a.voiceEventSink = func(dashboard.VoiceEvent) {}
	var offered atomic.Int32
	a.voiceControlSink = func(dashboard.VoiceEvent) error { offered.Add(1); return dashboard.ErrNoVoiceHolder }
	send, _ := observerWire(t, a)
	send("speech_start", 1)
	holdWait(t, "the unheld control offered", func() bool { return offered.Load() == 1 })
	send("EOF", 0)
}
