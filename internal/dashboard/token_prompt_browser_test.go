//go:build !windows

package dashboard

import "testing"

const tokenPromptProbePage = `<!DOCTYPE html><html><head data-aii-token-required="1"><title>token</title></head><body>
<button id="send-btn">send</button>
<script type="module" src="/probe.js"></script>
</body></html>`

// .
// .
// .
// .
// .
const tokenPromptProbeJS = `
import { report } from '/__harness.js';

try {
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  const nativeFetch = window.fetch.bind(window);
  const calls = [];
  let authorized = false;
  window.fetch = async (url, options = {}) => {
    if (url !== '/auth/token') return nativeFetch(url, options);
    const method = options.method || 'GET';
    calls.push({ method, body: options.body || '' });
    if (method === 'GET') return { status: authorized ? 204 : 401, ok: authorized };
    if (options.body === 'right-token') { authorized = true; return { status: 204, ok: true }; }
    return { status: 401, ok: false };
  };

  const answers = ['wrong-token', 'right-token'];
  let asks = 0;
  const prompts = [];
  window.prompt = message => { asks++; prompts.push(message); return answers.shift() || null; };

  const sockets = [];
  class ControlledSocket {
    constructor(url) { this.url = url; this.readyState = 0; sockets.push(this); }
    send() {}
    open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
    fail() { this.readyState = 3; if (this.onclose) this.onclose({}); }
  }
  ControlledSocket.CONNECTING = 0; ControlledSocket.OPEN = 1;
  ControlledSocket.CLOSING = 2; ControlledSocket.CLOSED = 3;
  window.WebSocket = ControlledSocket;

  const waitFor = async (predicate, label) => {
    const deadline = performance.now() + 5000;
    while (!predicate()) {
      if (performance.now() >= deadline) throw new Error(label + ' timed out');
      await new Promise(resolve => setTimeout(resolve, 5));
    }
  };

  const { connect } = await import('/ws.js');
  connect();
  sockets[0].fail();
  await waitFor(() => authorized && sockets.length === 2, 'login and reconnect');
  assert(asks === 2, 'a refused token did not retry exactly once: asks=' + asks);
  assert(prompts[0].includes('aii dashboard-token') && prompts[0].includes('AII OS machine'),
    'the recovery prompt did not name the remote-safe CLI path: ' + prompts[0]);
  assert(calls.length >= 3 && calls[0].method === 'GET' && calls[1].body === 'wrong-token' && calls[2].body === 'right-token',
    'login did not pass through the server route: ' + JSON.stringify(calls));
  assert(!document.cookie.includes('aii_token'), 'page JavaScript wrote the access cookie');

  sockets[1].open();
  sockets[1].fail();
  await waitFor(() => calls.filter(c => c.method === 'GET').length === 2, 'post-connect auth status');
  assert(asks === 2, 'a transport close prompted despite valid server authentication');
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`

func TestTokenLoginAndReconnectUnderTheShippingCSP(t *testing.T) {
	real := func(p string) []byte {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return b
	}
	modules := map[string][]byte{
		"/probe.js": []byte(tokenPromptProbeJS),
		"/ws.js":    real("ws.js"),
		"/state.js": real("state.js"),
		"/util.js":  real("util.js"),

		"/presence.js":       stubModule("renderPresence", "setThinking"),
		"/app.js":            stubModule("go", "renderFirstbootVisibility", "toast"),
		"/views/chat.js":     stubModule("addMsg", "attachSpeaker", "sysLine", "toolEventLive", "thinkingEvent", "renderHistory", "renderChatSubstrate", "renderComposer", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering", "renderAsks"),
		"/views/home.js":     stubModule("renderHome"),
		"/views/work.js":     stubModule("renderWorkPill"),
		"/views/projects.js": stubModule("renderProjPill", "renderProjects", "newlyCreatedID", "rejectCreate", "rejectFocusSave", "rejectContractSave", "acceptCreate", "acceptFocusSave", "acceptContractSave", "projectsConnectionLost"),
		"/views/memory.js":   stubModule("renderMemory"),
		"/views/identity.js": stubModule("renderIdentity"),
		"/views/plugins.js":  stubModule("renderPlugins"),
		"/views/settings.js": stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "acceptSpeechLists", "rejectSpeechLists", "acceptDashboardToken", "settingsConnectionLost"),
		"/firstboot.js":      stubModule("renderProviderOptions", "setModelOptions", "fbHint", "fbResult", "acceptDiscoveryResponse", "firstbootConnectionLost"),
		"/sections.js":       stubModule("publish", "onSections", "onLayout", "onTokensChanged"),
		"/panel.js":          stubModule("renderPanel"),
		"/theme.js":          stubModule("onTheme"),
		"/overlay.js":        stubModule("onOverlayChanged"),
		"/voice.js":          stubModule("bindTransport", "render", "speak", "connectionLost"),
	}
	runPageInEnginesWithHeaders(t, tokenPromptProbePage, modules,
		map[string]string{"Content-Security-Policy": uiCSP})
}
