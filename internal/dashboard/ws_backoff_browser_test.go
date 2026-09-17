//go:build !windows

// .
// .

package dashboard

import (
	"testing"
)

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
const wsBackoffPage = `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { assert, report } from './__harness.js';
try {
  let nextTimer = 1;
  const scheduled = [];
  window.setTimeout = (fn, delay, ...args) => {
    const timer = { id: nextTimer++, fn, delay, args };
    scheduled.push(timer);
    return timer.id;
  };
  window.clearTimeout = id => {
    const i = scheduled.findIndex(timer => timer.id === id);
    if (i >= 0) scheduled.splice(i, 1);
  };

  const randoms = [0, 0.999, 0, 0.999, 0, 0.999];
  Math.random = () => randoms.shift() ?? 0.5;
  const sockets = [];
  class ControlledSocket {
    constructor(url) {
      this.url = url;
      this.readyState = 0;
      this.onopen = null;
      this.onclose = null;
      this.onmessage = null;
      sockets.push(this);
    }
    send() {}
    fail() { this.readyState = 3; if (this.onclose) this.onclose({}); }
    open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
  }
  ControlledSocket.CONNECTING = 0; ControlledSocket.OPEN = 1;
  ControlledSocket.CLOSING = 2; ControlledSocket.CLOSED = 3;
  window.WebSocket = ControlledSocket;

  const { connect } = await import('./ws.js');
  connect();
  assert(sockets.length === 1, 'initial connect did not create exactly one socket');

  const expected = [800, 2399.2, 3200, 9596.8, 8000, 10000];
  const near = (got, want) => Math.abs(got - want) < 0.001;
  for (let i = 0; i < expected.length; i++) {
    sockets[sockets.length - 1].fail();
    assert(scheduled.length === 1, 'failure ' + (i + 1) + ' left ' + scheduled.length + ' pending reconnects');
    assert(near(scheduled[0].delay, expected[i]),
      'failure ' + (i + 1) + ' scheduled ' + scheduled[0].delay + 'ms, want ' + expected[i] + 'ms');
    const timer = scheduled.shift();
    timer.fn(...timer.args);
  }
  assert(sockets.length === expected.length + 1, 'timer execution did not reconnect exactly once per failure');

  // A successful connection resets the exponential state to its floor.
  Math.random = () => 0.5;
  sockets[sockets.length - 1].open();
  sockets[sockets.length - 1].fail();
  assert(scheduled.length === 1, 'post-success close did not leave exactly one reconnect');
  assert(scheduled[0].delay === 1000, 'success did not reset the reconnect floor: ' + scheduled[0].delay + 'ms');
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
window.addEventListener('error', ev => report('FAIL: uncaught ' + ((ev.error && ev.error.message) || ev.message)));
window.addEventListener('unhandledrejection', ev => report('FAIL: rejected ' + String(ev.reason)));
</script>`

func TestWSReconnectBacksOff(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false };`)
	runPageInEngines(t, wsBackoffPage, wsPageModules(wsJS, stateJS))
}

// .
// .
func wsPageModules(wsJS, stateJS []byte) map[string][]byte {
	return map[string][]byte{
		"/ws.js":    wsJS,
		"/state.js": stateJS,
		// .
		// .
		// .
		"/views/projects.js": []byte(`export const renderProjPill = () => {}; export const renderProjects = () => {}; export const newlyCreatedID = () => null; export const rejectCreate = () => false; export const acceptCreate = () => ''; export const acceptFocusSave = () => false; export const acceptContractSave = () => false; export const rejectFocusSave = () => false; export const rejectContractSave = () => false; export const projectsConnectionLost = () => false;`),
		"/views/memory.js":   []byte(`export const renderMemory = () => {};`),
		"/views/identity.js": []byte(`export const renderIdentity = () => {};`),
		"/views/plugins.js":  []byte(`export const renderPlugins = () => {};`),
		"/views/settings.js": []byte(`export const renderSettings = () => {}; export const acceptSettingsConfig = () => {}; export const rejectSettingsConfig = () => {}; export const acceptProviderSave = () => {}; export const rejectProviderSave = () => {}; export const acceptSpeechLists = () => {}; export const rejectSpeechLists = () => false; export const acceptDashboardToken = () => false; export const settingsConnectionLost = () => {};`),
		"/firstboot.js":      []byte(`export const renderProviderOptions = () => {}; export const setModelOptions = () => {}; export const fbHint = () => {}; export const fbResult = () => {}; export const acceptDiscoveryResponse = () => {}; export const firstbootConnectionLost = () => {};`),
		"/sections.js":       []byte(`export const publish = () => {}; export const onSections = () => {}; export const onLayout = () => {}; export const onTokensChanged = () => {};`),
		"/panel.js":          []byte(`export const renderPanel = () => {};`),
		"/theme.js":          []byte(`export const onTheme = () => {};`),
		"/overlay.js":        []byte(`export const onOverlayChanged = () => {};`),
		"/voice.js":          []byte(`export const bindTransport = () => {}; export const render = () => {}; export const speak = () => {}; export const connectionLost = () => {};`),
		"/app.js":            []byte(`export const sysLine = () => {}; export const toast = () => {}; export const go = () => {}; export const renderFirstbootVisibility = () => {};`),
		"/views/chat.js":     []byte(`export const addMsg = () => null; export const attachSpeaker = () => false; export const sysLine = () => {}; export const toolEventLive = () => {}; export const thinkingEvent = () => {}; export const renderHistory = () => {}; export const renderChatSubstrate = () => {}; export const renderComposer = () => {}; export const acceptSubstrateConfig = () => {}; export const rejectSubstrateConfig = () => {}; export const substrateConnectionLost = () => {}; export const renderSteering = () => {}; export const renderAsks = () => {};`),
		"/views/home.js":     []byte(`export const renderHome = () => {};`),
		"/views/work.js":     []byte(`export const renderWorkPill = () => {};`),
		"/presence.js":       []byte(`export const renderPresence = () => {}; export const setThinking = () => {};`),
		"/util.js":           []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`),
		"/bridge.js":         []byte(`export const send = () => ''; export const query = () => '';`),
	}
}

// .
// .
// .
// .
const tokenCancelPage = `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { assert, report } from './__harness.js';
import { S } from './state.js';
import { recoverAuthentication } from './ws.js';
try {
  document.head.dataset.aiiTokenRequired = '1';
  window.fetch = async () => ({ status: 401, ok: false });
  let prompts = 0;
  window.prompt = () => { prompts++; return null; };
  await recoverAuthentication();
  assert(prompts === 1, 'the prompt did not appear: ' + prompts);
  assert(S.tokenPrompted === false, 'a cancelled prompt left the page believing it had asked');
  await recoverAuthentication();
  assert(prompts === 2, 'the next reconnect did not ask again: ' + prompts);
  report('OK');
} catch (e) {
  report('FAIL ' + (e && e.message ? e.message : String(e)));
}
</script>`

func TestACancelledTokenPromptAsksAgainNextTime(t *testing.T) {
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false };`)
	runPageInEngines(t, tokenCancelPage, wsPageModules(wsJS, stateJS))
}
