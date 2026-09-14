package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
// .
func TestOverlayQueryOverWebSocket(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// .
	// .
	// .
	// .
	write("custom.css", "/* additive layer */\n.panel-col{border-color:#0a5}\n")
	write("theme.css", "/* replaces shipped frame */\n:root{}\n")
	write("work.js", "console.log('a view that does not ship statically')\n")

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
	for _, path := range []string{"/custom.css", "/theme.css", "/work.js"} {
		resp, err := testClient.Get("https://" + addr + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		resp.Body.Close()
	}

	conn := dialWS(t, addr)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "ui.overlay"})
	m := drainUntil(t, conn, "overlays")
	if len(m.Overlays) != 3 {
		t.Fatalf("over the wire: got %d events, want 3 (additive + fork + new file)", len(m.Overlays))
	}

	var additive, fork, newFile bool
	for _, ev := range m.Overlays {
		switch {
		case strings.Contains(ev.Outcome, "accepted: additive layer"):
			additive = true
			if ev.Path != "/custom.css" {
				t.Errorf("additive path: got %s, want /custom.css", ev.Path)
			}
		case strings.Contains(ev.Outcome, "FORK"):
			fork = true
			if ev.Path != "/theme.css" {
				t.Errorf("fork path: got %s, want /theme.css", ev.Path)
			}
		case strings.Contains(ev.Outcome, "accepted: new file"):
			newFile = true
			if ev.Path != "/work.js" {
				t.Errorf("new-file path: got %s, want /work.js", ev.Path)
			}
		default:
			t.Errorf("unclassified outcome for %s: %q", ev.Path, ev.Outcome)
		}
		if ev.DecidedAt == "" {
			t.Errorf("event %s: DecidedAt is empty", ev.Path)
		}
		if _, err := time.Parse(time.RFC3339, ev.DecidedAt); err != nil {
			t.Errorf("event %s: DecidedAt %q is not RFC3339", ev.Path, ev.DecidedAt)
		}
	}
	if !additive || !fork || !newFile {
		t.Fatalf("outcomes over the wire: additive=%v fork=%v newFile=%v, want all three", additive, fork, newFile)
	}

	// .
	// .
	unit := s.overlayMessage()
	if len(unit.Overlays) != len(m.Overlays) {
		t.Fatalf("wire answer %d events, overlayMessage() %d — two truths", len(m.Overlays), len(unit.Overlays))
	}

	// .
	// .
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "ui.overlay"})
	m2 := drainUntil(t, conn, "overlays")
	if len(m2.Overlays) != 3 {
		t.Fatalf("second query: %d events, want the same 3 (stable log, not counter)", len(m2.Overlays))
	}
}
