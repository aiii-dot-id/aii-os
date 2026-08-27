//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import (
	"strings"
	"testing"
)

const substrateTransactionPage = `<!doctype html>
<select id="fb-provider"></select><select id="fb-model"></select>
<input id="fb-apikey"><input id="fb-name"><input id="fb-operator">
<a id="fb-subscribe"></a><div id="fb-cred-why"></div><div id="fb-hint"></div>
<div id="fb-result"></div><button id="fb-birth">Birth</button>
<textarea id="msg-input"></textarea><button id="send-btn"></button>
<div id="thread"></div><div id="thread-inner"></div>
<div id="chat-substrate-wrap"><select id="chat-provider"></select>
<select id="chat-model"></select><button id="chat-substrate-apply"></button>
<span id="chat-substrate-status"></span></div>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames, setConnected } from './ws.js';
import { renderProviderOptions, acceptDiscoveryResponse, firstbootConnectionLost } from './firstboot.js';
import { renderChatSubstrate, acceptSubstrateConfig, substrateConnectionLost } from './views/chat.js';
import { renderSettings, acceptSettingsConfig, settingsConnectionLost } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  S.providers = [
    { name: 'Claude', endpoint: 'https://example.test', models: ['c1'], default_model: 'c1', preselect: true },
    { name: 'Other', endpoint: 'https://other.test', models: ['o1'], default_model: 'o1' },
  ];
  renderProviderOptions();
  const firstDiscovery = frames.at(-1).request_id;
  document.getElementById('fb-apikey').value = 'key';
  document.getElementById('fb-apikey').dispatchEvent(new Event('change'));
  const latestDiscovery = frames.at(-1).request_id;
  assert(firstDiscovery !== latestDiscovery, 'discovery ids are unique');
  assert(!acceptDiscoveryResponse(firstDiscovery, 'Claude'), 'stale discovery accepted');
  assert(acceptDiscoveryResponse(latestDiscovery, 'Claude'), 'latest discovery refused');
  document.getElementById('fb-provider').value = '1';
  document.getElementById('fb-provider').onchange();
  assert(document.getElementById('fb-apikey').value === '', 'provider switch retained another provider key');

  document.getElementById('fb-name').value = 'Dawn';
  document.getElementById('fb-operator').value = 'Op';
  setConnected(false);
  document.getElementById('fb-birth').click();
  assert(!document.getElementById('fb-birth').disabled, 'unsent birth stayed disabled');
  assert(document.getElementById('fb-result').textContent.includes('did not start'), 'unsent birth was not reported');
  setConnected(true);
  document.getElementById('fb-birth').click();
  assert(document.getElementById('fb-birth').disabled, 'sent birth is not pending');
  assert(firstbootConnectionLost(), 'birth disconnect was not handled');
  assert(!document.getElementById('fb-birth').disabled, 'birth disconnect stayed disabled');

  S.identityExists = true;
  S.providers = [
    { name: 'old', models: ['m1'], default_model: 'm1' },
    { name: 'new', models: ['m2'], default_model: 'm2' },
  ];
  S.config = { llm: { provider: 'old', model: 'm1', resolved_provider: 'old', resolved_model: 'm1', timeout_seconds: 120 } };
  renderChatSubstrate();
  const chatProvider = document.getElementById('chat-provider');
  chatProvider.value = 'new'; chatProvider.onchange();
  document.getElementById('chat-model').value = 'm2';
  document.getElementById('chat-substrate-apply').click();
  const chatRequest = frames.at(-1).request_id;
  assert(document.getElementById('chat-substrate-apply').disabled, 'chat switch is not pending');
  assert(!acceptSubstrateConfig('stale'), 'stale chat acknowledgement accepted');
  S.config.llm = { provider: 'new', model: 'm2', resolved_provider: 'new', resolved_model: 'm2', timeout_seconds: 120 };
  assert(acceptSubstrateConfig(chatRequest), 'matching chat acknowledgement refused');
  renderChatSubstrate();
  assert(document.getElementById('chat-substrate-status').textContent.includes('inference verified'), 'chat success not rendered');
  document.getElementById('chat-provider').value = 'old'; document.getElementById('chat-provider').onchange();
  document.getElementById('chat-model').value = 'm1';
  document.getElementById('chat-substrate-apply').click();
  assert(substrateConnectionLost(), 'chat disconnect was not handled');
  assert(!document.getElementById('chat-substrate-apply').disabled, 'chat disconnect stayed disabled');

  S.providersLoaded = true;
  renderSettings();
  document.getElementById('cfg-provider').value = 'old'; document.getElementById('cfg-provider').onchange();
  document.getElementById('cfg-model').value = 'm1';
  document.querySelector('[data-save="llm"]').click();
  const settingsRequest = frames.at(-1).request_id;
  assert(!acceptSettingsConfig('stale'), 'stale settings acknowledgement accepted');
  S.config.llm = { provider: 'old', model: 'm1', resolved_provider: 'old', resolved_model: 'm1', timeout_seconds: 120 };
  assert(acceptSettingsConfig(settingsRequest), 'matching settings acknowledgement refused');
  renderSettings();
  assert(document.getElementById('settings-stack').textContent.includes('inference verified'), 'settings success not rendered');
  document.getElementById('cfg-provider').value = 'new'; document.getElementById('cfg-provider').onchange();
  document.getElementById('cfg-model').value = 'm2';
  document.querySelector('[data-save="llm"]').click();
  assert(settingsConnectionLost(), 'settings disconnect was not handled');
  assert(document.getElementById('settings-stack').textContent.includes('Connection lost'), 'settings disconnect not rendered');
});
</script>`

func TestSubstrateTransactionsInBrowser(t *testing.T) {
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
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = value => String(value ?? '');`)
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

	runPageInEngines(t, substrateTransactionPage, modules)
}
