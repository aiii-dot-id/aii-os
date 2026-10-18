package dashboard

import (
	"context"
	"encoding/json"
	"testing"
)

// THE HUSH CROSSES THE SOCKET WITH THE NAME OF WHAT IT STOPS: the session,
// the reply's synthesis id (empty for the browser's voice), the route and
// the reason — what a page needs to stop exactly that reply.
func TestAVoiceHushCrossesTheSocket(t *testing.T) {
	s := New("127.0.0.1", 0, newVoiceHarness().handler())
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	// THE STATUS FRAME IS THE SERVER'S WORD THAT THIS CLIENT IS REGISTERED.
	// A broadcast reaches whoever is registered when it is sent; dialing
	// returns before that, and under load the first hush went to nobody
	// and the read waited out its whole deadline.
	drainUntil(t, conn, "status")
	for _, h := range []VoiceHush{
		{SessionID: "vs-1", SynthesisID: "fb-1", Route: "cloud", Reason: "the operator spoke"},
		{SessionID: "vs-1", Route: "browser", Reason: "the session was aborted"},
	} {
		s.BroadcastVoiceHush(h)
		got := drainUntil(t, conn, "voice_hush")
		wire, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var frame struct {
			Hush map[string]any `json:"voice_hush"`
		}
		if err := json.Unmarshal(wire, &frame); err != nil {
			t.Fatal(err)
		}
		id, _ := frame.Hush["synthesis_id"].(string)
		if frame.Hush["session_id"] != h.SessionID || id != h.SynthesisID || frame.Hush["route"] != h.Route || frame.Hush["reason"] != h.Reason {
			t.Fatalf("the hush lost its name on the wire: %s; want %+v", wire, h)
		}
	}
}
