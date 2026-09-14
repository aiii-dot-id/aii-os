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

// .
// .
// .
func TestPluginActionInstallUninstallAndRefusal(t *testing.T) {
	var mu sync.Mutex
	var calls []PluginAction
	h := &WSHandler{
		PluginAct: func(a PluginAction) error {
			mu.Lock()
			calls = append(calls, a)
			mu.Unlock()
			if a.ID == "org.bad" {
				return fmt.Errorf("refused on purpose")
			}
			return nil
		},
		GetConfig: func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	writeJSON := func(v map[string]interface{}) {
		t.Helper()
		data, _ := json.Marshal(v)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	writeJSON(map[string]interface{}{"type": "plugin", "request_id": "1",
		"plugin": map[string]interface{}{"action": "install", "id": "org.x"}})
	if msg := readMsg(t, conn); msg.Type != "config" || msg.RequestID != "1" {
		t.Fatalf("install answers with config wearing the id: %+v", msg)
	}
	writeJSON(map[string]interface{}{"type": "plugin", "request_id": "2",
		"plugin": map[string]interface{}{"action": "uninstall", "id": "org.y"}})
	if msg := readMsg(t, conn); msg.Type != "config" || msg.RequestID != "2" {
		t.Fatalf("uninstall answers with config: %+v", msg)
	}
	writeJSON(map[string]interface{}{"type": "plugin", "request_id": "3",
		"plugin": map[string]interface{}{"action": "install", "id": "org.bad"}})
	if msg := readMsg(t, conn); msg.Type != "error" || msg.RequestID != "3" {
		t.Fatalf("a refused action answers with error wearing the id: %+v", msg)
	}

	mu.Lock()
	got := append([]PluginAction(nil), calls...)
	mu.Unlock()
	if len(got) != 3 || got[0] != (PluginAction{Action: "install", ID: "org.x"}) || got[1] != (PluginAction{Action: "uninstall", ID: "org.y"}) {
		t.Fatalf("PluginAct saw the operator's actions in order: %+v", got)
	}
}

func TestPluginActionNotAvailableWithoutHandler(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	data, _ := json.Marshal(map[string]interface{}{"type": "plugin", "request_id": "9",
		"plugin": map[string]interface{}{"action": "install", "id": "x"}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
	if msg := readMsg(t, conn); msg.Type != "error" {
		t.Fatalf("with no handler the action answers not available: %+v", msg)
	}
}
