package dashboard

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// THE SANDBOX SWITCH IS ASKED FOR IN SO MANY WORDS, OR NOT AT ALL. A
// "sandbox_switch" carries sandbox_off, true or false. A request that
// leaves it out, sends null, or sends something that is not a boolean is
// refused and reaches no hook: read as false it would turn the sandbox
// on, read as true it would turn it off, and the operator asked for
// neither. A well-formed one reaches the hook with exactly what was
// asked, and the answer is the sandbox as the host holds it, echoing the
// request so the page that asked can tell its own answer from another's.
func TestTheSandboxSwitchRefusesAMalformedRequestAndEchoesAWellFormedOne(t *testing.T) {
	for _, c := range []struct {
		name      string
		frame     map[string]any
		refuse    bool // the hook refuses
		missing   bool // the host has no such hook
		wantCalls []bool
		wantType  string
		wantID    string
		wantSays  string
		wantOff   bool
	}{
		{name: "off", frame: map[string]any{"type": "sandbox_switch", "request_id": "r1", "sandbox_off": true}, wantCalls: []bool{true}, wantType: "sandbox", wantID: "r1", wantOff: true},
		{name: "on", frame: map[string]any{"type": "sandbox_switch", "request_id": "r2", "sandbox_off": false}, wantCalls: []bool{false}, wantType: "sandbox", wantID: "r2"},
		{name: "left out", frame: map[string]any{"type": "sandbox_switch", "request_id": "r3"}, wantType: "error", wantID: "r3", wantSays: "nothing was changed"},
		{name: "null", frame: map[string]any{"type": "sandbox_switch", "request_id": "r4", "sandbox_off": nil}, wantType: "error", wantID: "r4", wantSays: "nothing was changed"},
		{name: "a word", frame: map[string]any{"type": "sandbox_switch", "request_id": "r5", "sandbox_off": "yes"}, wantType: "error", wantSays: "invalid message format"},
		{name: "a number", frame: map[string]any{"type": "sandbox_switch", "request_id": "r6", "sandbox_off": 1}, wantType: "error", wantSays: "invalid message format"},
		{name: "the roots request's field", frame: map[string]any{"type": "sandbox_switch", "request_id": "r7", "roots": []string{"/project"}, "enabled": true}, wantType: "error", wantID: "r7", wantSays: "nothing was changed"},
		{name: "refused by the host", frame: map[string]any{"type": "sandbox_switch", "request_id": "r8", "sandbox_off": true}, refuse: true, wantCalls: []bool{true}, wantType: "error", wantID: "r8", wantSays: "fixture: not saved"},
		{name: "no such hook", frame: map[string]any{"type": "sandbox_switch", "request_id": "r9", "sandbox_off": true}, missing: true, wantType: "error", wantID: "r9", wantSays: "not available"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var calls []bool
			off := false
			h := &WSHandler{
				SetSandbox: func([]string) error {
					t.Error("a sandbox_switch reached the roots hook")
					return nil
				},
				SetSandboxOff: func(want, _ bool) error {
					mu.Lock()
					defer mu.Unlock()
					calls = append(calls, want)
					if c.refuse {
						return errors.New("fixture: not saved")
					}
					off = want
					return nil
				},
				GetSandbox: func() (*SandboxState, error) {
					mu.Lock()
					defer mu.Unlock()
					return &SandboxState{Root: "/identity", Off: off}, nil
				},
			}
			if c.missing {
				h.SetSandboxOff = nil
			}
			s := New("127.0.0.1", 0, h)
			addr, err := s.Start(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Shutdown(context.Background()) })
			conn := dialWS(t, addr)
			sendMsg(t, conn, c.frame)
			answer := readMsg(t, conn)
			if answer.Type != c.wantType || answer.RequestID != c.wantID || !strings.Contains(answer.Message, c.wantSays) {
				t.Fatalf("reply: type=%q id=%q message=%q, want %s/%s saying %q", answer.Type, answer.RequestID, answer.Message, c.wantType, c.wantID, c.wantSays)
			}
			if c.wantType == "sandbox" && (answer.Sandbox == nil || answer.Sandbox.Off != c.wantOff) {
				t.Fatalf("the answer is the sandbox as the host holds it: %+v", answer.Sandbox)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != len(c.wantCalls) || len(calls) == 1 && calls[0] != c.wantCalls[0] {
				t.Fatalf("the hook was asked %v, want %v", calls, c.wantCalls)
			}
		})
	}
}

// EVERY OTHER OPEN PAGE HEARS THE SWITCH. A page that still showed the
// box unticked after another page turned the sandbox off would be saying
// a wall stands that does not. The page that asked gets its answer with
// its request's id; every other page gets the state the host holds with
// none, which it takes as a read. After a refusal the page that asked is
// told why, and the others are still told what the host holds, because a
// refusal can follow a change that was applied.
func TestEveryOtherOpenPageHearsTheSandboxSwitch(t *testing.T) {
	var mu sync.Mutex
	off, refuse := false, false
	h := &WSHandler{
		SetSandboxOff: func(want, _ bool) error {
			mu.Lock()
			defer mu.Unlock()
			if refuse {
				return errors.New("fixture: not saved")
			}
			off = want
			return nil
		},
		GetSandbox: func() (*SandboxState, error) {
			mu.Lock()
			defer mu.Unlock()
			return &SandboxState{Root: "/identity", Off: off}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	asking, other := dialWS(t, addr), dialWS(t, addr)
	// Each page has been answered once, so each is being served.
	for id, page := range map[string]*websocket.Conn{"hello-asking": asking, "hello-other": other} {
		sendMsg(t, page, ClientMessage{Type: "query", Query: "sandbox", RequestID: id})
		if answer := readMsg(t, page); answer.Type != "sandbox" || answer.RequestID != id || answer.Sandbox == nil || answer.Sandbox.Off {
			t.Fatalf("a page reads the sandbox, on: %+v", answer)
		}
	}

	yes := true
	sendMsg(t, asking, ClientMessage{Type: "sandbox_switch", RequestID: "turn-off", SandboxOff: &yes})
	if answer := readMsg(t, asking); answer.Type != "sandbox" || answer.RequestID != "turn-off" || answer.Sandbox == nil || !answer.Sandbox.Off {
		t.Fatalf("the page that asked: %+v", answer)
	}
	if heard := readMsg(t, other); heard.Type != "sandbox" || heard.RequestID != "" || heard.Sandbox == nil || !heard.Sandbox.Off {
		t.Fatalf("the other page hears the sandbox is off, as a read: %+v", heard)
	}

	// A refusal: the page that asked is told why; the other hears what the
	// host holds, which here is what it held.
	mu.Lock()
	refuse = true
	mu.Unlock()
	no := false
	sendMsg(t, asking, ClientMessage{Type: "sandbox_switch", RequestID: "turn-on", SandboxOff: &no})
	if answer := readMsg(t, asking); answer.Type != "error" || answer.RequestID != "turn-on" || !strings.Contains(answer.Message, "fixture: not saved") {
		t.Fatalf("the page that asked is told of the refusal: %+v", answer)
	}
	if heard := readMsg(t, other); heard.Type != "sandbox" || heard.RequestID != "" || heard.Sandbox == nil || !heard.Sandbox.Off {
		t.Fatalf("after a refusal the other page hears what the host holds, still off: %+v", heard)
	}
}

// THE HOST IS TOLD WHETHER THE PAGE THAT ASKS SIGNED IN WITH THE ACCESS
// TOKEN IN FORCE, and turns the sandbox off for such a page only. While no
// token is asked for, the socket admits any client that sets a Host and
// an Origin, the identity's own shell among them: such a connection
// carried nothing, and the host hears so. A page that signed in with the
// token is said to have. And a connection admitted before a token was
// asked for is not made a signed-in one by the token being asked for
// since: it is answered no in the moment before it is closed.
func TestTheSandboxSwitchTellsTheHostWhetherThePageSignedInWithTheToken(t *testing.T) {
	var mu sync.Mutex
	var heard []bool
	h := &WSHandler{
		SetSandboxOff: func(_, signedIn bool) error {
			mu.Lock()
			defer mu.Unlock()
			heard = append(heard, signedIn)
			return nil
		},
		GetSandbox: func() (*SandboxState, error) { return &SandboxState{Root: "/identity"}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	ask := func(conn *websocket.Conn, want bool, why string) {
		t.Helper()
		sendMsg(t, conn, map[string]any{"type": "sandbox_switch", "request_id": "r", "sandbox_off": true})
		if answer := readMsg(t, conn); answer.Type != "sandbox" {
			t.Fatalf("%s: the switch was not answered: %+v", why, answer)
		}
		mu.Lock()
		defer mu.Unlock()
		if got := heard[len(heard)-1]; got != want {
			t.Fatalf("%s: the host heard signedIn=%v, want %v", why, got, want)
		}
	}

	open, err := dialWSToken(addr, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { open.CloseNow() })
	ask(open, false, "no token is asked for")

	openOnServer, early := onlySession(t, s)
	if s.signedIn(openOnServer) {
		t.Fatal("a connection that was asked for nothing signed in with nothing")
	}

	// A token is asked for from now on. The open connection is closed by
	// that (SetAccessToken); what it would be answered until then is no.
	s.SetAccessToken(true, "the-switch-tests-token")
	// A server never told what to ask for asks for nothing, and admitted
	// under no policy at all.
	if now := s.auth.Load(); early == nil || early.policy != nil && early.policy.required || !now.required {
		t.Fatalf("the earlier connection was admitted under a policy that asked for nothing: %+v", early)
	}

	signed, err := dialWSToken(addr, s.accessHash())
	if err != nil {
		t.Fatalf("the right token was refused: %v", err)
	}
	t.Cleanup(func() { signed.CloseNow() })
	ask(signed, true, "the page signed in with the token in force")
	signedOnServer, _ := onlySession(t, s)
	if !s.signedIn(signedOnServer) {
		t.Fatal("the session signed in with the token in force is not signed in")
	}

	// The token is no longer asked for: the connection stays, and what it
	// signed in with is not in force.
	s.SetAccessToken(false, "the-switch-tests-token")
	ask(signed, false, "the token is no longer asked for")
	if s.signedIn(signedOnServer) {
		t.Fatal("a session is signed in with a token no longer asked for")
	}
}

// onlySession is the one session the server holds: its carrier, the key
// signedIn is asked by, and its client. It waits until exactly one is held
// (a session the requirement cut off leaves the set as it closes).
func onlySession(t *testing.T, s *Server) (carrier, *wsClient) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.wsMu.Lock()
		var c carrier
		var cl *wsClient
		n := len(s.wsConns)
		for k, v := range s.wsConns {
			c, cl = k, v
		}
		s.wsMu.Unlock()
		if n == 1 {
			return c, cl
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server holds %d sessions, want 1", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
