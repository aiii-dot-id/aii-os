//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

/*
A SECTION THAT HAS NOT SAID HELLO DOES NOT STOP THE FRAME.

	A section is mounted as soon as the layout names it, and it has no
	bridge until its frame says hello. publish() called the bridge of
	every mounted section with no check, and ws.js calls publish() ahead
	of its own handling of each message, with nothing around it. So a
	status, projects or work message that arrived before a section's
	hello threw, and was never handled by the frame at all; a section
	whose script fails, or never imports the section API, lost the frame
	every such message from then on.

	This mounts the REAL sections.js with a section whose entry does not
	exist, so it never says hello, and publishes the messages a host
	sends.
*/
const sectionNoHelloPage = `<!doctype html>
<div id="nav"></div><div id="views"></div>
<div id="slot-rail"></div><div id="slot-panel"></div><div id="slot-dock"></div><div id="slot-overlay"></div>
<script type="module">
import { report, assert, until } from '/__harness.js';
import { onSections, onLayout, publish, onTokensChanged } from '/sections.js';
try {
  const layout = { profiles: { desktop: { rail: ['quiet'] }, mobile: { rail: ['quiet'] } } };
  onSections([{ id: 'quiet', slot: 'rail', title: 'Quiet', entry: 'never-there.html' }], '');
  onLayout(layout);
  await until(() => document.querySelector('#slot-rail iframe') !== null);
  assert(document.querySelector('#slot-rail iframe') !== null, 'rig: the section was not mounted');
  // The frame has not said hello and never will. Every message a host
  // pushes must pass it by.
  for (const msg of [
    { type: 'status', stats: { name: 'Fixture' } },
    { type: 'projects', projects: [] },
    { type: 'work', work: null },
    { type: 'continuity', continuity: { mode: 'normal' } },
  ]) {
    publish(msg);
  }
  onTokensChanged();
  report('OK');
} catch (e) { report('FAIL: a mounted section with no bridge stopped a message: ' + ((e && e.message) || String(e))); }
</script>`

func TestASectionThatHasNotSaidHelloDoesNotStopTheFrame(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	stub := func(body string) []byte { return []byte(body) }
	runPageInEngines(t, sectionNoHelloPage, map[string][]byte{
		"/sections.js":        read("sections.js"),
		"/sections-frames.js": read("sections-frames.js"),
		"/bridge.js":          read("bridge.js"),
		"/state.js":           read("state.js"),
		"/util.js":            read("util.js"),
		"/ws.js":              stub("export function send() { return ''; }\n"),
		"/app.js":             stub("export function go() {}\n"),
		"/panel.js":           stub("export function renderPanel() {}\n"),
	})
}

/*
A FAULT IN A SECTION DOES NOT END THE SOCKET'S HANDLING OF A MESSAGE.

	The other half of the same finding, held on the REAL ws.js: the
	socket's handler publishes each message to the sections ahead of its
	own handling of it. Here a section has said hello and has a bridge,
	and that bridge throws on every publish (a section is code the
	operator, an identity or a plugin wrote; it can fail in any way). The
	status that follows must still reach the frame: S.stats is what the
	name, the update chip and the voice state are drawn from.

	The section's frame is served this same document. Inside the frame it
	does what a section does first, and only that: it says hello to the
	page, so the page builds its bridge by the shipped path.
*/
const sectionThrowsPage = `<!doctype html>
<div id="nav"></div><div id="views"></div>
<div id="slot-rail"></div><div id="slot-panel"></div><div id="slot-dock"></div><div id="slot-overlay"></div>
<button id="send-btn" disabled></button><button id="mic" disabled></button>
<script type="module">
if (window.top !== window) {
  parent.postMessage({ type: 'aii-section-hello', v: 1 }, '*');
} else {
  const { run, until } = await import('/__harness.js');
  const { S } = await import('/state.js');
  const sockets = [];
  class Socket {
    constructor(url) { this.url = url; this.readyState = 0; this.sent = []; sockets.push(this); }
    send(m) { this.sent.push(JSON.parse(m)); }
    open() { this.readyState = 1; this.onopen?.({}); }
    close() { this.readyState = 3; }
    // A browser reports a throw from onmessage to the page, not to the
    // socket: the throw is kept here so the page's own assertion speaks.
    recv(m) { try { this.onmessage?.({ data: JSON.stringify(m) }); } catch (e) { window.__thrown = (e && e.message) || String(e); } }
  }
  Socket.CONNECTING = 0; Socket.OPEN = 1; Socket.CLOSING = 2; Socket.CLOSED = 3;
  window.WebSocket = Socket;
  const ws = await import('/ws.js');
  const { initSections } = await import('/sections.js');
  run(async (assert) => {
    S.view = 'chat';
    initSections();
    ws.connect(); sockets[0].open();
    sockets[0].recv({ type: 'sections', sections: [{ id: 'loud', slot: 'rail', title: 'Loud', entry: 'loud.html', topics: ['status'] }] });
    sockets[0].recv({ type: 'layout', layout: { profiles: { desktop: { rail: ['loud'] }, mobile: { rail: ['loud'] } } } });
    await until(() => document.querySelector('#slot-rail iframe') !== null);
    assert(document.querySelector('#slot-rail iframe') !== null, 'rig: the section was not mounted');
    await until(() => window.__bridges === 1);
    assert(window.__bridges === 1, 'rig: the section said hello and got ' + window.__bridges + ' bridges');
    sockets[0].recv({ type: 'status', stats: { name: 'Fixture' } });
    assert(window.__published >= 1, 'rig: the status never reached the section, so its fault was never tried');
    assert(S.stats && S.stats.name === 'Fixture',
      'A SECTION STOPPED THE FRAME: a section whose bridge throws ended the handling of a status message (' + (window.__thrown || 'no throw seen') + ')');
  });
}
</script>`

// The bridge a section gets is the shipped one, except that its publish
// throws, and the page counts what was made and what was tried.
const sectionThrowsBridgeJS = `export { topicOf, WIRED_COMMANDS } from './bridge-shipped.js';
import { createFrameBridge as shipped } from './bridge-shipped.js';
export function createFrameBridge(opts) {
  const b = shipped(opts);
  window.__bridges = (window.__bridges || 0) + 1;
  b.frame = function () { window.__published = (window.__published || 0) + 1; throw new Error('the section failed'); };
  return b;
}
`

func TestASectionThatThrowsDoesNotStopTheSocketsHandling(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		t.Helper()
		b, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false };`)
	modules := wsPageModules(wsJS, stateJS)
	modules["/sections.js"] = read("sections.js")
	modules["/sections-frames.js"] = read("sections-frames.js")
	modules["/bridge-shipped.js"] = read("bridge.js")
	modules["/bridge.js"] = []byte(sectionThrowsBridgeJS)
	runPageInEngines(t, sectionThrowsPage, modules)
}
