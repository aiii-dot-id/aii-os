package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// D72: the project answer — success or refusal — wears the request id
// of the act that caused it. Before this, both paths answered bare, so
// the page could not tell ITS create from a broadcast: a refused create
// left the client armed, and the next unrelated projects payload moved
// the operator to whichever id it happened to carry. The id already
// existed on every request; the echo is the whole contract.
func TestProjectActionsEchoTheRequestID(t *testing.T) {
	h := &WSHandler{
		IdentityName: "X",
		ProjectAct: func(action, id, name string, description, focus *string) error {
			if action == "create" && name == "refuse-me" {
				return fmt.Errorf("refused on purpose")
			}
			return nil
		},
		GetProjects: func() ([]ProjectState, error) {
			return []ProjectState{{ID: "p1", Name: "One", State: "open"}}, nil
		},
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

	// A successful action answers with a projects payload wearing the id.
	writeJSON(map[string]interface{}{
		"type": "project", "request_id": "42",
		"project": map[string]interface{}{"action": "create", "name": "ok"},
	})
	msg := readMsg(t, conn)
	if msg.Type != "projects" {
		t.Fatalf("after create: got %q, want projects", msg.Type)
	}
	if msg.RequestID != "42" {
		t.Fatalf("the success answer does not wear the request id: %q (D72)", msg.RequestID)
	}

	// A refused action answers with an error wearing the id.
	writeJSON(map[string]interface{}{
		"type": "project", "request_id": "43",
		"project": map[string]interface{}{"action": "create", "name": "refuse-me"},
	})
	msg = readMsg(t, conn)
	if msg.Type != "error" {
		t.Fatalf("after refused create: got %q, want error", msg.Type)
	}
	if msg.RequestID != "43" {
		t.Fatalf("the refusal does not wear the request id: %q (D72)", msg.RequestID)
	}

	// The plain projects query is correlated the same way.
	writeJSON(map[string]interface{}{"type": "query", "query": "projects", "request_id": "44"})
	msg = readMsg(t, conn)
	if msg.Type != "projects" || msg.RequestID != "44" {
		t.Fatalf("query answer: type %q request_id %q, want projects/44", msg.Type, msg.RequestID)
	}
}
