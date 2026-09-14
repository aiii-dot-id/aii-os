package app

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
// .
// .
// .
func TestSessionReadyReportsTheSettingsInEffect(t *testing.T) {
	a := newVoiceApp(t)
	done := make(chan struct{})
	a.voiceSessions.Store("vs-1", &voiceHandle{id: "vs-1", v: &fakeEngineSession{}, b: &audio.Binding{InputHandle: "in-vs-1", Contained: true}, done: done, drained: make(chan struct{})})
	a.voiceModes.Store("vs-1", true)
	ready := func(id string, models map[string]any) pluginhost.Event {
		body := map[string]any{"type": "session_ready", "session_id": id, "sequence": 1}
		if models != nil {
			body["models"] = models
		}
		raw, _ := json.Marshal(body)
		return pluginhost.Event{Type: "session_ready", SessionID: id, Raw: raw}
	}
	var fanned []string
	sink := func(ev dashboard.VoiceEvent) { fanned = append(fanned, ev.Type) }

	a.voiceEngineEvent(nil, ready("vs-1", map[string]any{"models_loaded": 4}), sink)
	if got := a.appliedVoiceSettings(); got != nil {
		t.Fatalf("a session_ready without operator_settings reports nothing: %v", got)
	}
	a.voiceEngineEvent(nil, ready("vs-1", map[string]any{"models_loaded": 4, "operator_settings": map[string]any{"stt_language": "de-DE", "tts_top_k": 40, "tts_temperature": 0.9}}), sink)
	got := a.appliedVoiceSettings()
	if len(got) != 1 || got["vs-1"]["stt_language"] != "de-DE" || got["vs-1"]["tts_top_k"] != 40.0 || got["vs-1"]["tts_temperature"] != 0.9 {
		t.Fatalf("the session's applied values, by session id: %v", got)
	}
	if len(fanned) != 2 || fanned[0] != "session_ready" {
		t.Fatalf("session_ready is fanned to the page as evidence like any other event: %v", fanned)
	}
	a.voiceEngineEvent(nil, ready("vs-9", map[string]any{"operator_settings": map[string]any{"stt_language": "fr-FR"}}), sink)
	if got := a.appliedVoiceSettings(); len(got) != 1 {
		t.Fatalf("a session the host does not hold is ignored: %v", got)
	}
	got["vs-1"]["stt_language"] = "mutated"
	if a.appliedVoiceSettings()["vs-1"]["stt_language"] != "de-DE" {
		t.Fatal("the view gets a copy, never the session's own map")
	}
	close(done)
	if got := a.appliedVoiceSettings(); got != nil {
		t.Fatalf("an ended session reports nothing: %v", got)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestASessionReadyThatOutrunsItsHandleIsStillTheSessionsWord(t *testing.T) {
	a := newVoiceApp(t)
	sink := func(dashboard.VoiceEvent) {}
	ready := func(id, lang string) pluginhost.Event {
		raw, _ := json.Marshal(map[string]any{"type": "session_ready", "session_id": id, "sequence": 1,
			"models": map[string]any{"models_loaded": 5, "operator_settings": map[string]any{"stt_language": lang}}})
		return pluginhost.Event{Type: "session_ready", SessionID: id, Raw: raw}
	}
	register := func(id string) chan struct{} {
		done := make(chan struct{})
		h := &voiceHandle{id: id, v: &fakeEngineSession{}, b: &audio.Binding{InputHandle: "in-" + id, Contained: true}, done: done, drained: make(chan struct{})}
		a.voiceSessions.Store(id, h)
		a.adoptPendingSettings(h)
		return done
	}

	// .
	// .
	a.voiceModes.Store("vs-1", true)
	a.voiceEngineEvent(nil, ready("vs-1", "de-DE"), sink)
	if got := a.appliedVoiceSettings(); got != nil {
		t.Fatalf("nothing is reported for a session with no handle yet: %v", got)
	}
	done := register("vs-1")
	if got := a.appliedVoiceSettings(); len(got) != 1 || got["vs-1"]["stt_language"] != "de-DE" {
		t.Fatalf("the handle adopts the readback that preceded it: %v", got)
	}

	// .
	a.voiceEngineEvent(nil, ready("vs-99", "fr-FR"), sink)
	if _, held := a.voicePending.Load("vs-99"); held {
		t.Fatal("a readback for a session this host never opened is dropped, never accumulated")
	}

	// .
	close(done)
	a.voiceSessions.Delete("vs-1")
	a.voiceModes.Delete("vs-1")
	a.voicePending.Delete("vs-1")
	if got := a.appliedVoiceSettings(); got != nil {
		t.Fatalf("an ended session reports nothing: %v", got)
	}
}

// .
// .
// .
func TestTheReadbackAndItsHandleLandInEitherOrder(t *testing.T) {
	a := newVoiceApp(t)
	sink := func(dashboard.VoiceEvent) {}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("vs-r%d", i)
		raw, _ := json.Marshal(map[string]any{"type": "session_ready", "session_id": id, "sequence": 1,
			"models": map[string]any{"operator_settings": map[string]any{"stt_language": "en-US"}}})
		ev := pluginhost.Event{Type: "session_ready", SessionID: id, Raw: raw}
		a.voiceModes.Store(id, true)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); a.voiceEngineEvent(nil, ev, sink) }()
		go func() {
			defer wg.Done()
			h := &voiceHandle{id: id, v: &fakeEngineSession{}, b: &audio.Binding{InputHandle: "in-" + id, Contained: true}, done: make(chan struct{}), drained: make(chan struct{})}
			a.voiceSessions.Store(id, h)
			a.adoptPendingSettings(h)
		}()
		wg.Wait()
		if got := a.appliedVoiceSettings(); got[id]["stt_language"] != "en-US" {
			t.Fatalf("round %d: whichever landed first, the session reports the engine's word: %v", i, got)
		}
	}
}
