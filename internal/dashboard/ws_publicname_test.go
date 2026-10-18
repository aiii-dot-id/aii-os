package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestThePagesLookReachesTheHostsAsking(t *testing.T) {
	var looked atomic.Int32
	relays := []RelayChoice{{Name: "relay.example.test", Port: 443, Suggested: true}}
	s := New("127.0.0.1", 0, &WSHandler{
		PublicNameLook: func() (PublicNameState, error) {
			looked.Add(1)
			return PublicNameState{Status: "prepared", Name: "ui.x.example.test", Relays: &relays}, nil
		},
		PublicNameState: func() PublicNameState { return PublicNameState{Status: "prepared", Name: "ui.x.example.test"} },
	})
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+bound+"/ws?interaction_version=1", &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://" + bound}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"public_name_look","request_id":"look"}`)); err != nil {
		t.Fatal(err)
	}
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("the page's look was not answered: %v", err)
		}
		var m ServerMessage
		if json.Unmarshal(raw, &m) != nil || m.RequestID != "look" {
			continue
		}
		if m.Type != "public_name" || m.PublicName == nil || m.PublicName.Relays == nil || len(*m.PublicName.Relays) != 1 {
			t.Fatalf("the look was answered %q with %+v, want the name's state with the relays the service offers", m.Type, m.PublicName)
		}
		break
	}
	if n := looked.Load(); n != 1 {
		t.Fatalf("one look by the page made the host ask %d times", n)
	}
}
