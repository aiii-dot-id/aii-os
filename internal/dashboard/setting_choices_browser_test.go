//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestALookedUpSettingOnTheCardInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="plugins-stack"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';
import { applySettingChoices } from './views/plugin-setting.js';
import { frames } from './ws.js';
const cfg = value => ({ secrets_ok: true, plugins: { autoload: 'T3', catalog: [], catalog_url: '', auth_profiles: [], providers: [],
  installed: [{ id: 'id.aiii.codequality', version: '0.1.1', tier: 'T3', mode: 'wasm', variant: 'linux-x86_64-wasm',
    settings: [{ key: 'model', type: 'string', title: 'Jev model', default: 'jev-latest', choices_from: true, value: value }] }] } });
const asks = () => frames.filter(f => f.type === 'plugin_setting_choices');
run(() => {
  S.config = cfg('jev-1.13.0');
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  let field = st.querySelector('[data-pset-key="model"]');
  assert(field && field.tagName === 'INPUT' && field.value === 'jev-1.13.0', 'while the plugin is asked, the field is the saved value');
  assert(st.textContent.includes('looking up the choices'), 'the card says it is asking');
  assert(asks().length === 1 && asks()[0].plugin_key.plugin === 'id.aiii.codequality' && asks()[0].plugin_key.key === 'model', 'the card asks the plugin once: ' + JSON.stringify(asks()));
  renderPlugins();
  assert(asks().length === 1, 'a re-render does not ask again');

  applySettingChoices({ plugin: 'id.aiii.codequality', key: 'model', choices: [{ value: 'jev-latest', label: 'The latest Jev' }, { value: 'jev-preview' }] });
  renderPlugins();
  field = st.querySelector('[data-pset-key="model"]');
  assert(field && field.tagName === 'SELECT', 'the answer is a drop-down');
  const opts = [...field.options].map(o => o.value + '=' + o.textContent);
  assert(opts.includes('jev-latest=The latest Jev') && opts.includes('jev-preview=jev-preview'), 'the plugin\'s choices under their names: ' + opts);
  assert(field.value === 'jev-1.13.0' && opts.some(o => o.startsWith('jev-1.13.0=') && o.includes('saved, not offered now')), 'the saved value stays visible, named as saved: ' + opts);

  S.config = cfg(undefined);
  applySettingChoices({ plugin: 'id.aiii.codequality', key: 'model', error: 'the plugin could not look them up: 401' });
  renderPlugins();
  field = st.querySelector('[data-pset-key="model"]');
  assert(field && field.tagName === 'INPUT', 'a failed lookup leaves the field editable');
  assert(st.textContent.includes('could not be looked up: the plugin could not look them up: 401'), 'the card says why');
  st.querySelector('[data-choices-refresh]').click();
  assert(asks().length === 2, 'Refresh asks again');
  assert(st.textContent.includes('looking up the choices'), 'and the card says it is asking');
});
</script>`
	modules := map[string][]byte{
		"/state.js": []byte(`export const S = { stats: { name: 'TestIdentity' }, providers: [], config: null, providersLoaded: false };`),
		"/util.js":  utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0;`),
		"/ws.js":    []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; } export function wsReady() { return true; } export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`),
		"/views/settings.js": []byte(`export const saved = []; export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection(section) { saved.push(section); }
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; } export const sent = []; export function sendConfigChanges(section, ch) { saved.push(section); sent.push(ch); } export function configFeedbackHTML() { return ''; }`),
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
