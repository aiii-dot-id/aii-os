package app

import (
	"context"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// SPOKEN WORDS THAT WAIT FOR A RUNNING TURN ARE SHOWN WAITING ON EVERY
// SCREEN, AS TYPED ONES ARE. The page clears its transcript line at the
// operator's final, because those words are a message they sent and the
// record shows them. Steered into a running turn, they reach the record
// only when the turn next takes its mailbox, which is after the model call
// in flight returns: minutes, with a slow model. A typed message in the same
// place is announced to every screen as waiting; spoken words were announced
// to none, so for those minutes the page showed nothing of them at all.
func TestSpokenWordsQueuedBehindARunningTurnAreShownWaitingOnEveryScreen(t *testing.T) {
	a := newVoiceApp(t)
	srv := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{PendingSteers: a.PendingSteers})
	if _, err := srv.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	a.dashboard = srv
	pages := []*websocket.Conn{openPage(t, srv), openPage(t, srv)}
	// PageOpen means ANY page, so the first page satisfies openPage's check
	// for the second before the second's handshake has reached
	// registration, and a broadcast made then is sent to one page only and
	// never again. A reply on each socket proves that connection reached
	// its read loop (notices_test.go does the same, for the same reason).
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	for _, page := range pages {
		if err := wsjsonWrite(ctx, page, map[string]any{"type": "probe", "request_id": "words-ready"}); err != nil {
			t.Fatal(err)
		}
		readUntil(t, page, func(m *dashboard.ServerMessage) bool { return m.Type == "probe" && m.RequestID == "words-ready" })
	}

	// A turn is running: the identity is inside a model call.
	if !a.TryBeginTurn() {
		t.Fatal("could not take the turn gate")
	}
	defer a.releaseTurn()

	const said = "is the build green"
	if err := a.observeVoice(context.Background(), heardUtterance{Source: "speech engine", Text: said, Answer: true, SessionID: "vs-waiting", Sequence: 1, Gen: 1, Operator: true}); err != nil {
		t.Fatal(err)
	}
	waiting := func(m *dashboard.ServerMessage) bool { return m.Type == "steering" && len(m.Pending) > 0 }
	for i, page := range pages {
		msg := readUntil(t, page, waiting)
		if len(msg.Pending) != 1 || msg.Pending[0] != voiceMarker+said {
			t.Fatalf("screen %d was shown %q waiting, and the operator said %q", i, msg.Pending, said)
		}
	}

	// The turn takes its mailbox at the next tool call: the words are the
	// turn's, and every screen sees the queue empty.
	if taken := a.DrainSteering(); len(taken) != 1 || taken[0] != voiceMarker+said {
		t.Fatalf("the running turn took %q", taken)
	}
	for i, page := range pages {
		if msg := readUntil(t, page, func(m *dashboard.ServerMessage) bool { return m.Type == "steering" }); len(msg.Pending) != 0 {
			t.Fatalf("screen %d still shows %q waiting after the turn took the words", i, msg.Pending)
		}
	}
}
