package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/coder/websocket"
)

// A REPLY IS SENT TO THE PAGE AS IT IS WRITTEN, on a real
// server and socket. The turn's observer is driven as the loop drives it:
// pieces of a call, then the call's end. Every draft frame names the
// operator's message the turn answers, its seq only rises, the words are on
// the socket before the turn completes, the turn's end is a draft frame of
// its own sent before the final response, kept calls stay kept when a later
// call is withdrawn, a final text unlike its pieces is declared replaced, a
// page that connects mid-turn is sent the draft whole, and pieces told
// quickly are coalesced.

var draftOperatorRow = interaction.Location{Identity: "fixture", Source: "recorded", Incarnation: "disk", ID: "op-1"}

// draftServer runs a chat turn whose ObserveChat records the operator's
// message and hands the turn's reply observer to observe.
func draftServer(t *testing.T, observe func(ctx context.Context, re conversation.ReplyEmitter) (string, error)) (string, *Server) {
	t.Helper()
	h := &WSHandler{Speaker: "identity", HandleMessage: func(context.Context, string) (string, error) { return "", nil }, ReleaseTurn: func() {}}
	h.ObserveChat = func(ctx context.Context, _ string, _ func(string, string, string)) (string, error) {
		loc := draftOperatorRow
		ChatRecorded(ctx, &loc, nil)
		re := conversation.ReplyObserverFrom(ctx)
		if re == nil {
			return "", errors.New("the turn was handed no reply observer")
		}
		return observe(ctx, re)
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return addr, s
}

// framesUntil reads frames until stop says so, keeping every draft frame and
// the type of each frame in order.
func framesUntil(t *testing.T, conn *websocket.Conn, stop func(ServerMessage) bool) (drafts []DraftView, order []string) {
	t.Helper()
	for i := 0; i < 400; i++ {
		m := readMsg(t, conn)
		order = append(order, m.Type)
		if m.Type == "draft" && m.Draft != nil {
			drafts = append(drafts, *m.Draft)
		}
		if stop(m) {
			return drafts, order
		}
	}
	t.Fatal("the turn did not end within 400 frames")
	return nil, nil
}

func finalResponse(m ServerMessage) bool {
	return (m.Type == "response" && m.Done) || m.Type == "error"
}

func TestAReplysWordsReachThePageBeforeItsTurnEnds(t *testing.T) {
	release := make(chan struct{})
	addr, _ := draftServer(t, func(ctx context.Context, re conversation.ReplyEmitter) (string, error) {
		re.ReplyText(1, "Hel")
		<-release
		re.ReplyText(1, "lo")
		re.ReplyEnded(1, "Hello", "")
		return "Hello", nil
	})
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "hi"})
	early, _ := framesUntil(t, conn, func(m ServerMessage) bool { return m.Type == "draft" })
	if len(early) != 1 || early[0].Add != "Hel" || early[0].Ref == nil || *early[0].Ref != draftOperatorRow {
		t.Fatalf("the first words were not on the socket, naming the operator's message, before the turn went on: %+v", early)
	}
	close(release)
	drafts, order := framesUntil(t, conn, finalResponse)
	end := drafts[len(drafts)-1]
	if end.End != "kept" || end.Kept != "Hello" || !end.Snapshot {
		t.Fatalf("the turn's end is not a kept snapshot of the reply: %+v", end)
	}
	if order[len(order)-1] != "response" || order[len(order)-2] != "draft" {
		t.Fatalf("the draft's end was not sent before the final response: %v", order)
	}
	last := early[0].Seq
	for _, d := range drafts {
		if d.Seq <= last {
			t.Fatalf("seq did not rise: %d after %d", d.Seq, last)
		}
		last = d.Seq
	}
}

func TestADraftKeepsWhatWasKeptAndDeclaresWhatWasNot(t *testing.T) {
	for _, c := range []struct {
		name                      string
		turn                      func(re conversation.ReplyEmitter) (string, error)
		end, kept, withdrawn, why string
		failure                   bool
	}{
		{"a provider failing after text", func(re conversation.ReplyEmitter) (string, error) {
			re.ReplyText(1, "Half")
			re.ReplyEnded(1, "", conversation.WithdrawnProvider)
			return "", errors.New("LLM call: provider reported: the upstream closed")
		}, "withdrawn", "", "Half", conversation.WithdrawnProvider, true},
		{"a first call kept, the second stopped after text", func(re conversation.ReplyEmitter) (string, error) {
			re.ReplyText(1, "Reading the file.")
			re.ReplyEnded(1, "Reading the file.", "")
			re.ReplyText(2, "Half")
			re.ReplyEnded(2, "", conversation.WithdrawnStopped)
			return "Reading the file.", context.Canceled
		}, "kept", "Reading the file.", "Half", conversation.WithdrawnStopped, true},
		{"a final text unlike its pieces", func(re conversation.ReplyEmitter) (string, error) {
			re.ReplyText(1, "Hel")
			re.ReplyText(1, "lo")
			re.ReplyEnded(1, "Hello there.", "")
			return "Hello there.", nil
		}, "kept", "Hello there.", "Hello", WithdrawnReplaced, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr, _ := draftServer(t, func(_ context.Context, re conversation.ReplyEmitter) (string, error) { return c.turn(re) })
			conn := dialWS(t, addr)
			sendMsg(t, conn, ClientMessage{Type: "chat", Message: "go"})
			drafts, _ := framesUntil(t, conn, func(m ServerMessage) bool {
				return finalResponse(m) || (m.Type == "draft" && m.Draft != nil && m.Draft.End != "" && c.failure)
			})
			end := drafts[len(drafts)-1]
			var withdrawn, why string
			if n := len(end.Withdrawals); n > 0 {
				withdrawn, why = end.Withdrawals[n-1].Words, end.Withdrawals[n-1].Why
			}
			if end.End != c.end || end.Kept != c.kept || withdrawn != c.withdrawn || why != c.why {
				t.Fatalf("the draft ended %q kept %q withdrawn %q (%s); want %q kept %q withdrawn %q (%s)", end.End, end.Kept, withdrawn, why, c.end, c.kept, c.withdrawn, c.why)
			}
		})
	}
}

func TestAPageThatConnectsMidTurnIsSentTheDraftWhole(t *testing.T) {
	hold, release := make(chan struct{}), make(chan struct{})
	addr, _ := draftServer(t, func(_ context.Context, re conversation.ReplyEmitter) (string, error) {
		re.ReplyText(1, "Reading the file.")
		re.ReplyEnded(1, "Reading the file.", "")
		re.ReplyText(2, "Then ")
		close(hold)
		<-release
		re.ReplyText(2, "done.")
		re.ReplyEnded(2, "Then done.", "")
		return "Reading the file.\n\nThen done.", nil
	})
	first := dialWS(t, addr)
	sendMsg(t, first, ClientMessage{Type: "chat", Message: "go"})
	<-hold
	// The second page connects once the first has been sent call 2's words.
	framesUntil(t, first, func(m ServerMessage) bool { return m.Type == "draft" && m.Draft != nil && m.Draft.Call == 2 })
	second := dialWS(t, addr)
	got, _ := framesUntil(t, second, func(m ServerMessage) bool { return m.Type == "draft" })
	d := got[0]
	if !d.Snapshot || d.Kept != "Reading the file." || d.Call != 2 || d.Add != "Then " || d.Ref == nil {
		t.Fatalf("the page that connected mid-turn was not sent the draft whole: %+v", d)
	}
	close(release)
	framesUntil(t, second, finalResponse)
}

func TestPiecesToldQuicklyAreCoalesced(t *testing.T) {
	const pieces = 300
	addr, _ := draftServer(t, func(_ context.Context, re conversation.ReplyEmitter) (string, error) {
		for i := 0; i < pieces; i++ {
			re.ReplyText(1, "w ")
			time.Sleep(time.Millisecond)
		}
		re.ReplyEnded(1, strings.Repeat("w ", pieces), "")
		return strings.Repeat("w ", pieces), nil
	})
	conn := dialWS(t, addr)
	start := time.Now()
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "go"})
	drafts, _ := framesUntil(t, conn, finalResponse)
	most := int(time.Since(start)/draftEvery) + 2
	if len(drafts) > most {
		t.Fatalf("%d pieces sent as %d frames over %s; at most %d expected at one per %s", pieces, len(drafts), time.Since(start), most, draftEvery)
	}
	if end := drafts[len(drafts)-1]; end.Kept != strings.Repeat("w ", pieces) {
		t.Fatalf("the coalesced draft lost words: kept %d bytes", len(end.Kept))
	}
}

// A turn that tells nothing (a reply read whole, or a request with an output
// contract, which the loop never tells) sends no draft frame.
func TestATurnThatTellsNothingSendsNoDraft(t *testing.T) {
	addr, s := draftServer(t, func(context.Context, conversation.ReplyEmitter) (string, error) { return "Whole.", nil })
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "go"})
	_, order := framesUntil(t, conn, finalResponse)
	for _, typ := range order {
		if typ == "draft" {
			t.Fatalf("a turn that told nothing sent a draft: %v", order)
		}
	}
	s.draftMu.Lock()
	d := s.draft
	s.draftMu.Unlock()
	select {
	case <-d.done:
	case <-time.After(time.Second):
		t.Fatal("the draft's publisher did not stop with its turn")
	}
}

// A page that connects just after the turn ended is sent the ended draft:
// kept when the turn kept text, withdrawn when it kept none.
func TestAPageThatConnectsAtTheEndIsSentTheEnd(t *testing.T) {
	for _, c := range []struct {
		name, end string
		turn      func(re conversation.ReplyEmitter) (string, error)
	}{
		{"kept", "kept", func(re conversation.ReplyEmitter) (string, error) {
			re.ReplyText(1, "Done.")
			re.ReplyEnded(1, "Done.", "")
			return "Done.", nil
		}},
		{"nothing kept", "withdrawn", func(re conversation.ReplyEmitter) (string, error) {
			re.ReplyText(1, "Half")
			re.ReplyEnded(1, "", conversation.WithdrawnProvider)
			return "", errors.New("LLM call: provider reported: the upstream closed")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			addr, _ := draftServer(t, func(_ context.Context, re conversation.ReplyEmitter) (string, error) { return c.turn(re) })
			first := dialWS(t, addr)
			sendMsg(t, first, ClientMessage{Type: "chat", Message: "go"})
			framesUntil(t, first, finalResponse)
			second := dialWS(t, addr)
			got, _ := framesUntil(t, second, func(m ServerMessage) bool { return m.Type == "draft" })
			if d := got[0]; !d.Snapshot || d.End != c.end || d.Ref == nil {
				t.Fatalf("a page connecting at the end was sent %+v, want a snapshot ending %q", d, c.end)
			}
		})
	}
}

// A page that stops reading does not hold the provider: the observer is told
// every piece at once while that page's writes stall, the turn ends, and the
// draft's publisher stops (the stalled page is dropped by the write bound).
func TestAStalledPageDoesNotHoldTheProvider(t *testing.T) {
	big := strings.Repeat("words of a long reply ", 12<<10) // ~250 KB a piece
	var slowest time.Duration
	addr, s := draftServer(t, func(_ context.Context, re conversation.ReplyEmitter) (string, error) {
		for i := 0; i < 40; i++ {
			start := time.Now()
			re.ReplyText(1, big)
			slowest = max(slowest, time.Since(start))
			time.Sleep(draftEvery)
		}
		re.ReplyEnded(1, "", conversation.WithdrawnStopped)
		return "", context.Canceled
	})
	stalled := dialWS(t, addr) // never read again: its writes fill the socket and stall
	_ = stalled
	reader := dialWS(t, addr)
	sendMsg(t, reader, ClientMessage{Type: "chat", Message: "go"})
	deadline := time.Now().Add(30 * time.Second)
	for {
		s.draftMu.Lock()
		d := s.draft
		s.draftMu.Unlock()
		if d != nil {
			select {
			case <-d.done:
				// A provider that waited on a page would wait out the stalled
				// page's write bound; half of it is more than a loaded host
				// takes and less than any such wait.
				if slowest > writeWait/2 {
					t.Fatalf("telling a piece took %s while a page stalled (a page's write bound is %s)", slowest, writeWait)
				}
				return
			default:
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the turn's draft did not stop while a page stalled")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A page that connects while a frame is still owed is sent the draft as the
// last frame left it, at that frame's seq: the owed text comes in the next
// frame, once. The publisher is driven by hand here, so the page connects
// inside that window.
func TestAConnectSnapshotIsTheDraftAsLastSent(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	ctx := context.WithValue(context.Background(), turnRefKey{}, &turnRef{ref: &draftOperatorRow})
	d := &replyDraft{s: s, ctx: ctx, wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{})}
	s.draft = d
	d.ReplyText(1, "A")
	first, _ := d.next()
	d.ReplyText(1, "B") // owed: no frame has carried it
	conn, ok := s.draftForConnect()
	if !ok {
		t.Fatal("a page that connected mid-turn was sent no draft")
	}
	if got := conn.Draft; got.Seq != first.Draft.Seq || got.Add != "A" || !got.Snapshot {
		t.Fatalf("the connect snapshot is not the draft as last sent: seq %d add %q (the last frame was seq %d)", got.Seq, got.Add, first.Draft.Seq)
	}
	next, _ := d.next()
	if next.Draft.Seq != first.Draft.Seq+1 || next.Draft.Add != "B" || conn.Draft.Add+next.Draft.Add != "AB" {
		t.Fatalf("the next frame does not follow the connect snapshot once: %+v", next.Draft)
	}
}

// Every withdrawal of a turn is kept with its call and its cause: in the
// turn's snapshots, and in what a page that connects at the end is sent.
func TestEveryWithdrawalOfATurnIsKept(t *testing.T) {
	addr, _ := draftServer(t, func(_ context.Context, re conversation.ReplyEmitter) (string, error) {
		re.ReplyText(1, "Before ")
		re.ReplyEnded(1, "", conversation.WithdrawnAskedAgain)
		re.ReplyText(2, "Hel")
		re.ReplyEnded(2, "Hello there.", "")
		return "Hello there.", nil
	})
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{Type: "chat", Message: "go"})
	drafts, _ := framesUntil(t, conn, finalResponse)
	want := []DraftWithdrawal{{Call: 1, Words: "Before ", Why: conversation.WithdrawnAskedAgain}, {Call: 2, Words: "Hel", Why: WithdrawnReplaced}}
	same := func(got []DraftWithdrawal) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if end := drafts[len(drafts)-1]; !same(end.Withdrawals) {
		t.Fatalf("the turn's end kept %+v, want both withdrawals %+v", end.Withdrawals, want)
	}
	later := dialWS(t, addr)
	got, _ := framesUntil(t, later, func(m ServerMessage) bool { return m.Type == "draft" })
	if !same(got[0].Withdrawals) {
		t.Fatalf("a page connecting at the end was sent %+v, want both withdrawals", got[0].Withdrawals)
	}
}

// stalledPage is a page that has stopped reading: each write waits out its
// deadline and fails, as a write to a full socket does.
type stalledPage struct{ n int }

func (*stalledPage) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	<-ctx.Done()
	return 0, nil, ctx.Err()
}
func (*stalledPage) Write(ctx context.Context, _ websocket.MessageType, _ []byte) error {
	<-ctx.Done()
	return ctx.Err()
}
func (*stalledPage) Close(websocket.StatusCode, string) error { return nil }
func (*stalledPage) CloseNow() error                          { return nil }

// The turn's end waits about two write bounds however many pages have
// stopped reading: the draft frame in flight and the end each go to every
// page at once. Eight pages stop reading once the turn runs, and are held in
// the draft's frame when the turn ends; one after another they would cost a
// write bound each. (Every other frame, the turn's start among them, is the
// serial broadcast's, and pays a write bound per stalled page as before.)
func TestATurnsEndIsBoundedWhilePagesStall(t *testing.T) {
	running, stalled := make(chan struct{}), make(chan struct{})
	ended := make(chan time.Time, 1)
	addr, s := draftServer(t, func(_ context.Context, re conversation.ReplyEmitter) (string, error) {
		close(running)
		<-stalled
		re.ReplyText(1, "Written while eight pages have stopped reading.")
		time.Sleep(2 * draftEvery) // the draft's frame is out, held by the stalled pages
		re.ReplyEnded(1, "Written while eight pages have stopped reading.", "")
		ended <- time.Now()
		return "Written while eight pages have stopped reading.", nil
	})
	reader := dialWS(t, addr)
	sendMsg(t, reader, ClientMessage{Type: "chat", Message: "go"})
	<-running
	s.wsMu.Lock()
	for i := 0; i < 8; i++ {
		s.wsConns[&stalledPage{n: i}] = &wsClient{}
	}
	s.wsMu.Unlock()
	close(stalled)
	framesUntil(t, reader, finalResponse)
	took := time.Since(<-ended)
	if most := 2*writeWait + time.Second; took > most {
		t.Fatalf("the turn's end took %s with eight pages stalled; at most %s", took, most)
	}
}
