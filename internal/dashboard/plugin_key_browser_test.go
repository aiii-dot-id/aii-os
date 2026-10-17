//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestAPluginKeyIsPastedOnTheCardInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="plugins-stack"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
import { frames } from './ws.js';
import { acceptSettingsConfig } from './views/settings.js';
window.confirm = () => true;
const card = (secret, secretsOK) => ({ secrets_ok: secretsOK, plugins: { autoload: 'T3', catalog: [], catalog_url: '', auth_profiles: [], providers: [],
  installed: [{ id: 'id.aiii.codequality', version: '0.1.0', tier: 'T3', mode: 'wasm', variant: 'linux-x86_64-wasm',
    settings: [secret, { key: 'model', type: 'string', title: 'Jev model' }] }] } });
const fresh = { key: 'api_key', type: 'secret', title: 'Jev API key', key_host: 'api.typesafe.ai:443', handles: [] };
run(() => {
  S.config = card(fresh, true);
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  let inp = st.querySelector('[data-pkey-input]');
  assert(inp && inp.type === 'password' && inp.value === '', 'the key is pasted into an empty password field');
  assert(st.textContent.includes('no key yet') && st.textContent.includes('rides only to api.typesafe.ai:443'), 'the card names the one host the key rides to');
  assert(!st.textContent.includes('grant this plugin a credential handle first'), 'no config-file chore is asked of the operator');
  assert(st.querySelector('select[data-pset-key="api_key"]') === null, 'no empty profile picker');

  inp.value = 'sk-typed-123';
  inp.dispatchEvent(new Event('input'));
  inp.focus();
  renderPlugins();
  inp = st.querySelector('[data-pkey-input]');
  assert(inp.value === 'sk-typed-123', 'a pasted key survives a re-render');
  assert(document.activeElement === inp, 'and keeps its focus');
  assert(!st.innerHTML.includes('sk-typed-123'), 'the draft is never written into the markup');

  assert(inp.closest('.pkey-row').querySelector('button') === null, 'the key has no Save of its own');
  const other = frames.length;
  st.querySelector('[data-save="plugin:id.aiii.codequality"]').click();
  const set = frames.slice(other).filter(f => f.type === 'plugin_key_set');
  assert(set.length === 1 && set[0].plugin_key.plugin === 'id.aiii.codequality' && set[0].plugin_key.key === 'api_key' && set[0].plugin_key.secret === 'sk-typed-123', 'the card\'s Save sends the key once: ' + JSON.stringify(set));
  assert(!frames.some(f=>f.type==='config_set'), 'settings were sent before the key was confirmed');
  S.config.plugins.installed[0].settings[0]=Object.assign({},fresh,{key_kept:true});
  assert(acceptSettingsConfig(set[0].request_id), 'key acknowledgement was not correlated');
  const cfg=frames.find(f=>f.type==='config_set');
  assert(cfg, 'settings did not follow the confirmed key');
  assert(acceptSettingsConfig(cfg.request_id), 'settings acknowledgement was not correlated');
  renderPlugins();
  st.querySelector('[data-save="plugin:id.aiii.codequality"]').click();
  assert(frames.filter(f => f.type === 'plugin_key_set').length === 1, 'a second Save sends no key again');
  renderPlugins();
  assert(st.querySelector('[data-pkey-input]').value === '', 'the key is gone from the page once sent');

  S.config = card(Object.assign({}, fresh, { key_kept: true, value: 'id.aiii.codequality.api_key', handles: ['id.aiii.codequality.api_key'] }), true);
  renderPlugins();
  assert(st.textContent.includes('a key is kept for this setting'), 'a kept key is said to be kept');
  assert(st.querySelector('[data-pkey-input]').placeholder.includes('replace'), 'a kept key is replaced by pasting a new one');
  assert(st.querySelector('select[data-pset-key="api_key"]') === null, 'the card\'s own profile is not offered as a pick');
  st.querySelector('[data-pkey-remove]').click();
  assert(frames.some(f => f.type === 'plugin_key_clear' && f.plugin_key.key === 'api_key' && !('secret' in f.plugin_key)), 'Remove forgets the key and carries none');

  S.config = card(Object.assign({}, fresh, { key_kept: true, value: 'id.aiii.codequality.api_key' }), false);
  renderPlugins();
  assert(st.querySelector('[data-pkey-input]') === null && st.querySelector('[data-nosecrets]') !== null, 'a connection that is not private is not offered the field');
  assert(st.querySelector('[data-pkey-remove]') !== null, 'forgetting a key needs no private connection');

  S.config = card(Object.assign({}, fresh, { value: 'shared', effective: 'shared', handles: ['shared', 'another'] }), true);
  renderPlugins();
  assert(st.textContent.includes('a key is configured for this setting'), 'a shared configured key is not described as missing');
  assert(st.querySelector('select[data-pset-key="api_key"]') === null, 'no config-file profile dropdown even when other profiles exist');
  assert(!st.textContent.includes('or a credential profile from the config file'), 'the unwanted alternate field is absent');
  assert(st.querySelector('[data-pkey-input]').value === '' && st.querySelector('[data-pkey-input]').placeholder.includes('replace'), 'replacement is still possible without displaying the secret');

  S.config = card({ key: 'api_key', type: 'secret', title: 'Jev API key', key_not: 'its package declares 2 outbound hosts, not one', handles: ['acme'] }, true);
  renderPlugins();
  assert(st.textContent.includes('a key cannot be pasted for this plugin: its package declares 2 outbound hosts'), 'the card says why it cannot take a key');
  assert(st.querySelector('[data-pkey-input]') === null, 'and offers no field');
  const pick = st.querySelector('select[data-pset-key="api_key"]');
  assert(pick === null, 'no alternate profile field for a package without a single host either');
});
</script>`
	modules := map[string][]byte{
		"/state.js": []byte(`export const S = { stats: { name: 'TestIdentity' }, providers: [], config: null, providersLoaded: false };`),
		"/ws.js":    []byte(`export const frames = []; export function send(f) { f.request_id='req-'+(frames.length+1); frames.push(f); return f.request_id; } export function wsReady() { return true; } export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`),
	}
	for _, path := range []string{"static/views/plugins.js", "static/views/plugin-setting.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	runPageInEngines(t, page, modules)
}
