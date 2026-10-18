package supervisor

import (
	"context"
	"testing"
	"time"
)

type sessionNopDisp struct{}

func (sessionNopDisp) Dispatch(context.Context, string, []byte) ([]byte, error) {
	return []byte(`{}`), nil
}

// Session mode end to end on a REAL child process (V-W): the supervisor
// spawns fakechild in session mode, hands its frame pump to a session
// client, a control op is admitted, its terminal event arrives as a
// notification, and the serialized Invoke refuses on this lane.
func TestSessionModeDrivesARealChildAndRefusesInvoke(t *testing.T) {
	s, err := Start(Spec{PluginID: "com.example.voice", Argv: []string{fakechildBin, "session"}, SessionMode: true}, sessionNopDisp{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	frames, stdin, ok := s.SessionChannels()
	if !ok {
		t.Fatal("a running session-mode child exposes its channels")
	}
	c := NewSessionClientFrames(frames, stdin, s.Dispatcher(), 8)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go c.Run(ctx)

	res, err := c.Control(ctx, "speech.session.synthesize", map[string]any{"synthesis_id": "s1"})
	if err != nil || !hasKey(res, "accepted") {
		t.Fatalf("admission from the real child: %v %s", err, res)
	}
	select {
	case ev := <-c.Events():
		if !hasVal(ev, "type", "synthesis_end") {
			t.Fatalf("terminal event: %s", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the child's terminal event never arrived")
	}
	if _, err := s.Invoke(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"invoke.call","params":{}}`)); err == nil {
		t.Fatal("Invoke must refuse on a session-mode supervisor")
	}
}

// A session lane is handed out only for a child that is alive with its
// stdout open. After the child dies, the lane is refused from the moment
// its frame pump ends — before the reaper has moved the state — and the
// restarted child's lane is a NEW one, never the old channels revived.
func TestSessionChannelsRefuseADeadChildAndServeTheRestartedOne(t *testing.T) {
	s, err := Start(Spec{
		PluginID: "session.dead", Argv: []string{fakechildBin, "session"}, SessionMode: true,
		Backoff: Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 2},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	frames, _, ok := s.SessionChannels()
	if !ok {
		t.Fatal("a running child serves its lane")
	}
	s.mu.Lock()
	c := s.child
	s.mu.Unlock()
	if err := c.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// The pump ends: the old frames channel closes.
	select {
	case _, open := <-frames:
		if open {
			t.Fatal("no frame was expected from a killed child")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the frame pump never ended after the kill")
	}
	if _, _, ok := s.SessionChannels(); ok {
		t.Fatal("a child whose lane ended must not be handed out, whatever the state says meanwhile")
	}
	select {
	case <-c.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the child was never reaped")
	}
	// The restarted child serves a new lane.
	deadline := time.Now().Add(5 * time.Second)
	for {
		nf, _, ok := s.SessionChannels()
		if ok {
			if nf == frames {
				t.Fatal("the restarted child's lane must be a new one")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the restarted child never served a lane")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
