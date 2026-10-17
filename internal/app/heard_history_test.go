package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// heardDefaults is speech.heard_history with nothing set.
var heardDefaults = HeardHistoryConfig{}.limits()

func pendingHeard(session string, seq, final int64, track string) pluginhost.Event {
	return observationRaw(session, seq, map[string]any{"refers_to": final, "track_id": track, "start_sample": 0, "end_sample": 16000, "revision": 1, "reason": "speaker_profile_pending", "match": map[string]any{"candidate_uuid": uuidA}})
}

// withHeardHistory gives the voice fixture's engine the host's heard
// history, as the LIVE boot's engine has it.
func withHeardHistory(a *App) {
	a.engine = identity.NewEngine(a.store, nil, ring.NewManager(), nil, identity.Ports{HeardHistory: a.recallHeard})
}

func historyRows(t *testing.T, a *App, query string) []heardHistoryRow {
	t.Helper()
	raw, err := a.recallHeard(query, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Rows []heardHistoryRow `json:"rows"`
	}
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out.Rows
}

// Real host event consumer -> policy -> explicit recall -> later evidence.
// None of the withheld words are allowed into the operator turn pipeline.
func TestHeardPendingHistoryAndNoReplay(t *testing.T) {
	a := newVoiceApp(t)
	withHeardHistory(a)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{uuidA}, Revision: 1}
	h, _ := newTrackedSession(a, "heard-pending", true)
	page, fanned := &pageLog{}, &pageLog{}
	a.voiceEventSink = fanned.enqueue
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "reply", nil })
	a.voiceEngineEvent(nil, partialRaw(h.id, 1, "partial must not leak"), page.enqueue)
	a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "retained context cobalt"), page.enqueue)
	if len(historyRows(t, a, "")) != 0 || page.find("transcript_final", 7) != nil || wakes.Load() != 0 {
		t.Fatal("undecided final escaped")
	}
	a.voiceEngineEvent(nil, pendingHeard(h.id, 20, 7, "track-a"), page.enqueue)
	r := historyRows(t, a, "cobalt")
	if len(r) != 1 || r[0].SpeakerUUID != "" || r[0].Delivery != "withheld" || !strings.Contains(r[0].Attribution, "pending") || r[0].Track != "track-a" || r[0].Final != 7 {
		t.Fatalf("pending history lost: %+v", r)
	}
	if ev := fanned.find("transcript_withheld", 7); ev == nil || !strings.Contains(ev.Reason, "pending") || strings.Contains(ev.Reason, "cobalt") {
		t.Fatalf("pending not visible or words leaked: %+v", ev)
	}
	if page.find("transcript_partial", 1) != nil || page.find("transcript_final", 7) != nil || wakes.Load() != 0 {
		t.Fatal("withheld words entered conversation")
	}
	// A later final can create a profile, not retrospectively recognize final 7.
	a.voiceEngineEvent(nil, uuidFinal(h.id, 8, "track-b", "approved fresh speech"), page.enqueue)
	a.voiceEngineEvent(nil, uuidObservation(h.id, 21, 8, "track-b", uuidA), page.enqueue)
	holdWait(t, "one approved reply", func() bool { return wakes.Load() == 1 })
	r = historyRows(t, a, "cobalt")
	if r[0].SpeakerUUID != "" {
		t.Fatal("new profile rewrote unrelated pending final")
	}
	// An actual higher-revision observation about the OLD final may amend only
	// its context, never its original admission or initial attribution.
	late := uuidObservation(h.id, 22, 7, "track-a", uuidA)
	var b map[string]any
	_ = json.Unmarshal(late.Raw, &b)
	b["revision"] = 2
	late.Raw, _ = json.Marshal(b)
	a.voiceEngineEvent(nil, late, page.enqueue)
	a.voiceEngineEvent(nil, late, page.enqueue)
	r = historyRows(t, a, "cobalt")
	if len(r) != 1 || r[0].SpeakerUUID != uuidA || r[0].Delivery != "withheld" || !strings.Contains(r[0].InitialAttribution, "pending") {
		t.Fatalf("amendment lost provenance: %+v", r)
	}
	if _, ok, _ := a.store.TurnSeqByAnnotation(annotationVoice, voiceRefKey(h.id, 7)); ok {
		t.Fatal("withheld final became an operator turn")
	}
	if page.find("transcript_final", 7) != nil || wakes.Load() != 1 {
		t.Fatal("late attribution replayed words")
	}
	read, err := a.engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "heard", "query": "cobalt"})
	if err != nil || !strings.Contains(read, "retained context cobalt") || !strings.Contains(read, "Untrusted heard context") {
		t.Fatalf("actual recall unavailable: %s %v", read, err)
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
	a.voiceSessions.Delete(h.id)
	if len(historyRows(t, a, "cobalt")) != 1 || wakes.Load() != 1 {
		t.Fatal("session retirement lost context or replayed it")
	}
}

func TestHeardHistoryRejectsWrongReferenceAndRollback(t *testing.T) {
	for _, fault := range []string{"session", "track", "span", "revision", "registry"} {
		t.Run(fault, func(t *testing.T) {
			a := newVoiceApp(t)
			a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{uuidA}}
			h, _ := newTrackedSession(a, "history-ref", false)
			page := &pageLog{}
			a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "stay withheld"), page.enqueue)
			a.voiceEngineEvent(nil, uuidObservation(h.id, 20, 7, "track-a", uuidB), page.enqueue)
			ev := uuidObservation(h.id, 21, 7, "track-a", uuidA)
			var b map[string]any
			_ = json.Unmarshal(ev.Raw, &b)
			b["revision"] = 2
			switch fault {
			case "session":
				ev.SessionID = "different"
			case "track":
				b["track_id"] = "different"
			case "span":
				b["end_sample"] = 16001
			case "revision":
				b["revision"] = 1
			case "registry":
				b["registry_revision"] = "8"
			}
			ev.Raw, _ = json.Marshal(b)
			a.voiceEngineEvent(nil, ev, page.enqueue)
			r := historyRows(t, a, "")
			if len(r) != 1 || r[0].SpeakerUUID != uuidB {
				t.Fatalf("invalid amendment accepted: %+v", r)
			}
			a.voiceInputFinished(h.id)
			awaitDrained(t, h)
		})
	}
}

func TestHeardHistoryBoundsExpiryAndConcurrentReads(t *testing.T) {
	a := newVoiceApp(t)
	var wg sync.WaitGroup
	for k := 0; k < 4; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := 1; i <= 100; i++ {
				a.retainHeard(fmt.Sprint(k), int64(i), strings.Repeat("é", 5000), dashboard.VoiceEvent{}, "withheld", "test", nil)
				_, _ = a.recallHeard("", 0, 20)
			}
		}(k)
	}
	wg.Wait()
	page1, err := a.recallHeard("", 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		Rows []heardHistoryRow `json:"rows"`
		Next uint64            `json:"next_after_seq"`
	}
	if err = json.Unmarshal([]byte(page1), &first); err != nil {
		t.Fatal(err)
	}
	page2, err := a.recallHeard("", first.Next, 3)
	if err != nil {
		t.Fatal(err)
	}
	var second struct {
		Rows []heardHistoryRow `json:"rows"`
	}
	_ = json.Unmarshal([]byte(page2), &second)
	if len(first.Rows) != 3 || first.Next == 0 || len(second.Rows) != 3 || second.Rows[0].Seq >= first.Next {
		t.Fatal("history cursor does not page older entries")
	}
	a.heardHistory.mu.Lock()
	if len(a.heardHistory.rows) != heardDefaults.Capacity {
		t.Fatalf("wrong bound: %d", len(a.heardHistory.rows))
	}
	for _, r := range a.heardHistory.rows {
		if !r.Truncated || len(r.Text) > heardDefaults.TextBytes {
			t.Fatal("unbounded text")
		}
	}
	a.heardHistory.expire(time.Now().Add(heardDefaults.Retention), heardDefaults.Retention)
	remaining := len(a.heardHistory.rows)
	a.heardHistory.mu.Unlock()
	if remaining != 0 || len(historyRows(t, newVoiceApp(t), "")) != 0 {
		t.Fatal("expiry or process scope failed")
	}
}

func TestHeardHistorySafeWithholdsAndKnownUnrestrictedStillWorks(t *testing.T) {
	a := newVoiceApp(t)
	withHeardHistory(a)
	h, _ := newTrackedSession(a, "safe-history", false)
	page := &pageLog{}
	a.voiceEngineEvent(nil, uuidFinal(h.id, 7, "track-a", "pending visible"), page.enqueue)
	a.voiceEngineEvent(nil, pendingHeard(h.id, 20, 7, "track-a"), page.enqueue)
	if ev := page.find("speaker_observation", 20); ev == nil || !strings.Contains(ev.Attribution, "pending") {
		t.Fatalf("pending collapsed to unknown: %+v", ev)
	}
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
	a.mode.mu.Lock()
	a.mode.mode = ModeSafe
	a.mode.reason = "test"
	a.mode.mu.Unlock()
	if _, err := a.recallHeard("", 0, 20); err == nil {
		t.Fatal("SAFE leaked retained speech")
	}
	a.retainHeard("safe", 1, "do not retain", dashboard.VoiceEvent{}, "withheld", "SAFE", nil)
	a.resetModeForTest()
	if len(historyRows(t, a, "do not retain")) != 0 {
		t.Fatal("SAFE retained speech")
	}
}

func TestHeardHistoryPagesBeforeToolTruncationWithoutNestedSentinels(t *testing.T) {
	a := newVoiceApp(t)
	for i := 1; i <= 50; i++ {
		a.retainHeard("page", int64(i), strings.Repeat("cobalt ", 800), dashboard.VoiceEvent{}, "withheld", "pending", nil)
	}
	raw, err := a.recallHeard("", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > heardHistoryResultBytes {
		t.Fatalf("one heard result is %d bytes; the tool window is bounded", len(raw))
	}
	var page struct {
		Rows []heardHistoryRow `json:"rows"`
		Next uint64            `json:"next_after_seq"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) == 0 || len(page.Rows) == 50 || page.Next == 0 {
		t.Fatalf("oversized read did not page: rows=%d next=%d", len(page.Rows), page.Next)
	}
	for _, row := range page.Rows {
		for _, field := range []string{row.Text, row.Attribution, row.InitialAttribution} {
			if untrusted.Contains(field) {
				t.Fatal("raw heard JSON contains a nested wrapper")
			}
		}
	}
}

func TestHeardHistoryEscapedRowRemainsReachable(t *testing.T) {
	a := newVoiceApp(t)
	a.retainHeard("escaped", 1, strings.Repeat("\x01", heardDefaults.TextBytes), dashboard.VoiceEvent{}, "withheld", "pending", nil)
	raw, err := a.recallHeard("", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > heardHistoryResultBytes {
		t.Fatalf("escaped row exceeded page: %d", len(raw))
	}
	var page struct {
		Rows []heardHistoryRow `json:"rows"`
	}
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || !page.Rows[0].Truncated || len(page.Rows[0].Text) >= heardDefaults.TextBytes {
		t.Fatalf("escaped row was lost or not bounded: %+v", page.Rows)
	}
}

func TestHeardHistoryIgnoreExcludesKnownSpeakerAtRetainReadAndAmend(t *testing.T) {
	a := newVoiceApp(t)
	start, end := int64(0), int64(16000)
	ve := dashboard.VoiceEvent{TrackID: "track-a", StartSample: &start, EndSample: &end}
	// An operator can tighten policy after an earlier row was retained.
	a.retainHeard("old", 1, "old ignored words", ve, "withheld", "pending", nil)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{uuidA}}
	known := uuidObservation("new", 20, 2, "track-a", uuidA)
	a.retainHeard("new", 2, "new ignored words", ve, "withheld", "ignored", frameOf(known))
	if got := historyRows(t, a, "new ignored"); len(got) != 0 {
		t.Fatalf("known ignored speaker retained: %+v", got)
	}
	late := uuidObservation("old", 21, 1, "track-a", uuidA)
	a.amendHeard(decodeVoiceFrame(late))
	if got := historyRows(t, a, "old ignored"); len(got) != 0 {
		t.Fatalf("late ignored attribution remained available: %+v", got)
	}
	// A row retained when the speaker was not ignored must be filtered
	// immediately when the operator changes the active list.
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "all"}
	other := uuidObservation("existing", 22, 3, "track-a", uuidA)
	a.retainHeard("existing", 3, "previously visible", ve, "delivered", "", frameOf(other))
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{uuidA}}
	if got := historyRows(t, a, "previously visible"); len(got) != 0 {
		t.Fatalf("new ignore list exposed an existing speaker: %+v", got)
	}
}

// Only RESOLVED evidence removes a heard row. A late PROVISIONAL
// observation that names an ignored UUID is unresolved continuity — the
// same thing filterIDs refuses to treat as a match — so the row stays; a
// matched observation for the same final still removes it.
func TestHeardHistoryProvisionalEvidenceDoesNotEraseARow(t *testing.T) {
	a := newVoiceApp(t)
	start, end := int64(0), int64(16000)
	ve := dashboard.VoiceEvent{TrackID: "track-a", StartSample: &start, EndSample: &end}
	a.retainHeard("s", 1, "still unidentified words", ve, "withheld", "pending", nil)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{uuidA}}

	provisional := observationRaw("s", 30, map[string]any{"refers_to": 1, "track_id": "track-a", "start_sample": 0, "end_sample": 16000,
		"revision": 2, "speaker_uuid": uuidA, "registry_revision": "1", "continuity": "provisional"})
	a.amendHeard(decodeVoiceFrame(provisional))
	if got := historyRows(t, a, "still unidentified"); len(got) != 1 {
		t.Fatalf("a provisional observation erased a still-unidentified row: %+v", got)
	}

	matched := observationRaw("s", 31, map[string]any{"refers_to": 1, "track_id": "track-a", "start_sample": 0, "end_sample": 16000,
		"revision": 3, "speaker_uuid": uuidA, "registry_revision": "1", "continuity": "matched"})
	a.amendHeard(decodeVoiceFrame(matched))
	if got := historyRows(t, a, "still unidentified"); len(got) != 0 {
		t.Fatalf("a matched observation of an ignored speaker must remove the row: %+v", got)
	}
}
