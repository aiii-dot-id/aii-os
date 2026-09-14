package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// .
// .
// .
// .
// .
// .
func TestADiscoveryAnswersTheAskerAndBroadcastsNothing(t *testing.T) {
	models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"fresh"}]}`))
	}))
	t.Cleanup(models.Close)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Disc")
	cfg := safebootConfig(t, dir, "Disc", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(a.Stop)
	writeTestProviders(t, dir, providerEntry{Name: "provider", APIType: "openai", URL: models.URL, APIKey: "k", Models: []string{"configured"}})
	conn := dialWSS(t, a.dashboard.Origin())
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wcancel()
	if err := conn.Write(wctx, websocket.MessageText, []byte(`{"type":"query","query":"discover","provider":"provider","request_id":"d1"}`)); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	rctx, rcancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer rcancel()
	var answers, broadcasts int
	for {
		_, data, err := conn.Read(rctx)
		if err != nil {
			break
		}
		var m struct {
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
		}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "models":
			if m.RequestID == "d1" {
				answers++
			}
		case "providers":
			broadcasts++
		}
	}
	if answers != 1 || broadcasts != 0 {
		t.Fatalf("a discovery must answer the asker once and broadcast nothing: models=%d providers=%d", answers, broadcasts)
	}
}
