package dashboard

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestOverlayPushWithoutQuery(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.css"),
		[]byte("/* additive layer */\n.panel-col{border-color:#0a5}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New("127.0.0.1", 0, &WSHandler{
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{Name: "X"}, nil
		},
	})
	s.overlayDir = dir
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	// .
	// .
	conn := dialWS(t, addr)

	// .
	// .
	// .

	// .
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer fetchCancel()
	req, _ := http.NewRequestWithContext(fetchCtx, http.MethodGet,
		"https://"+addr+"/custom.css", nil)
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	resp.Body.Close()

	// .
	// .
	// .
	m := drainUntil(t, conn, "overlays")
	if len(m.Overlays) == 0 {
		t.Fatalf("pushed overlays message carried zero events")
	}
	if m.Overlays[0].Path != "/custom.css" {
		t.Errorf("pushed path: got %s, want /custom.css", m.Overlays[0].Path)
	}
	if m.Overlays[0].Outcome == "" {
		t.Errorf("pushed outcome is empty")
	}
}
