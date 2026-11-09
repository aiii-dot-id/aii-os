//go:build !windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/coder/websocket"
)

// commandIn is command in a session of a target the page holds: a sandboxed frame
// runs in a process of its own, and the page's own session does not reach it.
func (c *cdp) commandIn(session, method string, params map[string]any, budget time.Duration) (json.RawMessage, error) {
	c.seq++
	id := c.seq
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	req, err := json.Marshal(map[string]any{"id": id, "sessionId": session, "method": method, "params": params})
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

// reloadWatch runs in each document before anything of it: when the page last
// changed, and whether it was ever shown as a frame with no name in it.
const reloadWatch = `(() => {
  window.__drawnAt = 0; window.__partial = '';
  new MutationObserver(() => { window.__drawnAt = performance.now(); }).observe(document, { subtree: true, childList: true, characterData: true, attributes: true, attributeFilter: ['class', 'hidden'] });
  const watch = () => {
    const app = document.querySelector('.app'), name = document.getElementById('p-name');
    if (app && name && getComputedStyle(app).opacity !== '0' && name.textContent.trim() === '…' && !window.__partial) window.__partial = 'the page was shown with no name, at ' + Math.round(performance.now()) + ' ms';
    requestAnimationFrame(watch);
  };
  requestAnimationFrame(watch);
})()`

const reloadSettled = `
  if (window.__leaving || document.querySelector('.p-state.connecting')) return null;
  // The conversation is drawn from reads of its own: Chat waits for its rows.
  if (location.hash === '#/chat' && !document.querySelector('#thread-inner > .interaction-item')) return null;
  // Projects draws its dock from a read of its own (the gate at e274c2c3, 2026-11-06: the shell held still for half a second before it, and the read saw none of its words).
  if (location.hash === '#/projects' && !document.querySelector('#dock .dock-item, #dock .dock-empty')) return null;
  // A built-in section's place is settled when its frame runs in it (it draws what the page drew).
  const place = location.hash.replace(/^#\/+/, '');
  if ((place === 'settings' || (place.startsWith('identity') && !place.startsWith('identity/record'))) &&
      ![...document.querySelectorAll('.section-frame')].some(f => f.getBoundingClientRect().height > 0 && f.style.visibility !== 'hidden')) return null;
  const now = (document.querySelector('.app') || document.body).innerText;
  if (now !== window.__seen) { window.__seen = now; window.__since = Date.now(); return null; }
  return Date.now() - window.__since < 500 ? null : 'ok';`

const reloadLook = `(() => {
  const scrollers = [...document.querySelectorAll('.stage, .stage *')].filter(e => e.getClientRects().length && e.scrollHeight > e.clientHeight + 4 && /(auto|scroll)/.test(getComputedStyle(e).overflowY));
  const at = e => e.id || e.className;
  return JSON.stringify({
    hash: location.hash, text: (document.querySelector('.app') || document.body).innerText,
    open: [...document.querySelectorAll('.stage details[open]')].filter(e => e.getClientRects().length && !e.closest('#thread')).map(e => (e.querySelector(':scope > summary') || e).textContent.trim()),
    scroll: Object.fromEntries(scrollers.map(e => [at(e), Math.round(e.scrollTop)])),
    reading: (() => { const t = document.getElementById('thread'); if (!t || !t.getClientRects().length || t.scrollHeight <= t.clientHeight) return ''; const top = t.getBoundingClientRect().top; const first = [...document.querySelectorAll('#thread-inner > .interaction-item')].find(e => e.getBoundingClientRect().bottom > top); return first ? first.dataset.interactionKey + ' at ' + Math.round(first.getBoundingClientRect().top - top) : ''; })(),
    draft: document.getElementById('msg-input')?.value ?? '', drawn: Math.round(window.__drawnAt), partial: window.__partial,
  });
})()`

type reloadSight struct {
	Hash    string           `json:"hash"`
	Text    string           `json:"text"`
	Open    []string         `json:"open"`
	Scroll  map[string]int64 `json:"scroll"`
	Draft   string           `json:"draft"`
	Reading string           `json:"reading"`
	Drawn   int64            `json:"drawn"`
	Partial string           `json:"partial"`
}

var reloadClock = regexp.MustCompile(`\b\d{1,2}:\d{2}(:\d{2})?(\s?[AP]M)?\b|\b\d+ (second|minute|hour)s? ago\b`)

func reloadLines(s string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(reloadClock.ReplaceAllString(s, "<time>"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out[l] = true
		}
	}
	return out
}

// A RELOAD IS FAST, COMPLETE AND IDENTICAL (the operator's three, word for
// word). On the real page, with a long thread: at each place what a person
// changes is changed (a scroll by the wheel, a disclosure opened, a draft
// typed), the page is reloaded, and what it shows after is what it showed
// before: the same words, the same open disclosures, the same scroll, the same
// draft, drawn no slower than the first time and never as a frame with no name
// in it. Over a slow link too, since that is where a page stands half-drawn.
func TestAReloadIsFastCompleteAndIdentical(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Reload")
	a := New(safebootConfig(t, dir, "Reload", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	for i := 0; i < 400; i++ {
		role := interaction.Operator
		if i%2 == 1 {
			role = interaction.Resident
		}
		words := fmt.Sprintf("Row %d of a long conversation, with enough words in it to take a line or two of the page when it is drawn.", i)
		if _, err := a.store.RecordInteraction(t.Context(), store.InteractionInput{ID: fmt.Sprintf("long_%d", i), Kind: interaction.Message, Role: role, Content: words}); err != nil {
			t.Fatal(err)
		}
	}
	seedLongBeliefs(t, a.store)
	page := openInChrome(t, chrome, a.dashboard.Origin(), "/")
	command := func(method string, params map[string]any) {
		t.Helper()
		if _, err := page.command(method, params, 15*time.Second); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	command("Page.enable", nil)
	command("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": reloadWatch})
	// The sandboxed frames the page holds, as they attach and go.
	type frameSession struct{ url, session string }
	frames := map[string]frameSession{}
	trace := page.trace
	page.trace = func(raw []byte) {
		trace(raw)
		var event struct {
			Method string `json:"method"`
			Params struct {
				SessionID  string `json:"sessionId"`
				TargetID   string `json:"targetId"`
				TargetInfo struct {
					TargetID string `json:"targetId"`
					URL      string `json:"url"`
				} `json:"targetInfo"`
			} `json:"params"`
		}
		if json.Unmarshal(raw, &event) != nil {
			return
		}
		switch event.Method {
		case "Target.attachedToTarget":
			frames[event.Params.TargetInfo.TargetID] = frameSession{event.Params.TargetInfo.URL, event.Params.SessionID}
		case "Target.targetInfoChanged":
			if f, ok := frames[event.Params.TargetInfo.TargetID]; ok {
				f.url = event.Params.TargetInfo.URL
				frames[event.Params.TargetInfo.TargetID] = f
			}
		case "Target.detachedFromTarget":
			for id, f := range frames {
				if f.session == event.Params.SessionID {
					delete(frames, id)
				}
			}
		}
	}
	command("Target.setDiscoverTargets", map[string]any{"discover": true})
	settle := func(what string) reloadSight {
		t.Helper()
		got, err := page.evalRetry(awaitJS(reloadSettled, 25000), true, 35*time.Second, 5)
		if err != nil || got != "ok" {
			t.Fatalf("%s did not settle: %v %s", what, err, got)
		}
		raw, err := page.evalRetry(reloadLook, false, 10*time.Second, 3)
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		var s reloadSight
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return s
	}
	act := func(js string) string {
		t.Helper()
		got, err := page.eval(js, true, 15*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	reload := func() {
		t.Helper()
		act(`(() => { window.__leaving = true; return 'ok'; })()`)
		command("Page.reload", map[string]any{})
	}
	// inSection reads a built-in section's sandboxed frame, which the page cannot.
	inSection := func(section, expr string) string {
		t.Helper()
		// Asking anything hears what has attached since.
		if _, err := page.command("Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": false, "flatten": true}, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		session := ""
		for _, f := range frames {
			if strings.Contains(f.url, "/built-in-sections/"+section+"/") {
				session = f.session
			}
		}
		if session == "" {
			return ""
		}
		out, err := page.commandIn(session, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true}, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out, &v); err != nil {
			t.Fatal(err)
		}
		return v.Result.Value
	}
	sectionOf := func(place string) string {
		switch {
		case place == "settings":
			return "status"
		case strings.HasPrefix(place, "identity") && place != "identity/record":
			return "about"
		}
		return ""
	}
	const sectionLook = `(() => { const se = document.scrollingElement || document.documentElement; const all = [se, ...document.querySelectorAll('body *')].filter(e => e === se || /(auto|scroll)/.test(getComputedStyle(e).overflowY)); const s = all.sort((p, q) => (q.scrollHeight - q.clientHeight) - (p.scrollHeight - p.clientHeight))[0]; return JSON.stringify({ text: document.body.innerText, scroll: Math.round(s.scrollTop), max: Math.round(s.scrollHeight - s.clientHeight) }); })()`
	type sectionSight struct {
		Text   string `json:"text"`
		Scroll int64  `json:"scroll"`
		Max    int64  `json:"max"`
	}
	for _, size := range []struct {
		w, h   int
		places []string
	}{
		{1280, 900, []string{"chat", "projects", "identity", "identity/between", "identity/beliefs", "identity/experiences", "identity/record",
			"settings", "settings/phone", "settings/model", "settings/voice", "settings/permissions", "settings/people", "settings/access", "settings/backups", "settings/app", "settings/technical"}},
		{390, 844, []string{"chat", "identity/record", "settings/model", "settings/app"}},
	} {
		command("Emulation.setDeviceMetricsOverride", map[string]any{"width": size.w, "height": size.h, "deviceScaleFactor": 1, "mobile": false})
		for _, place := range size.places {
			name := fmt.Sprintf("%s at %d", place, size.w)
			command("Page.navigate", map[string]any{"url": a.dashboard.Origin() + "/#/" + place})
			reload() // a fresh document at this address, so the first draw is measured
			first := settle(name)
			// A person's own: the wheel over what scrolls, a disclosure, a draft.
			over := act(`(() => {
  const s = [...document.querySelectorAll('.stage, .stage *')].filter(e => e.getClientRects().length && e.scrollHeight > e.clientHeight + 40 && /(auto|scroll)/.test(getComputedStyle(e).overflowY)).sort((p, q) => q.scrollHeight - p.scrollHeight)[0];
  const d = [...document.querySelectorAll('.stage details:not([open])')].find(e => e.getClientRects().length && !e.closest('#thread'));
  if (d) d.open = true;
  const m = document.getElementById('msg-input');
  if (m && m.getClientRects().length) { m.value = 'A draft I was typing'; m.dispatchEvent(new Event('input', { bubbles: true })); }
  if (!s) return '';
  window.__wheeled = s;
  const r = s.getBoundingClientRect();
  return JSON.stringify({ x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2), dy: s.id === 'thread' ? -900 : 240, at: Math.round(s.scrollTop) });
})()`)
			if over != "" {
				var at struct{ X, Y, Dy, At int }
				if err := json.Unmarshal([]byte(over), &at); err != nil {
					t.Fatal(err)
				}
				command("Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": at.X, "y": at.Y, "deltaX": 0, "deltaY": at.Dy})
				// The wheel scrolls in the browser's own time, which under load is
				// after the measure: what is measured is the place the wheel left,
				// so wait until the scroller has moved and come to rest.
				moved := false
				for i, was := 0, -1; i < 50; i++ {
					got, err := page.eval(`String(Math.round(window.__wheeled ? window.__wheeled.scrollTop : -1))`, false, 5*time.Second)
					if err != nil {
						t.Fatal(err)
					}
					now, _ := strconv.Atoi(got)
					if now != at.At && now == was {
						moved = true
						break
					}
					was = now
					time.Sleep(100 * time.Millisecond)
				}
				if place == "chat" && !moved {
					t.Errorf("%s: the wheel did not move the conversation (it stood at %d)", name, at.At)
				}
			}
			var sectionBefore sectionSight
			section := sectionOf(place)
			if section != "" {
				// The wheel reaches a frame in its own process in its own time,
				// and one that comes before the frame can take it is lost (the
				// gate of 204377a4, 2026-11-06: 0 of 23,333 under load; and
				// three turns back to back all lost under load, the bisect at
				// e274c2c3): the frame is laid out first (Max > 0), then a wheel
				// that moved nothing is turned again, 250 ms apart, eight
				// times at most.
				for i := 0; i < 100; i++ {
					if err := json.Unmarshal([]byte(inSection(section, sectionLook)), &sectionBefore); err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					if sectionBefore.Max > 0 {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				for turn := 0; turn < 8 && (turn == 0 || sectionBefore.Max > 0 && sectionBefore.Scroll == 0); turn++ {
					if turn > 0 {
						time.Sleep(250 * time.Millisecond)
					}
					command("Input.dispatchMouseEvent", map[string]any{"type": "mouseWheel", "x": size.w * 2 / 3, "y": size.h / 2, "deltaX": 0, "deltaY": 400})
					settle(name + ", scrolled")
					for i, was := 0, int64(-1); i < 40; i++ {
						if err := json.Unmarshal([]byte(inSection(section, sectionLook)), &sectionBefore); err != nil {
							t.Fatalf("%s: %v", name, err)
						}
						if sectionBefore.Max == 0 || (sectionBefore.Scroll > 0 && sectionBefore.Scroll == was) {
							break
						}
						was = sectionBefore.Scroll
						time.Sleep(100 * time.Millisecond)
					}
				}
				if strings.TrimSpace(sectionBefore.Text) == "" {
					t.Fatalf("%s: the section's frame says nothing: the test is reading the wrong one", name)
				}
				if place == "identity/beliefs" && sectionBefore.Scroll == 0 {
					t.Fatalf("%s: the wheel moved nothing in the section (%d of %d)", name, sectionBefore.Scroll, sectionBefore.Max)
				}
				// A reload is what a person does a moment later.
				if _, err := page.eval(`new Promise(r => setTimeout(r, 300))`, true, 5*time.Second); err != nil { // waits by design: the section says its place as it settles
					t.Fatal(err)
				}
			}
			// An editor a press opens is a disclosure like the others.
			if place == "settings/model" {
				if _, err := page.eval(`(() => { document.querySelector('[data-prov-manage]').click(); return 'ok'; })()`, false, 10*time.Second); err != nil {
					t.Fatal(err)
				}
			}
			// What was asked of recall is the reader's place.
			if place == "identity/beliefs" {
				inSection(section, `(() => { const s = document.getElementById('mem-search'); s.value = 'lighthouse'; s.dispatchEvent(new Event('input', { bubbles: true })); s.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); return 'asked'; })()`)
				got := ""
				for i := 0; i < 100 && got == ""; i++ {
					got = inSection(section, `document.querySelector('.beliefs__recalled') ? 'in' : ''`)
					if got == "" {
						time.Sleep(50 * time.Millisecond)
					}
				}
				if got == "" {
					t.Errorf("%s: recall did not answer", name)
				}
				raw := inSection(section, sectionLook)
				if err := json.Unmarshal([]byte(raw), &sectionBefore); err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if _, err := page.eval(`new Promise(r => setTimeout(r, 300))`, true, 5*time.Second); err != nil { // waits by design: the section says its place as it settles
					t.Fatal(err)
				}
			}
			before := settle(name + ", changed")
			if place == "chat" && before.Reading == "" {
				t.Errorf("%s: no row of the seeded conversation stands at the thread's top to compare", name)
			}
			reload()
			after := settle(name + ", reloaded")
			// What the page saved as it went is where the reader was.
			if place == "chat" {
				kept, err := page.eval(`(() => { const b = (JSON.parse(sessionStorage.getItem('aii.history.position') || '{}') || {}).reading; return b ? b.key + ' at ' + Math.round(b.offset) : ''; })()`, false, 10*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if kept != before.Reading {
					t.Errorf("%s: the page saved %s as it went; the reader was at %s", name, kept, before.Reading)
				}
				t.Logf("%s: the reader at %s; the page saved %s; after the reload %s", name, before.Reading, kept, after.Reading)
			}
			if place == "identity/beliefs" {
				got := ""
				for i := 0; i < 100 && got == ""; i++ {
					got = inSection(section, `document.querySelector('.beliefs__recalled') ? 'in' : ''`)
					if got == "" {
						time.Sleep(50 * time.Millisecond)
					}
				}
				if got == "" {
					t.Errorf("%s: what was asked of recall was not asked again", name)
				}
			}
			if section != "" {
				// The section draws in its own frame, after the page has
				// settled: it is read once its words have held still for half
				// a second, ten seconds at most (the gate of 204377a4: read
				// before Experiences had said "No experiences yet.").
				var sectionAfter sectionSight
				for i, held, last := 0, 0, ""; i < 100 && held < 5; i++ {
					raw := inSection(section, sectionLook)
					if err := json.Unmarshal([]byte(raw), &sectionAfter); err != nil {
						t.Fatalf("%s: %v (%s)", name, err, raw)
					}
					if sectionAfter.Text != "" && sectionAfter.Text == last {
						held++
					} else {
						held, last = 0, sectionAfter.Text
					}
					time.Sleep(100 * time.Millisecond)
				}
				if d := sectionBefore.Scroll - sectionAfter.Scroll; d > 2 || d < -2 {
					t.Errorf("%s: the section was scrolled to %d of %d and is now at %d of %d", name, sectionBefore.Scroll, sectionBefore.Max, sectionAfter.Scroll, sectionAfter.Max)
				}
				was, is := reloadLines(sectionBefore.Text), reloadLines(sectionAfter.Text)
				for l := range was {
					if !is[l] {
						t.Errorf("%s: the section lost %q", name, l)
					}
				}
				for l := range is {
					if !was[l] {
						t.Errorf("%s: the section gained %q", name, l)
					}
				}
			}
			was, is := reloadLines(before.Text), reloadLines(after.Text)
			var lost, added []string
			for l := range was {
				if !is[l] {
					lost = append(lost, l)
				}
			}
			for l := range is {
				if !was[l] {
					added = append(added, l)
				}
			}
			if len(lost)+len(added) > 0 {
				t.Errorf("%s: the words differ; lost %q, added %q", name, lost, added)
			}
			if place == "settings/model" {
				open, err := page.evalRetry(`document.querySelector('[data-provider-editor]') ? 'open' : 'closed'`, false, 10*time.Second, 3)
				if err != nil || open != "open" {
					t.Errorf("%s: the provider editor opened before the reload is %s (%v)", name, open, err)
				}
			}
			if before.Hash != after.Hash {
				t.Errorf("%s: the address moved from %s to %s", name, before.Hash, after.Hash)
			}
			if fmt.Sprint(before.Open) != fmt.Sprint(after.Open) {
				t.Errorf("%s: the open disclosures differ: %q, then %q", name, before.Open, after.Open)
			}
			// The thread's place is the row at its top and where it stands.
			if before.Reading != after.Reading {
				t.Errorf("%s: the reader was at %s and is now at %s", name, before.Reading, after.Reading)
			}
			for k, v := range before.Scroll {
				if k == "thread" {
					continue
				}
				if d := v - after.Scroll[k]; d > 2 || d < -2 {
					t.Errorf("%s: %s was scrolled to %d and is now at %d", name, k, v, after.Scroll[k])
				}
			}
			if before.Draft != after.Draft {
				t.Errorf("%s: the draft was %q and is now %q", name, before.Draft, after.Draft)
			}
			if after.Partial != "" {
				t.Errorf("%s: %s", name, after.Partial)
			}
			if after.Drawn > 2*first.Drawn+1000 {
				// A LOAD SPIKE FAILS NOTHING (a busy machine can tip this bound
				// while the page alone is quick): the second draw is measured once more, and a page
				// that is really slower breaks the bound again.
				reload()
				again := settle(name + ", reloaded again")
				if again.Drawn > 2*first.Drawn+1000 {
					t.Errorf("%s: drawn in %d ms after the reload and %d ms after another, %d ms the first time", name, after.Drawn, again.Drawn, first.Drawn)
				} else {
					t.Logf("%s: drawn in %d ms after the reload (a spike: %d ms after another), %d ms the first time", name, after.Drawn, again.Drawn, first.Drawn)
				}
			}
			t.Logf("%s: first draw %d ms, reload %d ms", name, first.Drawn, after.Drawn)
		}
	}
	// A slow link: the page is not shown half-drawn while its parts arrive.
	command("Emulation.setDeviceMetricsOverride", map[string]any{"width": 1280, "height": 900, "deviceScaleFactor": 1, "mobile": false})
	command("Network.enable", nil)
	command("Network.emulateNetworkConditions", map[string]any{"offline": false, "latency": 150, "downloadThroughput": 500000, "uploadThroughput": 500000})
	for _, place := range []string{"chat", "settings/model"} {
		command("Page.navigate", map[string]any{"url": a.dashboard.Origin() + "/#/" + place})
		reload()
		after := settle(place + " over a slow link")
		if after.Partial != "" {
			t.Errorf("%s over a slow link: %s", place, after.Partial)
		}
		t.Logf("%s over a slow link: drawn in %d ms", place, after.Drawn)
	}
}

// TALK ANSWERS, AND THE MICROPHONE HOLDS WHAT TALK SAYS. On the real page and
// host: a choice under Talk and Save, the answer beside Save, and the status the
// reply box's microphone is drawn from carries the same pair at the same moment.
func TestTalkAnswersAndTheMicrophoneHoldsTheSame(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Talk")
	a := New(safebootConfig(t, dir, "Talk", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/settings/voice")
	awaitOnPage(t, page, "Talk is drawn", `return document.getElementById('sp-voice-mode') ? 'ok' : null;`)
	for _, listen := range []string{"meeting", "off", "interactive"} {
		if _, err := page.eval(`(() => {
  const radio = document.getElementById('sp-mode-listen-`+listen+`');
  radio.checked = true; radio.dispatchEvent(new Event('change', { bubbles: true }));
  document.querySelector('[data-save="speech"]').click();
  return 'ok';
})()`, false, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		awaitOnPage(t, page, "Talk's answer to "+listen, `
  const said = document.querySelector('[data-said="speech"]');
  if (!said || !said.getClientRects().length) return null;
  return said.textContent === 'Saved.' ? 'ok' : 'FAIL a save on Talk says: ' + said.textContent;`)
		// THE PAGE COMES TO AGREE. "Saved." answers the save; the listening
		// the host reports (stats) arrives in its own broadcast, after it.
		// Read once, the two raced, and under the race detector the stats
		// were a beat behind (the public tree's gate, cut h, 2026-11-08:
		// stats "meeting", config "off", rev 1 of 2). Polled, the page must
		// still agree within the wait, or the test fails with what it read.
		if _, err := page.eval(`(async () => { window.__talkState = (await import('/state.js')).S; return 'bound'; })()`, true, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		got, err := page.evalRetry(awaitJS(`const S = window.__talkState;
  const radio = document.querySelector('input[name="sp-mode-listen"]:checked');
  const p = { stats: S.stats.voice_listen, config: S.config.speech.mode.listen, radio: radio ? radio.value : '', rev: S.stats.voice_mode_revision, configRev: S.config.speech.mode.revision };
  window.__talkPair = JSON.stringify(p);
  return p.stats === '`+listen+`' && p.config === '`+listen+`' && p.radio === '`+listen+`' && p.rev === p.configRev ? 'ok' : null;`, 20000), true, 30*time.Second, 3)
		if err != nil {
			t.Fatal(err)
		}
		if got != "ok" {
			pair, _ := page.eval(`window.__talkPair`, false, 5*time.Second)
			t.Fatalf("after %s the page never came to agree: %s", listen, pair)
		}
	}
	// A change of echo is said as its own.
	got := awaitOnPage(t, page, "the echo choice", `const pick = document.getElementById('sp-echo-mode'); if (!pick) return null;
  const other = [...pick.options].find(o => o.value !== pick.value); if (!other) return 'none';
  pick.value = other.value; pick.dispatchEvent(new Event('change', { bubbles: true }));
  document.querySelector('[data-save="speech"]').click();
  return 'pressed';`)
	if got == "pressed" {
		said := awaitOnPage(t, page, "echo's answer", `const said = document.querySelector('[data-said="speech"]'); return said && said.textContent.includes('echo') ? said.textContent : null;`)
		if !strings.Contains(said, "echo choice") {
			t.Fatalf("a change of echo says: %s", said)
		}
	}
}

// A RELOAD REDRAWS THE CONVERSATION THE PAGE HELD. A read comes back fifty rows
// at a time, and a page that stays open gathers more as the conversation goes on,
// up to its window of two hundred. What it held is read again wherever the reader
// was: a page at the bottom once read only its newest fifty. And a screen is never
// left short: fifty rows of one long turn draw as one receipt, and the conversation
// above it is read until the screen is full or the record begins.
func TestAReloadRedrawsTheConversationThePageHeld(t *testing.T) {
	chrome := needChrome(t)
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Held")
	a := New(safebootConfig(t, dir, "Held", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	say := func(i int) {
		t.Helper()
		role := interaction.Operator
		if i%2 == 1 {
			role = interaction.Resident
		}
		if _, err := a.store.RecordInteraction(t.Context(), store.InteractionInput{ID: fmt.Sprintf("held_%d", i), Kind: interaction.Message, Role: role, Content: fmt.Sprintf("Reply %d.", i)}); err != nil {
			t.Fatal(err)
		}
	}
	say(0)
	say(1)
	page := openInChrome(t, chrome, a.dashboard.Origin(), "/#/chat")
	awaitOnPage(t, page, "the conversation", `return document.getElementById('thread-inner')?.innerText.includes('Reply 1.') ? 'ok' : null;`)
	// The conversation goes on with the page open.
	for i := 2; i < 120; i++ {
		say(i)
		if i%10 == 9 {
			a.dashboard.PokeInteractions()
			awaitOnPage(t, page, fmt.Sprintf("reply %d", i), fmt.Sprintf(`return document.getElementById('thread-inner')?.innerText.includes('Reply %d.') ? 'ok' : null;`, i))
		}
	}
	drawn := `(() => JSON.stringify({ items: [...document.querySelectorAll('#thread-inner .interaction-item[data-interaction-key]')].filter(e => !e.hidden).length, seen: (() => { const t = document.getElementById('thread').getBoundingClientRect(); return [...document.querySelectorAll('#thread-inner .interaction-item')].filter(e => { const r = e.getBoundingClientRect(); return r.bottom > t.top && r.top < t.bottom; }).map(e => e.innerText.replace(/\s+/g, ' ').trim()).join(' / '); })() }))()`
	before, err := page.eval(drawn, false, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Settled is the conversation drawn and still for 800 ms. "Loading
	// conversation…" is the thread before its first read has come: under load
	// that wait passed 800 ms, and the test judged the page before it had read
	// anything (the gate of 25599bb0, 2026-11-06; main failed the same way).
	settled := `if (window.__leaving || document.querySelector('.p-state.connecting')) return null;
  const now = document.getElementById('thread-inner')?.innerText || '';
  if (!now || now.includes('Loading conversation…')) return null;
  if (now !== window.__seen) { window.__seen = now; window.__since = Date.now(); return null; }
  return Date.now() - window.__since < 800 ? null : 'ok';`
	if _, err := page.eval(`(() => { window.__leaving = true; location.reload(); return 'ok'; })()`, false, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	awaitOnPage(t, page, "the reload", settled)
	after, err := page.evalRetry(drawn, false, 10*time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("the reload did not redraw the conversation it held:\n before %.400s\n after  %.400s", before, after)
	}

	// One long turn since: a new tab has no place kept, and fifty of its rows draw
	// as one receipt. The screen is not left short of the conversation.
	turn := "long_turn"
	for i := 0; i < 90; i++ {
		if err := a.store.RecordToolStart(turn, i+1, "main", "m", "", "shell", "{}"); err != nil {
			t.Fatal(err)
		}
		if err := a.store.RecordToolDoneMeasured(turn, i+1, "shell", "{}", "done", interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := page.eval(`(() => { sessionStorage.clear(); window.__leaving = true; location.reload(); return 'ok'; })()`, false, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	awaitOnPage(t, page, "a new reading of the long turn", settled)
	short, err := page.evalRetry(`(() => { const t = document.getElementById('thread'); return t.scrollHeight > t.clientHeight || document.getElementById('thread-inner').innerText.includes('Reply 0.') ? 'ok' : 'short: ' + t.scrollHeight + ' of ' + t.clientHeight + ': ' + document.getElementById('thread-inner').innerText.slice(0, 200); })()`, false, 10*time.Second, 3)
	if err != nil {
		t.Fatal(err)
	}
	if short != "ok" {
		t.Errorf("the screen is left short of the conversation: %s", short)
	}
}
