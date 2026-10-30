package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestThePageIsNeverAnsweredTheDashboardToken(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Identity.KeyPath = filepath.Join(filepath.Dir(cfg.SourcePath), "identity.sec")
	cfg.Dashboard = DashboardConfig{Host: "127.0.0.1", Port: 0, RequireToken: true, AccessToken: "the-running-token-of-honest-length"}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	h := &dashboard.WSHandler{GetConfig: a.configState}
	a.wireDashboardAccessHooks(h)
	d, err := a.newDashboard(h)
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	if _, err := d.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Shutdown(t.Context()) })
	token := a.dashboardToken()
	if token == "" || !d.AccessTokenRequired() {
		t.Fatal("no token is asked for, so this shows nothing")
	}
	base := a.DashboardURL()
	client := signedIn(t, base, token)
	jar, key := client.Jar, client.Transport.(*pageKeyed).pageKey()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{"Origin": []string{"http://" + u.Host}}
	for _, c := range jar.Cookies(u) {
		header.Add("Cookie", c.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+u.Host+"/ws?interaction_version=1", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("a browser signed in with the token could not open a socket: %v", err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","page_key":"`+key+`"}`)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"config", "dashboard_token"} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"query","query":"`+q+`","request_id":"`+q+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	answered := map[string]bool{}
	for !answered["config"] || !answered["unknown"] {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("the socket ended before both questions were answered (%v): %v", answered, err)
		}
		if strings.Contains(string(raw), token) {
			t.Fatalf("the page was answered the dashboard token: %.200s", raw)
		}
		var m dashboard.ServerMessage
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		switch {
		case m.Type == "config":
			answered["config"] = true
		case m.Type == "error" && strings.Contains(m.Message, "unknown query: dashboard_token"):
			answered["unknown"] = true
		}
	}
}
