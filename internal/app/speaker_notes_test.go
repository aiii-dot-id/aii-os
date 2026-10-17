package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func observation(session string, seq, refersTo int64, speaker, decision string, score float64, late bool) pluginhost.Event {
	raw, _ := json.Marshal(map[string]any{"type": "speaker_observation", "session_id": session, "sequence": seq, "refers_to": refersTo, "speaker": speaker, "decision": decision, "score": score, "late": late})
	return pluginhost.Event{Type: "speaker_observation", SessionID: session, Sequence: seq, Raw: raw}
}

func TestSpeakerObservationAmendsTheTurnItNames(t *testing.T) {
	a := newVoiceApp(t)
	var fanned []dashboard.VoiceEvent
	sink := func(ev dashboard.VoiceEvent) { fanned = append(fanned, ev) }
	last := func() dashboard.VoiceEvent { return fanned[len(fanned)-1] }
	lastUser := func() string {
		hist, _, err := a.buildHistory(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].Role == "user" {
				return hist[i].Content
			}
		}
		return ""
	}

	b := &voiceBinding{session: "vs-1", gen: 1, done: func() {}, seq: 7, text: "hello there"}
	a.turnMu.Lock()
	a.steers = append(a.steers, steerEntry{role: roleOperator, content: voiceMarker + "hello there", voice: b})
	a.turnMu.Unlock()
	if said := a.DrainSteering(); len(said) != 1 {
		t.Fatalf("the steer was delivered: %v", said)
	}
	seq, ok, err := a.store.TurnSeqByAnnotation("voice", "vs-1/7")
	if err != nil || !ok {
		t.Fatalf("a spoken turn carries its voice reference: %v %v", ok, err)
	}
	if got := lastUser(); got != voiceMarker+"hello there" {
		t.Fatalf("before any observation the marker is the channel's: %q", got)
	}

	a.voiceEngineEvent(nil, observation("vs-1", 20, 7, "Quinn", "known", 0.92, false), sink)
	if ev := last(); ev.Type != "speaker_observation" || ev.RefersTo != 7 || ev.Attribution != "Quinn" || ev.Decision != "known" {
		t.Fatalf("the page gets the observation with its attribution: %+v", ev)
	}
	if got := lastUser(); got != "[voice · Quinn] hello there" {
		t.Fatalf("the identity reads the attribution in the channel marker: %q", got)
	}
	views, err := a.recentTurnViews()
	if err != nil {
		t.Fatal(err)
	}
	var spoken *dashboard.HistoryTurn
	for i := range views {
		if views[i].VoiceRef == "vs-1/7" {
			spoken = &views[i]
		}
	}
	if spoken == nil || spoken.Note != "Quinn" || spoken.Role != "operator" {
		t.Fatalf("the transcript view carries the reference and the attribution: %+v", views)
	}

	a.voiceEngineEvent(nil, observation("vs-1", 21, 7, "Quinn", "uncertain", 0.6, true), sink)
	if got := lastUser(); got != "[voice · uncertain: Quinn 0.60] hello there" {
		t.Fatalf("a late result amends the same turn: %q", got)
	}
	if n, _ := a.store.ConversationTurnCount(); n != 1 {
		t.Fatalf("an observation is never a turn: %d turns", n)
	}
	a.voiceEngineEvent(nil, observation("vs-1", 22, 7, "", "unknown", 0, true), sink)
	if got := lastUser(); got != "[voice · unknown speaker] hello there" {
		t.Fatalf("unknown stays unknown: %q", got)
	}
	if ann, _ := a.store.TurnAnnotations("speaker", []uint64{seq}); len(ann) != 1 {
		t.Fatalf("one speaker annotation per turn, the latest: %v", ann)
	}

	before := len(fanned)
	a.voiceEngineEvent(nil, observation("vs-1", 23, 99, "Jim", "known", 0.9, false), sink)
	if len(fanned) != before+1 || last().RefersTo != 99 {
		t.Fatal("an observation for a final the host never recorded is still shown")
	}
	if _, ok, _ := a.store.TurnSeqByAnnotation("voice", "vs-1/99"); ok {
		t.Fatal("and recorded nowhere")
	}

	// A typed-through-voice turn (the wake path) is tagged the same way.
	b2 := &voiceBinding{session: "vs-1", gen: 2, done: func() {}, seq: 8, text: "second"}
	a.turnMu.Lock()
	a.turnVoice = append(a.turnVoice, b2)
	a.turnMu.Unlock()
	seq2, err := a.engine.RecordConversationTurnSeq("operator", voiceMarker+"second")
	if err != nil {
		t.Fatal(err)
	}
	a.tagSpokenTurn(seq2, voiceMarker+"second")
	if got, ok, _ := a.store.TurnSeqByAnnotation("voice", "vs-1/8"); !ok || got != seq2 {
		t.Fatalf("the wake path's turn carries its reference: %d %v", got, ok)
	}
	a.tagSpokenTurn(seq2, "typed words")
	if got, _, _ := a.store.TurnSeqByAnnotation("voice", "vs-1/8"); got != seq2 {
		t.Fatal("a turn whose words are not the binding's is left alone")
	}

	a.enterSafe("the test's SAFE")
	before, dropped := len(fanned), a.voiceSafeDropped.Load()
	a.voiceEngineEvent(nil, observation("vs-1", 24, 7, "Quinn", "known", 0.99, false), sink)
	if len(fanned) != before || a.voiceSafeDropped.Load() != dropped+1 {
		t.Fatalf("SAFE withholds an observation with the transcript: fanned=%d dropped=%d", len(fanned)-before, a.voiceSafeDropped.Load()-dropped)
	}
	hist, _, _ := a.buildHistory(t.Context())
	var first string
	for _, m := range hist {
		if strings.Contains(m.Content, "hello there") {
			first = m.Content
		}
	}
	if !strings.HasPrefix(first, "[voice · unknown speaker]") {
		t.Fatalf("nothing changed under SAFE: %q", first)
	}
}

// observationRaw is an observation exactly as the engine framed it —
// including one with NO score field at all, which is what an engine
// with nobody enrolled sends.
func observationRaw(session string, seq int64, body map[string]any) pluginhost.Event {
	body["type"], body["session_id"], body["sequence"] = "speaker_observation", session, seq
	raw, _ := json.Marshal(body)
	return pluginhost.Event{Type: "speaker_observation", SessionID: session, Sequence: seq, Raw: raw}
}

func TestAnObservationWithNothingToCompareSaysSoRatherThanUncertain(t *testing.T) {
	a := newVoiceApp(t)
	var fanned []dashboard.VoiceEvent
	sink := func(ev dashboard.VoiceEvent) { fanned = append(fanned, ev) }
	last := func() dashboard.VoiceEvent { return fanned[len(fanned)-1] }
	lastUser := func() string {
		hist, _, err := a.buildHistory(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].Role == "user" {
				return hist[i].Content
			}
		}
		return ""
	}

	b := &voiceBinding{session: "vs-1", gen: 1, done: func() {}, seq: 3, text: "who am I"}
	a.turnMu.Lock()
	a.steers = append(a.steers, steerEntry{role: roleOperator, content: voiceMarker + "who am I", voice: b})
	a.turnMu.Unlock()
	if said := a.DrainSteering(); len(said) != 1 {
		t.Fatalf("the steer was delivered: %v", said)
	}
	seq, ok, err := a.store.TurnSeqByAnnotation("voice", "vs-1/3")
	if err != nil || !ok {
		t.Fatalf("the spoken turn carries its voice reference: %v %v", ok, err)
	}

	// Nobody enrolled: uncertain, a reason, and no score field at all.
	a.voiceEngineEvent(nil, observationRaw("vs-1", 30, map[string]any{
		"refers_to": 3, "speaker": "", "decision": "uncertain", "reason": "enrollment_unavailable",
		"used_for_permissions": false}), sink)
	if got := lastUser(); got != "[voice · speaker enrollment unavailable] who am I" {
		t.Fatalf("the identity is told there was nothing to compare against, not that a match was ambiguous: %q", got)
	}
	if ev := last(); ev.Attribution != "speaker enrollment unavailable" || ev.Reason != "enrollment_unavailable" || ev.Score != nil {
		t.Fatalf("the page gets the engine's own reason and NO score: %+v (score %v)", ev, ev.Score)
	}
	ann, err := a.store.TurnAnnotations("speaker", []uint64{seq})
	if err != nil || len(ann) != 1 {
		t.Fatalf("one speaker annotation on the turn: %v %v", ann, err)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(ann[seq]), &rec); err != nil {
		t.Fatal(err)
	}
	if _, present := rec["score"]; present {
		t.Fatalf("AN ABSENT SCORE IS NOT ZERO — the record must not invent one: %s", ann[seq])
	}
	if rec["reason"] != "enrollment_unavailable" {
		t.Fatalf("the record keeps the engine's reason: %s", ann[seq])
	}

	a.voiceEngineEvent(nil, observationRaw("vs-1", 30, map[string]any{
		"refers_to": 3, "speaker": "", "decision": "uncertain", "reason": "no_enrollments"}), sink)
	if got := lastUser(); got != "[voice · no speaker is enrolled] who am I" {
		t.Fatalf("a profile that was read and holds nobody says so, distinctly: %q", got)
	}

	// An ordinary ambiguous match still reads as one, score and all.
	a.voiceEngineEvent(nil, observationRaw("vs-1", 31, map[string]any{
		"refers_to": 3, "speaker": "Quinn", "decision": "uncertain", "score": 0.41}), sink)
	if got := lastUser(); got != "[voice · uncertain: Quinn 0.41] who am I" {
		t.Fatalf("an acoustic near-miss is unchanged: %q", got)
	}
	if ev := last(); ev.Score == nil || *ev.Score != 0.41 || ev.Reason != "" {
		t.Fatalf("a score the engine sent is carried as sent: %+v", ev)
	}
	rec = nil
	ann, _ = a.store.TurnAnnotations("speaker", []uint64{seq})
	if err := json.Unmarshal([]byte(ann[seq]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["score"] != 0.41 {
		t.Fatalf("the record keeps a score that was sent: %s", ann[seq])
	}

	// A reason the host has no sentence for is still shown, not swallowed.
	a.voiceEngineEvent(nil, observationRaw("vs-1", 32, map[string]any{
		"refers_to": 3, "speaker": "", "decision": "uncertain", "reason": "audio_too_short"}), sink)
	if got := lastUser(); got != "[voice · uncertain: audio too short] who am I" {
		t.Fatalf("an unfamiliar reason reaches the identity in the engine's own words: %q", got)
	}
	if n, _ := a.store.ConversationTurnCount(); n != 1 {
		t.Fatalf("no observation is ever a turn: %d", n)
	}
}

func TestAnObservationThatOutrunsItsTurnIsHeldAndReachesTheTurnItNames(t *testing.T) {
	a := newVoiceApp(t)
	var fanned []dashboard.VoiceEvent
	sink := func(ev dashboard.VoiceEvent) { fanned = append(fanned, ev) }
	done := make(chan struct{})
	a.voiceSessions.Store("vs-2", &voiceHandle{id: "vs-2", v: &fakeEngineSession{}, done: done, drained: make(chan struct{})})

	// The engine identifies the voice BEFORE the words reach a boundary.
	a.voiceEngineEvent(nil, observationRaw("vs-2", 40, map[string]any{
		"refers_to": 5, "speaker": "Quinn", "decision": "known", "score": 0.93}), sink)
	if _, held := a.speakerPending.Load("vs-2/5"); !held {
		t.Fatal("an observation whose turn is not recorded yet is HELD, not dropped")
	}
	if len(fanned) != 1 {
		t.Fatalf("and it still reaches the page as evidence: %v", fanned)
	}

	// Now the words reach a turn boundary.
	b := &voiceBinding{session: "vs-2", gen: 1, done: func() {}, seq: 5, text: "it is me"}
	a.turnMu.Lock()
	a.steers = append(a.steers, steerEntry{role: roleOperator, content: voiceMarker + "it is me", voice: b})
	a.turnMu.Unlock()
	said := a.DrainSteering()

	if _, held := a.speakerPending.Load("vs-2/5"); held {
		t.Fatal("the held observation is adopted by the turn, not left behind")
	}
	seq, ok, err := a.store.TurnSeqByAnnotation("voice", "vs-2/5")
	if err != nil || !ok {
		t.Fatalf("the spoken turn carries its voice reference: %v %v", ok, err)
	}
	ann, _ := a.store.TurnAnnotations("speaker", []uint64{seq})
	if len(ann) != 1 {
		t.Fatalf("and the observation landed on it: %v", ann)
	}

	// THE IDENTITY READS IT ON THE TURN IT IS ANSWERING.
	if len(said) != 1 || said[0] != "[voice · Quinn] it is me" {
		t.Fatalf("the words the identity reads name the speaker, not one turn later: %q", said)
	}

	// And a turn that never comes leaves nothing behind when the session
	// ends — but words still queued for a turn keep theirs: they are
	// recorded after the session's end all the same.
	a.voiceEngineEvent(nil, observationRaw("vs-2", 41, map[string]any{
		"refers_to": 99, "speaker": "Quinn", "decision": "known", "score": 0.9}), sink)
	a.voiceEngineEvent(nil, observationRaw("vs-2", 43, map[string]any{
		"refers_to": 6, "speaker": "Quinn", "decision": "known", "score": 0.9}), sink)
	if _, held := a.speakerPending.Load("vs-2/99"); !held {
		t.Fatal("held while the session lives")
	}
	queued := &voiceBinding{session: "vs-2", gen: 1, done: func() {}, seq: 6, text: "still queued"}
	a.turnMu.Lock()
	a.steers = append(a.steers, steerEntry{role: roleOperator, content: voiceMarker + "still queued", voice: queued})
	a.turnMu.Unlock()
	a.forgetPendingObservations("vs-2")
	if _, held := a.speakerPending.Load("vs-2/99"); held {
		t.Fatal("a session's end releases what no queued words can claim")
	}
	if _, held := a.speakerPending.Load("vs-2/6"); !held {
		t.Fatal("a session's end kept nothing for words still queued for a turn")
	}
	close(done)

	// An observation naming a session the host does not hold is dropped
	// as it always was, never accumulated.
	a.voiceEngineEvent(nil, observationRaw("vs-gone", 42, map[string]any{
		"refers_to": 1, "speaker": "X", "decision": "known", "score": 0.9}), sink)
	if _, held := a.speakerPending.Load("vs-gone/1"); held {
		t.Fatal("no session, no hold")
	}
}

// A typed turn is untouched: attribution only rewrites a spoken one.
func TestATypedTurnGainsNoAttribution(t *testing.T) {
	a := newVoiceApp(t)
	a.turnMu.Lock()
	a.steers = append(a.steers, steerEntry{role: roleOperator, content: "typed words"})
	a.turnMu.Unlock()
	if said := a.DrainSteering(); len(said) != 1 || said[0] != "typed words" {
		t.Fatalf("a typed turn reaches the identity exactly as typed: %q", said)
	}
}
