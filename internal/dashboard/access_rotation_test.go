package dashboard

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// .
// .
// .
// .
// .
// .
func TestARotatedTokenCutsOffTheSessionsAdmittedUnderTheOldOne(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{DashboardToken: func() string { return "the-token-in-force" }})
	s.SetAccessToken(true, "old-token")
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	oldHash := s.accessHash()
	conn, err := dialWSToken(addr, oldHash)
	if err != nil {
		t.Fatalf("the token in force was refused: %v", err)
	}
	defer conn.CloseNow()

	s.SetAccessToken(true, "rotated-token")

	// .
	// .
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"query","query":"dashboard_token","request_id":"after-rotation"}`))
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("the session admitted under the old token stayed open through the rotation")
			}
			break
		}
		var msg ServerMessage
		if json.Unmarshal(raw, &msg) == nil && msg.DashboardToken != "" {
			t.Fatalf("the old session was handed a token after the rotation: %q", msg.DashboardToken)
		}
	}
	if c, err := dialWSToken(addr, oldHash); err == nil {
		c.CloseNow()
		t.Fatal("the old token still opens a session")
	}
	fresh, err := dialWSToken(addr, s.accessHash())
	if err != nil {
		t.Fatalf("the rotated token was refused: %v", err)
	}
	defer fresh.CloseNow()

	// .
	// .
	s.SetAccessToken(true, "rotated-token")
	if err := fresh.Write(ctx, websocket.MessageText, []byte(`{"type":"query","query":"dashboard_token","request_id":"same-policy"}`)); err != nil {
		t.Fatalf("the session was closed by a policy that did not change: %v", err)
	}
	for {
		_, raw, err := fresh.Read(ctx)
		if err != nil {
			t.Fatalf("the session was closed by a policy that did not change: %v", err)
		}
		var msg ServerMessage
		if json.Unmarshal(raw, &msg) == nil && msg.DashboardToken == "the-token-in-force" {
			return
		}
	}
}

// .
// .
// .
// .
// .
// .
func TestARotationDuringAdmissionCutsThatSessionOffToo(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{DashboardToken: func() string { return "the-token-in-force" }})
	s.SetAccessToken(true, "old-token")
	var once sync.Once
	s.wsAdmitHook = func() { once.Do(func() { s.SetAccessToken(true, "rotated-token") }) }
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	oldHash := s.accessHash()
	conn, err := dialWSToken(addr, oldHash)
	if err != nil {
		t.Fatalf("the cookie check refused the token in force: %v", err)
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"query","query":"dashboard_token","request_id":"in-the-window"}`))
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				t.Fatal("a session admitted during the rotation stayed open")
			}
			break
		}
		var msg ServerMessage
		if json.Unmarshal(raw, &msg) == nil && msg.DashboardToken != "" {
			t.Fatalf("a session admitted during the rotation was handed a token: %q", msg.DashboardToken)
		}
	}
	s.wsAdmitHook = nil
	fresh, err := dialWSToken(addr, s.accessHash())
	if err != nil {
		t.Fatalf("the rotated token was refused: %v", err)
	}
	fresh.CloseNow()
}
