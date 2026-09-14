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
// .
const tokenPromptProbePage = `<!DOCTYPE html><html><head data-aii-token-required="1"><title>token</title>
<script>window.AII_TOKEN_REQUIRED = true</script>
</head><body>
<button id="send-btn">send</button>
<script type="module" src="/probe.js"></script>
</body></html>`

const tokenPromptProbeJS = `
import { report } from '/__harness.js';

try {
  // A stale token is already stored, and the server refuses every
  // connect (the rig serves no /ws — the socket never once opens).
  document.cookie = 'aii_token=stale-and-refused; path=/';
  let asks = 0;
  window.prompt = () => { asks++; return 'fresh-from-the-console'; };

  // Observe real refused WebSocket handshakes, but capture ws.js's
  // reconnect timer. The browser still enforces the shipping CSP,
  // cookie policy, and socket behavior; only idle wall time disappears.
  const realSetTimeout = window.setTimeout.bind(window);
  const NativeWebSocket = window.WebSocket;
  const closes = [];
  function ObservedWebSocket(url, protocols) {
    const socket = protocols === undefined
      ? new NativeWebSocket(url)
      : new NativeWebSocket(url, protocols);
    closes.push(new Promise(resolve => socket.addEventListener('close', resolve, { once: true })));
    return socket;
  }
  ObservedWebSocket.CONNECTING = NativeWebSocket.CONNECTING;
  ObservedWebSocket.OPEN = NativeWebSocket.OPEN;
  ObservedWebSocket.CLOSING = NativeWebSocket.CLOSING;
  ObservedWebSocket.CLOSED = NativeWebSocket.CLOSED;
  ObservedWebSocket.prototype = NativeWebSocket.prototype;
  window.WebSocket = ObservedWebSocket;

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
  const within = (promise, label) => Promise.race([
    promise,
    new Promise((_, reject) => realSetTimeout(() => reject(new Error(label + ' timed out')), 5000)),
  ]);
  const waitFor = async (predicate, label) => {
    const deadline = performance.now() + 5000;
    while (!predicate()) {
      if (performance.now() >= deadline) throw new Error(label + ' timed out');
      await new Promise(resolve => realSetTimeout(resolve, 5));
    }
  };
  const assert = (condition, message) => { if (!condition) throw new Error(message); };

  const { connect } = await import('/ws.js');
  connect();

  await waitFor(() => closes.length === 1, 'initial socket creation');
  await within(closes[0], 'initial refused socket');
  await waitFor(() => scheduled.length === 1 && asks === 1, 'initial close handling');
  assert(window.AII_TOKEN_REQUIRED === undefined,
    'the inline <script> carrier RAN under uiCSP — the policy weakened');
  assert(asks === 1,
    'after the first refused connect asks=' + asks + ' — a stale cookie must re-prompt');
  assert(document.cookie.includes('aii_token=fresh-from-the-console'),
    'the entered token did not replace the stale cookie: ' + document.cookie);

  // Execute two following reconnects immediately. Each remains a real
  // refused browser socket, and neither may prompt again this page load.
  for (let i = 1; i <= 2; i++) {
    const timer = scheduled.shift();
    timer.fn(...timer.args);
    await waitFor(() => closes.length === i + 1, 'reconnect ' + i + ' socket creation');
    await within(closes[i], 'reconnect ' + i + ' refused socket');
    await waitFor(() => scheduled.length === 1, 'reconnect ' + i + ' close handling');
    assert(asks === 1, 'reconnect ' + i + ' asked again (asks=' + asks + ') — once per page load broke');
  }
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`

// .
// .
// .
func TestTokenPromptFlowUnderTheShippingCSP(t *testing.T) {
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
		"/views/chat.js":     stubModule("addMsg", "attachSpeaker", "sysLine", "toolEventLive", "thinkingEvent", "renderHistory", "renderChatSubstrate", "acceptSubstrateConfig", "rejectSubstrateConfig", "substrateConnectionLost", "renderSteering", "renderAsks"),
		"/views/home.js":     stubModule("renderHome"),
		"/views/work.js":     stubModule("renderWorkPill"),
		"/views/projects.js": stubModule("renderProjPill", "renderProjects", "newlyCreatedID", "rejectCreate", "rejectFocusSave", "rejectContractSave", "acceptCreate", "acceptFocusSave", "acceptContractSave", "projectsConnectionLost"),
		"/views/memory.js":   stubModule("renderMemory"),
		"/views/identity.js": stubModule("renderIdentity"),
		"/views/plugins.js":  stubModule("renderPlugins"),
		"/views/settings.js": stubModule("renderSettings", "acceptSettingsConfig", "rejectSettingsConfig", "acceptProviderSave", "rejectProviderSave", "settingsConnectionLost"),
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
