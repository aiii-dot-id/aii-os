package app

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAFinalOfASessionThisApplicationDidNotOpenIsNoOnesWords(t *testing.T) {
	a := newVoiceApp(t)
	withHeardHistory(a)
	h, _ := newTrackedSession(a, "vs-opened", true)
	page := &pageLog{}
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "a reply", nil })
	for i, id := range []string{"check-1", "vs-never-opened", ""} {
		a.voiceEngineEvent(nil, finalRaw(id, int64(i+1), "cobalt lantern, from "+id), page.enqueue)
		a.voiceEngineEvent(nil, partialRaw(id, int64(i+10), "cobalt"), page.enqueue)
	}
	time.Sleep(200 * time.Millisecond)
	if rows := historyRows(t, a, "cobalt"); len(rows) != 0 {
		t.Fatalf("a final of a session this application did not open was recorded as heard: %+v", rows)
	}
	for _, typ := range page.types() {
		if strings.HasPrefix(typ, "transcript") {
			t.Fatalf("its words reached the page: %v", page.types())
		}
	}
	if wakes.Load() != 0 {
		t.Fatalf("its words were answered as a turn: %d", wakes.Load())
	}

	// The operator's own session: heard, recorded and answered.
	a.voiceEngineEvent(nil, finalRaw(h.id, 20, "the operator's own lantern"), page.enqueue)
	voiceWait(t, "the operator's final is answered", 5*time.Second, func() bool { return wakes.Load() == 1 })
	if rows := historyRows(t, a, "lantern"); len(rows) != 1 || !strings.Contains(rows[0].Text, "operator's own") {
		t.Fatalf("the operator's own words must be recorded: %+v", rows)
	}

	// A session opened here whose handle was already let go.
	ended, _ := newTrackedSession(a, "vs-ended", false)
	a.noteVoiceOpened(ended.id, nil)
	a.voiceSessionEnded(ended)
	a.voiceEngineEvent(nil, finalRaw(ended.id, 30, "last words of an ended session"), page.enqueue)
	if rows := historyRows(t, a, "last words"); len(rows) != 1 {
		t.Fatalf("a session opened here is heard after its handle was let go: %+v", rows)
	}
}
