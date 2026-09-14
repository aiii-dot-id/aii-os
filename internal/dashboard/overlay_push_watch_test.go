package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
func TestBroadcastOverlayOverWebSocket(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{Name: "X"}, nil
		},
	})
	s.overlayDir = filepath.Join(dir, "ui")
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	conn := dialWS(t, addr)
	// .
	// .
	// .
	// .
	waitForConns(t, s, 1)

	// .
	// .
	// .
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
