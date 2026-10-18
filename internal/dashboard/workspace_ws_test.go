package dashboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPushWorkspaceDeliversToViewingConnOnly(t *testing.T) {
	fetch := func(id string) (*WorkspaceState, error) {
		return &WorkspaceState{Project: ProjectState{ID: id, Name: "Board"}}, nil
	}
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats:     func() (*StatsResponse, error) { return &StatsResponse{Name: "X"}, nil },
		GetWorkspace: fetch,
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	viewer := dialWS(t, addr)
	stranger := dialWS(t, addr)
	waitForConns(t, s, 2)

	// The viewer queries the workspace — the query IS the view signal.
	sendMsg(t, viewer, ClientMessage{Type: "query", Query: "workspace", Name: "board"})
	m := drainUntil(t, viewer, "workspace")
	if m.Workspace == nil || m.Workspace.Project.ID != "board" {
		t.Fatalf("query answer missing workspace, got %+v", m.Workspace)
	}

	// The push: fresh payload, same project.
	s.PushWorkspace("board", &WorkspaceState{Project: ProjectState{ID: "board", Name: "Board"}})

	// The viewing connection must receive it.
	m = drainUntil(t, viewer, "workspace")
	if m.Workspace == nil || m.Workspace.Project.ID != "board" {
		t.Fatalf("viewing conn must receive the pushed workspace, got %+v", m.Workspace)
	}

	// The stranger must NOT. Absence cannot be proven in general,
	// only sampled: one deadline-bounded quiet window after two more
	// pushes is the honest bound.
	s.PushWorkspace("board", &WorkspaceState{Project: ProjectState{ID: "board", Name: "Board"}})
	s.PushWorkspace("board", &WorkspaceState{Project: ProjectState{ID: "board", Name: "Board"}})
	// The stranger's reads may legitimately carry connect-time frames
	// (status, history). The push being view-scoped means: no WORKSPACE
	// frame ever arrives. Sample a quiet window for the type.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		_, data, err := stranger.Read(ctx)
		cancel()
		if err != nil {
			return // quiet window: green silence on the workspace type
		}
		var m ServerMessage
		if json.Unmarshal(data, &m) == nil && m.Type == "workspace" {
			t.Fatal("non-viewing conn received a workspace push — the push must be view-scoped")
		}
	}
}

// TestPushWorkspaceNilIsSilent: a nil payload is the app layer saying
// "the project is gone" — the push stays silent (no fan, no panic);
// the projects broadcast owns telling the dock the project vanished.
func TestPushWorkspaceNilIsSilent(t *testing.T) {
	fetch := func(id string) (*WorkspaceState, error) {
		return &WorkspaceState{Project: ProjectState{ID: id, Name: "Board"}}, nil
	}
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats:     func() (*StatsResponse, error) { return &StatsResponse{Name: "X"}, nil },
		GetWorkspace: fetch,
	})
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	viewer := dialWS(t, addr)
	sendMsg(t, viewer, ClientMessage{Type: "query", Query: "workspace", Name: "board"})
	drainUntil(t, viewer, "workspace")

	s.PushWorkspace("board", nil) // nil payload: silence, no fan, no panic
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err := viewer.Read(ctx)
	if err == nil {
		t.Fatal("nil push must stay silent — a frame arrived")
	}
}
