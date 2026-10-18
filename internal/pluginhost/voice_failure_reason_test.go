package pluginhost

import "testing"

func TestVoiceFailureRetainsEngineReason(t *testing.T) {
	p := newEpochProbe(t)
	p.open("current")
	p.frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"failure","session_id":"old","sequence":1,"reason":"stale failure"}}`)
	p.frames <- []byte(`{"jsonrpc":"2.0","method":"session.event","params":{"type":"failure","session_id":"current","sequence":2,"reason":"speaker match reason invalid"}}`)
	p.observed()
	if !p.v.Faulted() || p.v.FaultReason() != "speaker match reason invalid" {
		t.Fatalf("engine cause lost: %q", p.v.FaultReason())
	}
}

func TestVoiceFailureWithoutReasonKeepsFallback(t *testing.T) {
	p := newEpochProbe(t)
	p.open("current")
	p.event("failure", "current", 1)
	p.observed()
	if !p.v.Faulted() || p.v.FaultReason() != "engine reported failure" {
		t.Fatalf("missing reason lost fallback: %q", p.v.FaultReason())
	}
}
