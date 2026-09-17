//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
func TestGesturesPingPresenceThrottled(t *testing.T) {
	page := `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { run } from '/__harness.js';
class FakeWS {
  constructor(url) { this.url = url; this.readyState = 1; this.sent = []; FakeWS.last = this; setTimeout(() => { if (this.onopen) this.onopen(); }, 0); }
  send(m) { this.sent.push(m); }
  close() { this.readyState = 3; }
}
window.WebSocket = FakeWS;
const ws = await import('/ws.js');
run(async (assert) => {
  assert(ws.pingPresence() === false, 'no socket yet: no ping');
  ws.connect();
  await new Promise(r => setTimeout(r, 20));
  const sock = FakeWS.last;
  assert(sock && sock.readyState === 1, 'the fake socket is open');
  const presence = () => sock.sent.filter(m => JSON.parse(m).type === 'presence');
  document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
  assert(presence().length === 1, 'a gesture sends one presence frame, got ' + presence().length);
  assert(Object.keys(JSON.parse(presence()[0])).join(',') === 'type', 'the frame carries no payload');
  document.body.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true }));
  document.body.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
  assert(presence().length === 1, 'gestures inside the window send nothing more');
  assert(ws.pingPresence(Date.now() + 31000) === true, 'past the window a gesture pings again');
  assert(presence().length === 2, 'two frames in all');
});
</script></body></html>`
	wsJS, err := staticFS.ReadFile("static/ws.js")
	if err != nil {
		t.Fatal(err)
	}
	stateJS := []byte(`export const S = { connected: false, reconnectTimer: null, identityExists: false, view: 'home', tokenPrompted: false, wsEverOpened: false };`)
	runPageInEngines(t, page, map[string][]byte{
		"/ws.js":             wsJS,
		"/state.js":          stateJS,
		"/views/projects.js": []byte(`export const renderProjPill = () => {}; export const renderProjects = () => {}; export const newlyCreatedID = () => null; export const rejectCreate = () => {}; export const rejectFocusSave = () => {}; export const rejectContractSave = () => {}; export const acceptCreate = () => {}; export const acceptFocusSave = () => {}; export const acceptContractSave = () => {}; export const projectsConnectionLost = () => {}; export const viewProject = () => {}; export const acceptProjectFile = () => {}; export const rejectProjectFile = () => {};`),
		"/views/memory.js":   []byte(`export const renderMemory = () => {};`),
		"/views/identity.js": []byte(`export const renderIdentity = () => {};`),
		"/views/plugins.js":  []byte(`export const renderPlugins = () => {};`),
		"/views/settings.js": []byte(`export const renderSettings = () => {}; export const acceptSettingsConfig = () => {}; export const rejectSettingsConfig = () => {}; export const acceptProviderSave = () => {}; export const rejectProviderSave = () => {}; export const acceptSpeechLists = () => {}; export const rejectSpeechLists = () => false; export const acceptDashboardToken = () => false; export const settingsConnectionLost = () => {}; export const acceptSandboxSave = () => {}; export const rejectSandboxSave = () => {}; export const renderLogsList = () => {}; export const renderLogTail = () => {};`),
		"/firstboot.js":      []byte(`export const renderProviderOptions = () => {}; export const setModelOptions = () => {}; export const fbHint = () => {}; export const fbResult = () => {}; export const acceptDiscoveryResponse = () => true; export const firstbootConnectionLost = () => {}; export const firstbootDiscoveryMismatch = () => {};`),
		"/sections.js":       []byte(`export const publish = () => {}; export const onSections = () => {}; export const onLayout = () => {}; export const onTokensChanged = () => {};`),
		"/panel.js":          []byte(`export const renderPanel = () => {};`),
		"/theme.js":          []byte(`export const onTheme = () => {};`),
		"/overlay.js":        []byte(`export const onOverlayChanged = () => {};`),
		"/voice.js":          []byte(`export const bindTransport = () => {}; export const render = () => {}; export const speak = () => {}; export const connectionLost = () => {};`),
		"/app.js":            []byte(`export const sysLine = () => {}; export const toast = () => {}; export const go = () => {}; export const renderFirstbootVisibility = () => {};`),
		"/views/chat.js":     []byte(`export const addMsg = () => null; export const attachSpeaker = () => false; export const sysLine = () => {}; export const toolEventLive = () => {}; export const thinkingEvent = () => {}; export const renderHistory = () => {}; export const renderChatSubstrate = () => {}; export const renderComposer = () => {}; export const acceptSubstrateConfig = () => {}; export const rejectSubstrateConfig = () => {}; export const substrateConnectionLost = () => {}; export const renderSteering = () => {}; export const renderAsks = () => {}; export const toggleThinkingDots = () => {}; export const scrollThread = () => {};`),
		"/views/home.js":     []byte(`export const renderHome = () => {};`),
		"/views/work.js":     []byte(`export const renderWorkPill = () => {};`),
		"/presence.js":       []byte(`export const renderPresence = () => {}; export const setThinking = () => {};`),
		"/util.js":           []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`),
		"/bridge.js":         []byte(`export const send = () => ''; export const query = () => '';`),
	})
}
