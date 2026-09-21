package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
func TestVoiceStableSpeakerIDSurvivesHost(t *testing.T) {
	for _, tc := range []struct{ name, id, label string }{
		{"first duplicate label", "sam-one", "Sam"},
		{"second duplicate label", "sam-two", "Sam"},
		{"renamed same identity", "sam-one", "Jim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newVoiceApp(t)
			done := make(chan struct{})
			defer close(done)
			a.voiceSessions.Store("stable", &voiceHandle{id: "stable", done: done})
			var fanned []dashboard.VoiceEvent
			ev := observationRaw("stable", 12, map[string]any{
				"refers_to": 7, "speaker": tc.label, "speaker_id": tc.id,
				"decision": "known", "score": 0.94, "used_for_permissions": false,
			})
			a.voiceEngineEvent(nil, ev, func(v dashboard.VoiceEvent) { fanned = append(fanned, v) })
			if len(fanned) != 1 {
				t.Fatalf("wanted one page observation: %v", fanned)
			}
			wire, err := json.Marshal(fanned[0])
			if err != nil {
				t.Fatal(err)
			}
			var page map[string]any
			if err := json.Unmarshal(wire, &page); err != nil {
				t.Fatal(err)
			}
			if page["speaker_id"] != tc.id || page["speaker"] != tc.label {
				t.Errorf("page lost the stable ID or confused it with a label: %s", wire)
			}
			if fanned[0].Operator {
				t.Error("speaker identification must not mint operator authority")
			}
			pending, ok := a.speakerPending.Load("stable/7")
			if !ok {
				t.Fatal("observation should be held for the forthcoming turn")
			}
			assertStableIDAnnotation(t, pending.(string), tc.id)
			b := &voiceBinding{session: "stable", seq: 7, text: "my opening words", done: func() {}}
			a.turnMu.Lock()
			a.steers = append(a.steers, steerEntry{role: roleOperator, content: voiceMarker + b.text, voice: b})
			a.turnMu.Unlock()
			said := a.DrainSteering()
			want := "[voice · " + tc.label + " (speaker_id=\"" + tc.id + "\")] my opening words"
			if len(said) != 1 || said[0] != want {
				t.Errorf("live identity lost the exact speaker ID: got %q want %q", said, want)
			}
			seq, exists, err := a.store.TurnSeqByAnnotation(annotationVoice, "stable/7")
			if err != nil || !exists {
				t.Fatalf("missing recorded voice reference: %v %v", exists, err)
			}
			ann, err := a.store.TurnAnnotations(annotationSpeaker, []uint64{seq})
			if err != nil {
				t.Fatal(err)
			}
			assertStableIDAnnotation(t, ann[seq], tc.id)
			history, _, err := a.buildHistory()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, turn := range history {
				if strings.Contains(turn.Content, "my opening words") {
					found = true
					if turn.Content != want || turn.Role != "user" {
						t.Errorf("history lost identity or changed the existing role: %+v", turn)
					}
				}
			}
			if !found {
				t.Fatal("recorded speech absent from history")
			}
			if n, _ := a.store.ConversationTurnCount(); n != 1 {
				t.Fatalf("observation invented a turn: %d", n)
			}

			// .
			// .
			for i, decision := range []string{"uncertain", "unknown"} {
				update := observationRaw("stable", int64(13+i), map[string]any{
					"refers_to": 7, "speaker": tc.label, "speaker_id": tc.id,
					"decision": decision, "late": true,
				})
				a.voiceEngineEvent(nil, update, func(v dashboard.VoiceEvent) { fanned = append(fanned, v) })
				wire, _ := json.Marshal(fanned[len(fanned)-1])
				page = nil
				if err := json.Unmarshal(wire, &page); err != nil {
					t.Fatal(err)
				}
				if id, _ := page["speaker_id"].(string); id != "" {
					t.Errorf("%s still advertises a known ID: %s", decision, wire)
				}
				ann, err = a.store.TurnAnnotations(annotationSpeaker, []uint64{seq})
				if err != nil {
					t.Fatal(err)
				}
				assertStableIDAnnotation(t, ann[seq], "")
				if strings.Contains(a.attributeSpoken(seq, voiceMarker+b.text), "speaker_id=") {
					t.Errorf("%s leaves a stale ID in the prompt", decision)
				}
			}
		})
	}
}

func assertStableIDAnnotation(t *testing.T, payload, want string) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal([]byte(payload), &record); err != nil {
		t.Fatal(err)
	}
	got, _ := record["speaker_id"].(string)
	if got != want {
		t.Errorf("annotation stable ID = %q, want %q: %s", got, want, payload)
	}
}

func TestVoiceStableSpeakerIDNeverInventedOrLeaked(t *testing.T) {
	for _, tc := range []struct{ name, decision, id string }{
		{"legacy known label is not an ID", "known", ""},
		{"uncertain is not known", "uncertain", "sam"},
		{"unknown is not known", "unknown", "sam"},
		{"future decision is not known", "future", "sam"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := observationRaw("s", 2, map[string]any{"refers_to": 1, "speaker": "Sam", "speaker_id": tc.id, "decision": tc.decision})
			ve, ok := voiceEventFor(ev, false)
			if !ok {
				t.Fatal("observation unexpectedly suppressed")
			}
			wire, _ := json.Marshal(ve)
			var obj map[string]any
			if err := json.Unmarshal(wire, &obj); err != nil {
				t.Fatal(err)
			}
			if id, _ := obj["speaker_id"].(string); id != "" || strings.Contains(ve.Attribution, "speaker_id=") {
				t.Fatalf("invented a known identity: %s", wire)
			}
		})
	}
	a := newVoiceApp(t)
	a.enterSafe("stable ID regression proof")
	a.voiceSessions.Store("s", &voiceHandle{id: "s"})
	ev := observationRaw("s", 2, map[string]any{"refers_to": 1, "speaker": "Sam", "speaker_id": "sam", "decision": "known"})
	fanned := 0
	a.voiceEngineEvent(nil, ev, func(dashboard.VoiceEvent) { fanned++ })
	if _, pending := a.speakerPending.Load("s/1"); fanned != 0 || pending {
		t.Fatal("SAFE leaked speaker identity")
	}
	if ve, ok := voiceEventFor(ev, true); ok {
		t.Fatalf("SAFE page received %+v", ve)
	}
}

func TestVoiceStableSpeakerIDRejectsPartialDecode(t *testing.T) {
	a := newVoiceApp(t)
	a.voiceSessions.Store("s", &voiceHandle{id: "s"})
	ev := observationRaw("s", 2, map[string]any{
		"refers_to": 1, "speaker": "Sam", "speaker_id": "sam", "decision": "known", "score": "not a score",
	})
	ve, ok := voiceEventFor(ev, false)
	if !ok {
		t.Fatal("malformed evidence should remain an unattributed observation")
	}
	wire, _ := json.Marshal(ve)
	var page map[string]any
	if err := json.Unmarshal(wire, &page); err != nil {
		t.Fatal(err)
	}
	if id, _ := page["speaker_id"].(string); id != "" || strings.Contains(ve.Attribution, "speaker_id=") {
		t.Fatalf("partial decode certified a known ID: %s", wire)
	}
	a.noteSpeakerObservation(ev, false)
	if _, pending := a.speakerPending.Load("s/1"); pending {
		t.Fatal("malformed identification was retained")
	}
	if got := attributionOf(string(ev.Raw)); got != "" {
		t.Fatalf("malformed stored identification was rendered: %q", got)
	}
}
