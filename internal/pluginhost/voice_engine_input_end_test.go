package pluginhost

import (
	"context"
	"strings"
	"testing"
	"time"
)

// THE ENGINE CAN STOP LISTENING FIRST, and the host must be able to say
// so. A capture limit the operator reached is the engine ending the input
// of its own accord: nothing the page asked for, nothing a timer in the
// page can know. The session records WHY, signals it once, and keeps the
// last words and the reply coming.

// closed reports whether the engine-end signal has fired, without waiting
// on a scheduler: the event has already been observed when this is asked.
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
	// THE WHOLE DESCRIPTOR, TOGETHER. A cutoff without the handle it
	// belongs to, or a reason beside the completion instead of on it, is
	// how a reader ends up guessing which input ended.
	c := p.v.InputCompletionInfo()
	if c == nil || c.StreamID != "in" || c.EndSample != 160 || c.ProcessedEndSample != 152 || c.Sequence != 1 || c.Reason != "capture_limit" {
		t.Fatalf("the completion descriptor is kept whole: %+v", c)
	}
	if p.v.Faulted() {
		t.Fatalf("an engine-ended input is not a fault: %s", p.v.FaultReason())
	}
	// A repeat says the same thing; the signal is closed once, and a
	// second close would panic if this were not idempotent.
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":2,"stream_id":"in","end_sample":160,"processed_end_sample":160,"reason":"capture_limit"}`)
	p.observed()
	if p.v.Faulted() {
		t.Fatalf("a duplicate completion is not a fault: %s", p.v.FaultReason())
	}
}

// A half-close the PAGE asked for is not news to the page: the microphone
// already stopped when the operator stopped it. Only the engine's own end
// fires the signal.
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

// AN EARLIER ENGINE CAP IS NOT A CONTRADICTION. A Finish registers its
// cutoff before it is sent, so a listening limit the engine reached first
// arrives while a later browser cutoff sits registered and unacknowledged.
// That is the engine's boundary winning a race — resolve to the engine's
// sample, and do not fault a lane that is working exactly as specified.
func TestFinishRacingTheCaptureLimitResolvesToTheEnginesBoundary(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	done := make(chan error, 1)
	go func() { done <- p.v.FinishInputFor(p.ctx, "s1", "in", 960) }()
	id := p.read("speech.session.finish_input") // in flight, unacknowledged
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
	// The engine then refuses the Finish it had already outrun. The
	// session is not faulted by that refusal either.
	p.reply(id, `{"error":{"code":-32001,"message":"input already completed"}}`)
	<-done
	if p.v.Faulted() {
		t.Fatalf("the engine refusing an outrun Finish is not a fault: %s", p.v.FaultReason())
	}
}

// The permission does not widen: a cutoff LATER than the one the host
// registered means the engine accepted input the host never allowed, and
// that is still a lane the host cannot trust.
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

// LOSING THE EVENT MUST NOT LOSE THE REASON. The engine says an input
// completed twice over — as the event, and in every status snapshot. The
// snapshot is folded through the same one place, so a page whose engine
// event never arrived still learns why its microphone stopped.
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

// A NEW SESSION LISTENS AGAIN. The signal belongs to the instance that
// was stopped; the next one starts open, so a completion that ended the
// last conversation cannot stop the next one's microphone.
func TestTheEngineEndSignalIsRenewedPerSession(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	first := p.v.InputClosed()
	probeEvent(p, `{"type":"input_finished","session_id":"s1","sequence":1,"stream_id":"in","end_sample":160,"processed_end_sample":160,"reason":"capture_limit"}`)
	p.observed()
	if !closedNow(p.v) {
		t.Fatal("the first session was stopped")
	}
	p.event("session_end", "s1", 2) // s1 ends; the same driver reopens as s2
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
