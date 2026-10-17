package mobile

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
)

func TestMobileStartRequiresAuthenticationAndRecoversOnRestart(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	path := filepath.Join(home, "config.json")
	config := `{"dashboard":{"host":"127.0.0.1","port":0,"tls":false},"genesis":{"server_url":"http://127.0.0.1:1","firewall_url":"http://127.0.0.1:1","bootstrap_url":"http://127.0.0.1:1"},"certificate":{"server_url":"none"}}`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var first string
	for boot := 0; boot < 2; boot++ {
		rt, err := Start(path, home)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer stopRuntime(t, rt)
			base := rt.DashboardURL()
			token, err := rt.DashboardAccessTokenForOrigin(strings.TrimSuffix(base, "/"))
			if err != nil || token == "" {
				t.Fatal("shell could not obtain its current credential", err)
			}
			if boot == 0 {
				first = token
			} else if first != token {
				t.Fatal("restart replaced the existing credential")
			}
			cfg, err := app.LoadConfig(path)
			if err != nil || cfg.Dashboard.RequireToken || cfg.Dashboard.AccessToken != token {
				t.Fatal("config is not the one durable credential owner", err)
			}
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
			r, err := client.Get(base + "auth/token")
			if err != nil {
				t.Fatal(err)
			}
			r.Body.Close()
			if r.StatusCode != 401 {
				t.Fatal("mobile loopback admitted a client with no credential")
			}
			r, err = client.Post(base+"auth/token", "text/plain", strings.NewReader(token))
			if err != nil {
				t.Fatal(err)
			}
			r.Body.Close()
			if r.StatusCode != 204 || len(r.Cookies()) != 1 || !r.Cookies()[0].HttpOnly {
				t.Fatal("shell could not sign in through the existing route")
			}
		}()
	}
}
