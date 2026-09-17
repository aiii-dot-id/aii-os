package app

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
func finalRaw(session string, seq int64, text string) pluginhost.Event {
	raw, _ := json.Marshal(map[string]any{"type": "transcript_final", "session_id": session, "sequence": seq, "text": text})
	return pluginhost.Event{Type: "transcript_final", SessionID: session, Sequence: seq, Raw: raw}
}

func partialRaw(session string, seq int64, text string) pluginhost.Event {
	raw, _ := json.Marshal(map[string]any{"type": "transcript_partial", "session_id": session, "sequence": seq, "text": text})
	return pluginhost.Event{Type: "transcript_partial", SessionID: session, Sequence: seq, Raw: raw}
}

// .
func TestTheSpeakerPolicyDecides(t *testing.T) {
	for _, c := range []struct {
		mode, unid, decision, id string
		deliver                  bool
	}{
		{"all", "", "known", "james", true}, {"all", "", "unknown", "", true}, {"all", "withhold", "uncertain", "", true},
		{"only", "", "known", "james", true}, {"only", "", "known", "ada", false}, {"only", "", "unknown", "", false},
		{"only", "", "uncertain", "james", false}, {"only", "deliver", "unknown", "", false}, {"only", "", "known", "", false},
		{"ignore", "", "known", "james", false}, {"ignore", "", "known", "ada", true}, {"ignore", "", "unknown", "", true},
		{"ignore", "withhold", "unknown", "", false}, {"ignore", "withhold", "uncertain", "james", false}, {"ignore", "withhold", "known", "ada", true},
	} {
		p := policyFrom(SpeakerPolicyConfig{Mode: c.mode, UIDs: []string{"james"}, Unidentified: c.unid})
		if c.mode == "all" {
			p = policyFrom(SpeakerPolicyConfig{Mode: c.mode, Unidentified: c.unid})
		}
		got, reason := p.decide(c.decision, c.id)
		if got != c.deliver {
			t.Errorf("%s/%s decision=%s id=%q: deliver=%v want %v (%s)", c.mode, c.unid, c.decision, c.id, got, c.deliver, reason)
		}
		if !got && reason == "" {
			t.Errorf("%s: a withheld final needs its reason", c.mode)
		}
		if !got && strings.Contains(reason, "ada") {
			t.Errorf("a reason names a speaker the operator did not list: %q", reason)
		}
	}
}

// .
// .
func TestASpeakerPolicyIsAcceptedWholeOrNotAtAll(t *testing.T) {
	a := newVoiceApp(t)
	// .
	persist := func(*Config) (bool, error) { return true, nil }
	for _, bad := range []interface{}{
		map[string]any{"mode": "some"},
		map[string]any{"mode": "only", "uids": []any{"james", "james"}},
		map[string]any{"mode": "only", "uids": []any{"Sam Ewing"}},
		map[string]any{"mode": "ignore", "unidentified": "maybe"},
		map[string]any{"mode": "all", "uids": []any{"james"}},
		map[string]any{"mode": "only", "uids": []any{"james"}, "labels": []any{"x"}},
		"only",
	} {
		if _, err := a.applyConfigChangeWith(map[string]interface{}{"speech.speakers": bad}, persist); err == nil {
			t.Errorf("accepted %v", bad)
		}
		if got := a.configSnapshot().Speech.Speakers; got.Mode != "" || got.Revision != 0 {
			t.Fatalf("a refusal changed the policy: %+v", got)
		}
	}
	if _, err := a.applyConfigChangeWith(map[string]interface{}{"speech.speakers": map[string]any{"mode": "only", "uids": []any{"james-one", "ada.2"}, "unidentified": "withhold"}}, persist); err != nil {
		t.Fatal(err)
	}
	got := a.configSnapshot().Speech.Speakers
	if got.Mode != "only" || len(got.UIDs) != 2 || got.Unidentified != "" || got.Revision != 1 {
		t.Fatalf("the accepted policy is not what was sent (unidentified is ignore's alone): %+v", got)
	}
	if _, err := a.applyConfigChangeWith(map[string]interface{}{"speech.speakers": map[string]any{"mode": "ignore", "uids": []any{"ada.2"}, "unidentified": "withhold"}}, persist); err != nil {
		t.Fatal(err)
	}
	st := a.speakerPolicyState()
	if st.Mode != "ignore" || st.Revision != 2 || st.Unidentified != "withhold" || len(st.UIDs) != 1 {
		t.Fatalf("readback: %+v", st)
	}
	if _, err := a.applyConfigChangeWith(map[string]interface{}{"speech.speakers": map[string]any{"mode": "all"}}, persist); err != nil {
		t.Fatal(err)
	}
	if st := a.speakerPolicyState(); st.Mode != "all" || st.Revision != 3 || len(st.UIDs) != 0 {
		t.Fatalf("readback after all: %+v", st)
	}
}

type pageLog struct {
	mu  sync.Mutex
	evs []dashboard.VoiceEvent
}

func (l *pageLog) enqueue(v dashboard.VoiceEvent) {
	l.mu.Lock()
	l.evs = append(l.evs, v)
	l.mu.Unlock()
}
func (l *pageLog) types() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.evs))
	for _, e := range l.evs {
		out = append(out, e.Type)
	}
	return out
}
func (l *pageLog) find(typ string, seq int64) *dashboard.VoiceEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := range l.evs {
		if l.evs[i].Type == typ && l.evs[i].Sequence == seq {
			return &l.evs[i]
		}
	}
	return nil
}

func holdWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestARestrictedPolicyHoldsAFinalUntilItsObservation(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"james-one"}, Revision: 3}
	h, _ := newTrackedSession(a, "vs-only", true)
	page, fanned := &pageLog{}, &pageLog{}
	a.voiceEventSink = fanned.enqueue
	var woke atomic.Int32
	stubWake(t, func() (string, error) { woke.Add(1); return "an answer", nil })

	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "hello there"), page.enqueue)
	if page.find("transcript_final", 7) != nil || woke.Load() != 0 {
		t.Fatalf("the final reached the page or the model before its speaker was known: %v woke=%d", page.types(), woke.Load())
	}
	if _, remembered := a.finalText(h.id, 7); remembered {
		t.Fatal("a held final was remembered before its decision")
	}
	a.voiceEngineEvent(nil, observationRaw(h.id, 12, map[string]any{"refers_to": 7, "speaker": "Sam", "speaker_id": "james-one", "decision": "known", "score": 0.93}), page.enqueue)
	holdWait(t, "the delivered final to wake the model", func() bool { return woke.Load() == 1 })
	if got := page.types(); len(got) < 2 || got[0] != "transcript_final" || got[1] != "speaker_observation" {
		t.Fatalf("the words must precede their attribution on the page: %v", got)
	}
	if text, ok := a.finalText(h.id, 7); !ok || text != "hello there" {
		t.Fatal("a delivered final was not remembered")
	}

	a.voiceEngineEvent(nil, finalRaw(h.id, 8, "the secret words"), page.enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 13, map[string]any{"refers_to": 8, "speaker": "", "decision": "unknown"}), page.enqueue)
	w := fanned.find("transcript_withheld", 8)
	if w == nil || w.Revision != 3 || !strings.Contains(w.Reason, "not identified") || strings.Contains(w.Reason, "secret") {
		t.Fatalf("the withheld final was not reported, or reported with its words: %+v", w)
	}
	if page.find("transcript_final", 8) != nil || woke.Load() != 1 {
		t.Fatalf("withheld words reached the page or the model: %v woke=%d", page.types(), woke.Load())
	}
	if _, remembered := a.finalText(h.id, 8); remembered {
		t.Fatal("withheld words were remembered")
	}
	if out, _ := h.replyOutcome.Load().(string); out != "withheld by the speaker policy, no reply" {
		t.Fatalf("the drain's reason: %q", out)
	}
	if a.speakerWithheldFinals.Load() != 1 || a.speakerPolicyState().WithheldFinals != 1 {
		t.Fatal("the withheld final was not counted for the readback")
	}
	// .
	if _, pending := a.speakerPending.Load(voiceRefKey(h.id, 8)); pending {
		t.Fatal("an observation for withheld words was held for a turn that never comes")
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
}

// .
// .
func TestAnIgnorePolicyWithholdsTheListedSpeaker(t *testing.T) {
	for _, unid := range []string{"deliver", "withhold"} {
		t.Run("unidentified "+unid, func(t *testing.T) {
			a := newVoiceApp(t)
			a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}, Unidentified: unid, Revision: 1}
			h, _ := newTrackedSession(a, "vs-ignore", true)
			page, fanned := &pageLog{}, &pageLog{}
			a.voiceEventSink = fanned.enqueue
			var woke atomic.Int32
			stubWake(t, func() (string, error) { woke.Add(1); return "an answer", nil })
			a.voiceEngineEvent(nil, finalRaw(h.id, 1, "from the visitor"), page.enqueue)
			a.voiceEngineEvent(nil, observationRaw(h.id, 2, map[string]any{"refers_to": 1, "speaker": "Visitor", "speaker_id": "visitor", "decision": "known"}), page.enqueue)
			a.voiceEngineEvent(nil, finalRaw(h.id, 3, "from james"), page.enqueue)
			a.voiceEngineEvent(nil, observationRaw(h.id, 4, map[string]any{"refers_to": 3, "speaker": "Sam", "speaker_id": "james-one", "decision": "known"}), page.enqueue)
			a.voiceEngineEvent(nil, finalRaw(h.id, 5, "from nobody known"), page.enqueue)
			a.voiceEngineEvent(nil, observationRaw(h.id, 6, map[string]any{"refers_to": 5, "decision": "uncertain", "reason": "enrollment_unavailable"}), page.enqueue)
			wantWoke := int32(1)
			if unid == "deliver" {
				wantWoke = 2
			}
			holdWait(t, "the delivered finals to wake the model", func() bool { return woke.Load() == wantWoke })
			if fanned.find("transcript_withheld", 1) == nil || page.find("transcript_final", 1) != nil {
				t.Fatal("the listed speaker was heard")
			}
			if page.find("transcript_final", 3) == nil {
				t.Fatal("another speaker was not heard")
			}
			if (unid == "deliver") != (page.find("transcript_final", 5) != nil) || (unid == "withhold") != (fanned.find("transcript_withheld", 5) != nil) {
				t.Fatalf("the unidentified rule %s was not applied: page=%v", unid, page.types())
			}
			a.voiceInputFinished(h.id)
			awaitDrained(t, h)
		})
	}
}

// .
func TestTheDecisionBoundSettlesAFinalNoObservationNames(t *testing.T) {
	prev := speakerDecisionBound
	speakerDecisionBound = 40 * time.Millisecond
	t.Cleanup(func() { speakerDecisionBound = prev })
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"james-one"}, Revision: 2}
	h, _ := newTrackedSession(a, "vs-bound", true)
	page, fanned := &pageLog{}, &pageLog{}
	a.voiceEventSink = fanned.enqueue
	stubWake(t, func() (string, error) { t.Error("the model woke for words nobody identified"); return "", nil })
	a.voiceEngineEvent(nil, finalRaw(h.id, 9, "who said this"), page.enqueue)
	holdWait(t, "the bound to withhold the final", func() bool { return fanned.find("transcript_withheld", 9) != nil })
	if w := fanned.find("transcript_withheld", 9); !strings.Contains(w.Reason, "bound") {
		t.Fatalf("the reason does not name the bound: %q", w.Reason)
	}
	// .
	a.voiceEngineEvent(nil, observationRaw(h.id, 10, map[string]any{"refers_to": 9, "speaker": "Sam", "speaker_id": "james-one", "decision": "known"}), page.enqueue)
	if page.find("transcript_final", 9) != nil {
		t.Fatal("a late observation replayed withheld words")
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
}

// .
// .
func TestPartialsAreWithheldUnderARestriction(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "vs-partial", true)
	page := &pageLog{}
	a.voiceEngineEvent(nil, partialRaw(h.id, 1, "hel"), page.enqueue)
	if page.find("transcript_partial", 1) == nil {
		t.Fatal("under all, a partial reaches the page")
	}
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"james-one"}}
	a.voiceEngineEvent(nil, partialRaw(h.id, 2, "hello"), page.enqueue)
	if page.find("transcript_partial", 2) != nil || a.speakerWithheldPartials.Load() != 1 {
		t.Fatalf("under only, a partial must be withheld and counted: %v %d", page.types(), a.speakerWithheldPartials.Load())
	}
}

// .
// .
func TestASessionsEndWithholdsWhatIsHeld(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"james-one"}, Revision: 5}
	h, _ := newTrackedSession(a, "vs-ending", true)
	page, fanned := &pageLog{}, &pageLog{}
	a.voiceEventSink = fanned.enqueue
	stubWake(t, func() (string, error) { t.Error("the model woke after the session ended"); return "", nil })
	a.voiceEngineEvent(nil, finalRaw(h.id, 4, "held words"), page.enqueue)
	a.voiceSessionEnded(h)
	w := fanned.find("transcript_withheld", 4)
	if w == nil || w.Revision != 5 || !strings.Contains(w.Reason, "session ended") {
		t.Fatalf("the session's end did not withhold the held final: %+v", w)
	}
	settled := make(chan struct{})
	go func() { h.work.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("the hold's work was never given back")
	}
	if _, live := a.voiceSessions.Load(h.id); live {
		t.Fatal("the session was not let go")
	}
}

// .
// .
// .
func TestAPolicyChangeAppliesToWhatIsStillHeld(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"james-one"}, Revision: 1}
	h, _ := newTrackedSession(a, "vs-change", true)
	page, fanned := &pageLog{}, &pageLog{}
	a.voiceEventSink = fanned.enqueue
	var woke atomic.Int32
	stubWake(t, func() (string, error) { woke.Add(1); return "an answer", nil })
	a.voiceEngineEvent(nil, finalRaw(h.id, 2, "ada speaking"), page.enqueue)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}, Revision: 2}
	a.voiceEngineEvent(nil, observationRaw(h.id, 3, map[string]any{"refers_to": 2, "speaker": "Ada", "speaker_id": "ada", "decision": "known"}), page.enqueue)
	holdWait(t, "the final decided under the new policy", func() bool { return woke.Load() == 1 })
	if page.find("transcript_final", 2) == nil || fanned.find("transcript_withheld", 2) != nil {
		t.Fatal("the held final was decided under the old policy")
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
}

// .
func TestEveryoneIsHeardWithoutAHold(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "vs-all", true)
	page := &pageLog{}
	var woke atomic.Int32
	stubWake(t, func() (string, error) { woke.Add(1); return "an answer", nil })
	a.voiceEngineEvent(nil, finalRaw(h.id, 1, "everyone"), page.enqueue)
	if page.find("transcript_final", 1) == nil {
		t.Fatal("the final did not reach the page at once")
	}
	holdWait(t, "the model to wake", func() bool { return woke.Load() == 1 })
	if text, ok := a.finalText(h.id, 1); !ok || text != "everyone" {
		t.Fatal("the final was not remembered")
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
}
