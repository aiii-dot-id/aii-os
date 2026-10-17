package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// THE MESSAGES READ IS CORRELATED AND ITS FAILURE IS SAID. The page's
// request carries its limit to the host; the answer comes back on the
// request's own id, and a read the host cannot make comes back as an
// error on that id, never as an empty page.
func TestTheMessagesReadAnswersItsOwnRequest(t *testing.T) {
	limits := make(chan int, 4)
	s := New("127.0.0.1", 0, &WSHandler{
		Messages: func(_ context.Context, r MessagesRequest) (MessagesState, error) {
			limits <- r.Limit
			if r.Limit == 7 {
				return MessagesState{}, errors.New("the store is closed")
			}
			return MessagesState{Limit: max(r.Limit, 50), Arrivals: []ArrivalRow{{ID: "in_1", Body: "<b>hi</b>"}}, ArrivalsTotal: 1}, nil
		},
	})
	s.SetAccessToken(true, "private-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	c, err := dialWSToken(addr, s.accessHash())
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ask := func(id, body string) ServerMessage {
		t.Helper()
		if err := c.Write(ctx, websocket.MessageText, []byte(body)); err != nil {
			t.Fatal(err)
		}
		for {
			_, raw, err := c.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var reply ServerMessage
			if err := json.Unmarshal(raw, &reply); err != nil {
				t.Fatal(err)
			}
			if reply.RequestID == id {
				return reply
			}
		}
	}
	first := ask("m1", `{"type":"query","query":"messages","request_id":"m1"}`)
	if first.Type != "messages" || first.Messages == nil || first.Messages.Arrivals[0].Body != "<b>hi</b>" || <-limits != 0 {
		t.Fatalf("first read: %+v", first)
	}
	more := ask("m2", `{"type":"query","query":"messages","request_id":"m2","messages":{"limit":100}}`)
	if more.Type != "messages" || more.Messages.Limit != 100 || <-limits != 100 {
		t.Fatalf("a larger page was not asked for: %+v", more)
	}
	failed := ask("m3", `{"type":"query","query":"messages","request_id":"m3","messages":{"limit":7}}`)
	if failed.Type != "error" || failed.Messages != nil || failed.Message != "messages could not be read: the store is closed" {
		t.Fatalf("a failed read was not said: %+v", failed)
	}

	bare := New("127.0.0.1", 0, &WSHandler{})
	bare.SetAccessToken(true, "private-token")
	addr, err = bare.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bare.Shutdown(context.Background())
	c2, err := dialWSToken(addr, bare.accessHash())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	c = c2
	if got := ask("m4", `{"type":"query","query":"messages","request_id":"m4"}`); got.Type != "error" {
		t.Fatalf("no identity, yet: %+v", got)
	}
}
