package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

const uuidA = "76d3a1b4-4df8-421c-a08f-821912ff103a"
const uuidB = "9078c44a-6fbe-4f24-a20e-bd212f520530"

// .
// .
func TestTimestampedFinalDoesNotRequireSpeakerTracks(t *testing.T) {
	for _, start := range []bool{false, true} {
		a := newVoiceApp(t)
		h, _ := newTrackedSession(a, "timestamped", false)
		page := &pageLog{}
		ev := finalRaw(h.id, 1, "ordinary timestamped words")
		var body map[string]any
		_ = json.Unmarshal(ev.Raw, &body)
		body["end_sample"] = 16000
		if start {
			body["start_sample"] = 0
		}
		ev.Raw, _ = json.Marshal(body)
		a.voiceEngineEvent(nil, ev, page.enqueue)
		a.voiceInputFinished(h.id)
		awaitDrained(t, h)
		if page.find("transcript_final", 1) == nil || page.find("transcript_withheld", 1) != nil {
			t.Fatal("ordinary timestamps became mandatory speaker segmentation")
		}
		if _, ok, err := a.store.TurnSeqByAnnotation(annotationVoice, voiceRefKey(h.id, 1)); err != nil || !ok {
			t.Fatalf("ordinary timestamped final not recorded: %v", err)
		}
	}
}

func TestEmptyDeclaredSpeakerTrackCannotBecomeOrdinarySpeech(t *testing.T) {
	for _, track := range []any{nil, ""} {
		a := newVoiceApp(t)
		h, _ := newTrackedSession(a, "bad-track", false)
		page := &pageLog{}
		ev := finalRaw(h.id, 1, "invalid track words")
		var body map[string]any
		_ = json.Unmarshal(ev.Raw, &body)
		body["track_id"], body["start_sample"], body["end_sample"] = track, 0, 16000
		ev.Raw, _ = json.Marshal(body)
		a.voiceEngineEvent(nil, ev, page.enqueue)
		if page.find("transcript_final", 1) != nil || page.find("transcript_withheld", 1) == nil {
			t.Fatal("invalid explicit track silently became ordinary speech")
		}
		a.voiceInputFinished(h.id)
		awaitDrained(t, h)
	}
}

func uuidFinal(session string, seq int64, track, text string) pluginhost.Event {
	ev := finalRaw(session, seq, text)
	var b map[string]any
	_ = json.Unmarshal(ev.Raw, &b)
	b["track_id"], b["start_sample"], b["end_sample"] = track, 0, 16000
	ev.Raw, _ = json.Marshal(b)
	return ev
}
func uuidObservation(session string, seq, refers int64, track, id string) pluginhost.Event {
	return observationRaw(session, seq, map[string]any{"refers_to": refers,
		"track_id": track, "start_sample": 0, "end_sample": 16000, "revision": 1,
		"decision": "uncertain", "speaker_id": "", "speaker_uuid": id,
		"registry_revision": "9", "continuity": "matched", "display_label": "Same label"})
}

// .
// .
func TestSpeakerUUIDPolicyFiltersBeforeDelivery(t *testing.T) {
	for _, mode := range []string{"only", "ignore"} {
		t.Run(mode, func(t *testing.T) {
			a := newVoiceApp(t)
			a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: mode, UIDs: []string{uuidA}, Unidentified: "withhold"}
			h, _ := newTrackedSession(a, "uuid-policy", false)
			page := &pageLog{}
			a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "alpha words"), page.enqueue)
			a.voiceEngineEvent(nil, uuidFinal(h.id, 8, "track-b", "beta words"), page.enqueue)
			if page.find("transcript_final", 7) != nil || page.find("transcript_final", 8) != nil {
				t.Fatal("unattributed words escaped")
			}
			a.voiceEngineEvent(nil, uuidObservation(h.id, 20, 8, "track-b", uuidB), page.enqueue)
			a.voiceEngineEvent(nil, uuidObservation(h.id, 21, 7, "track-a", uuidA), page.enqueue)
			a.voiceInputFinished(h.id)
			awaitDrained(t, h)
			allowed, denied, id := int64(7), int64(8), uuidA
			if mode == "ignore" {
				allowed, denied, id = 8, 7, uuidB
			}
			if page.find("transcript_final", allowed) == nil || page.find("transcript_final", denied) != nil {
				t.Fatal("UUID filtering failed")
			}
			seq, ok, err := a.store.TurnSeqByAnnotation(annotationVoice, voiceRefKey(h.id, allowed))
			if err != nil || !ok {
				t.Fatalf("allowed final not recorded: %v", err)
			}
			annotations, err := a.store.TurnAnnotations(annotationSpeaker, []uint64{seq})
			if err != nil || !strings.Contains(annotations[seq], id) {
				t.Fatalf("UUID annotation lost: %v %v", annotations, err)
			}
			got := a.attributeSpoken(seq, voiceMarker+"words")
			if !strings.Contains(got, "speaker_uuid=\""+id+"\"") || !strings.Contains(got, "not authentication") {
				t.Fatalf("prompt lost evidence limits: %s", got)
			}
			if _, ok, _ := a.store.TurnSeqByAnnotation(annotationVoice, voiceRefKey(h.id, denied)); ok {
				t.Fatal("excluded final recorded")
			}
		})
	}
}

func TestSpeakerUUIDExactFinalAndFailClosed(t *testing.T) {
	for _, bad := range []string{"session", "track", "span", "missing-track", "uuid", "revision", "provisional"} {
		t.Run(bad, func(t *testing.T) {
			a := newVoiceApp(t)
			a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{uuidA}}
			h, _ := newTrackedSession(a, "uuid-reject", false)
			page := &pageLog{}
			a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "do not release"), page.enqueue)
			ev := uuidObservation(h.id, 20, 7, "track-a", uuidA)
			var b map[string]any
			_ = json.Unmarshal(ev.Raw, &b)
			switch bad {
			case "session":
				ev.SessionID = "another-session"
			case "track":
				b["track_id"] = "track-b"
			case "span":
				b["end_sample"] = 16001
			case "missing-track":
				delete(b, "track_id")
			case "uuid":
				b["speaker_uuid"] = "not-a-uuid"
			case "revision":
				b["registry_revision"] = "0"
			case "provisional":
				b["continuity"] = "provisional"
			}
			ev.Raw, _ = json.Marshal(b)
			a.voiceEngineEvent(nil, ev, page.enqueue)
			a.decideHeld(h, 7, "", "", true, nil)
			a.voiceInputFinished(h.id)
			awaitDrained(t, h)
			if page.find("transcript_final", 7) != nil {
				t.Fatal("unresolved or wrong final passed allow-list")
			}
		})
	}
}

func TestSpeakerUUIDAllWaitsAndStaleUpdateCannotRelabel(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "uuid-all", false)
	page := &pageLog{}
	a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "kept separate"), page.enqueue)
	if page.find("transcript_final", 7) != nil {
		t.Fatal("segmented final delivered without its attribution")
	}
	a.voiceEngineEvent(nil, uuidObservation(h.id, 20, 7, "track-a", uuidA), page.enqueue)
	h.work.Wait()
	before := len(page.types())
	a.voiceEngineEvent(nil, uuidObservation(h.id, 21, 7, "track-a", uuidB), page.enqueue)
	if len(page.types()) != before {
		t.Fatal("same attribution revision relabelled a final")
	}
	seq, ok, _ := a.store.TurnSeqByAnnotation(annotationVoice, voiceRefKey(h.id, 7))
	if !ok || !strings.Contains(a.attributeSpoken(seq, voiceMarker+"text"), uuidA) {
		t.Fatal("stored UUID lost")
	}
	// .
	a.voiceEngineEvent(nil, observationRaw(h.id, 22, map[string]any{"refers_to": 7, "decision": "known", "speaker_id": "other"}), page.enqueue)
	if len(page.types()) != before {
		t.Fatal("unsegmented update bypassed the exact-span join")
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
}

func TestSpeakerUUIDPageAndSafe(t *testing.T) {
	ev := uuidObservation("uuid-page", 2, 1, "track-a", uuidA)
	ve, ok := voiceEventFor(ev, false)
	if !ok {
		t.Fatal("missing page event")
	}
	wire, _ := json.Marshal(ve)
	var b map[string]any
	_ = json.Unmarshal(wire, &b)
	if b["speaker_uuid"] != uuidA || b["registry_revision"] != "9" || b["continuity"] != "matched" || ve.Operator {
		t.Fatalf("page metadata differs: %s", wire)
	}
	if _, ok := voiceEventFor(ev, true); ok {
		t.Fatal("SAFE leaked UUID")
	}
}

func TestSpeakerUUIDTimeoutAndMalformedFinalAreExplicit(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "uuid-timeout", false)
	page := &pageLog{}
	a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "unresolved words"), page.enqueue)
	a.decideHeld(h, 7, "", "", true, nil)
	h.work.Wait()
	ve := page.find("transcript_final", 7)
	if ve == nil || !strings.Contains(ve.Attribution, "host attribution wait expired") {
		t.Fatal("timeout delivered bare speech")
	}
	seq, ok, _ := a.store.TurnSeqByAnnotation(annotationVoice, voiceRefKey(h.id, 7))
	if !ok || !strings.Contains(a.attributeSpoken(seq, voiceMarker+"words"), "host attribution wait expired") {
		t.Fatal("record hid missing attribution")
	}
	// .
	a.voiceEngineEvent(nil, uuidObservation(h.id, 20, 7, "track-a", uuidA), page.enqueue)
	if !strings.Contains(a.attributeSpoken(seq, voiceMarker+"words"), uuidA) {
		t.Fatal("late evidence not joined")
	}
	ev := uuidFinal(h.id, 8, "track-a", "malformed must not escape")
	var body map[string]any
	_ = json.Unmarshal(ev.Raw, &body)
	delete(body, "end_sample")
	ev.Raw, _ = json.Marshal(body)
	a.voiceEngineEvent(nil, ev, page.enqueue)
	if page.find("transcript_final", 8) != nil || page.find("transcript_withheld", 8) == nil {
		t.Fatal("partial segment downgraded to unrestricted legacy final")
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
}

func TestSpeakerUUIDIsPresentAtTheLiveSteeringBoundary(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "uuid-live", true)
	page := &pageLog{}
	entered, release := make(chan struct{}), make(chan struct{})
	prior := voiceWake
	voiceWake = func(*App, context.Context, string, string) (string, error) { close(entered); <-release; return "", nil }
	t.Cleanup(func() { close(release); h.work.Wait(); a.settleVoice(context.Background(), ""); voiceWake = prior })
	a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "first words"), page.enqueue)
	a.voiceEngineEvent(nil, uuidObservation(h.id, 20, 7, "track-a", uuidA), page.enqueue)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first turn never admitted")
	}
	a.voiceEngineEvent(nil, uuidFinal(h.id, 21, "track-b", "second voice words"), page.enqueue)
	a.voiceEngineEvent(nil, uuidObservation(h.id, 22, 21, "track-b", uuidB), page.enqueue)
	holdWait(t, "UUID steer", func() bool { a.turnMu.Lock(); defer a.turnMu.Unlock(); return len(a.steers) == 1 })
	said := a.DrainSteering()
	if len(said) != 1 || !strings.Contains(said[0], uuidB) || strings.Contains(said[0], uuidA) || !strings.Contains(said[0], "second voice words") {
		t.Fatalf("live model input lost exact speaker: %q", said)
	}
	if err := h.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
}
