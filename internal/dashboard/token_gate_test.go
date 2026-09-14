package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func tokenHash(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func dialWSToken(addr, cookie string) (*websocket.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{"Origin": []string{"https://" + addr}}
	if cookie != "" {
		h.Set("Cookie", "aii_token="+cookie)
	}
	conn, _, err := websocket.Dial(ctx, "wss://"+addr+"/ws", &websocket.DialOptions{
		HTTPClient: testClient,
		HTTPHeader: h,
	})
	return conn, err
}

// .
// .
// .
func TestTokenGateRefusesAndAdmits(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetAccessToken(true, tokenHash("the-token"))
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
	conn, err := dialWSToken(addr, "the-token")
	if err != nil {
		t.Fatalf("the right token was refused: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// .
// .
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

// .
// .
func TestTokenRequirementInjectsThePageFlag(t *testing.T) {
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
	gated.SetAccessToken(true, tokenHash("x"))
	body := fetch(gated)
	if !strings.Contains(body, `<head data-aii-token-required="1">`) {
		t.Fatal("required token did not reach the page as the <head> data attribute")
	}
	// .
	// .
	// .
	// .
	if strings.Contains(body, "AII_TOKEN_REQUIRED") {
		t.Fatal("the flag travels as an inline script — uiCSP refuses to run it")
	}

	plain := New("127.0.0.1", 0, &WSHandler{})
	if strings.Contains(fetch(plain), "data-aii-token-required") {
		t.Fatal("the default page is not byte-identical: it carries the auth flag")
	}
}
