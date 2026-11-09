package dashboard

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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

// A QUESTION THAT STAYS ANSWERED IS NOT TOLD TO THE PAGE AS OPEN. A Yes on a voice card
// has named its voice whatever became of the words, so when its message is not recorded
// the record still has the question closed. The page learns that from the record: it is
// not told the question is open, and no page gets the card back.
func TestAFailedYesIsNotToldAsAnOpenQuestion(t *testing.T) {
	const why = "fixture: the record would not take it"
	settled := make(chan bool, 1)
	h := &WSHandler{
		AnswerAsk: func(AskAnswer) (string, func(bool), error) {
			return "yes, that voice is mine", func(recorded bool) { settled <- recorded }, nil
		},
		GetAsks: func() []AskView { return []AskView{} },
		HandleMessage: func(ctx context.Context, _ string) (string, error) {
			ChatRecorded(ctx, nil, errors.New(why))
			return "noted", nil
		},
		Channels: carriedChannels(t),
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	asker := dialWS(t, addr)
	waitForConns(t, s, 1)
	sendMsg(t, asker, ClientMessage{Type: "ask", RequestID: "press", Ask: &AskAnswer{ID: "ask-voice-1", Answer: "confirm"}})
	select {
	case recorded := <-settled:
		if recorded {
			t.Fatal("the fixture's message was recorded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the answer was never settled")
	}
	// The asks frame after the settle, and no error before it.
	for lists := 0; lists < 2; {
		m := readMsg(t, asker)
		if m.Type == "error" && m.RequestID == "press" {
			t.Fatalf("the page was told %q about a question the record has closed", m.Message)
		}
		if m.Type == "asks" {
			lists++
			if len(m.Asks) != 0 {
				t.Fatalf("a closed question came back: %+v", m.Asks)
			}
		}
	}
}

// A VOICE NAMED IN SETTINGS CHANGES WHICH QUESTIONS ARE OPEN ON EVERY SCREEN. A connected
// second screen would otherwise keep showing a card about a voice already named.
func TestNamingAVoiceInSettingsSendsEveryScreenTheQuestions(t *testing.T) {
	for name, change := range map[string]map[string]any{
		"alone":               {"speech.operator_speakers": []any{"a-voice"}},
		"with a speech field": {"speech.operator_speakers": []any{"a-voice"}, "speech.stt.provider": "fixture"},
		"with the substrate":  {"speech.operator_speakers": []any{"a-voice"}, "llm.model": "fixture"},
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			open := []AskView{{ID: "voice-1", Kind: "voice", Text: "Was that you?"}}
			h := &WSHandler{
				GetAsks: func() []AskView {
					mu.Lock()
					defer mu.Unlock()
					return open
				},
				SetConfig: func(map[string]any) (*ConfigState, error) {
					mu.Lock()
					defer mu.Unlock()
					open = []AskView{}
					return &ConfigState{}, nil
				},
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Shutdown(context.Background()) })
			settings, other := dialWS(t, addr), dialWS(t, addr)
			waitForConns(t, s, 2)
			sendMsg(t, settings, ClientMessage{Type: "config_set", RequestID: "name", Config: change})
			for {
				m := readMsg(t, other)
				if m.Type == "asks" {
					if len(m.Asks) != 0 {
						t.Fatalf("the other screen still holds %+v", m.Asks)
					}
					return
				}
			}
		})
	}
}

// A save that publishes a name and reports a durability error has changed which questions
// are open, as a clean save has, whichever way the save went out.
func TestASaveThatPublishesWithAnErrorSendsTheQuestions(t *testing.T) {
	for name, change := range map[string]map[string]any{
		"alone":               {"speech.operator_speakers": []any{"a-voice"}},
		"with a speech field": {"speech.operator_speakers": []any{"a-voice"}, "speech.stt.provider": "fixture"},
	} {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			open := []AskView{{ID: "voice-1", Kind: "voice", Text: "Was that you?"}}
			h := &WSHandler{
				GetAsks: func() []AskView {
					mu.Lock()
					defer mu.Unlock()
					return open
				},
				SetConfig: func(map[string]any) (*ConfigState, error) {
					mu.Lock()
					defer mu.Unlock()
					open = []AskView{}
					return nil, errors.New("fixture: published, and the folder did not sync")
				},
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Shutdown(context.Background()) })
			settings, other := dialWS(t, addr), dialWS(t, addr)
			waitForConns(t, s, 2)
			sendMsg(t, settings, ClientMessage{Type: "config_set", RequestID: "name", Config: change})
			for {
				m := readMsg(t, other)
				if m.Type == "asks" {
					if len(m.Asks) != 0 {
						t.Fatalf("the other screen still holds %+v", m.Asks)
					}
					return
				}
			}
		})
	}
}
