package pluginhost

import (
	"context"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .

// .
// .
func closedNow(v *VoiceSession) bool {
	select {
	case <-v.InputClosed():
		return true
	default:
		return false
	}
}

func TestEngineEndedInputIsNamedAndSignalled(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	if closedNow(p.v) {
		t.Fatal("an open session has not stopped listening")
	}
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":160,"processed_end_sample":152,"reason":"capture_limit"}`)
	p.observed()
	if !closedNow(p.v) {
		t.Fatal("the engine ending the input must be signalled: nothing else stops the microphone")
	}
	if why := p.v.InputCompletionReason(); why != "capture_limit" {
		t.Fatalf(`the engine's own word for why, kept: %q`, why)
	}
	// .
	// .
	// .
	c := p.v.InputCompletionInfo()
	if c == nil || c.StreamID != "in" || c.EndSample != 160 || c.ProcessedEndSample != 152 || c.Sequence != 1 || c.Reason != "capture_limit" {
		t.Fatalf("the completion descriptor is kept whole: %+v", c)
	}
	if p.v.Faulted() {
		t.Fatalf("an engine-ended input is not a fault: %s", p.v.FaultReason())
	}
	// .
	// .
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":2,"stream_id":"in","end_sample":160,"processed_end_sample":160,"reason":"capture_limit"}`)
	p.observed()
	if p.v.Faulted() {
		t.Fatalf("a duplicate completion is not a fault: %s", p.v.FaultReason())
	}
}

// .
// .
// .
func TestHostRegisteredFinishDoesNotSignalTheEngineEnd(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	done := make(chan error, 1)
	go func() { done <- p.v.FinishInputFor(p.ctx, "s1", "in", 320) }()
	p.reply(p.read("speech.session.finish_input"), `{"accepted":true}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":320,"processed_end_sample":320}`)
	p.observed()
	if fin, end := p.v.InputFinished(); !fin || end != 320 {
		t.Fatalf("the completion is still recorded: finished=%v end=%d", fin, end)
	}
	if closedNow(p.v) {
		t.Fatal("the page asked for this half-close; telling it the engine did is a lie")
	}
}

// .
// .
// .
// .
// .
func TestFinishRacingTheCaptureLimitResolvesToTheEnginesBoundary(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	done := make(chan error, 1)
	go func() { done <- p.v.FinishInputFor(p.ctx, "s1", "in", 960) }()
	id := p.read("speech.session.finish_input")
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":320,"processed_end_sample":320,"reason":"capture_limit"}`)
	p.observed()
	if p.v.Faulted() {
		t.Fatalf("an engine cap reached before an in-flight Finish is no contradiction: %s", p.v.FaultReason())
	}
	if fin, end := p.v.InputFinished(); !fin || end != 320 {
		t.Fatalf("the ENGINE's boundary is the accepted one: finished=%v end=%d", fin, end)
	}
	if !closedNow(p.v) {
		t.Fatal("the engine ended it, so the page is told even though a Finish was in flight")
	}
	if why := p.v.InputCompletionReason(); why != "capture_limit" {
		t.Fatalf("the reason survives the race: %q", why)
	}
	// .
	// .
	p.reply(id, `{"error":{"code":-32001,"message":"input already completed"}}`)
	<-done
	if p.v.Faulted() {
		t.Fatalf("the engine refusing an outrun Finish is not a fault: %s", p.v.FaultReason())
	}
}

// .
// .
// .
func TestCompletionBeyondTheRegisteredCutoffStillFaults(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	done := make(chan error, 1)
	go func() { done <- p.v.FinishInputFor(p.ctx, "s1", "in", 320) }()
	id := p.read("speech.session.finish_input")
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":999,"processed_end_sample":999}`)
	p.observed()
	if !p.v.Faulted() || !strings.Contains(p.v.FaultReason(), "cutoff") {
		t.Fatalf("a completion past the registered cutoff must fault: faulted=%v (%s)", p.v.Faulted(), p.v.FaultReason())
	}
	p.reply(id, `{"accepted":true}`)
	<-done
}

// .
// .
// .
// .
func TestStatusSnapshotTeachesTheEngineEndedInput(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	go func() {
		p.reply(p.read("speech.session.status"),
			`{"session_id":"s1","state_sequence":4,"lifecycle":"open","input":{"state":"finishing","admitted_end_sample":480,"processed_end_sample":480},"recognition":{"utterance_open":false,"finalization_pending":true},"synthesis":{"synthesis_id":"","state":"idle"},"playback":{"synthesis_id":"","state":"idle","queued_samples":0},"input_completion":{"stream_id":"in","end_sample":480,"processed_end_sample":480,"sequence":7,"reason":"capture_limit"}}`)
	}()
	ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
	defer cancel()
	if _, err := p.v.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if !closedNow(p.v) {
		t.Fatal("the status owner's word is the engine's word: the page is told")
	}
	if why := p.v.InputCompletionReason(); why != "capture_limit" {
		t.Fatalf("the snapshot's reason is kept: %q", why)
	}
	if c := p.v.InputCompletionInfo(); c == nil || c.EndSample != 480 || c.Sequence != 7 {
		t.Fatalf("the snapshot's whole descriptor is kept: %+v", c)
	}
}

// .
// .
// .
func TestTheEngineEndSignalIsRenewedPerSession(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	first := p.v.InputClosed()
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":160,"processed_end_sample":160,"reason":"capture_limit"}`)
	p.observed()
	if !closedNow(p.v) {
		t.Fatal("the first session was stopped")
	}
	p.event("session_end", "s1", 2)
	p.observed()
	p.open("s2")
	if closedNow(p.v) {
		t.Fatal("a new session starts listening: the last one's end is not its own")
	}
	if why := p.v.InputCompletionReason(); why != "" {
		t.Fatalf("the last session's reason is not this one's: %q", why)
	}
	if c := p.v.InputCompletionInfo(); c != nil {
		t.Fatalf("the last session's completion is not this one's: %+v", c)
	}
	select {
	case <-first:
	default:
		t.Fatal("the stopped session's signal stays closed for anything still holding it")
	}
}
