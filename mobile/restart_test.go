package mobile

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/app"
	"github.com/coder/websocket"
)

func restartFixture(t *testing.T) (*Runtime, string) {
	t.Helper()
	home := t.TempDir()
	t.Chdir(home)
	path := filepath.Join(home, "config.json")
	raw := `{"dashboard":{"host":"127.0.0.1","port":0,"tls":false},"genesis":{"server_url":"http://127.0.0.1:1","firewall_url":"http://127.0.0.1:1","bootstrap_url":"http://127.0.0.1:1"},"certificate":{"server_url":"none"}}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Start(path, home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.a.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return r, path
}

func TestMobileDashboardRestartHandsOffOnceAndKeepsAuthentication(t *testing.T) {
	r, path := restartFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	base := r.DashboardURL()
	token, err := r.DashboardAccessTokenForOrigin(strings.TrimSuffix(base, "/"))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Post(base+"auth/token", "text/plain", strings.NewReader(token))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent || len(response.Cookies()) != 1 {
		t.Fatal("sign-in failed")
	}
	header := http.Header{}
	header.Set("Cookie", response.Cookies()[0].String())
	header.Set("Origin", strings.TrimSuffix(base, "/"))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"ws", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	wait := make(chan bool, 1)
	go func() { wait <- r.WaitForRestart() }()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"restart","request_id":"mobile-restart-proof"}`)); err != nil {
		t.Fatal(err)
	}
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Type, RequestID string
			Error           string
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(data["type"], &message.Type)
		_ = json.Unmarshal(data["request_id"], &message.RequestID)
		if message.RequestID != "mobile-restart-proof" {
			continue
		}
		if message.Type != "restart" {
			t.Fatalf("restart refused: %s", raw)
		}
		break
	}
	select {
	case requested := <-wait:
		if !requested {
			t.Fatal("shutdown confused with restart")
		}
	case <-ctx.Done():
		t.Fatal("mobile owner never received restart")
	}
	next, err := r.Restart()
	if err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, next)
	if next == r {
		t.Fatal("restart reused the closed App")
	}
	if r.WaitForRestart() {
		t.Fatal("successor started without shutting down the predecessor")
	}
	// A repeated native delivery must reuse the completed handoff, not start
	// another owner. Sequential repetition makes this a deterministic oracle.
	again, err := r.Restart()
	if err != nil || again != next {
		t.Fatalf("duplicate successor: %p %p %v", again, next, err)
	}
	duplicates := make(chan *Runtime, 4)
	for i := 0; i < cap(duplicates); i++ {
		go func() {
			got, err := r.Restart()
			if err != nil {
				duplicates <- nil
				return
			}
			duplicates <- got
		}()
	}
	for i := 0; i < cap(duplicates); i++ {
		if got := <-duplicates; got != next {
			t.Fatal("concurrent request created another successor")
		}
	}
	got, err := next.DashboardAccessTokenForOrigin(strings.TrimSuffix(next.DashboardURL(), "/"))
	if err != nil || got != token {
		t.Fatal("handoff changed or lost the credential", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("restart rewrote configuration", err)
	}
	resp, err := client.Get(next.DashboardURL() + "auth/token")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("successor disabled mobile authentication")
	}
	response, err = client.Post(next.DashboardURL()+"auth/token", "text/plain", strings.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatal("new dashboard login failed")
	}
}

func TestMobileRestartDoesNotRetryTerminalTeardownFailure(t *testing.T) {
	// An uninitialized turn gate gives Shutdown a real terminal refusal before
	// any file can close. Actual final-sync/close failure is covered in app.
	r := &Runtime{a: &app.App{}, configPath: filepath.Join(t.TempDir(), "must-not-exist.json")}
	next, first := r.Restart()
	if first == nil || next != nil || r.CanRetryRestart() {
		t.Fatal("terminal shutdown allowed startup/retry")
	}
	next, again := r.Restart()
	if again != first || next != nil {
		t.Fatal("terminal outcome was erased on retry")
	}
	if _, err := os.Stat(r.configPath); !os.IsNotExist(err) {
		t.Fatal("refused handoff created configuration")
	}
}

func TestMobileRestartFailureCanRetryWithoutResettingConfig(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{true: "missing", false: "unreadable"}[missing], func(t *testing.T) {
			r, path := restartFixture(t)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if missing {
				err = os.Remove(path)
			} else {
				err = os.WriteFile(path, []byte("{broken"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if next, err := r.Restart(); next != nil || err == nil {
				t.Fatal("invalid config was accepted")
			}
			if !r.CanRetryRestart() {
				t.Fatal("clean startup failure cannot retry")
			}
			if missing {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("restart minted a default config")
				}
			} else {
				raw, _ := os.ReadFile(path)
				if string(raw) != "{broken" {
					t.Fatal("restart replaced unreadable config")
				}
			}
			matches, err := filepath.Glob(path + ".unreadable-*")
			if err != nil || len(matches) != 0 {
				t.Fatal("restart quarantined config", err)
			}
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			next, err := r.Restart()
			if err != nil {
				t.Fatal("correcting the input did not recover", err)
			}
			defer stopRuntime(t, next)
			if next.DashboardURL() == "" {
				t.Fatal("recovery has no dashboard")
			}
		})
	}
}
