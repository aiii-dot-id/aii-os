package dashboard

import (
	"context"
	"math"
	"testing"
)

// A MESSAGE THAT CANNOT BE ENCODED IS AN ERROR, NOT AN EMPTY FRAME. sendMsg
// dropped json.Marshal's error and wrote an empty text frame, which the page
// cannot parse and the sender never heard about. A NaN anywhere in a value the
// page is shown is enough: a provider's extra facts are `any`.
func TestAMessageThatCannotBeEncodedIsAnErrorNotAnEmptyFrame(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn, err := dialPlain(bound, "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	waitForConns(t, s, 1)
	s.wsMu.Lock()
	var serverConn carrier
	for c := range s.wsConns {
		serverConn = c
	}
	s.wsMu.Unlock()
	msg := ServerMessage{Type: "providers", Providers: []ProviderInfo{{Name: "p", Extra: map[string]any{"rate": math.NaN()}}}}
	if err := s.sendMsg(context.Background(), serverConn, msg); err == nil {
		t.Fatal("an unencodable message was reported as sent")
	}
	if s.client(serverConn) == nil {
		t.Fatal("an encoding failure dropped a healthy page")
	}
}
