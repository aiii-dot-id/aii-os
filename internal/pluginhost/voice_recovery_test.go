package pluginhost

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestAutomaticVoiceRecoveryKeepsRoutesAndOldHandlesDistinct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "recover.voice", Argv: []string{fakechildBin, "session-audio"}, SessionMode: true, AudioPair: true}, nopDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Close() })
	bound := make(chan *VoiceSession, 3)
	ap := &ActivePlugin{ID: "recover.voice", sup: sup, voicePublished: func(v *VoiceSession) { bound <- v }}
	if err := ap.bindVoiceSession(sup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ap.sessionCancel(); <-ap.sessionDone })
	first := <-bound
	ap.admitted.Store(true)
	op := ap.newOperationTool("speaker_probe", "enroll", "probe", nil, tools.Discovery{})
	reg := newRegistry(t)
	reg.Register(op)
	invoke := func() {
		t.Helper()
		result, err := reg.Execute(ctx, op.Name(), nil)
		if err != nil || result.Error != "" || !strings.Contains(result.Output, `"accepted":true`) {
			t.Fatalf("stable route failed: %v %+v", err, result)
		}
	}
	invoke()
	if err := first.Open(ctx, "before", nil); err != nil {
		t.Fatal(err)
	}
	// Freeze an operation after it selected the old handle, then crash the
	// child. Even with a replacement ready it must not replay on that child.
	chosen, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var interrupted error
	inv := sessionInvoker{voice: func() *VoiceSession {
		v := ap.Voice.Load()
		close(chosen)
		select {
		case <-resume:
		case <-ctx.Done():
		}
		return v
	}}
	go func() {
		defer close(finished)
		_, interrupted = inv.Invoke(ctx, []byte(`{"params":{"operation":"enroll","arguments":{}}}`))
	}()
	t.Cleanup(func() { cancel(); <-finished })
	<-chosen
	oldPin := first.PinReleased()
	if killed, err := sup.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	var second *VoiceSession
	select {
	case second = <-bound:
	case <-ctx.Done():
		t.Fatal("replacement child was never bound automatically")
	}
	if second == first || second.exited == first.exited || second.pair == nil || second.pair == first.pair {
		t.Fatal("replacement mixed transport ownership")
	}
	close(resume)
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("interrupted operation did not settle")
	}
	if !errors.Is(interrupted, ErrSessionDead) {
		t.Fatalf("old operation was replayed or lost its failure: %v", interrupted)
	}
	select {
	case <-first.endedCh:
	default:
		t.Fatal("old handle did not terminate before replacement")
	}
	select {
	case <-oldPin:
	case <-ctx.Done():
		t.Fatal("old child's verified reap did not release its pin")
	}
	if err := first.Open(ctx, "must-not-revive", nil); !errors.Is(err, ErrSessionDead) {
		t.Fatalf("old handle revived: %v", err)
	}
	invoke() // the SAME registry entry, built before the crash
	if err := second.Open(ctx, "after", nil); err != nil {
		t.Fatal(err)
	}
	ap.sessionCancel()
	select {
	case <-ap.sessionDone:
	case <-ctx.Done():
		t.Fatal("session owner did not join")
	}
	if second.Alive() {
		t.Fatal("joined owner left a live session")
	}
	// Even if the process owner later restarts, a stopped session owner
	// cannot resurrect the activation's transport.
	if killed, err := sup.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	if _, err := sup.NextSession(ctx, second.exited); err != nil {
		t.Fatal(err)
	}
	if ap.Voice.Load() != second {
		t.Fatal("stopped owner published a later child")
	}
}

func TestVoicePinWaitsForItsChildNotItsTransport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reaped := make(chan struct{})
		v := &VoiceSession{exited: reaped, terminalCh: make(chan struct{}), endedCh: make(chan struct{})}
		close(v.endedCh)
		released := v.PinReleased()
		synctest.Wait()
		select {
		case <-released:
			t.Fatal("transport failure was mistaken for physical release")
		default:
		}
		close(reaped)
		synctest.Wait()
		select {
		case <-released:
		default:
			t.Fatal("the owning child's reap did not release its pin")
		}
	})
}

func TestVoiceBindingUsesOneProvidedChildSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "snapshot.voice", Argv: []string{fakechildBin, "session-audio"}, SessionMode: true, AudioPair: true}, nopDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Close() })
	old, ok := sup.SessionConnection()
	if !ok {
		t.Fatal("fixture has no child")
	}
	if killed, err := sup.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	if _, err := sup.NextSession(ctx, old.Exited); err != nil {
		t.Fatal(err)
	}
	// The supervisor now holds another child. Binding the supplied snapshot
	// must not silently borrow that child's audio or reap handle.
	ap := &ActivePlugin{}
	v := ap.voiceForConnection(old, sup)
	stopped, stop := context.WithCancel(ctx)
	stop()
	_ = v.c.Run(stopped)
	<-v.endedCh
	if v.exited != old.Exited || v.pair == nil || v.pair.in != old.AudioIn || v.pair.out != old.AudioOut {
		t.Fatal("binding mixed the supplied child with the supervisor's new child")
	}
}

type blockedSessionBroker struct {
	entered chan struct{}
	left    chan struct{}
	calls   atomic.Int32
}

func (b *blockedSessionBroker) Dispatch(ctx context.Context, _ string, _ []byte) ([]byte, error) {
	b.calls.Add(1)
	close(b.entered)
	<-ctx.Done()
	close(b.left)
	return nil, ctx.Err()
}

func TestVoiceRestartCancelsThePredecessorsBrokerCall(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b := &blockedSessionBroker{entered: make(chan struct{}), left: make(chan struct{})}
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "broker.voice", Argv: []string{fakechildBin, "hostcall"}, SessionMode: true}, b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sup.Close() })
	bound := make(chan *VoiceSession, 3)
	ap := &ActivePlugin{ID: "broker.voice", sup: sup, voicePublished: func(v *VoiceSession) { bound <- v }}
	if err := ap.bindVoiceSession(sup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ap.sessionCancel(); <-ap.sessionDone })
	first := <-bound
	answered := make(chan error, 1)
	go func() { _, err := first.control(ctx, "probe", nil); answered <- err }()
	select {
	case <-b.entered:
	case <-ctx.Done():
		t.Fatal("broker call never entered")
	}
	if killed, err := sup.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	select {
	case <-b.left:
	case <-ctx.Done():
		t.Fatal("old child's broker call survived its transport")
	}
	select {
	case next := <-bound:
		if next == first {
			t.Fatal("old handle reused")
		}
	case <-ctx.Done():
		t.Fatal("no automatic replacement")
	}
	select {
	case err := <-answered:
		if err == nil {
			t.Fatal("interrupted call reported success")
		}
	case <-ctx.Done():
		t.Fatal("old call did not settle")
	}
	if b.calls.Load() != 1 {
		t.Fatal("interrupted hostcall was replayed")
	}
}
