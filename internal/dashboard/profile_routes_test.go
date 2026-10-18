package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The auth profile routes: a connect answers with the
// authorize URL wearing the id; a device sign-in answers with the code;
// a paste completion, a disconnect, a create and a delete answer with
// the configuration; a refusal answers with an error wearing the id; a
// secret in the edit reaches the handler and never a reply.
func TestAuthProfileRoutes(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	var edits []AuthProfileEdit
	h := &WSHandler{
		SignInProfile: func(name string) (string, error) {
			mu.Lock()
			seen = append(seen, "signin:"+name)
			mu.Unlock()
			if name == "none" {
				return "", fmt.Errorf("no auth profile %q", name)
			}
			return "https://auth.example/authorize?state=s", nil
		},
		CompleteProfileSignIn: func(name, input string) error {
			mu.Lock()
			seen = append(seen, "complete:"+name+":"+input)
			mu.Unlock()
			return nil
		},
		DeviceSignInProfile: func(name string) (*DeviceCodeView, error) {
			mu.Lock()
			seen = append(seen, "device:"+name)
			mu.Unlock()
			return &DeviceCodeView{UserCode: "ABCD-1234", VerificationURI: "https://auth.example/device", Expires: "2026-09-12T20:00:00Z"}, nil
		},
		DisconnectProfile: func(name string) error {
			mu.Lock()
			seen = append(seen, "disconnect:"+name)
			mu.Unlock()
			return nil
		},
		SetAuthProfile: func(e AuthProfileEdit) error {
			mu.Lock()
			edits = append(edits, e)
			mu.Unlock()
			return nil
		},
		DeleteAuthProfile: func(name string) error {
			mu.Lock()
			seen = append(seen, "delete:"+name)
			mu.Unlock()
			return nil
		},
		GetConfig: func() *ConfigState { return &ConfigState{} },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	writeJSON := func(v map[string]any) {
		t.Helper()
		data, _ := json.Marshal(v)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	writeJSON(map[string]any{"type": "profile_signin", "request_id": "1", "profile": "gh"})
	if msg := readMsg(t, conn); msg.Type != "profile_signin" || msg.RequestID != "1" || msg.SignInURL == "" {
		t.Fatalf("connect answers with the authorize URL: %+v", msg)
	}
	writeJSON(map[string]any{"type": "profile_signin", "request_id": "2", "profile": "none"})
	if msg := readMsg(t, conn); msg.Type != "error" || msg.RequestID != "2" {
		t.Fatalf("a refusal answers with error wearing the id: %+v", msg)
	}
	writeJSON(map[string]any{"type": "profile_device", "request_id": "3", "profile": "gh"})
	if msg := readMsg(t, conn); msg.Type != "profile_device" || msg.RequestID != "3" || msg.Device == nil || msg.Device.UserCode != "ABCD-1234" {
		t.Fatalf("a device sign-in answers with the code: %+v", msg)
	}
	writeJSON(map[string]any{"type": "profile_signin_complete", "request_id": "4", "profile": "gh", "input": "http://127.0.0.1:8187/oauth/callback?code=c&state=s"})
	if msg := readMsg(t, conn); msg.Type != "config" || msg.RequestID != "4" {
		t.Fatalf("a completion answers with config: %+v", msg)
	}
	writeJSON(map[string]any{"type": "profile_disconnect", "request_id": "5", "profile": "gh"})
	if msg := readMsg(t, conn); msg.Type != "config" || msg.RequestID != "5" {
		t.Fatalf("a disconnect answers with config: %+v", msg)
	}
	writeJSON(map[string]any{"type": "auth_profile_set", "request_id": "6", "profile_edit": map[string]any{"name": "gh", "provider": "github", "client_id": "cid", "client_secret": "csecret", "services": map[string]string{"issues": "read"}}})
	if msg := readMsg(t, conn); msg.Type != "config" || msg.RequestID != "6" {
		t.Fatalf("a create answers with config: %+v", msg)
	}
	writeJSON(map[string]any{"type": "auth_profile_delete", "request_id": "7", "profile": "gh"})
	if msg := readMsg(t, conn); msg.Type != "config" || msg.RequestID != "7" {
		t.Fatalf("a delete answers with config: %+v", msg)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(edits) != 1 || edits[0].ClientSecret != "csecret" || edits[0].Services["issues"] != "read" {
		t.Fatalf("the edit reached the handler whole: %+v", edits)
	}
	want := []string{"signin:gh", "signin:none", "device:gh", "complete:gh:http://127.0.0.1:8187/oauth/callback?code=c&state=s", "disconnect:gh", "delete:gh"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("the handlers saw the operator's acts in order: %v", seen)
	}
}
