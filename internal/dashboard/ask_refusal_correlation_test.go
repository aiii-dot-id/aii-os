package dashboard

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// A refused answer to an ask is answered under the request's id, and is
// followed by the questions as they stand: with no id the page read it as
// a turn's error and left the card dead, and a refusal can mean the
// question is no longer open.
func TestARefusedAskAnswerNamesItsRequestAndCarriesTheOpenQuestions(t *testing.T) {
	open := []AskView{{ID: "ask-2", Kind: "clarify", Text: "Which week?"}}
	h := &WSHandler{
		AnswerAsk: func(AskAnswer) (string, func(bool), error) {
			return "", nil, errors.New("fixture: no question awaits an answer")
		},
		GetAsks: func() []AskView { return open },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	conn := dialWS(t, addr)

	for _, msg := range []ClientMessage{
		{Type: "ask", RequestID: "gone", Ask: &AskAnswer{ID: "ask-1", Answer: "no"}},
		{Type: "ask", RequestID: "empty"},
	} {
		sendMsg(t, conn, msg)
		if got := readMsg(t, conn); got.Type != "error" || got.RequestID != msg.RequestID {
			t.Fatalf("a refused answer got type=%q id=%q, want error/%s", got.Type, got.RequestID, msg.RequestID)
		}
		if msg.Ask == nil {
			continue
		}
		if got := readMsg(t, conn); got.Type != "asks" || len(got.Asks) != 1 || got.Asks[0].ID != "ask-2" {
			t.Fatalf("after the refusal the page got type=%q asks=%+v, want the open questions", got.Type, got.Asks)
		}
	}
}

// AN ANSWER THAT WAS NOT RECORDED IS TOLD UNDER ITS REQUEST, AND ITS QUESTION
// COMES BACK TO EVERY PAGE. The press takes the question off the list and the
// message's recording receipt settles it (h-turn-05); when the receipt says
// the answer was not recorded, the question is put back. Nothing told the
// page: the card that pressed stayed closed saying what was chosen, every
// other page's card stayed closed, and the identity's question was open with
// no card to answer it on. Now the page that pressed is told under the
// request its answer went under, in words that say the question is still
// open, and every page, one carried through a relay among them, is sent the
// questions. Both ways the receipt can say so are held: refused on the way
// (a turn that will take no more), and not recorded when the record was
// asked (a model call later). The request-less error the refusal on the way
// also sent, which the page read as a turn's error, is not sent.
func TestAnAnswerNotRecordedIsToldUnderItsRequestAndItsQuestionReachesEveryPage(t *testing.T) {
	for _, c := range []struct {
		name, why string
		hook      func(h *WSHandler, why string)
	}{
		{"refused on the way", "fixture: eight messages already wait in the turn", func(h *WSHandler, why string) {
			h.AdmitChat = func(context.Context, string) (bool, error) { return false, errors.New(why) }
		}},
		{"not recorded at the receipt", "fixture: the record would not take it", func(h *WSHandler, why string) {
			h.HandleMessage = func(ctx context.Context, _ string) (string, error) {
				// The turn goes on; only the record of the answer failed.
				ChatRecorded(ctx, nil, errors.New(why))
				return "noted", nil
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			open := true
			ask := AskView{ID: "ask-1", Kind: "choose", Text: "Aisle or window?", Choices: []string{"aisle", "window"}}
			h := &WSHandler{
				AnswerAsk: func(AskAnswer) (string, func(bool), error) {
					mu.Lock()
					open = false
					mu.Unlock()
					return "window", func(recorded bool) {
						mu.Lock()
						open = !recorded
						mu.Unlock()
					}, nil
				},
				GetAsks: func() []AskView {
					mu.Lock()
					defer mu.Unlock()
					if open {
						return []AskView{ask}
					}
					return []AskView{}
				},
				Channels: carriedChannels(t),
			}
			c.hook(h, c.why)
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Shutdown(context.Background()) })
			asker, other := dialWS(t, addr), dialWS(t, addr)
			carried := openCarriedChannelPage(t, s)
			waitForConns(t, s, 3)

			sendMsg(t, asker, ClientMessage{Type: "ask", RequestID: "press", Ask: &AskAnswer{ID: "ask-1", Answer: "chose", Choice: "window"}})
			want := "your answer was not recorded (" + c.why + "), so the question is still open."
			told := false
			for back := false; !back; {
				m := readMsg(t, asker)
				switch {
				case m.Type == "error" && m.RequestID == "press":
					if m.Message != want {
						t.Fatalf("the page that pressed was told %q, want %q", m.Message, want)
					}
					told = true
				case m.Type == "error":
					t.Fatalf("the page that pressed got an error with id %q: %q; it reads one with no id as a turn's", m.RequestID, m.Message)
				case m.Type == "asks" && len(m.Asks) == 1:
					back = true
				}
			}
			if !told {
				t.Fatal("AN ANSWER THAT WAS NOT RECORDED WAS NOT TOLD TO THE PAGE THAT PRESSED: its card says the operator chose, and the question is open")
			}
			// Nothing after it reads as a turn's error: the page's own probe
			// is answered with no error before it.
			sendMsg(t, asker, ClientMessage{Type: "probe", RequestID: "after"})
			for m := readMsg(t, asker); m.Type != "probe"; m = readMsg(t, asker) {
				if m.Type == "error" {
					t.Fatalf("the page that pressed also got an error with id %q: %q; it reads one with no id as a turn's", m.RequestID, m.Message)
				}
			}
			for {
				if m := readMsg(t, other); m.Type == "asks" && len(m.Asks) == 1 && m.Asks[0].ID == "ask-1" {
					break
				}
			}
			for {
				if m := carried.until(t, "asks", "its card stays closed on an open question"); len(m.Asks) == 1 && m.Asks[0].ID == "ask-1" {
					break
				}
			}
		})
	}
}
