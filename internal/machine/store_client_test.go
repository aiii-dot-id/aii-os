//go:build (linux && !android) || (darwin && !ios) || windows

package machine

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// AN IDENTITY'S LINK CARRIES THE STORE'S MESSAGES: a need answered with
// where the material stands, the status naming the store and the link's
// own references, a release, and the operator's store.place — each through
// the client an identity host runs, over its one connection.
func TestALinkedClientCarriesTheStoresMessages(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-a")
	f := newFetches()
	svc, err := StartServiceForTest(filepath.Join(root, "machine"), f.fetch)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()
	c := svc.ClientForTest(ClientOptions{Dir: svc.srv.opts.Dir, Slot: slot, Key: newKey(t), PollEvery: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "the link", func() bool { return c.Snapshot().State == StateLinked })

	work := viewWork(f.model("carried.bin", []byte("carried over the link")))
	need := NeedParams{Plugin: "org.example.carried", Works: []pluginhost.StoreWork{work}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := c.Need(ctx, need)
		if err != nil {
			t.Fatal(err)
		}
		if res.State == NeedPresent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the need never answered present: %+v", res)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitFor(t, "the status to name the store and the reference", func() bool {
		s := c.Snapshot()
		return s.Store == svc.srv.store.place() && len(s.Refs) == 1 && s.Refs[0] == LinkRef{Plugin: "org.example.carried", Entry: work.Entry}
	})
	if err := c.Release(ctx, []LinkRef{{Plugin: "org.example.carried", Entry: work.Entry}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the release to reach the status", func() bool { return len(c.Snapshot().Refs) == 0 })
	next := filepath.Join(root, "placed")
	if res, err := c.PlaceStore(ctx, next); err != nil || res.Store != next {
		t.Fatalf("store.place over the link: %+v, %v", res, err)
	}
	waitFor(t, "the status to name the new place", func() bool { return c.Snapshot().Store == next })
}

// A SERVICE THAT DOES NOT SPEAK THE STORE'S MESSAGES REFUSES EACH PLUGIN
// ALONE, naming the message it lacks and both builds; the rest of the link
// is served.
func TestAServiceWithoutNeedRefusesEachPluginAlone(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-a")
	svc, err := StartServiceForTest(filepath.Join(root, "machine"), nil, MsgStatus, MsgUpdateForce)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()
	c := svc.ClientForTest(ClientOptions{Dir: svc.srv.opts.Dir, Slot: slot, Key: newKey(t), PollEvery: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, "the link", func() bool { return c.Snapshot().State == StateLinked })
	_, err = c.Need(ctx, NeedParams{Plugin: "org.example.voice"})
	var missing *MissingError
	if !errors.As(err, &missing) || missing.Message != MsgNeed || missing.Needer != NeederPlugin("org.example.voice") || missing.Service == (Build{}) || missing.Identity == (Build{}) {
		t.Fatalf("a need of a service that does not speak it: %v", err)
	}
	if err := c.Covers(NeederStatusLine, []string{MsgStatus}); err != nil {
		t.Fatalf("the rest of the link is served: %v", err)
	}
}

// THE OPERATOR'S aii machine store AT THE PROMPT: proved with the slot's
// own key on a connection of its own, and answered with the new place and
// the store it drains.
func TestPlaceAtThePromptMovesTheStore(t *testing.T) {
	root := t.TempDir()
	slot := slotIn(t, root, "identity-a")
	svc, err := StartServiceForTest(filepath.Join(root, "machine"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()
	kp := newKey(t)
	pipe := func() net.Conn {
		a, b := net.Pipe()
		go svc.srv.serveConn(svc.ctx, a)
		t.Cleanup(func() { b.Close() })
		return b
	}
	if _, err := handshakeOn(pipe(), kp, slot, Self(), true); err != nil {
		t.Fatal(err)
	}
	old := svc.Store()
	next := filepath.Join(root, "placed")
	res, err := placeOn(pipe(), kp, slot, next)
	if err != nil || res.Store != next || len(res.Draining) != 1 || res.Draining[0] != old {
		t.Fatalf("aii machine store: %+v, %v", res, err)
	}
	if got, err := StoreDir(svc.srv.opts.Dir); err != nil || got != next {
		t.Fatalf("the recorded place: %s, %v", got, err)
	}
}
