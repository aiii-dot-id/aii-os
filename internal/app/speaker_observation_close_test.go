package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// queueSpoken puts one spoken final's words in the running turn's mailbox,
// exactly as observeOperatorVoice does while a turn is busy.
func queueSpoken(t *testing.T, a *App, session string, seq int64, text string) {
	t.Helper()
	b := &voiceBinding{session: session, seq: seq, text: text, done: func() {}}
	if steered, err := a.admit(roleOperator, voiceMarker+text, b, nil); err != nil || !steered {
		t.Fatalf("words not queued behind the running turn: steered=%v err=%v", steered, err)
	}
}

// spokenAttribution is the speaker annotation on the turn holding a final's
// words, and whether that turn was recorded at all.
func spokenAttribution(t *testing.T, a *App, key string) (string, bool) {
	t.Helper()
	seq, found, err := a.store.TurnSeqByAnnotation(annotationVoice, key)
	if err != nil || !found {
		return "", false
	}
	ann, err := a.store.TurnAnnotations(annotationSpeaker, []uint64{seq})
	if err != nil {
		t.Fatal(err)
	}
	return ann[seq], true
}

// knownObservation is an enrolled match for one final, unsegmented.
func knownObservation(refers int64, name, id string) map[string]any {
	return map[string]any{"refers_to": refers, "speaker": name, "decision": "known", "speaker_id": id}
}

func TestSpeakerObservationJoinsQueuedTurnAfterSessionEnd(t *testing.T) {
	for _, c := range []struct {
		name  string
		abort bool
		order string // when the observation arrives
		taken bool   // the running turn already holds the words, not yet recorded
	}{
		{"close, observation before the turn", false, "before close", false},
		{"abort, observation before the turn", true, "before close", false},
		{"close, observation after the close", false, "after close", false},
		{"close, turn before the observation", false, "after the turn", false},
		{"close while the running turn holds the words", false, "before close", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newVoiceApp(t)
			h, _ := newTrackedSession(a, "ended-before-turn", true)
			if !a.TryBeginTurn() {
				t.Fatal("no running turn to queue behind")
			}
			defer a.EndTurn()
			key := voiceRefKey(h.id, 7)
			observe := func() {
				a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
			}
			if c.order == "before close" {
				observe()
				if _, held := a.speakerPending.Load(key); !held {
					t.Fatal("observation was not held for its admitted final")
				}
			}
			if c.taken {
				a.holdVoice(&voiceBinding{session: h.id, seq: 7, text: "words admitted before close", done: func() {}})
			} else {
				queueSpoken(t, a, h.id, 7, "words admitted before close")
			}
			if c.abort {
				if err := h.Close(context.Background(), "abort"); err != nil {
					t.Fatal(err)
				}
			}
			a.voiceSessionEnded(h)
			if c.order == "after close" {
				observe()
			}
			if c.order != "after the turn" {
				if _, held := a.speakerPending.Load(key); !held {
					t.Fatal("the session's end dropped the observation of words still queued for a turn")
				}
			}
			said := a.DrainSteering()
			if c.taken { // the wake path records the words it took
				seq, err := a.engine.RecordConversationTurnSeq(roleOperator, voiceMarker+"words admitted before close")
				if err != nil {
					t.Fatal(err)
				}
				a.tagSpokenTurn(seq, voiceMarker+"words admitted before close")
			}
			if c.order == "after the turn" {
				observe()
			}
			got, found := spokenAttribution(t, a, key)
			if !found {
				t.Fatal("late committed turn lost its voice reference")
			}
			if !strings.Contains(got, `"speaker_id":"quinn"`) {
				t.Fatalf("observation arrived, but the late committed turn lost it: %q", got)
			}
			if c.order != "after the turn" && !c.taken && (len(said) != 1 || !strings.Contains(said[0], "Quinn")) {
				t.Fatalf("the identity did not read who spoke on the turn it answers: %q", said)
			}
			if _, held := a.speakerPending.Load(key); held {
				t.Fatal("observation remained pending after its turn consumed it")
			}
		})
	}
}

// The held path: under a restricted policy (or a declared track) the
// observation, or the host's own wait-expired decision, is held BEFORE the
// words are admitted, so it is the first thing a session's end could lose.
func TestHeldFinalAttributionJoinsItsTurnAfterSessionEnd(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"observation decides the held final", `"speaker_id":"quinn"`},
		{"host wait expired", "host_attribution_wait_expired"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newVoiceApp(t)
			h, _ := newTrackedSession(a, "held-then-ended", true)
			if !a.TryBeginTurn() {
				t.Fatal("no running turn to queue behind")
			}
			defer a.EndTurn()
			if c.want == "host_attribution_wait_expired" {
				a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "held words"), dropEvent)
				a.decideHeld(h, 7, "", nil, true, nil)
			} else {
				a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"quinn"}}
				a.voiceEngineEvent(nil, finalRaw(h.id, 7, "held words"), dropEvent)
				a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
			}
			holdWait(t, "the held words in the running turn's mailbox", func() bool {
				a.turnMu.Lock()
				defer a.turnMu.Unlock()
				return len(a.steers) == 1
			})
			a.voiceSessionEnded(h)
			a.DrainSteering()
			got, found := spokenAttribution(t, a, voiceRefKey(h.id, 7))
			if !found || !strings.Contains(got, c.want) {
				t.Fatalf("held attribution lost at the session's end: found=%v %q", found, got)
			}
		})
	}
}

// The usual way queued words commit: the running turn makes no further
// tool call, and its leftover steers open their own turn.
func TestLeftoverSpokenTurnCarriesItsVoiceReferenceAndObservation(t *testing.T) {
	a := meteredApp(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"type": "text", "text": "Heard."}},
			"stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 4, "output_tokens": 1}})
	})
	h, _ := newTrackedSession(a, "leftover", true)
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to queue behind")
	}
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
	queueSpoken(t, a, h.id, 7, "words left over")
	a.voiceSessionEnded(h)
	a.EndTurn() // no tool boundary drained them: they become their own turn
	a.bgWG.Wait()
	got, found := spokenAttribution(t, a, voiceRefKey(h.id, 7))
	if !found {
		t.Fatal("the leftover turn holding spoken words lost its voice reference")
	}
	if !strings.Contains(got, `"speaker_id":"quinn"`) {
		t.Fatalf("the leftover turn lost its observation: %q", got)
	}
}

// A successor session's finals restart their sequence. Each observation
// lands on its own session's turn, exactly once, whatever ended first.
func TestASuccessorSessionNeverTakesItsPredecessorsObservation(t *testing.T) {
	a := newVoiceApp(t)
	if !a.TryBeginTurn() {
		t.Fatal("no running turn to queue behind")
	}
	defer a.EndTurn()
	old, _ := newTrackedSession(a, "vs-old", true)
	a.voiceEngineEvent(nil, observationRaw(old.id, 20, knownObservation(7, "Ada", "ada")), dropEvent)
	queueSpoken(t, a, old.id, 7, "the old session's words")
	a.voiceSessionEnded(old)
	next, _ := newTrackedSession(a, "vs-next", true)
	a.voiceEngineEvent(nil, observationRaw(next.id, 20, knownObservation(7, "Bo", "bo")), dropEvent)
	queueSpoken(t, a, next.id, 7, "the next session's words")
	a.voiceSessionEnded(next)
	if said := a.DrainSteering(); len(said) != 2 {
		t.Fatalf("both sessions' words reach the turn: %q", said)
	}
	for key, want := range map[string]string{"vs-old/7": "ada", "vs-next/7": "bo"} {
		got, found := spokenAttribution(t, a, key)
		if !found || !strings.Contains(got, `"speaker_id":"`+want+`"`) {
			t.Fatalf("%s carries %q, want speaker %s", key, got, want)
		}
		if _, held := a.speakerPending.Load(key); held {
			t.Fatalf("%s still pending after its turn", key)
		}
	}
	if n, _ := a.store.ConversationTurnCount(); n != 2 {
		t.Fatalf("an observation is never a turn: %d turns", n)
	}
}

func TestAnObservationIsRetiredWithWordsThatAreNeverRecorded(t *testing.T) {
	t.Run("leftover words refused their turn", func(t *testing.T) {
		a := newVoiceApp(t) // no conversation loop: a leftover turn is refused
		h, _ := newTrackedSession(a, "refused", true)
		if !a.TryBeginTurn() {
			t.Fatal("no running turn to queue behind")
		}
		a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
		queueSpoken(t, a, h.id, 7, "words never run")
		a.voiceSessionEnded(h)
		a.EndTurn()
		a.bgWG.Wait()
		if _, held := a.speakerPending.Load(voiceRefKey(h.id, 7)); held {
			t.Fatal("refused leftover words left their observation held")
		}
	})
	t.Run("steering dropped words already carried", func(t *testing.T) {
		a := newVoiceApp(t)
		h, _ := newTrackedSession(a, "deduped", true)
		if !a.TryBeginTurn() {
			t.Fatal("no running turn to queue behind")
		}
		a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
		a.composedEvents, a.carriedEvents = []store.TurnEvent{{Arrival: &store.Inbound{ID: "in-carried"}}}, true
		b := &voiceBinding{session: h.id, seq: 7, text: "already carried", done: func() {}}
		ctx := context.WithValue(context.Background(), inboundRecordKey{}, "in-carried")
		if steered, err := a.steer(roleOperator, voiceMarker+"already carried", b, ctx); err != nil || !steered {
			t.Fatalf("not queued: %v %v", steered, err)
		}
		a.voiceSessionEnded(h)
		if said := a.DrainSteering(); len(said) != 0 {
			t.Fatalf("the carried arrival was said twice: %q", said)
		}
		a.settleVoice(context.Background(), "") // the turn's end, as runTurnLocked settles it
		a.EndTurn()
		if _, held := a.speakerPending.Load(voiceRefKey(h.id, 7)); held {
			t.Fatal("words the steering dedupe dropped left their observation held after the turn")
		}
	})
	t.Run("a turn ended without recording the words it took", func(t *testing.T) {
		a := newVoiceApp(t)
		h, _ := newTrackedSession(a, "unanswered", true)
		if !a.TryBeginTurn() {
			t.Fatal("no running turn")
		}
		a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
		a.holdVoice(&voiceBinding{session: h.id, seq: 7, text: "never recorded", done: func() {}})
		a.voiceSessionEnded(h)
		a.EndTurn()
		if _, held := a.speakerPending.Load(voiceRefKey(h.id, 7)); held {
			t.Fatal("a turn that ended without recording its words left their observation held")
		}
	})
	t.Run("room words the record refused", func(t *testing.T) {
		a := newVoiceApp(t)
		h, _ := newTrackedSession(a, "room", false)
		a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(5, "Quinn", "quinn")), dropEvent)
		a.store.SetFrozen(true, "fixture: the record refuses writes")
		if err := a.observeOperatorVoice(context.Background(), "room words", heardUtterance{SessionID: h.id, Sequence: 5, Text: "room words", Operator: true}); err == nil {
			t.Fatal("a refused record reported the room's words recorded")
		}
		if _, held := a.speakerPending.Load(voiceRefKey(h.id, 5)); held {
			t.Fatal("room words that were never recorded left their observation held")
		}
	})
	t.Run("words not admitted", func(t *testing.T) {
		a := newVoiceApp(t)
		h, _ := newTrackedSession(a, "not-admitted", true)
		if !a.TryBeginTurn() {
			t.Fatal("no running turn to queue behind")
		}
		defer a.EndTurn()
		a.steerFlush = func([]steerEntry) {} // the typed filler is not under test
		for i := 0; i < maxPendingSteers; i++ {
			if _, err := a.Steer("typed words"); err != nil {
				t.Fatal(err)
			}
		}
		a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(9, "Quinn", "quinn")), dropEvent)
		err := a.observeOperatorVoice(context.Background(), "one word too many", heardUtterance{SessionID: h.id, Sequence: 9, Text: "one word too many", Answer: true, Operator: true})
		if err == nil {
			t.Fatal("a full mailbox admitted the words")
		}
		if _, held := a.speakerPending.Load(voiceRefKey(h.id, 9)); held {
			t.Fatal("words that were not admitted left their observation held")
		}
	})
}
