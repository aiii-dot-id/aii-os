//go:build !windows

package dashboard

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/sections"
)

// sectionAtTopLevel runs a built-in section's own modules in a page of the
// harness, its bridge answered by the page: what the section does with what it
// is told can be read here, as a sandboxed frame's cannot. Nothing of the section
// differs but the API it is handed. The section's body stands in the page, and
// script runs once main.js has.
const sectionHostAPI = `
export const host = { subs: new Map(), acts: [], answers: {}, tokens: {} };
host.tell = (topic, data, before) => (host.subs.get(topic) || []).forEach(cb => cb(data, before));
host.sent = command => host.acts.filter(a => a.command === command);
const api = {
  data: { subscribe(topic, cb) { host.subs.set(topic, [...(host.subs.get(topic) || []), cb]); return Promise.resolve(true); } },
  act(command, args) { host.acts.push({ command, args: args || {} }); const a = host.answers[command]; return Promise.resolve().then(() => a ? a(args || {}) : {}); },
  tokens(cb) { if (cb) cb(host.tokens); return host.tokens; },
  resize() {},
};
export const ready = () => Promise.resolve(api);
`

func sectionAtTopLevel(t *testing.T, tree builtInTree, script string) (string, map[string][]byte) {
	t.Helper()
	paths, err := tree.files()
	if err != nil {
		t.Fatal(err)
	}
	modules := map[string][]byte{"/section-api.js": []byte(sectionHostAPI)}
	for _, p := range paths {
		if strings.HasSuffix(p, ".js") {
			b, err := fs.ReadFile(tree, p)
			if err != nil {
				t.Fatal(err)
			}
			modules["/"+p] = b
		}
	}
	index, err := fs.ReadFile(tree, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(index), "<body>")
	if !ok {
		t.Fatal("the section's page has no body")
	}
	body, _, _ = strings.Cut(body, "<script")
	return `<!doctype html><meta charset="utf-8"><body>` + body + `<script type="module">
import { run, until } from '/__harness.js';
import { host } from '/section-api.js';
` + script + `
</script></body>`, modules
}

// STATUS IN ITS SECTION FOLLOWS THE TURN AND READS AGAIN, as it does in the page:
//   - a connection lost before it was first drawn says nothing of now: frames
//     from before the loss are shown and not taken as read, so the heading says
//     nothing until both have come since;
//   - entered, it asks for its own read of the spend, so one lost is read again;
//   - while the identity answers it says so, from the page's turn;
//   - a turn that ends reads the spend again, of the same usage or not, and so
//     does the status frame's count of accounted calls, and nothing else.
func TestStatusInItsSectionFollowsTheTurnAndReadsAgain(t *testing.T) {
	tree, err := builtInStatusTree()
	if err != nil {
		t.Fatal(err)
	}
	page, modules := sectionAtTopLevel(t, tree, `
import W from '/strings/status.js';
host.answers['spend.read'] = () => ({ days: [] });
await import('/main.js');
const says = () => document.getElementById('status-stack').textContent;
const spends = () => host.sent('spend.read').length;
const stats = (accounted) => ({ name: 'Mira', accounted });
run(async assert => {
  host.tell('connection', false);
  host.tell('status', stats(3), true);
  host.tell('continuity', { mode: 'normal', ledger_seq: 9 }, true);
  assert(/Not connected/.test(says()) && !/Everything is working/.test(says()), 'first drawn while lost, it took frames from before the loss as now: ' + says());
  host.tell('connection', true);
  assert(!/Everything is working/.test(says()), 'found again, it took frames from before the loss as now: ' + says());
  host.tell('continuity', { mode: 'normal', ledger_seq: 9 });
  assert(!/Everything is working/.test(says()), 'continuity since, and status from before, is not both: ' + says());
  host.tell('status', stats(3));
  await until(() => /Everything is working/.test(says()));
  assert(/Everything is working/.test(says()), 'both frames since did not say the heading: ' + says());
  host.tell('connection', false);
  host.tell('connection', true);
  host.tell('status', stats(3));
  host.tell('continuity', { mode: 'normal', ledger_seq: 9 }, true);
  assert(!/Everything is working/.test(says()), 'status since, and continuity from before, is not both: ' + says());
  host.tell('continuity', { mode: 'normal', ledger_seq: 9 });
  await until(() => /Everything is working/.test(says()));

  host.tell('route', { view: 'status', hash: '#/settings/status', base: '' });
  await until(() => spends() === 1);
  assert(spends() === 1, 'entered, it did not ask for the spend: ' + spends());
  host.tell('route', { view: 'status', hash: '#/settings/status', base: '' });
  await until(() => spends() === 2);
  assert(spends() === 2, 'entered again, it did not ask for the spend again: ' + spends());

  host.tell('turn', { thinking: false, ended: 4 });
  assert(spends() === 2, 'the turn as it stood when first told is no turn ending: ' + spends());
  host.tell('turn', { thinking: true, ended: 4 });
  assert(says().toLowerCase().includes(W.stateAnswering.toLowerCase()), 'an identity answering is not said: ' + says());
  host.tell('turn', { thinking: false, ended: 5 });
  await until(() => spends() === 3);
  assert(spends() === 3, 'a turn that ended did not read the spend again: ' + spends());
  assert(!says().toLowerCase().includes(W.stateAnswering.toLowerCase()), 'an identity no longer answering is said to be: ' + says());
  host.tell('turn', { thinking: false, ended: 6 });
  await until(() => spends() === 4);
  assert(spends() === 4, 'a second turn of the same usage did not read it again: ' + spends());
  host.tell('status', stats(3));
  host.tell('status', stats(4));
  await until(() => spends() === 5);
  assert(spends() === 5, 'accounted calls moved, and it read ' + spends());
});
`)
	runPageInEngines(t, page, modules)
}

// THE PAGE'S KEYS REACH THE PAGE FROM INSIDE A SECTION, and no others do: only
// what shell/keys.js hears, H, P, A and S only straight after G, and from a field
// only the chord that opens Search, which types nothing, as in the page.
func TestOnlyThePagesKeysLeaveASection(t *testing.T) {
	tree, err := builtInStatusTree()
	if err != nil {
		t.Fatal(err)
	}
	page, modules := sectionAtTopLevel(t, tree, `
await import('/main.js');
const press = (el, key, more) => el.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...more }));
const left = () => { const out = host.sent('page.key').map(a => a.args.key + (a.args.ctrl ? '+ctrl' : '') + (a.args.meta ? '+meta' : '')); host.acts.length = 0; return out.join(' '); };
const seen = async want => { await until(() => host.sent('page.key').length >= (want ? want.split(' ').length : 0)); return left(); };
run(async assert => {
  const body = document.body, field = document.createElement('input');
  body.append(field);
  for (const key of ['x', 'h', 'Enter', 'Tab']) press(body, key);
  press(body, 'c', { ctrlKey: true });
  assert(await seen('') === '', 'a key the page does not hear left the section: ');
  press(body, 'k', { ctrlKey: true });
  assert(await seen('k+ctrl') === 'k+ctrl', 'Ctrl K did not reach the page');
  press(body, 'k', { metaKey: true });
  assert(await seen('k+meta') === 'k+meta', 'Cmd K did not reach the page');
  press(body, 'k', { ctrlKey: true, altKey: true });
  assert(await seen('') === '', 'a key with Alt left the section');
  press(body, '?');
  assert(await seen('?') === '?', '? did not reach the page');
  press(body, 'G'); press(body, 'a');
  assert(await seen('g a') === 'g a', 'G then A did not reach the page');
  press(body, 'g'); press(body, 'x'); press(body, 'a');
  assert(await seen('g') === 'g', 'a key between G and A let A out');
  for (const [key, more] of [['g'], ['a'], ['?'], ['x'], ['k']]) press(field, key, more);
  assert(await seen('') === '', 'a key pressed in a field left the section');
  press(field, 'k', { ctrlKey: true });
  assert(await seen('k+ctrl') === 'k+ctrl', 'Ctrl K in a field did not reach the page');
  press(field, 'k', { metaKey: true });
  assert(await seen('k+meta') === 'k+meta', 'Cmd K in a field did not reach the page');
});
`)
	runPageInEngines(t, page, modules)
}

// serverWithRealStatus is the dashboard's own server with the binary's own Status
// section, its page given the rig's reporting script.
func serverWithRealStatus(t *testing.T, pressed *int) *Server {
	t.Helper()
	tree, err := builtInStatusTree()
	if err != nil {
		t.Fatal(err)
	}
	status := builtInSection(t, "status")
	status.FS = reportingTree{tree}
	reg := sections.NewRegistry()
	if err := reg.Register(status); err != nil {
		t.Fatal(err)
	}
	s := New("127.0.0.1", 0, statusSectionHandler(pressed))
	s.SetSections(reg)
	return s
}

// A CHANGE OF WIDTH LEAVES A RUNNING SECTION RUNNING, in the place it is shown in:
// the same frame, not loaded again. A section whose address has changed starts
// again, and is opened there. The browser's own word that the width has crossed
// is what the rig stands in for, here where a page cannot resize its window.
const statusAcrossWidthsDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
const real = window.matchMedia.bind(window);
const heard = [];
let phone = false;
window.matchMedia = q => q === '(max-width: 767px)' ? { get matches() { return phone; }, addEventListener: (_, f) => heard.push(f), addListener: f => heard.push(f) } : real(q);
const frame = () => document.querySelector('iframe.section-frame[title="Status"]');
const place = () => document.querySelector('[id="view-section:status"]');
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  location.hash = '#/settings';
  await until(() => held && /Everything is working/.test(held.text));
  if (!held) return report('FAIL: the section did not draw');
  const was = frame();
  let loads = 0;
  was.addEventListener('load', () => { loads++; });
  phone = true;
  heard.forEach(f => f());
  // waits by design: that the frame does not load again is what is held.
  await new Promise(r => setTimeout(r, 300));
  if (frame() !== was) return report('FAIL: the width replaced the running section');
  if (loads) return report('FAIL: the width loaded the running section again');
  if (!place().classList.contains('on')) return report('FAIL: the width took the section out of its place');
  const { onSections } = await import('/sections.js');
  const list = await new Promise(ok => {
    const s = new WebSocket(location.origin.replace('http', 'ws') + '/ws');
    s.onopen = () => s.send(JSON.stringify({ type: 'query', query: 'sections' }));
    s.onmessage = e => { const m = JSON.parse(e.data); if (m.type === 'sections') { s.close(); ok(m.sections); } };
  });
  held = null;
  onSections(list.map(x => ({ ...x, address: '/built-in-sections/status/' })));
  await until(() => frame() && frame() !== was && held && /Everything is working/.test(held.text));
  if (frame() === was) return report('FAIL: a section whose address changed did not start again');
  if (!held || !/Everything is working/.test(held.text)) return report('FAIL: a section that started again was left unopened: ' + frame().getAttribute('src'));
  if (!place().classList.contains('on')) return report('FAIL: a section that started again is not in its place');
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestAChangeOfWidthLeavesARunningSectionRunning(t *testing.T) {
	pressed := 0
	runDashboardInEachEngine(t, func(t *testing.T) *Server { return serverWithRealStatus(t, &pressed) }, "/", statusAcrossWidthsDriverJS)
}

// A CONNECTION LOST BEFORE STATUS IS DRAWN IS SAID: through the real page, the
// frames kept from before the loss are given as frames from before it. The socket
// is the page's own, and the section is first opened while it is gone; nothing
// from before the loss may say the heading when it comes back, until both frames
// have come since. What the page asks goes nowhere here, and the frames are the
// driver's, so the moment between is there to read. The order is held, not
// waited for (run two of the gate at 0090120c, 2026-11-04, drew "Loading…"):
// the sections list is in before both frames are kept (a list that does not
// come fails the run at once), and once the connection is lost nothing more
// the socket brings reaches the page; a pass says how many frames it held.
const statusOpenedOfflineDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  const { S } = await import('/state.js');
  const { onSections, publish } = await import('/sections.js');
  await until(() => S.pageFrames && S.pageFrames.status && S.pageFrames.continuity);
  // The list is in, then both frames are kept.
  const list = await new Promise((ok, no) => {
    const s = new WebSocket(location.origin.replace('http', 'ws') + '/ws');
    const fail = why => { clearTimeout(late); s.onclose = s.onerror = s.onmessage = null; s.close(); no(new Error('rig: the sections list did not come: ' + why)); };
    // waits by design: a bound on this wait for the list, which ends when the list comes.
    const late = setTimeout(() => fail('none in 10 s'), 10000);
    s.onopen = () => s.send(JSON.stringify({ type: 'query', query: 'sections' }));
    s.onerror = () => fail('the socket failed');
    s.onclose = () => fail('the socket closed');
    s.onmessage = e => { const m = JSON.parse(e.data); if (m.type === 'sections') { clearTimeout(late); s.onclose = s.onerror = null; s.close(); ok(m.sections); } };
  });
  onSections(list);
  publish({ type: 'status', stats: { name: 'Mira' } });
  publish({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 8 } });
  // Then the connection lost: what the page asks goes nowhere, and what the socket brings is not heard.
  const data = Object.getOwnPropertyDescriptor(MessageEvent.prototype, 'data'), heldBack = new WeakSet();
  let heldCount = 0;
  Object.defineProperty(MessageEvent.prototype, 'data', { configurable: true, get() {
    if (!(this.target instanceof WebSocket)) return data.get.call(this);
    if (!heldBack.has(this)) { heldBack.add(this); heldCount++; }
    return '';
  } });
  WebSocket.prototype.send = () => {};
  S.sectionsLost();
  location.hash = '#/settings';
  await until(() => held && /Not connected/.test(held.text));
  if (!held || !/Not connected/.test(held.text)) return report('FAIL: opened while lost, the section did not say so: ' + (held && held.text));
  publish({ type: 'work', work: {} });
  // waits by design: that the heading does not come is what is held.
  await new Promise(r => setTimeout(r, 300));
  if (/Everything is working/.test(held.text)) return report('FAIL: frames from before the loss said the heading: ' + held.text);
  publish({ type: 'status', stats: { name: 'Mira' } });
  publish({ type: 'continuity', continuity: { mode: 'normal', ledger_seq: 9 } });
  await until(() => /Everything is working/.test(held.text));
  if (!/Everything is working/.test(held.text)) return report('FAIL: both frames since did not say the heading: ' + held.text);
  report('OK\n' + heldCount + ' frames from the socket held after the loss');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestAConnectionLostBeforeStatusIsDrawnIsSaid(t *testing.T) {
	pressed := 0
	runDashboardInEachEngine(t, func(t *testing.T) *Server { return serverWithRealStatus(t, &pressed) }, "/", statusOpenedOfflineDriverJS)
}

// AN OPENING BELONGS TO THE PLACE IT WAS ASKED FOR. The sections' frames are still
// on their way when the reader leaves Status for the conversation: when they come, the
// conversation stands alone, and Status's section has not been shown, loaded or told of its address.
// Opened again, it is shown as it would have been.
const statusLeftWhileFramesAreHeldDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
let held = null;
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') held = e.data; });
const shown = el => el && getComputedStyle(el).visibility !== 'hidden';
const place = () => document.querySelector('[id="view-section:status"]');
const frame = () => document.querySelector('iframe.section-frame[title="Status"]');
(async () => {
  const { S } = await import('/state.js');
  await until(() => S.connected && document.getElementById('send-btn'));
  // waits by design: the sections list must have come, or the section would not be asked for.
  await new Promise(r => setTimeout(r, 500));
  location.hash = '#/settings';
  await until(() => S.view === 'status');
  // waits by design: the frames' fetch is on its way, held at the server.
  await new Promise(r => setTimeout(r, 200));
  if (performance.getEntriesByType('resource').some(r => r.name.endsWith('/sections-frames.js') && r.responseEnd > 0)) return report('FAIL: rig: the frames were not held');
  location.hash = '#/chat';
  await until(() => S.view === 'chat');
  await fetch('/__release', { method: 'POST' });
  await until(frame);
  if (!frame()) return report('FAIL: rig: the frames never came');
  // waits by design: an opening that should not run is what is held.
  await new Promise(r => setTimeout(r, 300));
  if (shown(place())) return report('FAIL: an opening from a place the reader left showed its section over the conversation');
  if (place().classList.contains('on')) return report('FAIL: an opening from a place the reader left marked its section as the view on');
  if (frame().getAttribute('src')) return report('FAIL: an opening from a place the reader left loaded its section');
  if (!shown(document.getElementById('view-chat'))) return report('FAIL: the conversation does not stand after the frames came');
  location.hash = '#/settings';
  await until(() => held && /Everything is working/.test(held.text));
  if (!held) return report('FAIL: opened again, the section did not draw');
  await until(() => shown(place()));
  if (!shown(place())) return report('FAIL: opened again, the section is not shown');
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

// FRAMES THAT DO NOT COME LEAVE THE READER WHERE THE READER IS. Still in the place,
// the page draws it, the same module; gone from it, nothing moves them back, and
// the page draws it when they return.
const statusFramesDoNotComeDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
const shown = id => getComputedStyle(document.getElementById(id)).visibility !== 'hidden';
const drawn = () => /Everything is working/.test(document.getElementById('status-stack').textContent) && shown('view-status');
(async () => {
  const { S } = await import('/state.js');
  await until(() => S.connected && document.getElementById('send-btn'));
  // waits by design: the sections list must have come, or the section would not be asked for.
  await new Promise(r => setTimeout(r, 500));
  location.hash = '#/settings';
  await until(() => S.view === 'status');
  // waits by design: the frames' fetch is on its way, held at the server.
  await new Promise(r => setTimeout(r, 200));
  if (__GONE__) {
    location.hash = '#/chat';
    await until(() => S.view === 'chat');
  }
  await fetch('/__release', { method: 'POST' });
  if (__GONE__) {
    // waits by design: a move that should not come is what is held.
    await new Promise(r => setTimeout(r, 500));
    if (S.view !== 'chat') return report('FAIL: frames that did not come moved the reader to ' + S.view);
    if (!shown('view-chat')) return report('FAIL: the conversation does not stand');
    location.hash = '#/settings';
    await until(() => S.view === 'status');
  }
  await until(drawn);
  if (!drawn()) return report('FAIL: the page did not draw the place');
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestAnOpeningBelongsToThePlaceItWasAskedFor(t *testing.T) {
	pressed := 0
	framesHeld := func(t *testing.T, come bool) *Server {
		s := serverWithRealStatus(t, &pressed)
		released := make(chan struct{})
		next := s.server.Handler
		s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/__release":
				close(released)
				w.WriteHeader(http.StatusNoContent)
				return
			case "/sections-frames.js":
				select {
				case <-released:
				case <-r.Context().Done():
					return
				}
				if !come {
					http.Error(w, "gone", http.StatusNotFound)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
		return s
	}
	driver := func(gone string) string { return strings.ReplaceAll(statusFramesDoNotComeDriverJS, "__GONE__", gone) }
	t.Run("the frames come", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server { return framesHeld(t, true) }, "/", statusLeftWhileFramesAreHeldDriverJS)
	})
	t.Run("they do not come and the reader has gone", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server { return framesHeld(t, false) }, "/", driver("true"))
	})
	t.Run("they do not come and the reader is there", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server { return framesHeld(t, false) }, "/", driver("false"))
	})
}

// A SECTION WITH NO WORKING BRIDGE DOES NOT RUN, and the place goes back to the
// page, the same module drawing it, though the section said hello: when the
// bridge does not arrive, when it never answers, and when the section's own
// modules stop after their hello. A bridge that arrives late takes the place then.
const statusNoBridgeDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
const own = () => document.getElementById('status-stack');
const shown = id => getComputedStyle(document.querySelector(id)).visibility !== 'hidden';
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  location.hash = '#/settings';
  const frame = () => document.querySelector('iframe.section-frame[title="Status"]');
  await until(frame);
  if (frame() && getComputedStyle(frame()).visibility !== 'hidden') return report('FAIL: a section that has not run can be seen');
  await until(() => /Everything is working/.test(own().textContent) && shown('#view-status'));
  if (!/Everything is working/.test(own().textContent)) return report('FAIL: the page did not draw Status: ' + own().textContent);
  if (!shown('#view-status')) return report('FAIL: the page drew Status, and it is hidden');
  await until(() => !shown('[id="view-section:status"]'));
  if (shown('[id="view-section:status"]')) return report('FAIL: the section with no bridge still stands in the place');
  if (__LATE__) {
    await fetch('/__release', { method: 'POST' });
    await until(() => shown('[id="view-section:status"]') && !shown('#view-status'));
    if (!shown('[id="view-section:status"]') || shown('#view-status')) return report('FAIL: a bridge that came late did not take the place');
    if (getComputedStyle(frame()).visibility === 'hidden') return report('FAIL: a section that came alive cannot be seen');
  }
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestASectionWithNoWorkingBridgeLeavesThePlaceToThePage(t *testing.T) {
	pressed := 0
	withBridge := func(s *Server, answer func(w http.ResponseWriter, r *http.Request, next http.Handler)) {
		next := s.server.Handler
		s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/bridge.js" {
				answer(w, r, next)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	driver := func(late string) string { return strings.ReplaceAll(statusNoBridgeDriverJS, "__LATE__", late) }
	t.Run("it does not arrive", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server {
			s := serverWithRealStatus(t, &pressed)
			withBridge(s, func(w http.ResponseWriter, _ *http.Request, _ http.Handler) {
				http.Error(w, "gone", http.StatusNotFound)
			})
			return s
		}, "/", driver("false"))
	})
	t.Run("it never answers", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server {
			s := serverWithRealStatus(t, &pressed)
			withBridge(s, func(_ http.ResponseWriter, r *http.Request, _ http.Handler) { <-r.Context().Done() })
			return s
		}, "/", driver("false"))
	})
	t.Run("it comes late", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server {
			s := serverWithRealStatus(t, &pressed)
			released := make(chan struct{})
			withBridge(s, func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				select {
				case <-released:
				case <-r.Context().Done():
					return
				}
				next.ServeHTTP(w, r)
			})
			next := s.server.Handler
			s.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/__release" {
					close(released)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
			})
			return s
		}, "/", driver("true"))
	})
	t.Run("its modules stop after hello", func(t *testing.T) {
		runDashboardInEachEngine(t, func(t *testing.T) *Server {
			reg := sections.NewRegistry()
			if err := reg.Register(&sections.Section{
				Decl:    sections.Decl{ID: "status", Title: "Status", Slot: "view", Entry: "index.html"},
				BuiltIn: true, Views: []string{"status"},
				FS: fstest.MapFS{
					"index.html": {Data: []byte(`<!doctype html><meta charset="utf-8"><title>Status</title><script type="module" src="./main.js"></script>`)},
					"main.js":    {Data: []byte(`import '/section-api.js'; throw new Error('this section stops here');`)},
				},
			}); err != nil {
				t.Fatal(err)
			}
			s := New("127.0.0.1", 0, statusSectionHandler(&pressed))
			s.SetSections(reg)
			return s
		}, "/", driver("false"))
	})
}

// THE PAGE'S KEYS AND THE PAGE'S TURN REACH A SECTION THROUGH THE REAL PAGE: keys
// pressed inside the section open Search and move to a place; the page's turn is
// said there. The whole way is walked: the frame, the bridge, keys.js, presence.js.
const statusKeysDriverJS = `
const report = text => fetch('/__result', { method: 'POST', body: text, keepalive: true });
const until = async (ok, ms) => { for (let i = 0; i * 50 < (ms || 10000) && !ok(); i++) await new Promise(r => setTimeout(r, 50)); return ok(); };
let held = null;
const heard = [];
window.addEventListener('message', e => { if (e.data && e.data.type === 'section-holds') { held = e.data; heard.push(e.data.text); } });
const frame = () => document.querySelector('iframe.section-frame[title="Status"]');
(async () => {
  await until(() => document.getElementById('send-btn') && (document.getElementById('p-state') || {}).textContent);
  location.hash = '#/settings';
  await until(() => held && /Everything is working/.test(held.text));
  if (!held) return report('FAIL: the section did not draw');
  const W = (await import('/strings/status.js')).default;
  // This turn is the page's word alone, and the host knows of none: a probe the page
  // sends now is answered that no turn runs, and ends it. So it is enough that it was said.
  const says = text => text.toLowerCase().includes(W.stateAnswering.toLowerCase());
  const answering = () => held && says(held.text);
  heard.length = 0;
  (await import('/presence.js')).setThinking(true);
  await until(() => heard.some(says));
  if (!heard.some(says)) return report('FAIL: the page\'s turn was not said in the section: ' + held.text);
  (await import('/presence.js')).setThinking(false);
  await until(() => !answering());
  if (answering()) return report('FAIL: the page\'s turn stayed said in the section');
  frame().contentWindow.postMessage({ type: 'key', key: { key: 'k', ctrlKey: true } }, '*');
  const menu = () => document.querySelector('dialog.c-menu[open]');
  await until(menu);
  if (!menu()) return report('FAIL: Ctrl K inside the section did not open Search');
  menu().close();
  frame().contentWindow.postMessage({ type: 'key', key: { key: 'g' } }, '*');
  frame().contentWindow.postMessage({ type: 'key', key: { key: 'c' } }, '*');
  await until(() => location.hash === '#/chat');
  if (location.hash !== '#/chat') return report('FAIL: G then C inside the section did not go to the conversation: ' + location.hash);
  report('OK');
})().catch(e => report('FAIL: the driver stopped: ' + ((e && e.message) || e)));
`

func TestThePagesKeysAndTurnReachASectionThroughTheRealPage(t *testing.T) {
	pressed := 0
	runDashboardInEachEngine(t, func(t *testing.T) *Server { return serverWithRealStatus(t, &pressed) }, "/", statusKeysDriverJS)
}

// A MINTED ADDRESS IS NEVER WRITTEN DOWN, asked for as a service worker's script
// too, on the server's whole way in: the line says the section and the file, and
// the worker is still evicted.
func TestAMintedAddressAskedForAsAWorkerIsNotWrittenDown(t *testing.T) {
	said := logsink.CaptureForTest(t)
	s := New("127.0.0.1", 0, &WSHandler{})
	s.AllowHost("example.test:8443")
	capability := strings.Repeat("ab", 32)
	r := httptest.NewRequest(http.MethodGet, "http://example.test:8443/built-in-sections/status/~"+capability+"/index.html", nil)
	r.Header.Set("Service-Worker", "script")
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "unregister") {
		t.Fatalf("the worker was not evicted: %d %q", w.Code, w.Body.String())
	}
	lines := said.String()
	if !strings.Contains(lines, "service worker") {
		t.Fatalf("rig: the request wrote no line: %q", lines)
	}
	if strings.Contains(lines, capability) {
		t.Errorf("a line says the minted address: %q", lines)
	}
	if !strings.Contains(lines, "/built-in-sections/status/~/index.html") {
		t.Errorf("the line lost what it says of the request: %q", lines)
	}
}
