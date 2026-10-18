//go:build !windows

// THE §8.1 JOURNEY — the one gap the design of record held ITERATE
// open for: "no browser has rendered this workspace from a born
// identity's own store, over a live socket, in one unbroken path."
//
// This test is that path, with nothing test-shaped inside it:
//
//	birth → real startLive (watcher, dashboard on its live listener;
//	loopback HTTP under the test config, TLS when the config carries
//	a cert) → the operator's other window creates a project over the
//	real project verb → a real Chrome loads the real page → the real
//	socket carries the real payloads → the DOM renders the dock → a
//	real DOM click
//	commits the selection → the server's store changes → the render
//	answers with the active mark and the WORK card sentence → a page
//	reload proves the whole join again from a cold load.
//
// The page is observed from OUTSIDE via the Chrome DevTools protocol
// (--remote-debugging-port): the harness-in-page pattern of the
// dashboard browser tests cannot apply here, because the page under
// test is the production page served by the production server, and
// production carries no result channel. CDP observes without altering
// the artifact.
//
// COVERAGE LIMITS, NAMED (the browserengine precedent): this journey
// drives Blink only. Gecko exposes no CDP (WebDriver BiDi needs a
// geckodriver this repo does not carry), WebKit does not exist on
// Linux. Engine-level render parity for both IS covered — by the
// fixture-driven dashboard browser tests, which run Chrome and
// Firefox alike. What is Chrome-only here is the JOIN, not the render.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/browsertest"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// chromeBin is the Blink browser the journeys drive through its debug
// protocol, Chrome or Chromium, wherever this system installs it — on PATH,
// in a macOS application bundle, under Windows' Program Files — by the one
// finder the dashboard's engine rig uses (browsertest.Find), and narrowed
// by the same AII_BROWSER_ENGINES. When there is none, why says where it
// was looked for. (It looked on PATH alone, so on a Mac, where Chrome lives
// only in its bundle, every journey skipped.)
func chromeBin() (bin, why string) {
	if !browsertest.Allowed(browsertest.Chrome) {
		return "", browsertest.Looked(browsertest.Chrome, nil)
	}
	bin, looked := browsertest.Find(browsertest.Chrome)
	return bin, "looked: " + browsertest.Looked(browsertest.Chrome, looked)
}

// needChrome is chromeBin for a journey: with no browser, the journey skips
// and says where it looked.
func needChrome(t *testing.T) string {
	t.Helper()
	bin, why := chromeBin()
	if bin == "" {
		t.Skip("no Chrome or Chromium to drive — the journey needs Chrome's debug protocol; " + why)
	}
	return bin
}

// cdp is a minimal DevTools client over the repo's existing websocket
// dependency: sequenced Runtime.evaluate, events skipped by id-match.
type cdp struct {
	conn  *websocket.Conn
	seq   int
	trace func([]byte)
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

// command sends one explicit protocol command and retains navigation diagnostics.
func (c *cdp) command(method string, params map[string]any, budget time.Duration) (json.RawMessage, error) {
	c.seq++
	id := c.seq
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	req, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if err = c.conn.Write(ctx, websocket.MessageText, req); err != nil {
		return nil, err
	}
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		if c.trace != nil {
			c.trace(data)
		}
		var response struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &response) != nil || response.ID != id {
			continue
		}
		if response.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, response.Error.Message)
		}
		return response.Result, nil
	}
}

// eval runs an expression in the page and returns its string value.
// awaitPromise lets an expression hand back a Promise that resolves
// when the DOM reaches the awaited state — the in-page poll pattern.
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
		if c.trace != nil {
			c.trace(data)
		}
		var resp cdpResponse
		if json.Unmarshal(data, &resp) != nil || resp.ID != id {
			continue // an event, or another call's answer — not ours
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

// awaitJS wraps a predicate body into an in-page poll: the body runs
// every 100ms until it returns non-null (resolved with that string) or
// the budget lapses (resolved with a TIMEOUT diagnosis carrying the
// predicate's last look at the DOM, so a failure names what WAS there).
func awaitJS(predicateBody string, timeoutMs int) string {
	return `new Promise(resolve => {
  const t0 = Date.now();
  let last = '';
  (function poll() {
    let v = null;
    try { v = (function() { ` + predicateBody + ` })(); } catch (e) { last = 'threw: ' + e.message; }
    if (v !== null && v !== undefined) return resolve(String(v));
    if (Date.now() - t0 > ` + fmt.Sprint(timeoutMs) + `) return resolve('TIMEOUT ' + last + ' PAGE ' + JSON.stringify({url:document.URL,title:document.title,ready:document.readyState,body:document.body&&document.body.outerHTML.slice(0,16000),resources:performance.getEntriesByType('resource').map(x=>({name:x.name,responseStatus:x.responseStatus,duration:x.duration}))}));
    setTimeout(poll, 100);
  })();
})`
}

// evalRetry re-issues an eval across an execution-context change — a
// reload destroys the context mid-flight, which surfaces as an error or
// a page-threw; the next attempt lands in the new context.
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

// openInChrome starts a headless Chrome at origin+path and returns the
// page's debugger connection. The browser, its profile and the connection
// go with the test.
func openInChrome(t *testing.T, chrome, origin, path string) *cdp {
	t.Helper()
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
		// A desktop window, named. The dashboard lays itself out as a
		// phone below 768 px and hides the side panel there (layout.css),
		// and an engine's default headless window is no fact a journey may
		// rest on: Chrome's on macOS is 756 x 413, so the WORK card the
		// projects journey reads was hidden on a Mac and shown on Linux.
		"--window-size=1280,900",
		// Kept for configs that serve TLS with a self-signed cert;
		// inert on the loopback-HTTP listener the test config yields.
		"--ignore-certificate-errors",
		"--user-data-dir="+profile,
		"about:blank",
	)

	logDir := os.Getenv("AII_NATIVE_BROWSER_EVIDENCE")
	if logDir == "" {
		logDir = t.TempDir()
	}
	stem := strings.ReplaceAll(t.Name(), "/", "_") + fmt.Sprint(time.Now().UnixNano())
	browserLogPath := filepath.Join(logDir, stem+"-chrome.log")
	browserLog, err := os.OpenFile(browserLogPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	eventLogPath := filepath.Join(logDir, stem+"-cdp.jsonl")
	eventLog, err := os.OpenFile(eventLogPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		browserLog.Close()
		t.Fatal(err)
	}
	cmd.Stdout = browserLog
	cmd.Stderr = browserLog
	t.Logf("owned browser diagnostics: %s %s", browserLogPath, eventLogPath)
	t.Cleanup(func() {
		browserLog.Close()
		eventLog.Close()
		if t.Failed() {
			for _, p := range []string{browserLogPath, eventLogPath} {
				f, err := os.Open(p)
				if err != nil {
					t.Logf("read browser diagnostics %s: %v", p, err)
					continue
				}
				info, err := f.Stat()
				if err != nil {
					f.Close()
					t.Logf("stat browser diagnostics %s: %v", p, err)
					continue
				}
				b := make([]byte, min(info.Size(), 16000))
				n, _ := f.ReadAt(b, max(0, info.Size()-int64(len(b))))
				f.Close()
				t.Logf("%s: %s", p, b[:n])
			}
		}
	})
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.Env = append(os.Environ(), "DBUS_SESSION_BUS_ADDRESS=disabled:")
	if err := cmd.Start(); err != nil {
		t.Fatalf("chrome start: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	// ---- The debug port Chrome chose, from its own hand.
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

	// ---- Find the page target and dial its debugger socket.
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
				pages := 0
				for _, tg := range targets {
					if tg.Type == "page" {
						pages++
						wsURL = tg.WebSocketDebuggerURL
					}
				}
				if pages > 1 {
					t.Fatalf("fresh owned browser has %d page targets; refusing ambiguous navigation: %+v", pages, targets)
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
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	conn.SetReadLimit(1 << 20)

	page := &cdp{conn: conn}
	page.trace = func(data []byte) {
		var e struct {
			Method string `json:"method"`
			Params struct {
				Type    string `json:"type"`
				Request struct {
					URL string `json:"url"`
				} `json:"request"`
			} `json:"params"`
		}
		if json.Unmarshal(data, &e) != nil {
			return
		}
		switch e.Method {
		case "Page.frameNavigated", "Page.frameStoppedLoading", "Page.lifecycleEvent", "Network.loadingFailed":
			eventLog.Write(append(data, '\n'))
		case "Network.requestWillBeSent":
			b, _ := json.Marshal(map[string]string{"method": e.Method, "type": e.Params.Type, "url": e.Params.Request.URL})
			eventLog.Write(append(b, '\n'))
		}
	}
	for _, method := range []string{"Page.enable", "Network.enable"} {
		if _, err := page.command(method, map[string]any{}, 5*time.Second); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	destination := origin + path
	target, err := url.Parse(destination)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := page.command("Page.navigate", map[string]any{"url": destination}, 10*time.Second)
	if err != nil {
		t.Fatalf("explicit dashboard navigation: %v", err)
	}
	var nav struct {
		FrameID    string `json:"frameId"`
		ErrorText  string `json:"errorText"`
		IsDownload bool   `json:"isDownload"`
	}
	if json.Unmarshal(raw, &nav) != nil || nav.FrameID == "" || nav.ErrorText != "" || nav.IsDownload {
		t.Fatalf("dashboard navigation refused: %s", raw)
	}
	t.Logf("one explicit Page.navigate to %s acknowledged: %s", destination, raw)

	// The target may advertise its requested URL before its
	// initial about:blank execution context has committed that navigation.
	// Do not place an awaited promise into that initial context.
	deadline = time.Now().Add(20 * time.Second)
	var document string
	for time.Now().Before(deadline) {
		document, err = page.eval(`JSON.stringify({url:document.URL,origin:location.origin,title:document.title,ready:document.readyState})`, false, 5*time.Second)
		var state struct {
			URL    string `json:"url"`
			Origin string `json:"origin"`
			Ready  string `json:"ready"`
		}
		if err == nil && json.Unmarshal([]byte(document), &state) == nil && state.Origin == origin && (state.Ready == "interactive" || state.Ready == "complete") {
			committed, parseErr := url.Parse(state.URL)
			if parseErr == nil && committed.Path == target.Path && committed.RawQuery == target.RawQuery && (target.Fragment == "" || committed.Fragment == target.Fragment) {
				return page
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("dashboard document never committed: %s (%v)", document, err)
	return nil
}

func TestProjectsJourneyInARealBrowser(t *testing.T) {
	chrome := needChrome(t)
	t.Log("coverage limit, named: this journey drives Blink only; Gecko render " +
		"parity is covered by the fixture browser tests, the JOIN is Chrome-only")

	// ---- A born identity, live boot, real TLS dashboard.
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Journey")
	cfg := safebootConfig(t, dir, "Journey", keyPath, ledgerPath, dbPath)
	cfg.Dashboard.Port = 0
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	origin := a.dashboard.Origin()
	t.Logf("dashboard at %s", origin)

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

	// ---- A real Chrome at the real page, deep-linked to Projects by
	// address (§8.7): the URL is the router.
	page := openInChrome(t, chrome, origin, "/#/projects")

	// ---- STEP 1: the dock renders the born store's truth. Exactly the
	// one created project — a ghost name must be absent, and the count
	// must be exact, so this assertion cannot pass vacuously.
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

	// ---- STEP 2: a real DOM click — the click IS the commitment
	// (operator verdict, three reports). No synthetic select frame.
	if _, err := page.eval(
		`document.querySelector('#dock .dock-item[data-id="`+projID+`"]').click(); 'clicked'`,
		false, 10*time.Second); err != nil {
		t.Fatalf("bubble click: %v", err)
	}

	// ---- STEP 3: the render answers with server truth — the active
	// mark arrives from the select's round trip, and the address bar
	// carries the project (§8.7: one act, one address).
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

	// ---- STEP 4: the far end of the join — the identity's own store.
	if got := a.store.ActiveProjectID(); got != projID {
		t.Fatalf("store.ActiveProjectID() = %q, want %q — the DOM said selected but the store disagrees", got, projID)
	}

	// ---- STEP 5: the WORK card speaks both facts (the reported defect
	// of this arc: project focus and session state are different facts,
	// and the empty wording must be about sessions only).
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

	// ---- STEP 6: cold load. The reload walks boot restore → payloads
	// → render again; the focus must survive (the operator's "rebuilt
	// from main, chose a project, restarted" report, as a machine
	// check).
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
