//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

const substratePageMarkup = `<!doctype html>
<select id="fb-provider"></select><select id="fb-model"></select>
<div id="fb-url-row" hidden><input id="fb-url"></div>
<input id="fb-apikey"><p class="fb-meet" id="fb-meet">You are about to meet your AI identity. Your first conversation is important. You can decide on a name and introduce yourself. Your relationship begins here.</p>
<a id="fb-subscribe"></a><div id="fb-cred-why"></div><div id="fb-hint"></div>
<div id="fb-result"></div><button id="fb-birth">Birth</button>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<div id="thread"></div><div id="thread-inner"></div>
<div id="settings-stack"></div>
`

const substrateTransactionPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { renderProviderOptions, acceptDiscoveryResponse, firstbootConnectionLost } from './firstboot.js';
import { renderSettings, acceptSettingsConfig, settingsConnectionLost } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  S.providers = [
    { name: 'Claude', endpoint: 'https://example.test', models: ['c1'], default_model: 'c1', preselect: true, can_sign_in: true },
    { name: 'Other', endpoint: 'https://other.test', models: ['o1'], default_model: 'o1' },
  ];
  renderProviderOptions();
  const firstDiscovery = frames.at(-1).request_id;
  assert(!document.getElementById('fb-signin-start'), 'automatic preselection must not show a sign-in prompt');
  assert(!frames.some(f => f.type === 'provider_signin'), 'rendering must not start OAuth');
  document.getElementById('fb-apikey').value = 'key';
  document.getElementById('fb-apikey').dispatchEvent(new Event('change'));
  const latestDiscovery = frames.at(-1).request_id;
  assert(firstDiscovery !== latestDiscovery, 'discovery ids are unique');
  assert(!acceptDiscoveryResponse(firstDiscovery, 'Claude'), 'stale discovery accepted');
  assert(acceptDiscoveryResponse(latestDiscovery, 'Claude'), 'latest discovery refused');
  document.getElementById('fb-provider').value = '1';
  document.getElementById('fb-provider').onchange();
  assert(document.getElementById('fb-apikey').value === '', 'provider switch retained another provider key');
  assert(!document.getElementById('fb-signin-start'), 'unselected OAuth provider showed its sign-in control');
  document.getElementById('fb-provider').value = '0';
  document.getElementById('fb-provider').onchange();
  assert(document.getElementById('fb-signin-start'), 'explicit OAuth selection offers sign-in');
  S.providers[0].signin = {status:'pending',device:{user_code:'ABCD',verification_uri:'https://auth.example/device'}};
  renderProviderOptions();
  assert(document.getElementById('fb-provider').value === '0' && document.getElementById('fb-signin').textContent.includes('ABCD'), 'reconnect snapshot loses selection or device code');
  assert(!frames.some(f => f.type === 'provider_signin'), 'reconnecting started another consent');
  document.getElementById('fb-provider').value = '1';
  document.getElementById('fb-provider').onchange();

  assert(document.getElementById('fb-name') === null, 'identity name field still present — naming moved to the first conversation');
  assert(document.getElementById('fb-operator') === null, 'operator name field still present — naming moved to the first conversation');
  assert(document.getElementById('fb-meet') !== null, 'the meeting text is missing');
  assert(document.getElementById('fb-meet').textContent.includes('meet your AI identity'), 'the meeting text does not introduce the meeting');

  setConnected(false);
  document.getElementById('fb-birth').click();
  assert(!document.getElementById('fb-birth').disabled, 'unsent birth stayed disabled');
  assert(document.getElementById('fb-result').textContent.includes('Nothing has begun'), 'unsent birth was not reported');
  setConnected(true);
  document.getElementById('fb-birth').click();
  assert(document.getElementById('fb-birth').disabled, 'sent birth is not pending');
  assert(firstbootConnectionLost(), 'birth disconnect was not handled');
  assert(!document.getElementById('fb-birth').disabled, 'birth disconnect stayed disabled');

  S.identityExists = true;
  S.providers = [
    { name: 'old', models: ['m0', 'm1'], default_model: 'm0' },
    { name: 'new', models: ['m2'], default_model: 'm2' },
  ];
  S.config = { llm: { provider: 'old', model: 'm0', resolved_provider: 'old', resolved_model: 'm0', timeout_seconds: 120 } };
  S.providersLoaded = true;
  renderSettings();
  document.getElementById('cfg-provider').value = 'old'; document.getElementById('cfg-provider').onchange();
  document.getElementById('cfg-model').value = 'm1';
  document.querySelector('[data-save="llm"]').click();
  const settingsRequest = frames.at(-1).request_id;
  assert(!acceptSettingsConfig('stale'), 'stale settings acknowledgement accepted');
  S.config.llm = { provider: 'old', model: 'm1', resolved_provider: 'old', resolved_model: 'm1', timeout_seconds: 120, answered: true };
  assert(acceptSettingsConfig(settingsRequest), 'matching settings acknowledgement refused');
  renderSettings();
  assert(document.getElementById('settings-stack').textContent.includes('m1 answered and is now the model'), 'settings success not rendered');
  document.getElementById('cfg-provider').value = 'new'; document.getElementById('cfg-provider').onchange();
  document.getElementById('cfg-model').value = 'm2';
  document.querySelector('[data-save="llm"]').click();
  assert(settingsConnectionLost(), 'settings disconnect was not handled');
  assert(document.getElementById('settings-stack').textContent.includes('The connection dropped before the change was confirmed'), 'settings disconnect not rendered');
});
</script>`

const settingsChooserPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [
    { name: 'chatty', models: ['m1'], default_model: 'm1', chat: true },
    { name: 'Voices', chat: false, speech: { tts: { voices: [{ id: 'v1' }], voice_required: true } } },
  ];
  S.config = { llm: { provider: 'chatty', model: 'm1', resolved_provider: 'chatty', resolved_model: 'm1', timeout_seconds: 120 } };
  renderSettings();
  const offered = [...document.getElementById('cfg-provider').options].map(o => o.value);
  assert(offered.includes('chatty'), 'a chat entry is missing from the settings chooser');
  assert(!offered.includes('Voices'), 'a speech-only entry is offered as the substrate');
});
</script>`

func TestSubstrateTransactionsInBrowser(t *testing.T) {
	runPageInEngines(t, substrateTransactionPage, substrateModules(t))
}

func TestSettingsOffersOnlyEntriesThatChat(t *testing.T) {
	runPageInEngines(t, settingsChooserPage, substrateModules(t))
}

func substrateModules(t *testing.T) map[string][]byte {
	t.Helper()
	modules := map[string][]byte{}
	for _, path := range []string{
		"static/firstboot.js", "static/views/chat.js", "static/views/settings.js", "static/views/model-picker.js",
	} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, identityExists: false, providersLoaded: false };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = value => String(value ?? ''); export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`
let connected = true, seq = 0;
export const frames = [];
export const setConnected = value => { connected = value; };
export function send(frame) { if (!connected) return ''; frame.request_id ||= String(++seq); frames.push(structuredClone(frame)); return frame.request_id; }
export function query(name, extra) { return send(Object.assign({ type: 'query', query: name }, extra || {})); }
export const wsReady = () => connected;`)
	modules["/presence.js"] = []byte(`export function setThinking() {} export function toolPulse() {}`)
	modules["/app.js"] = []byte(`export function toast(message) { globalThis.lastToast = message; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)

	return modules
}
