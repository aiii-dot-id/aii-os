package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestVoiceReleasePolicyRejectsNullInsteadOfUnrestricting(t *testing.T) {
	policy, err := speakerPolicyFromChange(nil)
	if err == nil {
		t.Fatalf("null accepted as mode=%q; malformed input removed a restriction", policy.Mode)
	}
}

func TestVoiceReleasePolicyNullCannotReplaceLiveRestriction(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"sam"}, Revision: 3}
	persisted := false
	_, err := a.applyConfigChangeWith(map[string]interface{}{"speech.speakers": nil}, func(*Config) (bool, error) {
		persisted = true
		return true, nil
	})
	got := a.configSnapshot().Speech.Speakers
	if err == nil || persisted || got.Mode != "only" || got.Revision != 3 || !reflect.DeepEqual(got.UIDs, []string{"sam"}) {
		t.Fatalf("null must refuse without publishing: err=%v persisted=%v resulting policy=%+v", err, persisted, got)
	}
}

func TestVoiceReleasePolicyRejectsNullFieldsAndInactiveTypos(t *testing.T) {
	for _, change := range []map[string]any{
		{"mode": nil},
		{"mode": "ignore", "uids": nil},
		{"mode": "ignore", "unidentified": nil},
		{"mode": "only", "unidentified": "typo"},
	} {
		a := newVoiceApp(t)
		a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"sam"}, Revision: 3}
		persisted := false
		_, err := a.applyConfigChangeWith(map[string]interface{}{"speech.speakers": change}, func(*Config) (bool, error) { persisted = true; return true, nil })
		got := a.configSnapshot().Speech.Speakers
		if err == nil || persisted || got.Mode != "only" || got.Revision != 3 || !reflect.DeepEqual(got.UIDs, []string{"sam"}) {
			t.Errorf("malformed policy fields changed the restriction: change=%v got=%+v persisted=%v err=%v", change, got, persisted, err)
		}
	}
}

func TestVoiceReleasePolicyKeepsHeldFinalOrder(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}, Unidentified: "deliver", Revision: 3}
	// .
	// .
	h, _ := newTrackedSession(a, "release-order", false)
	page := &pageLog{}
	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "first utterance"), page.enqueue)
	a.voiceEngineEvent(nil, finalRaw(h.id, 8, "second utterance"), page.enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, map[string]any{"refers_to": 8, "decision": "uncertain", "reason": "speaker_worker_busy"}), page.enqueue)
	if page.find("transcript_final", 8) != nil || page.find("speaker_observation", 20) != nil {
		t.Fatal("later final/observation reached the page before earlier final 7 was decided")
	}
	a.voiceEngineEvent(nil, observationRaw(h.id, 21, map[string]any{"refers_to": 7, "speaker": "Sam", "speaker_id": "sam", "decision": "known"}), page.enqueue)
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
	page.mu.Lock()
	var got []int64
	for _, ev := range page.evs {
		if ev.Type == "transcript_final" || ev.Type == "speaker_observation" {
			got = append(got, ev.Sequence)
		}
	}
	page.mu.Unlock()
	if !reflect.DeepEqual(got, []int64{7, 21, 8, 20}) {
		t.Fatalf("page order=%v, want final/observation pairs [7 21 8 20]", got)
	}
	// .
	// .
	// .
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest == nil || latest.Content != voiceMarker+voiceRoomNote+"second utterance" {
		t.Fatalf("record order differs: latest=%+v err=%v", latest, err)
	}
}

func TestHeldSpeechAdmitsInOrderWithoutWaitingForInference(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}}
	h, _ := newTrackedSession(a, "admission-order", true)
	page := &pageLog{}
	entered, release := make(chan string, 1), make(chan struct{})
	prior := voiceWake
	voiceWake = func(_ *App, _ context.Context, _, text string) (string, error) {
		entered <- text
		<-release
		return "", nil
	}
	t.Cleanup(func() { voiceWake = prior })
	t.Cleanup(func() { close(release); h.work.Wait() })
	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "first utterance"), page.enqueue)
	a.voiceEngineEvent(nil, finalRaw(h.id, 8, "second utterance"), page.enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, map[string]any{"refers_to": 8, "decision": "uncertain"}), page.enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 21, map[string]any{"refers_to": 7, "decision": "uncertain"}), page.enqueue)
	select {
	case text := <-entered:
		if text != voiceMarker+"first utterance" {
			t.Fatalf("later words won the turn gate: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first utterance never entered inference")
	}
	// .
	// .
	holdWait(t, "second admission during first inference", func() bool {
		a.turnMu.Lock()
		defer a.turnMu.Unlock()
		return len(a.steers) == 1
	})
	said := a.DrainSteering()
	if len(said) != 1 || !strings.Contains(said[0], "second utterance") {
		t.Fatalf("second utterance lost its admission: %v", said)
	}
	// .
	if err := h.Interrupt(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHeldOrderTimeoutReleasesLaterDecision(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"sam"}}
	h, _ := newTrackedSession(a, "timeout-order", false)
	page, fan := &pageLog{}, &pageLog{}
	a.voiceEventSink = fan.enqueue
	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "unattributed words"), page.enqueue)
	a.voiceEngineEvent(nil, finalRaw(h.id, 8, "later known words"), page.enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, map[string]any{"refers_to": 8, "speaker_id": "sam", "decision": "known"}), page.enqueue)
	a.decideHeld(h, 7, "", "", true, nil)
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
	if page.find("transcript_final", 7) != nil || page.find("transcript_final", 8) == nil || fan.find("transcript_withheld", 7) == nil {
		t.Fatalf("timeout did not settle the head and release the successor: %v", page.types())
	}
}

func TestHeldOrderRelaxationCannotOvertakeExistingWords(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"sam"}}
	h, _ := newTrackedSession(a, "relax-order", false)
	page := &pageLog{}
	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "older held words"), page.enqueue)
	_, err := a.applyConfigChangeWith(map[string]interface{}{"speech.speakers": map[string]any{"mode": "all"}}, func(*Config) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	a.voiceEngineEvent(nil, finalRaw(h.id, 8, "new unrestricted words"), page.enqueue)
	if page.find("transcript_final", 8) != nil {
		t.Fatal("relaxing the policy let new speech overtake already held words")
	}
	a.decideHeld(h, 7, "", "", true, nil)
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
	if page.find("transcript_final", 7) == nil || page.find("transcript_final", 8) == nil {
		t.Fatal("the relaxed policy lost words instead of preserving order")
	}
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest == nil || latest.Content != voiceMarker+voiceRoomNote+"new unrestricted words" {
		t.Fatalf("the record lost the relaxed order: %+v %v", latest, err)
	}
}

func TestHeldOrderTighteningAppliesToApprovedButUndeliveredWords(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", Unidentified: "deliver", Revision: 3}
	h, _ := newTrackedSession(a, "tighten-order", false)
	page, fan := &pageLog{}, &pageLog{}
	a.voiceEventSink = fan.enqueue
	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "earlier undecided words"), page.enqueue)
	a.voiceEngineEvent(nil, finalRaw(h.id, 8, "visitor words approved before tightening"), page.enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, map[string]any{"refers_to": 8, "speaker_id": "visitor", "decision": "known"}), page.enqueue)
	_, err := a.applyConfigChangeWith(map[string]interface{}{"speech.speakers": map[string]any{"mode": "only", "uids": []string{"sam"}}}, func(*Config) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	a.decideHeld(h, 7, "", "", true, nil)
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
	if page.find("transcript_final", 8) != nil || fan.find("transcript_withheld", 8) == nil {
		t.Fatal("approved but undelivered visitor words escaped the tightened policy")
	}
	if got := fan.find("transcript_withheld", 8).Revision; got != 4 {
		t.Fatalf("withholding cites revision %d, want the tightened revision 4", got)
	}
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest != nil {
		t.Fatalf("withheld words reached the record: latest=%+v err=%v", latest, err)
	}
}

func TestHeldAdmissionRespectsItsPredecessor(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}}
	h, _ := newTrackedSession(a, "predecessor", true)
	previous := make(chan struct{})
	h.heardTail = previous
	entered := make(chan struct{}, 1)
	stubWake(t, func() (string, error) { entered <- struct{}{}; return "", nil })
	t.Cleanup(func() { close(previous); h.work.Wait() })
	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "must wait for admission"), dropEvent)
	a.decideHeld(h, 7, "", "", true, nil)
	select {
	case <-entered:
		t.Fatal("model admission bypassed the predecessor")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHeldOrderSessionEndDuringReleaseDoesNotReviveSuccessor(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}}
	h, _ := newTrackedSession(a, "close-order", false)
	page := &pageLog{}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	enqueue := func(v dashboard.VoiceEvent) {
		if v.Type == "transcript_final" && v.Sequence == 7 {
			close(entered)
			<-release
		}
		page.enqueue(v)
	}
	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "first words"), enqueue)
	a.voiceEngineEvent(nil, finalRaw(h.id, 8, "must stay withheld"), enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, map[string]any{"refers_to": 8, "decision": "uncertain"}), enqueue)
	go func() {
		defer close(finished)
		a.decideHeld(h, 7, "", "", true, nil)
	}()
	<-entered
	a.voiceSessionEnded(h)
	close(release)
	<-finished
	h.work.Wait()
	if page.find("transcript_final", 8) != nil || a.speakerWithheldFinals.Load() != 1 {
		t.Fatal("the close revived a ready successor or failed to settle it exactly once")
	}
}
