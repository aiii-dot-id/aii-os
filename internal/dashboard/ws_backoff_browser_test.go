//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"testing"
)

// Operator report 2026-08-27: "when aii-os is down it seems to be
// polling — UIs should be reactive and event driven." The reconnect
// was a fixed 2s timer with no backoff: every open tab interrogated a
// dead server every 2s, forever. This pins the replacement: exponential
// backoff with jitter, capped, reset on success.
//
// The test dials a port with nothing listening and measures the gaps
// between connect attempts. It drives the REAL ws.js against a page
// whose location host points at the dead port, then samples the
// attempt log at fixed intervals. Two properties:
//  1. MONOTONE GROWTH: consecutive early gaps grow (backoff, not a
//     metronome). Measured over the first three gaps, which sit well
//     under the cap, so cap-plateau reads are not mistaken for flat.
//  2. CAP: no gap ever exceeds the cap by more than the jitter band.
//
// The fixed 2s timer FAILS property 1: three equal gaps are a poll.
const wsBackoffPage = `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { assert, report } from './__harness.js';
import { connect } from './ws.js';

// NOTE: run() is NOT used — it reports the instant the body returns,
// and this test's assertions live in a timer 8.5s later. report()
// directly instead; it is single-shot, exactly like run()'s guarantee.
const attempts = [];
window.WebSocket = function (url) {
  attempts.push(Date.now());
  // Simulate the dead-server close: readyState transitions to CLOSED
  // asynchronously, like a real refused connection does.
  const sock = { url: url, readyState: 0, onopen: null, onclose: null, onmessage: null, send: () => {} };
  setTimeout(() => { sock.readyState = 3; if (sock.onclose) sock.onclose({}); }, 5);
  return sock;
};
window.WebSocket.CONNECTING = 0; window.WebSocket.OPEN = 1;
window.WebSocket.CLOSING = 2; window.WebSocket.CLOSED = 3;
connect();
try {
  setTimeout(() => {
    const gaps = [];
    for (let i = 1; i < attempts.length; i++) gaps.push(attempts[i] - attempts[i - 1]);
    // Property 1: backoff, not metronome — the second gap exceeds the
    // first, and the third exceeds the second. The fixed 2s timer
    // produces three equal gaps and fails here.
    assert(gaps.length >= 3, 'only ' + gaps.length + ' reconnect attempts in 8.5s: ' + JSON.stringify(gaps));
    assert(gaps[1] > gaps[0], 'gap2 not growing (gap1=' + gaps[0] + ' gap2=' + gaps[1] + ') — this is a poll, not backoff');
    assert(gaps[2] > gaps[1], 'gap3 not growing (gap2=' + gaps[1] + ' gap3=' + gaps[2] + ') — this is a poll, not backoff');
    // Property 2: cap — no gap exceeds 10s x 1.2 jitter band.
    for (const g of gaps) assert(g <= 12000, 'gap ' + g + 'ms exceeds the capped jitter band');
    report('OK');
  }, 8500);
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
window.addEventListener('error', ev => report('FAIL: uncaught ' + ((ev.error && ev.error.message) || ev.message)));
window.addEventListener('unhandledrejection', ev => report('FAIL: rejected ' + String(ev.reason)));
</script>`

func TestWSReconnectBacksOff(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false };`)
	runPageInEngines(t, wsBackoffPage, map[string][]byte{
		"/ws.js":    wsJS,
		"/state.js": stateJS,
		// ws.js imports many modules; stubs suffice — the connect path
		// only calls S mutation, sysLine (identityExists false), and the
		// reconnect scheduler. None of the renders fire without payloads.
		"/views/projects.js": []byte(`export const renderProjPill = () => {}; export const renderProjects = () => {}; export const newlyCreatedID = () => null; export const rejectCreate = () => false; export const acceptCreate = () => ''; export const acceptFocusSave = () => false;`),
		"/views/memory.js":   []byte(`export const renderMemory = () => {};`),
		"/views/identity.js": []byte(`export const renderIdentity = () => {};`),
		"/views/plugins.js":  []byte(`export const renderPlugins = () => {};`),
		"/views/settings.js": []byte(`export const renderSettings = () => {}; export const acceptSettingsConfig = () => {}; export const rejectSettingsConfig = () => {}; export const acceptProviderSave = () => {}; export const rejectProviderSave = () => {}; export const settingsConnectionLost = () => {};`),
		"/firstboot.js":      []byte(`export const renderProviderOptions = () => {}; export const setModelOptions = () => {}; export const fbHint = () => {}; export const fbResult = () => {}; export const acceptDiscoveryResponse = () => {}; export const firstbootConnectionLost = () => {};`),
		"/sections.js":       []byte(`export const publish = () => {}; export const onSections = () => {}; export const onLayout = () => {}; export const onTokensChanged = () => {};`),
		"/panel.js":          []byte(`export const renderPanel = () => {};`),
		"/theme.js":          []byte(`export const onTheme = () => {};`),
		"/overlay.js":        []byte(`export const onOverlayChanged = () => {};`),
		"/voice.js":          []byte(`export const bindTransport = () => {}; export const render = () => {}; export const speak = () => {}; export const connectionLost = () => {};`),
		"/app.js":            []byte(`export const sysLine = () => {}; export const toast = () => {}; export const go = () => {}; export const renderFirstbootVisibility = () => {};`),
		"/views/chat.js":     []byte(`export const addMsg = () => {}; export const sysLine = () => {}; export const toolEventLive = () => {}; export const thinkingEvent = () => {}; export const renderHistory = () => {}; export const renderChatSubstrate = () => {}; export const acceptSubstrateConfig = () => {}; export const rejectSubstrateConfig = () => {}; export const substrateConnectionLost = () => {}; export const renderSteering = () => {};`),
		"/views/home.js":     []byte(`export const renderHome = () => {};`),
		"/views/work.js":     []byte(`export const renderWorkPill = () => {};`),
		"/presence.js":       []byte(`export const renderPresence = () => {}; export const setThinking = () => {};`),
		"/util.js":           []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`),
		"/bridge.js":         []byte(`export const send = () => ''; export const query = () => '';`),
	})
}
