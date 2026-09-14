package dashboard

import (
	"context"
	"testing"
)

// .
// .
// .
// .
func TestProviderSignInOverWS(t *testing.T) {
	var gotName, gotInput string
	h := &WSHandler{
		SignInProvider: func(name string) (string, error) {
			return "https://auth.example/authorize?state=s1&client=" + name, nil
		},
		CompleteSignIn: func(name, input string) error { gotName, gotInput = name, input; return nil },
		GetProviders:   func() []ProviderInfo { return []ProviderInfo{{Name: "ChatGPT (Plus/Pro)", CanSignIn: true}} },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	other := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{Type: "provider_signin", Provider: "ChatGPT (Plus/Pro)", RequestID: "r1"})
	msg := readMsg(t, conn)
	if msg.Type != "provider_signin" || msg.SignInURL == "" || msg.RequestID != "r1" {
		t.Fatalf("sign-in reply: %+v", msg)
	}
	sendMsg(t, conn, ClientMessage{Type: "provider_signin_complete", Provider: "ChatGPT (Plus/Pro)", Input: "http://localhost:1455/auth/callback?code=c&state=s1", RequestID: "r2"})
	msg = readMsg(t, conn)
	if msg.Type != "providers" || msg.RequestID != "r2" || len(msg.Providers) != 1 || !msg.Providers[0].CanSignIn {
		t.Fatalf("after completion the directory must be re-sent: %+v", msg)
	}
	if gotName != "ChatGPT (Plus/Pro)" || gotInput == "" {
		t.Fatalf("the paste did not reach the app: %q %q", gotName, gotInput)
	}
	s.BroadcastProviders()
	if m := readMsg(t, other); m.Type != "providers" || len(m.Providers) != 1 {
		t.Fatalf("a background completion's broadcast did not reach another client: %+v", m)
	}
}
