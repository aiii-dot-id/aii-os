package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
func probeEvent(p *epochProbe, params string) {
	p.frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":` + params + `}`)
}

const inputFinished320 = `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":320,"processed_end_sample":320}`

// .
// .
// .
// .
// .
func TestReviewInputFinishedBeatsTheAcknowledgement(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	done := make(chan error, 1)
	go func() { done <- p.v.FinishInputFor(p.ctx, "s1", "in", 320) }()
	id := p.read("speech.session.finish_input")
	probeEvent(p, inputFinished320)
	p.observed()
	if fin, end := p.v.InputFinished(); !fin || end != 320 {
		t.Fatalf("the completion is recognized against the registered cutoff: finished=%v end=%d", fin, end)
	}
	if p.v.Faulted() {
		t.Fatalf("a completion matching the registered cutoff is no fault: %s", p.v.FaultReason())
	}
	cdone := make(chan error, 1)
	go func() { cdone <- p.v.CloseFor(p.ctx, "s1", "drain", "on the engine's word") }()
	cid := p.read("speech.session.close")
	p.reply(cid, `{"accepted":true}`)
	if err := <-cdone; err != nil {
		t.Fatalf("a drain is admissible on the engine's word before the Finish is acknowledged: %v", err)
	}
	p.reply(id, `{"accepted":true,"end_sample":320}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// .
// .
func TestReviewInputFinishedContradictingTheRegisteredCutoffFaults(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	done := make(chan error, 1)
	go func() { done <- p.v.FinishInputFor(p.ctx, "s1", "in", 320) }()
	p.reply(p.read("speech.session.finish_input"), `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":999,"processed_end_sample":999}`)
	p.observed()
	if !p.v.Faulted() || !strings.Contains(p.v.FaultReason(), "cutoff") {
		t.Fatalf("a contradicting completion must fault the session: faulted=%v (%s)", p.v.Faulted(), p.v.FaultReason())
	}

	// .
	q := newEpochProbe(t)
	q.open("s1")
	qdone := make(chan error, 1)
	go func() { qdone <- q.v.FinishInputFor(q.ctx, "s1", "in", 320) }()
	q.reply(q.read("speech.session.finish_input"), `{"accepted":true}`)
	if err := <-qdone; err != nil {
		t.Fatal(err)
	}
	probeEvent(q, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"other","end_sample":320,"processed_end_sample":320}`)
	q.observed()
	if !q.v.Faulted() || !strings.Contains(q.v.FaultReason(), "handle") {
		t.Fatalf("a completion naming another handle must fault the session: faulted=%v (%s)", q.v.Faulted(), q.v.FaultReason())
	}
}

// .
// .
// .
// .
func TestReviewInputFinishedIsIdempotentAndUnregisteredIsTheEnginesWord(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":160,"processed_end_sample":160}`)
	p.observed()
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":2,"stream_id":"in","end_sample":160,"processed_end_sample":160}`)
	p.observed()
	if fin, end := p.v.InputFinished(); !fin || end != 160 || p.v.Faulted() {
		t.Fatalf("the engine's word, once: finished=%v end=%d faulted=%v", fin, end, p.v.Faulted())
	}
	if err := p.v.FinishInputFor(p.ctx, "s1", "in", 160); err != nil {
		t.Fatalf("an identical Finish after completion is idempotent: %v", err)
	}
	// .
	done := make(chan error, 1)
	go func() { done <- p.v.SynthesizeFor(p.ctx, "s1", "syn-1", "after") }()
	id, sid := readOpSession(p, "speech.session.synthesize")
	if sid != "s1" {
		t.Fatalf("dispatched for %q", sid)
	}
	p.reply(id, `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// .
// .
// .
func TestReviewUnknownFinishReconcilesThroughStatus(t *testing.T) {
	snapshot := func(state string, admitted int64) string {
		return `{"session_id":"s1","state_sequence":1,"lifecycle":"open","input":{"state":"` + state + `","admitted_end_sample":` + itoa(admitted) + `,"processed_end_sample":0},"recognition":{"utterance_open":false,"finalization_pending":true},"synthesis":{"synthesis_id":"","state":"idle"},"playback":{"synthesis_id":"","state":"idle","queued_samples":0},"input_completion":null}`
	}
	t.Run("the engine holds the cutoff", func(t *testing.T) {
		p := newEpochProbe(t)
		p.open("s1")
		ctx, cancel := context.WithTimeout(p.ctx, 100*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- p.v.FinishInputFor(ctx, "s1", "in", 320) }()
		p.read("speech.session.finish_input")
		p.reply(p.read("speech.session.status"), snapshot("finishing", 320))
		if err := <-done; err != nil {
			t.Fatalf("an unknown Finish the engine's snapshot holds is good: %v", err)
		}
		cdone := make(chan error, 1)
		go func() { cdone <- p.v.CloseFor(p.ctx, "s1", "drain", "reconciled") }()
		p.reply(p.read("speech.session.close"), `{"accepted":true}`)
		if err := <-cdone; err != nil {
			t.Fatal(err)
		}
	})
	t.Run("the engine does not hold it", func(t *testing.T) {
		p := newEpochProbe(t)
		p.open("s1")
		ctx, cancel := context.WithTimeout(p.ctx, 100*time.Millisecond)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- p.v.FinishInputFor(ctx, "s1", "in", 320) }()
		p.read("speech.session.finish_input")
		p.reply(p.read("speech.session.status"), snapshot("accepting", 0))
		if err := <-done; !errors.Is(err, supervisor.ErrAdmissionUnknown) {
			t.Fatalf("no cutoff at the engine leaves the Finish unknown, never guessed good: %v", err)
		}
		if err := p.v.CloseFor(p.ctx, "s1", "drain", "guess"); err == nil || !strings.Contains(err.Error(), "finish_input") {
			t.Fatalf("no drain on a guess: %v", err)
		}
	})
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
