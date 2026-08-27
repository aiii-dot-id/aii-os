package dashboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// §8.11: a project action must reach every live window, not just the
// one that acted. Before the broadcast, the server answered the actor
// alone — a dashboard open in two windows kept a stale dock in the
// second until its own next query. This test pins the seam between two
// OWNERS of the same fact: the actor (whose answer wears the request
// id) keeps its echo; the bystander gets the bare broadcast.
func TestProjectActionBroadcastsToOtherWindows(t *testing.T) {
	h := &WSHandler{
		IdentityName: "X",
		ProjectAct: func(action, id, name string, description, focus *string) error {
			return nil
		},
		GetProjects: func() ([]ProjectState, error) {
			return []ProjectState{{ID: "beta", Name: "Beta Tracker", State: "open"}}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	actor := dialWS(t, addr)
	defer actor.Close(websocket.StatusNormalClosure, "")
	bystander := dialWS(t, addr)
	defer bystander.Close(websocket.StatusNormalClosure, "")

	writeJSON := func(conn *websocket.Conn, v map[string]interface{}) {
		t.Helper()
		data, _ := json.Marshal(v)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// The bystander sits idle while the actor creates a project.
	writeJSON(actor, map[string]interface{}{
		"type": "project", "request_id": "act-1",
		"project": map[string]interface{}{"action": "create", "name": "ok"},
	})

	// The actor must receive its answer wearing the request id.
	msg := readMsg(t, actor)
	if msg.Type != "projects" || msg.RequestID != "act-1" {
		t.Fatalf("actor answer: type %q id %q, want projects/act-1", msg.Type, msg.RequestID)
	}

	// The bystander must ALSO receive a projects payload — bare (no
	// request id), carrying the post-action truth.
	bmsg := readMsg(t, bystander)
	if bmsg.Type != "projects" {
		t.Fatalf("bystander got %q, want the broadcast projects payload (§8.11)", bmsg.Type)
	}
	if bmsg.RequestID != "" {
		t.Fatalf("bystander broadcast wore a request id %q — broadcasts must be bare", bmsg.RequestID)
	}
	if len(bmsg.Projects) != 1 || bmsg.Projects[0].ID != "beta" {
		t.Fatalf("bystander payload wrong: %+v", bmsg.Projects)
	}
}
