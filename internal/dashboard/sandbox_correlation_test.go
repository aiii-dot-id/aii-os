package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSandboxRepliesEchoRequestOnSuccessAndFailure(t *testing.T) {
	for _, kind := range []string{"save", "refused", "unavailable", "read-failed", "read-unavailable", "query"} {
		t.Run(kind, func(t *testing.T) {
			h := &WSHandler{
				ChangeSandbox: func(string, bool) error {
					if kind == "refused" {
						return errors.New("fixture refusal")
					}
					return nil
				},
				GetSandbox: func() (*SandboxState, error) {
					if kind == "read-failed" {
						return nil, errors.New("fixture read failure")
					}
					return &SandboxState{Root: "/identity"}, nil
				},
			}
			if kind == "unavailable" {
				h.ChangeSandbox = nil
			}
			if kind == "read-unavailable" {
				h.GetSandbox = nil
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Shutdown(context.Background()) })
			conn := dialWS(t, addr)
			msg := ClientMessage{Type: "sandbox_add", RequestID: "sandbox-" + kind, Root: "/project"}
			if kind == "query" {
				msg.Type = "query"
				msg.Query = "sandbox"
			}
			sendMsg(t, conn, msg)
			answer := readMsg(t, conn)
			want := "error"
			if kind == "save" || kind == "query" {
				want = "sandbox"
			}
			if answer.Type != want || answer.RequestID != msg.RequestID {
				t.Fatalf("reply: type=%q id=%q, want %s/%s", answer.Type, answer.RequestID, want, msg.RequestID)
			}
		})
	}
}

// A change to the roots reaches every page, one carried through a relay
// among them, and a refusal is followed by the list as it is: the page
// that was refused may be showing an old one.
func TestASandboxChangeReachesEveryPageAndARefusalCarriesTheList(t *testing.T) {
	roots := []string{"/client"}
	h := &WSHandler{
		ChangeSandbox: func(root string, add bool) error {
			if !add {
				return errors.New("fixture: not among the extra roots")
			}
			roots = append(roots, root)
			return nil
		},
		GetSandbox: func() (*SandboxState, error) {
			return &SandboxState{Root: "/identity", ExtraRoots: append([]string(nil), roots...)}, nil
		},
		Channels: carriedChannels(t),
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	asker, other := dialWS(t, addr), dialWS(t, addr)
	carried := openCarriedChannelPage(t, s)
	waitForConns(t, s, 3)

	sendMsg(t, asker, ClientMessage{Type: "sandbox_add", RequestID: "add", Root: "/notes"})
	if got := readMsg(t, asker); got.Type != "sandbox" || got.RequestID != "add" || got.Sandbox == nil || len(got.Sandbox.ExtraRoots) != 2 {
		t.Fatalf("the asking page got type=%q id=%q sandbox=%+v, want the new list under its own id", got.Type, got.RequestID, got.Sandbox)
	}
	if got := readMsg(t, other); got.Type != "sandbox" || got.RequestID != "" || got.Sandbox == nil || len(got.Sandbox.ExtraRoots) != 2 {
		t.Fatalf("the other page got type=%q id=%q sandbox=%+v, want the new list: it omits a root the identity now reaches", got.Type, got.RequestID, got.Sandbox)
	}
	if got := carried.until(t, "sandbox", "it omits a root the identity now reaches"); got.RequestID != "" || got.Sandbox == nil || len(got.Sandbox.ExtraRoots) != 2 {
		t.Fatalf("the carried page got id=%q sandbox=%+v, want the new list", got.RequestID, got.Sandbox)
	}

	sendMsg(t, asker, ClientMessage{Type: "sandbox_remove", RequestID: "gone", Root: "/removed-elsewhere"})
	if got := readMsg(t, asker); got.Type != "error" || got.RequestID != "gone" {
		t.Fatalf("a refused removal answered type=%q id=%q, want error/gone", got.Type, got.RequestID)
	}
	if got := readMsg(t, asker); got.Type != "sandbox" || got.RequestID != "" || got.Sandbox == nil || len(got.Sandbox.ExtraRoots) != 2 {
		t.Fatalf("after a refusal the page got type=%q id=%q sandbox=%+v, want the list as it is", got.Type, got.RequestID, got.Sandbox)
	}
}

// A PAGE FROM ANOTHER VERSION IS ANSWERED UNDER ITS OWN REQUEST. A page
// left open across an upgrade asks for what this version no longer does
// (here the whole list of roots, sandbox_set, which the one-root acts
// replaced); one left open across a downgrade asks for what this version
// does not do yet. The answer says neither which is older. Its pending card
// claims an answer by its request id, so an answer with none left the card
// waiting, Add and Remove disabled, until the page was reloaded, and the
// page read the error as a turn's. The answer names the request and says
// what to do. Nothing is changed.
func TestAMessageThisVersionDoesNotKnowIsAnsweredUnderItsRequest(t *testing.T) {
	acted := false
	s := New("127.0.0.1", 0, &WSHandler{
		ChangeSandbox: func(string, bool) error { acted = true; return nil },
		GetSandbox:    func() (*SandboxState, error) { return &SandboxState{Root: "/identity"}, nil },
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	conn := dialWS(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The frame as an older page's sandbox card wrote it.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"sandbox_set","request_id":"old-page-7","roots":["/project"]}`)); err != nil {
		t.Fatal(err)
	}
	got := readMsg(t, conn)
	want := "This page and the running program are different versions, and the page asked for something the program does not do (sandbox_set). Reload the page."
	if got.Type != "error" || got.RequestID != "old-page-7" || got.Message != want {
		t.Fatalf("a page from another version: its sandbox_set was answered type=%q id=%q %q, want error under its request id: %q", got.Type, got.RequestID, got.Message, want)
	}
	if acted {
		t.Fatal("an older page's whole list reached the roots")
	}
}
