package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
// .
// .
// .

const (
	completion480 = `"stream_id":"in","end_sample":480,"processed_end_sample":480,"sequence":3,"reason":"capture_limit"`
	snapshot480   = `{"session_id":"s1","state_sequence":2,"lifecycle":"open","input_completion":{` + completion480 + `}}`
	event480      = `{"type":"input_finished","session_id":"s1",` + completion480 + `}`
)

// .
func take(t *testing.T, v *VoiceSession) Event {
	t.Helper()
	select {
	case e, ok := <-v.Observe():
		if !ok {
			t.Fatal("the observer closed")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatalf("no observation (faulted=%v: %s)", v.Faulted(), v.FaultReason())
	}
	return Event{}
}

// .
// .
// .
func nothingMore(t *testing.T, v *VoiceSession) {
	t.Helper()
	select {
	case e := <-v.Observe():
		t.Fatalf("a second completion reached the observer: %s seq %d", e.Type, e.Sequence)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestTheObserverHearsOneCompletionWhicheverCarrierIsFirst(t *testing.T) {
	settled := func(t *testing.T, v *VoiceSession) {
		t.Helper()
		if v.Faulted() {
			t.Fatalf("the same completion twice is no fault: %s", v.FaultReason())
		}
		if fin, end := v.InputFinished(); !fin || end != 480 {
			t.Fatalf("recorded once: finished=%v end=%d", fin, end)
		}
		if !closedNow(v) || v.InputCompletionReason() != "capture_limit" {
			t.Fatalf("the page is told, with the reason: closed=%v why=%q", closedNow(v), v.InputCompletionReason())
		}
	}
	t.Run("the event first, then the status reply", func(t *testing.T) {
		p := newEpochProbe(t)
		p.open("s1")
		probeEvent(p, event480)
		if e := take(t, p.v); e.Type != "input_finished" {
			t.Fatalf("the event is observed: %s", e.Type)
		}
		go func() { p.reply(p.read("speech.session.status"), snapshot480) }()
		if _, err := p.v.Status(p.ctx); err != nil {
			t.Fatal(err)
		}
		nothingMore(t, p.v)
		settled(t, p.v)
	})
	t.Run("the status reply first, then the event", func(t *testing.T) {
		p := newEpochProbe(t)
		p.open("s1")
		go func() { p.reply(p.read("speech.session.status"), snapshot480) }()
		if _, err := p.v.Status(p.ctx); err != nil {
			t.Fatal(err)
		}
		// .
		// .
		e := take(t, p.v)
		if e.Type != "input_finished" || e.SessionID != "s1" || e.Sequence != 3 {
			t.Fatalf("the observer hears the completion status taught: %+v", e)
		}
		var c InputCompletion
		if json.Unmarshal(e.Raw, &c) != nil || c.EndSample != 480 || c.Reason != "capture_limit" || c.StreamID != "in" {
			t.Fatalf("whole: %s", e.Raw)
		}
		probeEvent(p, event480)
		nothingMore(t, p.v)
		settled(t, p.v)
	})
	t.Run("a repeat the engine itself sends still flows", func(t *testing.T) {
		// .
		// .
		// .
		p := newEpochProbe(t)
		p.open("s1")
		probeEvent(p, event480)
		take(t, p.v)
		probeEvent(p, strings.Replace(event480, `"sequence":3`, `"sequence":4`, 1))
		if e := take(t, p.v); e.Type != "input_finished" || e.Sequence != 4 {
			t.Fatalf("the engine's own repeat is its word: %+v", e)
		}
		settled(t, p.v)
	})
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestAReconciledCompletionFollowsWhatTheWireAlreadyDelivered(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	r, w := io.Pipe()
	frames := make(chan []byte)
	p := &epochProbe{t: t, ctx: ctx, frames: frames, reader: r}
	p.c = supervisor.NewSessionClientFrames(frames, w, nopDispatcher{}, 8)
	p.v = NewVoiceSession(p.c)
	done := make(chan error, 1)
	go func() { done <- p.c.Run(ctx) }()
	t.Cleanup(func() { cancel(); r.Close(); w.Close(); <-done })
	p.open("s1")

	final := func(seq int) []byte {
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"transcript_final","session_id":"s1","sequence":%d,"text":"word %d"}}`, seq, seq))
	}
	statusDone := make(chan error, 1)
	go func() { _, err := p.v.Status(p.ctx); statusDone <- err }()
	id := p.read("speech.session.status")
	p.v.mu.Lock()
	frames <- final(1)
	frames <- final(2)
	frames <- final(3)
	p.reply(id, `{"session_id":"s1","state_sequence":2,"lifecycle":"open","input_completion":{"stream_id":"in","end_sample":480,"processed_end_sample":480,"sequence":4,"reason":"capture_limit"}}`)
	p.v.mu.Unlock()
	if err := <-statusDone; err != nil {
		t.Fatal(err)
	}

	var order []string
	for i := 0; i < 4; i++ {
		e := take(t, p.v)
		order = append(order, fmt.Sprintf("%s/%d", e.Type, e.Sequence))
	}
	if strings.Join(order, " ") != "transcript_final/1 transcript_final/2 transcript_final/3 input_finished/4" {
		t.Fatalf("every final already on the wire reaches the observer before the reconciled completion: %v", order)
	}
	if p.v.Faulted() {
		t.Fatalf("no fault: %s", p.v.FaultReason())
	}
}
