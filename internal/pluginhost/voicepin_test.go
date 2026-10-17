package pluginhost

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func TestOpenSessionPinsUntilClosedOrTransportDies(t *testing.T) {
	newPair := func() (*ActivePlugin, *mockEngine, func()) {
		h2g_r, h2g_w, _ := os.Pipe()
		g2h_r, g2h_w, _ := os.Pipe()
		c := supervisor.NewSessionClient(g2h_r, h2g_w, nopDispatcher{}, 8)
		ctx, cancel := context.WithCancel(context.Background())
		go c.Run(ctx)
		eng := &mockEngine{}
		eng.statusSeq.Store(1)
		eng.playback.Store("idle")
		go eng.serve(h2g_r, g2h_w)
		ap := pluginWithTestVoice(&ActivePlugin{ID: "com.example.voice"}, NewVoiceSession(c))
		return ap, eng, func() { cancel(); h2g_w.Close(); g2h_w.Close(); h2g_r.Close(); g2h_r.Close() }
	}
	fired := func(ch <-chan struct{}, d time.Duration) bool {
		select {
		case <-ch:
			return true
		case <-time.After(d):
			return false
		}
	}
	ctx := context.Background()

	// 1. Idle but open: pinned, and not released by time passing.
	ap, eng, cleanup := newPair()
	if ap.Pinned() {
		t.Fatal("no session yet — not pinned")
	}
	if err := ap.Voice.Load().Open(ctx, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if !ap.Pinned() {
		t.Fatal("an open, IDLE session must pin the activation")
	}
	if fired(ap.PinReleased(), 300*time.Millisecond) {
		t.Fatal("a pin must not release merely because time passed")
	}
	// Close ADMISSION does not release it: the engine is aborting, and
	// the session — its endpoint, its state — is still the engine's until
	// it says otherwise.
	if err := ap.Voice.Load().Close(ctx, "abort", "operator"); err != nil {
		t.Fatal(err)
	}
	if fired(ap.PinReleased(), 300*time.Millisecond) || !ap.Pinned() {
		t.Fatal("a close that was merely ADMITTED must not release the pin")
	}
	if got := ap.Voice.Load().Label(); got != "Drained" && got != "Draining" {
		t.Fatalf("label after close admission = %q, want a closing label", got)
	}
	// The engine's terminal word releases it.
	eng.emit(`{"type":"session_end","session_id":"s1","sequence":2}`)
	if !fired(ap.PinReleased(), 5*time.Second) || ap.Pinned() {
		t.Fatal("the engine's session_end must release the pin")
	}
	if got := ap.Voice.Load().Label(); got != "Closed" {
		t.Fatalf("label after session_end = %q", got)
	}
	cleanup()

	// 2. A transport-only lane (no process behind it) releases when it
	// ends: there is no reap to wait for. The session itself is FAILED,
	// not closed — nobody reported closure.
	ap2, _, cleanup2 := newPair()
	if err := ap2.Voice.Load().Open(ctx, "s2", nil); err != nil {
		t.Fatal(err)
	}
	if !ap2.Pinned() {
		t.Fatal("open session pins")
	}
	released := ap2.PinReleased()
	cleanup2() // kills the transport: the child is gone
	if !fired(released, 5*time.Second) {
		t.Fatal("a dead transport-only lane must release the pin — nothing remains to close it")
	}
	if !ap2.Voice.Load().Faulted() || ap2.Voice.Load().Label() != "Failed" {
		t.Fatalf("a transport that ended before closure is a FAILED session: faulted=%v label=%q", ap2.Voice.Load().Faulted(), ap2.Voice.Load().Label())
	}
}

// Against a real supervised child: the pin releases on the engine's
// terminal word (the child still alive), and — separately — on the
// supervisor's verified reap of the child; never on close ADMISSION.
func TestPinReleasesOnVerifiedReapNotAdmission(t *testing.T) {
	bin := fakechildBin
	fired := func(ch <-chan struct{}, d time.Duration) bool {
		select {
		case <-ch:
			return true
		case <-time.After(d):
			return false
		}
	}
	ctx := context.Background()

	// 1. Terminal word with the child alive.
	sup, err := supervisor.Start(supervisor.Spec{PluginID: "pin.voice", Argv: []string{bin, "session"}, SessionMode: true}, nopDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	ap := &ActivePlugin{ID: "pin.voice", sup: sup}
	if err := ap.bindVoiceSession(sup); err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().Open(ctx, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if !ap.Pinned() {
		t.Fatal("open session pins")
	}
	released := ap.PinReleased()
	if err := ap.Voice.Load().Close(ctx, "abort", "operator"); err != nil {
		t.Fatal(err)
	}
	// fakechild answers the close with admission, then session_end.
	if !fired(released, 5*time.Second) {
		t.Fatal("the engine's session_end must release the pin")
	}
	if ap.Pinned() || ap.Voice.Load().Label() != "Closed" {
		t.Fatalf("after session_end: pinned=%v label=%q", ap.Pinned(), ap.Voice.Load().Label())
	}
	select {
	case <-sup.Exited():
		t.Fatal("the child must still be alive: this release came from the engine, not a reap")
	default:
	}
	ap.sessionCancel()
	sup.Close()

	// 2. Verified reap: the child is stopped with the session open and
	// no closure ever reported. The pin releases on the reap, and the
	// session is FAILED — not closed.
	sup2, err := supervisor.Start(supervisor.Spec{PluginID: "pin.voice", Argv: []string{bin, "session"}, SessionMode: true}, nopDispatcher{})
	if err != nil {
		t.Fatal(err)
	}
	ap2 := &ActivePlugin{ID: "pin.voice", sup: sup2}
	if err := ap2.bindVoiceSession(sup2); err != nil {
		t.Fatal(err)
	}
	if err := ap2.Voice.Load().Open(ctx, "s2", nil); err != nil {
		t.Fatal(err)
	}
	released2 := ap2.PinReleased()
	if fired(released2, 200*time.Millisecond) {
		t.Fatal("an open session with a live child must stay pinned")
	}
	sup2.Close() // the supervisor reaps the child
	if !fired(released2, 5*time.Second) {
		t.Fatal("the supervisor's verified reap must release the pin")
	}
	if !fired(ap2.Voice.Load().Done(), 5*time.Second) {
		t.Fatal("the application must learn the session is over")
	}
	if ap2.Voice.Load().Label() != "Failed" || !ap2.Voice.Load().Faulted() {
		t.Fatalf("a reaped child with an open session is a FAILED session: label=%q faulted=%v", ap2.Voice.Load().Label(), ap2.Voice.Load().Faulted())
	}
	if err := ap2.Voice.Load().Open(ctx, "s3", nil); err == nil {
		t.Fatal("a dead handle must refuse a new session — rebind instead")
	}
	ap2.sessionCancel()
}

// A cancellation scoped to a synthesis resolves that synthesis; only a
// session-scoped one (an aborted session's terminal word) closes the
// session and releases the pin.
func TestASynthesisScopedCancellationDoesNotCloseTheSession(t *testing.T) {
	h2g_r, h2g_w, _ := os.Pipe()
	g2h_r, g2h_w, _ := os.Pipe()
	c := supervisor.NewSessionClient(g2h_r, h2g_w, nopDispatcher{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); h2g_w.Close(); g2h_w.Close(); h2g_r.Close(); g2h_r.Close() }()
	go c.Run(ctx)
	eng := &mockEngine{}
	eng.statusSeq.Store(1)
	eng.playback.Store("idle")
	go eng.serve(h2g_r, g2h_w)
	ap := pluginWithTestVoice(&ActivePlugin{ID: "com.example.voice"}, NewVoiceSession(c))
	if err := ap.Voice.Load().Open(context.Background(), "s1", nil); err != nil {
		t.Fatal(err)
	}
	eng.emit(`{"type":"cancellation","session_id":"s1","synthesis_id":"g1","sequence":1}`)
	select {
	case <-ap.Voice.Load().Done():
		t.Fatal("a synthesis-scoped cancellation must not close the session")
	case <-time.After(300 * time.Millisecond):
	}
	if !ap.Pinned() || ap.Voice.Load().Label() == "Closed" {
		t.Fatalf("after a synthesis cancellation: pinned=%v label=%q", ap.Pinned(), ap.Voice.Load().Label())
	}
	eng.emit(`{"type":"cancellation","session_id":"s1","sequence":2}`)
	select {
	case <-ap.PinReleased():
	case <-time.After(5 * time.Second):
		t.Fatal("a session-scoped cancellation is terminal: it must release the pin")
	}
	if ap.Pinned() || ap.Voice.Load().Label() != "Closed" {
		t.Fatalf("after the session's cancellation: pinned=%v label=%q", ap.Pinned(), ap.Voice.Load().Label())
	}
}

// One handle, sessions in sequence: a second open is refused while the
// first is open, and after the engine's terminal word the next session
// starts fresh — no inherited closure, a new sequence, its own terminal.
func TestAHandleCarriesSessionsInSequenceWithFreshState(t *testing.T) {
	h2g_r, h2g_w, _ := os.Pipe()
	g2h_r, g2h_w, _ := os.Pipe()
	c := supervisor.NewSessionClient(g2h_r, h2g_w, nopDispatcher{}, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); h2g_w.Close(); g2h_w.Close(); h2g_r.Close(); g2h_r.Close() }()
	go c.Run(ctx)
	eng := &mockEngine{}
	eng.statusSeq.Store(1)
	eng.playback.Store("idle")
	go eng.serve(h2g_r, g2h_w)
	v := NewVoiceSession(c)
	bg := context.Background()
	if err := v.Open(bg, "s1", nil); err != nil {
		t.Fatal(err)
	}
	if err := v.Open(bg, "s2", nil); err == nil {
		t.Fatal("a second open while s1 is open must be refused")
	}
	eng.emit(`{"type":"session_end","session_id":"s1","sequence":7}`)
	select {
	case <-v.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("s1 never closed")
	}
	if err := v.Open(bg, "s2", nil); err != nil {
		t.Fatalf("open after terminal closure: %v", err)
	}
	if !v.IsOpen() || v.Label() != "Idle" {
		t.Fatalf("s2 starts fresh: open=%v label=%q", v.IsOpen(), v.Label())
	}
	select {
	case <-v.Done():
		t.Fatal("s2 must not inherit s1's closure")
	case <-time.After(200 * time.Millisecond):
	}
	// s1's LATE event is refused; s2's own sequence starts at one. The
	// observer is one stream across sessions, so s1's terminal event —
	// delivered before the reopen — precedes s2's first.
	eng.emit(`{"type":"transcript_final","session_id":"s1","sequence":8,"text":"late"}`)
	eng.emit(`{"type":"transcript_final","session_id":"s2","sequence":1,"text":"first"}`)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-v.Observe():
			if ev.SessionID == "s1" && ev.Type == "session_end" {
				continue // s1's own terminal word, delivered before s2 opened
			}
			if ev.SessionID != "s2" || ev.Sequence != 1 {
				t.Fatalf("the first event after s1's closure must be s2's first, got %+v", ev)
			}
		case <-deadline:
			t.Fatal("s2's event never arrived")
		}
		break
	}
	eng.emit(`{"type":"failure","session_id":"s2","sequence":2}`)
	select {
	case <-v.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("s2's failure never ended it")
	}
	if !v.Faulted() || v.Label() != "Failed" {
		t.Fatalf("after failure: faulted=%v label=%q", v.Faulted(), v.Label())
	}
}
