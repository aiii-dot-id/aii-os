package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .

func tlsClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
}

func dialWSS(t *testing.T, origin string) *websocket.Conn {
	t.Helper()
	wsScheme, httpScheme := "wss", "https"
	if strings.HasPrefix(origin, "http://") {
		wsScheme, httpScheme = "ws", "http"
	}
	addr := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsScheme+"://"+addr+"/ws", &websocket.DialOptions{
		HTTPClient: tlsClient(),
		HTTPHeader: http.Header{"Origin": []string{httpScheme + "://" + addr}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

// .
// .
// .
// .
// .
// .
// .
func TestOverlayE2EHotReloadPath(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "OvlE2E")
	cfg := safebootConfig(t, dir, "OvlE2E", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(a.Stop)

	addr := a.dashboard.Origin()
	t.Logf("dashboard at %s", addr)

	// .
	conn := dialWSS(t, addr)
	sendQuery(t, conn, "ui.overlay")
	firstReadback := readUntil(t, conn, isOverlayMsg)
	t.Logf("initial readback: %d events", len(firstReadback.Overlays))

	// .
	ovl := filepath.Join(dir, "ui")
	if err := os.MkdirAll(ovl, 0o755); err != nil {
		t.Fatal(err)
	}
	css := filepath.Join(ovl, "custom.css")
	if err := os.WriteFile(css, []byte("body{background:#0af}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// .
	// .
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if p := a.overlayLast.Load(); p != nil && *p != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if p := a.overlayLast.Load(); p == nil || *p == "" {
		t.Fatalf("watcher never snapshotted the edit: overlayLast=%v", a.overlayLast.Load())
	}
	t.Logf("watcher observed: %q", *a.overlayLast.Load())

	// .
	chg := readUntil(t, conn, isOverlayChanged)
	if chg.Token == 0 {
		t.Fatal("overlay_changed carried token 0 — not fresh")
	}
	if len(chg.Paths) != 1 || chg.Paths[0] != "/custom.css" {
		t.Fatalf("changed paths = %v, want [/custom.css]", chg.Paths)
	}
	t.Logf("overlay_changed: token=%d paths=%v", chg.Token, chg.Paths)

	// .
	// .
	client := tlsClient()
	body, hdr, err := httpsGet(t, client, addr+"/custom.css")
	if err != nil {
		t.Fatalf("fetch overlay: %v", err)
	}
	if !strings.Contains(body, "background:#0af") {
		t.Fatalf("served bytes stale: %q", body)
	}
	if cc := hdr.Get("Cache-Control"); cc != "no-cache" {
		// .
		// .
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
}

func sendQuery(t *testing.T, conn *websocket.Conn, kind string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjsonWrite(ctx, conn, map[string]any{"type": "query", "query": kind}); err != nil {
		t.Fatalf("send %s: %v", kind, err)
	}
}

func wsjsonWrite(ctx context.Context, conn *websocket.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

func readUntil(t *testing.T, conn *websocket.Conn, match func(*dashboard.ServerMessage) bool) *dashboard.ServerMessage {
	t.Helper()
	// .
	// .
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn.SetReadLimit(1 << 20)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var msg dashboard.ServerMessage
		if json.Unmarshal(data, &msg) == nil && match(&msg) {
			return &msg
		}
	}
}

func isOverlayMsg(m *dashboard.ServerMessage) bool { return m.Type == "overlays" }

func isOverlayChanged(m *dashboard.ServerMessage) bool {
	return m.Type == "overlay_changed" && m.Paths != nil
}

func httpsGet(t *testing.T, client *http.Client, url string) (string, http.Header, error) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	return string(raw), resp.Header, nil
}
