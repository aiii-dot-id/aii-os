//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

func TestTheProjectFileJourneyByKeyboard(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Keys")
	a := New(safebootConfig(t, dir, "Keys", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	origin := a.dashboard.Origin()

	op := dialWSS(t, origin)
	sendProjectAct(t, op, "keys-create", map[string]any{"action": "create", "name": "keyboard-room"})
	created := readUntil(t, op, func(m *dashboard.ServerMessage) bool {
		return m.Type == "projects" && m.RequestID == "keys-create"
	})
	if len(created.Projects) != 1 {
		t.Fatalf("after create: %d projects", len(created.Projects))
	}
	p, err := a.projects.Load(created.Projects[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Dir, "notes.md"), []byte("by keyboard\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	page := openInChrome(t, chrome, origin, "/#/home")
	await := func(what, predicate string) {
		t.Helper()
		got, err := page.evalRetry(awaitJS(predicate, 20000), true, 30*time.Second, 5)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if got != "ok" {
			t.Fatalf("%s: %s", what, got)
		}
	}
	// tabTo presses Tab until focus is on what the selector names, as a
	// person would; a control Tab never reaches fails here.
	tabTo := func(what, selector string) {
		t.Helper()
		seen := ""
		for i := 0; i < 120; i++ {
			got, err := page.eval(`(() => { const el = document.activeElement; return el && el.matches(`+jsString(selector)+`) ? 'ok' : (el ? el.tagName + '.' + el.className : 'none'); })()`, false, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if got == "ok" {
				return
			}
			seen = got
			if err := page.press("Tab"); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatalf("Tab never reached %s (last on %s)", what, seen)
	}
	press := func(key string) {
		t.Helper()
		if err := page.press(key); err != nil {
			t.Fatal(err)
		}
	}

	keyOn := func(what, selector, key string) {
		t.Helper()
		for i := 0; i < 20; i++ {
			tabTo(what, selector)
			if _, err := page.eval(`(() => { window.__keyLanded = 'no key'; document.addEventListener('keydown', (e) => { window.__keyLanded = e.target && e.target.matches && e.target.matches(`+jsString(selector)+`) ? 'ok' : 'elsewhere'; }, { capture: true, once: true }); return 'armed'; })()`, false, 10*time.Second); err != nil {
				t.Fatal(err)
			}
			press(key)
			got, err := page.eval(`window.__keyLanded`, false, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if got == "ok" {
				return
			}
		}
		t.Fatalf("%s never landed on %s: the page took focus from it every time", key, what)
	}

	await("the project's card on Home", `last = location.hash; return document.querySelector('.proj-card .proj-open') ? 'ok' : null;`)
	keyOn("the project's card", ".proj-card .proj-open", "Enter")
	await("the project opened", `
    const head = document.querySelector('#proj-space');
    last = location.hash + ' ' + (head ? head.textContent.slice(0, 80) : 'no space');
    return location.hash.startsWith('#/projects/') && head && head.textContent.includes('keyboard-room') ? 'ok' : null;`)
	keyOn("the Files tab", `#proj-space [data-tab="files"]`, "Enter")
	await("the file listed", `
    const tab = document.querySelector('#proj-space .tab.sel'), card = document.querySelector('#proj-space .card');
    last = 'tab=' + (tab ? tab.dataset.tab : 'none') + ' card=' + (card ? card.textContent.slice(0, 60) : 'none');
    return document.querySelector('#proj-space .fl-file[data-name="notes.md"] .fl-name') ? 'ok' : null;`)
	keyOn("the file", `#proj-space .fl-file[data-name="notes.md"] .fl-name`, "Enter")
	await("the viewer, with focus inside", `
    const v = document.getElementById('file-viewer');
    last = v ? 'open=' + v.open + ' focus=' + document.activeElement.tagName : 'no viewer';
    return v && v.open && v.matches(':modal') && v.contains(document.activeElement) && document.getElementById('fv-body').textContent.includes('by keyboard') ? 'ok' : null;`)
	for i := 0; i < 4; i++ {
		press("Tab")
		got, err := page.eval(`(() => { const v = document.getElementById('file-viewer'), el = document.activeElement; return v && (v.contains(el) || el === document.body) ? 'ok' : (el ? el.tagName + '.' + el.className : 'none'); })()`, false, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if got != "ok" {
			t.Fatalf("Tab left the open viewer for %s", got)
		}
	}
	press("Escape")
	await("the viewer closed, focus on the file", `
    const el = document.activeElement;
    last = 'viewer=' + !!document.getElementById('file-viewer') + ' focus=' + (el ? el.tagName + '.' + el.className + ' ' + el.textContent : 'none');
    return !document.getElementById('file-viewer') && el && el.matches('.fl-name') && el.textContent === 'notes.md' ? 'ok' : null;`)
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// press sends one key as the keyboard does: down, then up. Enter carries its
// character, which is what makes a focused button activate.
func (c *cdp) press(key string) error {
	keys := map[string]struct {
		code string
		vk   int
		text string
	}{"Tab": {"Tab", 9, ""}, "Enter": {"Enter", 13, "\r"}, "Escape": {"Escape", 27, ""}}
	k, ok := keys[key]
	if !ok {
		return fmt.Errorf("no key %q", key)
	}
	for _, typ := range []string{"keyDown", "keyUp"} {
		params := map[string]any{"type": typ, "key": key, "code": k.code, "windowsVirtualKeyCode": k.vk, "nativeVirtualKeyCode": k.vk}
		if typ == "keyDown" && k.text != "" {
			params["text"] = k.text
		}
		if err := c.call("Input.dispatchKeyEvent", params, 10*time.Second); err != nil {
			return err
		}
	}
	return nil
}

// call sends one debugger command and waits for its own answer.
func (c *cdp) call(method string, params map[string]any, budget time.Duration) error {
	c.seq++
	id := c.seq
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	req, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := c.conn.Write(ctx, websocket.MessageText, req); err != nil {
		return fmt.Errorf("cdp write: %w", err)
	}
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("cdp read: %w", err)
		}
		var resp cdpResponse
		if json.Unmarshal(data, &resp) != nil || resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return fmt.Errorf("cdp %s: %s", method, resp.Error.Message)
		}
		return nil
	}
}
