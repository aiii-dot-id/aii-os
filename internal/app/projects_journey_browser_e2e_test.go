//go:build !windows

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
func findChromeBin() string {
	for _, bin := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(bin); err == nil {
			return path
		}
	}
	return ""
}

// .
// .
type cdp struct {
	conn *websocket.Conn
	seq  int
}

type cdpResponse struct {
	ID     int `json:"id"`
	Result struct {
		Result struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// .
// .
// .
func (c *cdp) eval(expr string, awaitPromise bool, budget time.Duration) (string, error) {
	c.seq++
	id := c.seq
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	req, err := json.Marshal(map[string]any{
		"id":     id,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":    expr,
			"returnByValue": true,
			"awaitPromise":  awaitPromise,
		},
	})
	if err != nil {
		return "", err
	}
	if err := c.conn.Write(ctx, websocket.MessageText, req); err != nil {
		return "", fmt.Errorf("cdp write: %w", err)
	}
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return "", fmt.Errorf("cdp read: %w", err)
		}
		var resp cdpResponse
		if json.Unmarshal(data, &resp) != nil || resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return "", fmt.Errorf("cdp: %s", resp.Error.Message)
		}
		if ex := resp.Result.ExceptionDetails; ex != nil {
			detail := ex.Text
			if ex.Exception != nil && ex.Exception.Description != "" {
				detail = ex.Exception.Description
			}
			return "", fmt.Errorf("page threw: %s", detail)
		}
		var value string
		if len(resp.Result.Result.Value) > 0 {
			if json.Unmarshal(resp.Result.Result.Value, &value) != nil {
				value = string(resp.Result.Result.Value)
			}
		}
		return value, nil
	}
}

// .
// .
// .
// .
func awaitJS(predicateBody string, timeoutMs int) string {
	return `new Promise(resolve => {
  const t0 = Date.now();
  let last = '';
  (function poll() {
    let v = null;
    try { v = (function() { ` + predicateBody + ` })(); } catch (e) { last = 'threw: ' + e.message; }
    if (v !== null && v !== undefined) return resolve(String(v));
    if (Date.now() - t0 > ` + fmt.Sprint(timeoutMs) + `) return resolve('TIMEOUT ' + last);
    setTimeout(poll, 100);
  })();
})`
}

// .
// .
// .
func (c *cdp) evalRetry(expr string, awaitPromise bool, budget time.Duration, attempts int) (string, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		v, err := c.eval(expr, awaitPromise, budget)
		if err == nil {
			return v, nil
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	return "", lastErr
}

func sendProjectAct(t *testing.T, conn *websocket.Conn, reqID string, p map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjsonWrite(ctx, conn, map[string]any{
		"type": "project", "request_id": reqID, "project": p,
	}); err != nil {
		t.Fatalf("send project act: %v", err)
	}
}

func TestProjectsJourneyInARealBrowser(t *testing.T) {
	chrome := findChromeBin()
	if chrome == "" {
		t.Skip("no Blink binary on this host — the journey needs Chrome's debug protocol")
	}
	t.Log("coverage limit, named: this journey drives Blink only; Gecko render " +
		"parity is covered by the fixture browser tests, the JOIN is Chrome-only")

	// .
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Journey")
	cfg := safebootConfig(t, dir, "Journey", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(a.Stop)
	origin := a.dashboard.Origin()
	t.Logf("dashboard at %s", origin)

	// .
	// .
	opWin := dialWSS(t, origin)
	sendProjectAct(t, opWin, "journey-create", map[string]any{
		"action": "create", "name": "project-menu",
	})
	created := readUntil(t, opWin, func(m *dashboard.ServerMessage) bool {
		return m.Type == "projects" && m.RequestID == "journey-create"
	})
	if len(created.Projects) != 1 {
		t.Fatalf("after create: %d projects, want 1", len(created.Projects))
	}
	projID := created.Projects[0].ID
	if created.Projects[0].Name != "project-menu" {
		t.Fatalf("created name = %q", created.Projects[0].Name)
	}

	// .
	// .
	profile, err := os.MkdirTemp("", "aii-journey-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(profile) })
	cmd := exec.Command(chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu",
		"--no-first-run", "--no-default-browser-check",
		"--disable-extensions", "--disable-background-networking",
		"--remote-debugging-port=0", "--remote-allow-origins=*",
		// .
		// .
		"--ignore-certificate-errors",
		"--user-data-dir="+profile,
		origin+"/#/projects",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	cmd.Env = append(os.Environ(), "DBUS_SESSION_BUS_ADDRESS=disabled:")
	if err := cmd.Start(); err != nil {
		t.Fatalf("chrome start: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// .
	var debugPort string
	portFile := filepath.Join(profile, "DevToolsActivePort")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(portFile); err == nil {
			if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) > 0 && lines[0] != "" {
				debugPort = lines[0]
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if debugPort == "" {
		t.Fatal("chrome never wrote DevToolsActivePort")
	}

	// .
	var wsURL string
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && wsURL == "" {
		resp, err := http.Get("http://127.0.0.1:" + debugPort + "/json/list")
		if err == nil {
			var targets []struct {
				Type                 string `json:"type"`
				URL                  string `json:"url"`
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			if json.NewDecoder(resp.Body).Decode(&targets) == nil {
				for _, tg := range targets {
					if tg.Type == "page" && strings.Contains(tg.URL, strings.TrimPrefix(origin, "https://")) {
						wsURL = tg.WebSocketDebuggerURL
					}
				}
			}
			resp.Body.Close()
		}
		if wsURL == "" {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if wsURL == "" {
		t.Fatal("debug target for the dashboard page never appeared")
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("cdp dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(1 << 20)
	page := &cdp{conn: conn}

	// .
	// .
	// .
	dockState, err := page.evalRetry(awaitJS(`
    const items = document.querySelectorAll('#dock .dock-item[data-id]');
    const names = Array.from(items).map(el => (el.querySelector('.b-name')||{textContent:''}).textContent);
    last = 'dock names: [' + names.join(',') + ']';
    if (names.length === 1 && names[0] === 'project-menu') return last;
    return null;
  `, 20000), true, 30*time.Second, 5)
	if err != nil {
		t.Fatalf("dock await: %v", err)
	}
	if strings.HasPrefix(dockState, "TIMEOUT") {
		t.Fatalf("dock never rendered the created project from the live payload: %s", dockState)
	}
	t.Logf("dock rendered: %s", dockState)

	// .
	// .
	if _, err := page.eval(
		`document.querySelector('#dock .dock-item[data-id="`+projID+`"]').click(); 'clicked'`,
		false, 10*time.Second); err != nil {
		t.Fatalf("bubble click: %v", err)
	}

	// .
	// .
	// .
	activeState, err := page.evalRetry(awaitJS(`
    const el = document.querySelector('#dock .dock-item.active[data-id="`+projID+`"]');
    last = 'active-marked item: ' + (el ? 'present' : 'absent') + ', hash: ' + location.hash;
    if (el && location.hash === '#/projects/`+projID+`') return last;
    return null;
  `, 15000), true, 25*time.Second, 3)
	if err != nil {
		t.Fatalf("active-mark await: %v", err)
	}
	if strings.HasPrefix(activeState, "TIMEOUT") {
		t.Fatalf("click did not round-trip to an active mark + address: %s", activeState)
	}
	t.Logf("select round-tripped: %s", activeState)

	// .
	if got := a.store.ActiveProjectID(); got != projID {
		t.Fatalf("store.ActiveProjectID() = %q, want %q — the DOM said selected but the store disagrees", got, projID)
	}

	// .
	// .
	// .
	if _, err := page.eval(`location.hash = '#/home'; 'nav'`, false, 10*time.Second); err != nil {
		t.Fatalf("nav home: %v", err)
	}
	panelState, err := page.evalRetry(awaitJS(`
    const txt = document.body.innerText || '';
    const haveWorking = txt.indexOf('working in') !== -1 && txt.indexOf('project-menu') !== -1;
    const haveSession = txt.indexOf('no active session') !== -1;
    last = 'working-in: ' + haveWorking + ', session-line: ' + haveSession;
    if (haveWorking && haveSession) return last;
    return null;
  `, 15000), true, 25*time.Second, 3)
	if err != nil {
		t.Fatalf("panel await: %v", err)
	}
	if strings.HasPrefix(panelState, "TIMEOUT") {
		t.Fatalf("WORK card never spoke both facts: %s", panelState)
	}
	t.Logf("WORK card: %s", panelState)

	// .
	// .
	// .
	// .
	if _, err := page.eval(`location.reload(); 'reloading'`, false, 10*time.Second); err != nil {
		t.Fatalf("reload: %v", err)
	}
	reloadState, err := page.evalRetry(awaitJS(`
    const txt = document.body.innerText || '';
    last = 'post-reload text has working-in: ' + (txt.indexOf('working in') !== -1) +
      ', name: ' + (txt.indexOf('project-menu') !== -1);
    if (txt.indexOf('working in') !== -1 && txt.indexOf('project-menu') !== -1) return last;
    return null;
  `, 20000), true, 30*time.Second, 5)
	if err != nil {
		t.Fatalf("post-reload await: %v", err)
	}
	if strings.HasPrefix(reloadState, "TIMEOUT") {
		t.Fatalf("focus did not survive the cold load end to end: %s", reloadState)
	}
	t.Logf("cold load kept the focus: %s", reloadState)
}
