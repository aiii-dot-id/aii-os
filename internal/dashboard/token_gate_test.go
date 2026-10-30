package dashboard

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialWSToken opens the socket at addr over TLS with the cookie; given a page
// key, it sends the socket's first message with it, as a page does.
func dialWSToken(addr, cookie string, key ...string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{"Origin": []string{"https://" + addr}}
	if cookie != "" {
		_, port, _ := net.SplitHostPort(addr)
		h.Set("Cookie", tlsCookieName(port)+"="+cookie)
	}
	conn, _, err := websocket.Dial(ctx, "wss://"+addr+"/ws", &websocket.DialOptions{
		HTTPClient: testClient,
		HTTPHeader: h,
	})
	if err == nil && len(key) > 0 {
		err = sendHello(ctx, conn, key[0])
	}
	return conn, err
}

// sendHello sends a socket's first message: the page key a page holds.
func sendHello(ctx context.Context, conn *websocket.Conn, key string) error {
	return conn.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","page_key":"`+key+`"}`))
}

// pageKeyFor is the page key a sign-in on s answers under its policy now.
func pageKeyFor(s *Server) string { return mintPageKey(s.auth.Load()) }

func TestTokenGateRefusesAndAdmits(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetAccessToken(true, "the-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	if conn, err := dialWSToken(addr, ""); err == nil {
		conn.CloseNow()
		t.Fatal("no cookie was admitted past a required token")
	}
	if conn, err := dialWSToken(addr, "wrong-token"); err == nil {
		conn.CloseNow()
		t.Fatal("a wrong token was admitted")
	}
	conn, err := dialWSToken(addr, s.accessHash(), pageKeyFor(s))
	if err != nil {
		t.Fatalf("the right token was refused: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// Required with no configured token refuses EVERY session — never
// falls open.
func TestTokenRequiredWithNoHashRefusesEverything(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetAccessToken(true, "")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())
	if conn, err := dialWSToken(addr, "anything"); err == nil {
		conn.CloseNow()
		t.Fatal("required-with-no-hash fell open")
	}
}

func TestThePageCarriesNoTokenBit(t *testing.T) {
	fetch := func(s *Server) string {
		addr, err := s.Start(t.TempDir())
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		defer s.Shutdown(context.Background())
		resp, err := testClient.Get("https://" + addr + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	gated := New("127.0.0.1", 0, &WSHandler{})
	gated.SetAccessToken(true, "x")
	required, open := fetch(gated), fetch(New("127.0.0.1", 0, &WSHandler{}))
	if required != open {
		t.Fatal("the page differs by whether a token is required: it carries a bit the requirement can outdate")
	}
	if strings.Contains(required, "data-aii-token-required") || strings.Contains(required, "AII_TOKEN_REQUIRED") {
		t.Fatal("the page carries a token bit")
	}
}
