package dashboard

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestOverlayPushWithoutQuery pins the W2 mid-session push: an overlay
// decision made AFTER a client connected reaches the screen without the
// client ever asking. Until the push existed, the FRAME card rendered
// only on boot query — a decision mid-session (operator drops a file in
// the overlay dir while the tab is open) sat invisible until reload.
//
// The order is the proof: dial FIRST, no query sent, then fetch. If the
// message arrives anyway, something pushed it.
func TestOverlayPushWithoutQuery(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.css"),
		[]byte("/* additive layer */\n.panel-col{border-color:#0a5}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New("127.0.0.1", 0, &WSHandler{
		IdentityName: "X",
		GetStats: func() (*StatsResponse, error) {
			return &StatsResponse{Name: "X"}, nil
		},
	})
	s.overlayDir = dir
	addr, _ := s.Start(t.TempDir())
	defer s.Shutdown(context.Background())

	// Connect BEFORE any overlay activity. No query is sent — that is
	// the point of the test.
	conn := dialWS(t, addr)

	// Connect BEFORE any overlay activity. No query is sent — that is
	// the point of the test. (The socket's liveness is proven by the
	// drain below receiving the push the fetch triggers.)

	// Trigger the decision the way a browser would: fetch the overlay.
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer fetchCancel()
	req, _ := http.NewRequestWithContext(fetchCtx, http.MethodGet,
		"https://"+addr+"/custom.css", nil)
	resp, err := testClient.Do(req)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	resp.Body.Close()

	// The unbidden push: an overlays message must arrive with no query
	// in flight. drainUntil skips noise; a missing push surfaces as the
	// read timeout — the same failure shape as the negative control.
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
