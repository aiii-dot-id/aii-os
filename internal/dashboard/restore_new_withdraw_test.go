package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// THE WAY BACK FROM FIRST BOOT'S RESTORE DOOR, AT THE SERVER. Hello says
// whose restore stands on the machine, so a reloaded page draws it. That
// names the identity, so it goes only to a connection the door's privacy
// rule admits; on any other, hello says what it always said, whether the
// door may be used at all. A withdrawal is an act of the door like the
// others, taken only on a private connection and off the read loop; a
// refusal is said in the runtime's own words; and a runtime that gives no
// such act says so.
func TestKeysAreWithdrawnAtTheNewMachineDoor(t *testing.T) {
	const whole = "501265c63ca1065fb83d2036b58471092d8994505012aaaa5012bbbb5012cccc"
	const received = "ledger-20260920T181500Z-seq1410.tar.age"
	state := &RestoreNewBegun{Identity: whole, Received: []string{received}}
	withdrawals, asked, offLoop := 0, 0, 0
	var refuse error
	hooks := &RestoreNewHooks{
		Keys:    func(_, _ []byte) (RestoreNewKeys, error) { return RestoreNewKeys{}, nil },
		Upload:  func(_, _ string, _ io.Reader) error { return nil },
		Restore: func(context.Context, string, *GenesisRequest) (RestoreNewDone, error) { return RestoreNewDone{}, nil },
		Begun:   func() *RestoreNewBegun { asked++; return state },
		Withdraw: func() ([]string, error) {
			withdrawals++
			if refuse != nil {
				return nil, refuse
			}
			state = nil
			return []string{received}, nil
		},
	}
	background := func(work func()) bool { offLoop++; work(); return true }
	s := New("127.0.0.1", 0, &WSHandler{RestoreNew: hooks, Background: background})
	ask := func(srv *Server, private bool, action string) (got *RestoreNewReply, refused string) {
		req := RestoreNewRequest{Action: action}
		srv.answerRestoreNew(context.Background(), ClientMessage{Type: "restore_new", RestoreNew: &req}, private,
			func(r RestoreNewReply) { got = &r }, func(text string) { refused = text })
		return got, refused
	}
	if r, _ := ask(s, true, "hello"); r == nil || r.Begun == nil || r.Begun.Identity != whole || len(r.Begun.Received) != 1 || !r.SecretsOK {
		t.Fatalf("HELLO DOES NOT SAY WHAT STANDS HERE: a reloaded page would draw the first step over a restore begun: %+v", r)
	}
	// A CONNECTION THAT IS NOT PRIVATE IS TOLD NOTHING OF THE IDENTITY, and
	// the runtime is not even asked. The whole frame is looked at, as the
	// wire carries it. The connection: plain HTTP from another machine, as
	// the server's own rule reads it at the socket's handshake.
	away := httptest.NewRequest(http.MethodGet, "http://192.0.2.7:8080/ws", nil)
	away.RemoteAddr = "192.0.2.7:51000"
	if privateConnection(away) {
		t.Fatal("rig: plain HTTP from another machine was read as a private connection")
	}
	before := asked
	r, _ := ask(s, privateConnection(away), "hello")
	if r == nil || r.SecretsOK || r.Error != "" || r.Begun != nil || asked != before {
		t.Fatalf("HELLO ON A CONNECTION THAT IS NOT PRIVATE CARRIED WHAT STANDS HERE: %+v (asked %d times)", r, asked-before)
	}
	if frame, err := json.Marshal(ServerMessage{Type: "restore_new", RestoreNew: r}); err != nil || strings.Contains(string(frame), whole[:8]) || strings.Contains(string(frame), "begun") {
		t.Fatalf("the frame for a connection that is not private names the identity: %s %v", frame, err)
	}
	if r, _ := ask(s, false, "withdraw"); r == nil || !strings.Contains(r.Error, "not private") || r.Withdrawn || withdrawals != 0 {
		t.Fatalf("a withdrawal from a connection that is not private reached the host: %+v (%d)", r, withdrawals)
	}
	refuse = &RestoreNewRefusal{Reason: RestoreNewNotFirstBoot, Text: "Nothing was withdrawn: a snapshot is on this machine."}
	if r, _ := ask(s, true, "withdraw"); r == nil || r.Error != "Nothing was withdrawn: a snapshot is on this machine." || r.Withdrawn || withdrawals != 1 {
		t.Fatalf("a refused withdrawal: %+v (%d)", r, withdrawals)
	}
	refuse = nil
	if r, _ := ask(s, true, "withdraw"); r == nil || r.Error != "" || !r.Withdrawn || withdrawals != 2 || len(r.Snapshots) != 1 || r.Snapshots[0] != received {
		t.Fatalf("a withdrawal: %+v (%d)", r, withdrawals)
	}
	if offLoop != 2 {
		t.Fatalf("A WITHDRAWAL RAN ON THE READ LOOP: it waits for any act of the door that is running (%d of 2 handed off)", offLoop)
	}
	if r, _ := ask(s, true, "hello"); r == nil || r.Begun != nil {
		t.Fatalf("hello still reports a restore that was withdrawn: %+v", r)
	}
	// A runtime that gives neither: hello answers as it did, and the act is
	// refused instead of called.
	bare := New("127.0.0.1", 0, &WSHandler{RestoreNew: &RestoreNewHooks{}, Background: background})
	if r, _ := ask(bare, true, "hello"); r == nil || r.Begun != nil || !r.SecretsOK {
		t.Fatalf("hello on a runtime that reports nothing: %+v", r)
	}
	if r, refused := ask(bare, true, "withdraw"); r != nil || refused == "" {
		t.Fatalf("an act the runtime does not give: %+v %q", r, refused)
	}
}

// A BIRTH REFUSED FOR WHAT STANDS ON THE MACHINE IS SAID IN ITS OWN WORDS.
// Such a birth never began, so the page is not told that a genesis failed:
// the typed refusal's sentence is the whole message. Any other error keeps
// the lead it had.
func TestABirthRefusedForWhatStandsHereIsSaidInItsOwnWords(t *testing.T) {
	const sentence = "Nothing was sent, and no identity was born. The keys of identity 501265c63ca1065f… are on this machine."
	var mu sync.Mutex
	var answer error
	answers := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		answer = err
	}
	s := New("127.0.0.1", 0, &WSHandler{
		HandleGenesis: func(context.Context, *GenesisRequest) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			return "", answer
		},
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)

	answers(&RestoreNewRefusal{Reason: RestoreNewStands, Text: sentence})
	sendMsg(t, conn, ClientMessage{Type: "genesis", Genesis: &GenesisRequest{}})
	if m := drainUntil(t, conn, "error"); m.Message != sentence {
		t.Fatalf("the refusal was not said as it is: %q", m.Message)
	}
	answers(errors.New("the firstboot prompt got no answer"))
	sendMsg(t, conn, ClientMessage{Type: "genesis", Genesis: &GenesisRequest{}})
	if m := drainUntil(t, conn, "error"); m.Message != "genesis failed: the firstboot prompt got no answer" {
		t.Fatalf("a ceremony that failed lost its lead: %q", m.Message)
	}
}
