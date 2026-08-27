package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestBroadcastOverlayOverWebSocket pins the W3 push half: the watcher
// (app-owned) fires BroadcastOverlay when overlay bytes move, and that
// push reaches a connected client with NO query from the client. This
// test stands in for the app→dashboard seam by driving the dashboard's
// own broadcast entry point exactly as the watcher does — the delivery
// wire (broadcast → socket → "overlays" message) is what it owns.
func TestBroadcastOverlayOverWebSocket(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "X",
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{Name: "X"}, nil
		},
	})
	s.overlayDir = filepath.Join(dir, "ui")
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)

	// The edit event the watcher would see. Drive the real serving path
	// once so the readback has real content, then fan the broadcast
	// exactly as the app-owned watcher does.
	if err := os.WriteFile(filepath.Join(dir, "ui", "custom.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := testClient.Get("https://" + addr + "/custom.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	s.BroadcastOverlay()

	m := drainUntil(t, conn, "overlays")
	if len(m.Overlays) == 0 {
		t.Fatal("broadcast arrived with no events — the push must carry the readback, not an empty card")
	}
	if m.Overlays[0].Path != "/custom.css" {
		t.Fatalf("first event path: got %s, want /custom.css", m.Overlays[0].Path)
	}
}
