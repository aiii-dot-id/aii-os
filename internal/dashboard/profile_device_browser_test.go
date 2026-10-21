//go:build !windows

package dashboard

import "testing"

func TestADeviceCodeReachesThePluginsViewThroughTheConfig(t *testing.T) {
	page := `<!doctype html>
<button id="send-btn" disabled></button>
<button id="mic" disabled></button>
<script type="module">
import { run } from '/__harness.js';
import { S } from '/state.js';
import { seen } from '/views/plugins.js';
class FakeWS {
  constructor(url) { this.url = url; this.readyState = 1; this.sent = []; FakeWS.last = this; setTimeout(() => { if (this.onopen) this.onopen(); }, 0); }
  send(m) { this.sent.push(m); }
  close() { this.readyState = 3; }
}
window.WebSocket = FakeWS;
const ws = await import('/ws.js');
run(async (assert) => {
  ws.connect();
  await new Promise(r => setTimeout(r, 20));
  const sock = FakeWS.last;
  S.view = 'plugins';
  const deliver = m => sock.onmessage({ data: JSON.stringify(m) });
  const device = { user_code: 'ABCD-1234', verification_uri: 'https://auth.example/device' };
  deliver({ type: 'profile_device', request_id: 'r1', device });
  assert(seen.length === 0, 'the profile_device reply drove a render');
  deliver({ type: 'config', config: { plugins: { auth_profiles: [{ name: 'gh', signin: { status: 'pending', device } }] } } });
  assert(seen.length === 1 && seen[0].includes('ABCD-1234'), 'the config did not carry the code to the Plugins view: ' + seen.join(' | '));
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
		"/views/plugins.js":  []byte(`import { S } from '/state.js'; export const seen = []; export const renderPlugins = () => { seen.push(JSON.stringify(S.config)); }; export const rejectPluginAction = () => false; export const applyPluginStored = () => {};`),
		"/views/settings.js": []byte(`export const renderSettings = () => {}; export const acceptSettingsConfig = () => {}; export const rejectSettingsConfig = () => {}; export const acceptProviderSave = () => {}; export const rejectProviderSave = () => {}; export const acceptSpeechLists = () => {}; export const rejectSpeechLists = () => false; export const acceptDashboardToken = () => false; export const acceptAccessCode = () => {}; export const rejectAccessCode = () => {}; export const settingsConnectionLost = () => {}; export const acceptSandboxSave = () => {}; export const rejectSandboxSave = () => {}; export const renderLogsList = () => {}; export const renderLogTail = () => {};`),
		"/firstboot.js":      []byte(`export const renderProviderOptions = () => {}; export const setModelOptions = () => {}; export const fbHint = () => {}; export const fbResult = () => {}; export const acceptDiscoveryResponse = () => true; export const firstbootConnectionLost = () => {}; export const discoveryAnswered = () => {}; export const discoveryFailed = () => false; export const firstbootDiscoveryMismatch = () => {};`),
		"/sections.js":       []byte(`export const publish = () => {}; export const onSections = () => {}; export const onLayout = () => {}; export const onTokensChanged = () => {};`),
		"/panel.js":          []byte(`export const renderPanel = () => {};`),
		"/theme.js":          []byte(`export const onTheme = () => {};`),
		"/overlay.js":        []byte(`export const onOverlayChanged = () => {}; export const holdReloadWhile = () => {};`),
		"/voice.js":          []byte(`export const bindTransport = () => {}; export const render = () => {}; export const speak = () => {}; export const connectionLost = () => {};`),
		"/app.js":            []byte(`export const sysLine = () => {}; export const toast = () => {}; export const go = () => {}; export const renderFirstbootVisibility = () => {};`),
		"/views/chat.js":     []byte(`export const addMsg = () => null; export const attachSpeaker = () => false; export const sysLine = () => {}; export const toolEventLive = () => {}; export const thinkingEvent = () => {}; export const renderInteractionPages = () => {}; export const renderHistory = () => {}; export const renderChatSubstrate = () => {}; export const renderComposer = () => {}; export const acceptSubstrateConfig = () => {}; export const rejectSubstrateConfig = () => {}; export const substrateConnectionLost = () => {}; export const renderSteering = () => {}; export const renderAsks = () => {}; export const toggleThinkingDots = () => {}; export const scrollThread = () => {};`),
		"/views/home.js":     []byte(`export const renderHome = () => {};`),
		"/views/work.js":     []byte(`export const renderWorkPill = () => {};`),
		"/presence.js":       []byte(`export const renderPresence = () => {}; export const setThinking = () => {};`),
		"/util.js":           utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '');`),
		"/bridge.js":         []byte(`export const send = () => ''; export const query = () => '';`),
	})
}
