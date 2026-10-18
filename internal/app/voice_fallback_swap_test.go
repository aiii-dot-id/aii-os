package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestTheSpeakOffCommitHushesTheFallbackWhileATakeoverHoldsAdmit(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	h := &voiceHandle{id: "vs-swap", v: &fakeEngineSession{}, done: make(chan struct{})}
	h.hush = func(why string) { a.hushFallback(h, why) }
	a.voiceSessions.Store(h.id, h)
	var mu sync.Mutex
	var hushed []dashboard.VoiceHush
	a.voiceHushSink = func(v dashboard.VoiceHush) { mu.Lock(); hushed = append(hushed, v); mu.Unlock() }

	// The browser's own voice speaks the session's last reply.
	if !a.takeOver(h, h.gen.Load(), takenOver{route: "browser"}) {
		t.Fatal("the fixture's takeover was refused")
	}
	// The next reply's takeover parks inside its mint, holding admit.
	entered, release := make(chan struct{}), make(chan struct{})
	prev := voiceFallbackMint
	voiceFallbackMint = func(*App, string) string { close(entered); <-release; return "cloud-next" }
	t.Cleanup(func() { voiceFallbackMint = prev })
	took := make(chan bool, 1)
	go func() {
		took <- a.speakThrough(context.Background(), &voiceBinding{session: h.id, gen: h.gen.Load()}, "the next reply", true, "")
	}()
	awaitEntered(t, entered, "the takeover's mint")

	committed := make(chan error, 1)
	go func() {
		_, err := a.applyConfigChangeWith(map[string]any{
			"speech.mode": map[string]any{"listen": listenInteractive, "speak": speakOff},
		}, func(*Config) (bool, error) { return true, nil })
		committed <- err
	}()
	select {
	case err := <-committed:
		if err != nil {
			close(release)
			t.Fatalf("the door refused speak off: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the speak-off commit waited on the session's admit lock, held by a takeover's mint")
	}
	mu.Lock()
	got := append([]dashboard.VoiceHush(nil), hushed...)
	mu.Unlock()
	if len(got) != 1 || got[0].SessionID != h.id || got[0].Route != "browser" {
		t.Errorf("the off did not hush the voice speaking the session's last reply: %+v", got)
	}
	close(release)
	select {
	case <-took:
	case <-time.After(5 * time.Second):
		t.Fatal("the parked takeover never finished")
	}
}
