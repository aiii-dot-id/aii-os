package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestIgnoredEnrolledSpeakerIsNeitherRetainedNorRecalled(t *testing.T) {
	a := newVoiceApp(t)
	withHeardHistory(a)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}, Revision: 1}
	h, _ := newTrackedSession(a, "ignore-history", true)
	page, fanned := &pageLog{}, &pageLog{}
	a.voiceEventSink = fanned.enqueue
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "reply", nil })
	a.voiceEngineEvent(nil, finalRaw(h.id, 1, "private words from visitor"), page.enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 2, map[string]any{
		"refers_to": 1, "speaker": "Visitor", "speaker_id": "visitor", "decision": "known",
	}), page.enqueue)
	a.voiceEngineEvent(nil, finalRaw(h.id, 3, "ordinary words from friend"), page.enqueue)
	a.voiceEngineEvent(nil, observationRaw(h.id, 4, map[string]any{
		"refers_to": 3, "speaker": "Friend", "speaker_id": "friend", "decision": "known",
	}), page.enqueue)
	holdWait(t, "friend's reply", func() bool { return wakes.Load() == 1 })
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
	if fanned.find("transcript_withheld", 1) == nil || page.find("transcript_final", 1) != nil {
		t.Fatal("the ignored final reached the page")
	}
	if rows := historyRows(t, a, "private words"); len(rows) != 0 {
		t.Fatalf("ignored speaker remained recallable: %+v", rows)
	}
	a.heardHistory.mu.Lock()
	for _, row := range a.heardHistory.rows {
		if strings.Contains(row.Text, "private words") {
			a.heardHistory.mu.Unlock()
			t.Fatal("ignored speaker's words were retained")
		}
	}
	a.heardHistory.mu.Unlock()
	if rows := historyRows(t, a, "ordinary words"); len(rows) != 1 || !strings.Contains(rows[0].Text, "friend") {
		t.Fatalf("ordinary known speech stopped working: %+v", rows)
	}
	read, err := a.engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "heard", "query": "private words"})
	if err != nil || strings.Contains(read, "private words from visitor") {
		t.Fatalf("actual recall surfaced ignored words: %q %v", read, err)
	}
}

func TestIgnorePolicyChangeHidesPreviouslyRetainedSpeakerID(t *testing.T) {
	a := newVoiceApp(t)
	obs := observationRaw("history", 2, map[string]any{
		"refers_to": 1, "speaker": "Visitor", "speaker_id": "visitor", "decision": "known",
	})
	a.retainHeard("history", 1, "earlier visitor words", dashboard.VoiceEvent{}, "delivered", "", frameOf(obs))
	if rows := historyRows(t, a, "earlier visitor words"); len(rows) != 1 {
		t.Fatalf("normal history was lost before Ignore: %+v", rows)
	}
	a.cfgMu.Lock()
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}, Revision: 2}
	a.cfgMu.Unlock()
	if rows := historyRows(t, a, "earlier visitor words"); len(rows) != 0 {
		t.Fatalf("new Ignore policy did not withhold an existing known speaker: %+v", rows)
	}
}

// The same rule at the other two doors. A row kept before the operator
// ignored someone is hidden at recall when EITHER of their identifiers is
// listed, and a late exact observation naming an ignored enrolled id beside
// its UUID removes the row it amends. Someone else stays recallable.
func TestIgnoredEnrolledIDHidesAKnownMatchThatCarriesAUUID(t *testing.T) {
	a := newVoiceApp(t)
	start, end := int64(0), int64(16000)
	ve := dashboard.VoiceEvent{TrackID: "track-a", StartSample: &start, EndSample: &end}
	visitor := knownUUIDObservation("s", 20, 1, "track-a", "visitor", uuidA, "matched")
	friend := knownUUIDObservation("s", 21, 3, "track-a", "friend", uuidB, "matched")
	a.retainHeard("s", 1, "visitor words kept before the ignore", ve, "delivered", "", frameOf(visitor))
	a.retainHeard("s", 2, "words identified later", ve, "withheld", "pending", nil)
	a.retainHeard("s", 3, "friend words", ve, "delivered", "", frameOf(friend))
	a.cfgMu.Lock()
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}, Revision: 1}
	a.cfgMu.Unlock()
	if rows := historyRows(t, a, "visitor words"); len(rows) != 0 {
		t.Fatalf("recall surfaced a speaker ignored by enrolled id: %+v", rows)
	}
	a.amendHeard(decodeVoiceFrame(knownUUIDObservation("s", 22, 2, "track-a", "visitor", uuidA, "matched")))
	if heardRetained(a, "identified later") {
		t.Fatal("late evidence of an ignored enrolled id left the row retained")
	}
	if rows := historyRows(t, a, "friend words"); len(rows) != 1 {
		t.Fatalf("a speaker nobody ignored was hidden: %+v", rows)
	}
}

// heardRetained says whether the buffer itself holds the words, whatever
// recall would show: Ignore is not kept, not merely not shown.
func heardRetained(a *App, text string) bool {
	a.heardHistory.mu.Lock()
	defer a.heardHistory.mu.Unlock()
	for _, row := range a.heardHistory.rows {
		if strings.Contains(row.Text, text) {
			return true
		}
	}
	return false
}

func TestProvisionalOrOversizedSpeakerIDDoesNotBecomeAnIgnoreMatch(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor", uuidA}}
	provisional := observationRaw("pending", 2, map[string]any{
		"refers_to": 1, "speaker_id": "visitor", "decision": "known",
		"speaker_uuid": uuidA, "registry_revision": "1", "continuity": "provisional",
		"track_id": "track-a", "start_sample": 0, "end_sample": 16000,
	})
	a.retainHeard("pending", 1, "speech awaiting identity", dashboard.VoiceEvent{}, "withheld", "provisional", frameOf(provisional))
	if rows := historyRows(t, a, "awaiting identity"); len(rows) != 1 {
		t.Fatalf("provisional guess deleted unresolved speech: %+v", rows)
	}
	oversized := observationRaw("large", 3, map[string]any{
		"refers_to": 2, "speaker_id": strings.Repeat("v", 5000), "decision": "known",
	})
	a.retainHeard("large", 2, "bounded metadata", dashboard.VoiceEvent{}, "withheld", "bad id", frameOf(oversized))
	a.heardHistory.mu.Lock()
	defer a.heardHistory.mu.Unlock()
	for _, row := range a.heardHistory.rows {
		if len(row.filterIDs) != 0 {
			t.Fatalf("unresolved or oversized identifier stored as a match (%d ids)", len(row.filterIDs))
		}
	}
}
