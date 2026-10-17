package machine

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A MISSING MESSAGE REFUSES ONLY THE NEEDER THAT NEEDS IT, naming the
// message and both builds; a needer the service covers is served beside
// it. No build is compared: the service's and the identity's differ here,
// and that decides nothing.
func TestAMissingMessageRefusesOnlyItsNeeder(t *testing.T) {
	service := Build{Version: "0.1.16", Commit: "a1b2c3d4e5f6"}
	identity := Build{Version: "0.1.16", Commit: "9f8e7d6c5b4a"}
	s := Speaks{Messages: []string{MsgHello, MsgHandshake, MsgStatus}, Frames: []string{FrameJSON}}

	m := covers(s, "org.example.engine", []string{MsgStatus, "lane.audio"}, service, identity)
	if m == nil || m.Message != "lane.audio" || m.Needer != "org.example.engine" {
		t.Fatalf("a needer lacking lane.audio: %+v", m)
	}
	for _, want := range []string{"lane.audio", "org.example.engine", "0.1.16, a1b2c3d4e5f6", "0.1.16, 9f8e7d6c5b4a"} {
		if !strings.Contains(m.Error(), want) {
			t.Errorf("the refusal does not name %q: %s", want, m)
		}
	}
	if m := covers(s, "org.example.other", []string{MsgStatus}, service, identity); m != nil {
		t.Fatalf("a covered needer beside it was refused: %v", m)
	}
}

// THE SAME, OVER A REAL LINK: the client holds the service's set from its
// hello and answers each needer for itself.
func TestALinkedClientAnswersEachNeederForItself(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	slot := slotDir(t, root, "identity-0")
	serve(t, Options{Dir: dir, StartupSlots: startupSlots(slot), Build: Build{Version: "9.9.9", Commit: "service00000"}})
	c := runClient(t, ClientOptions{Dir: dir, Slot: slot, Key: newKey(t), Build: Build{Version: "0.0.1", Commit: "identity0000"}})
	waitState(t, c, StateLinked)

	err := c.Covers("org.example.engine", []string{"lane.audio"})
	var m *MissingError
	if !errors.As(err, &m) || m.Message != "lane.audio" || m.Service.Commit != "service00000" || m.Identity.Commit != "identity0000" {
		t.Fatalf("a needer the service does not cover: %v", err)
	}
	if err := c.Covers("org.example.other", []string{MsgStatus}); err != nil {
		t.Fatalf("a covered needer: %v", err)
	}
	// Two builds that differ link and are served: status answers. The
	// count of links is the first status poll's, a moment after linked.
	waitFor(t, "the first status answer", func() bool { return c.Snapshot().Linked == 1 })
	s := c.Snapshot()
	if s.Service.Version != "9.9.9" || s.Linked != 1 || len(s.Missing) != 0 {
		t.Fatalf("the linked snapshot: %+v", s)
	}
}

// runClient runs a link client until the test ends.
func runClient(t *testing.T, opts ClientOptions) *Client {
	t.Helper()
	if opts.PollEvery == 0 {
		opts.PollEvery = 50 * time.Millisecond
	}
	c := NewClient(opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("a client did not stop within 10s")
		}
	})
	return c
}

func waitState(t *testing.T, c *Client, want string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := c.Snapshot(); s.State == want {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the link never reached %q: %+v", want, c.Snapshot())
	return Snapshot{}
}
