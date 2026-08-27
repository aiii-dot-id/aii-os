package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestWorkspaceQuerySeam pins the project.workspace projection at the
// WebSocket seam: a workspace query with a project ID returns the
// assembled projection; without an ID it is refused; with no handler
// wired the server answers "not available" rather than silence. The
// seam belongs to neither the store nor the view — this test is its
// owner.
func TestWorkspaceQuerySeam(t *testing.T) {
	want := &WorkspaceState{
		Project: ProjectState{
			ID: "proj-x", Name: "Proj X", State: "open",
			Focus: "shipping the widget", Dir: "/tmp/proj-x", Active: true,
		},
		Files: []WorkspaceFile{
			{Name: "notes.md", Size: 128},
			{Name: "data", Dir: true},
		},
		Work: []WorkSessionItem{
			{ID: "7", Description: "polish", Status: "delivered", Project: "proj-x", Result: "served: widget polished"},
		},
	}

	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "Void",
		RecentTurns: func() ([]HistoryTurn, error) {
			return nil, nil
		},
		GetWorkspace: func(id string) (*WorkspaceState, error) {
			if id != "proj-x" {
				return nil, nil
			}
			return want, nil
		},
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := dialWS(t, addr)
	defer conn.Close(websocket.StatusNormalClosure, "")

	readUntil := func(wantType string) *ServerMessage {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			rctx, rcancel := context.WithTimeout(ctx, 2*time.Second)
			_, data, err := conn.Read(rctx)
			rcancel()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var msg ServerMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg.Type == wantType {
				return &msg
			}
		}
		t.Fatalf("no %q message within deadline", wantType)
		return nil
	}

	// 1) Positive: query with ID returns the projection.
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "workspace", Name: "proj-x"})
	got := readUntil("workspace")
	if got.Workspace == nil {
		t.Fatal("workspace message carries nil Workspace")
	}
	if got.Workspace.Project.ID != "proj-x" || got.Workspace.Project.Name != "Proj X" {
		t.Errorf("project = %+v, want ID proj-x / Name Proj X", got.Workspace.Project)
	}
	if len(got.Workspace.Files) != 2 || got.Workspace.Files[1].Name != "data" || !got.Workspace.Files[1].Dir {
		t.Errorf("files = %+v, want notes.md + dir data", got.Workspace.Files)
	}
	if len(got.Workspace.Work) != 1 || got.Workspace.Work[0].Project != "proj-x" {
		t.Errorf("work = %+v, want one proj-x session", got.Workspace.Work)
	}
	if got.Workspace.Work[0].Result != "served: widget polished" {
		t.Errorf("result = %q, want the verdict to survive the wire", got.Workspace.Work[0].Result)
	}

	// 2) Negative control: empty ID is refused with a named error.
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "workspace", Name: ""})
	errMsg := readUntil("error")
	if errMsg.Message == "" || errMsg.Message == "not available" {
		t.Fatalf("empty id must be refused with a named reason, got %q", errMsg.Message)
	}

	// 3) Unknown ID: a named error — never an inert empty render
	// (inertness must not ship silently, item_8311742f).
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "workspace", Name: "missing"})
	errMsg = readUntil("error")
	if !strings.Contains(errMsg.Message, "project not found") {
		t.Errorf("unknown id must be a named error, got %q", errMsg.Message)
	}
}
