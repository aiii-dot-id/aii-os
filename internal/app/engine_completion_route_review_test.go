// .
// .
// .
// .
// .

package app

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// .
// .
func TestReviewEngineCompletionAlreadyOrdersAnswerThenDrain(t *testing.T) {
	a := newVoiceApp(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	stubWake(t, func() (string, error) { close(entered); <-release; return "final answer", nil })
	h, engine := newTrackedSession(a, "review-engine-ended", true)
	a.voiceEngineEvent(nil, finalEvent(h.id, "last accepted words"), dropEvent)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("answer task did not start")
	}
	a.voiceEngineEvent(nil, finishedEvent(h.id, 960000), dropEvent)
	if !h.inputDone.Load() {
		t.Fatal("input completion was not consumed")
	}
	if got := engine.closes(); len(got) != 0 {
		t.Fatalf("drain raced ahead of final answer: %v", got)
	}
	unblock()
	awaitDrained(t, h)
	a.voiceEngineEvent(nil, finishedEvent(h.id, 960000), dropEvent)
	want := "synthesize:" + h.id + ",close:" + h.id
	if got := strings.Join(engine.opsSeen(), ","); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
