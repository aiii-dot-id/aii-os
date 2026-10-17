//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestAPluginsFilesAreTheIdentitysSandboxInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="plugins-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderPlugins } from './views/plugins.js';
import { assert, run } from './__harness.js';
run(() => {
  const cq = { id: 'id.aiii.codequality', version: '0.1.2', tier: 'T3', mode: 'wasm', variant: 'linux-x86_64-wasm', tools: ['pl_scan'],
    capabilities: ['fs.sandbox', 'fs.private', 'net.outbound'], grants: { listed: true, files: false, read_only: false, hosts: ['api.typesafe.ai:443'] } };
  const plain = { id: 'org.example.plain', version: '0.1.0', tier: 'T3', mode: 'wasm', variant: 'linux-x86_64-wasm', tools: ['pl_x'], capabilities: ['ring4.kv'], grants: {} };
  S.config = { llm: {}, dashboard: {}, prompt: {}, witness: {}, logs: {}, agency: {}, restart_required: [],
    plugins: { autoload: 'T0', catalog: [], auth_profiles: [], providers: [], installed: [cq, plain] } };
  S.providers = []; S.providersLoaded = true; S.view = 'plugins';
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  const box = st.querySelector('[data-grant-plugin="id.aiii.codequality"][data-grant-field="files"]');
  assert(box && !box.checked, 'a plugin signed for fs.sandbox is offered files');
  const row = box.closest('.grant-row').textContent;
  assert(row.includes('Settings → Sandbox') && row.includes('never its data directory') && row.includes('read only'), 'the box says where the plugin works: ' + row);
  assert(!st.querySelector('[data-grant-plugin="org.example.plain"][data-grant-field="files"]'), 'a plugin not signed for it is not');
  assert(!st.querySelector('[data-root-field]') && !st.textContent.includes('.roots'), 'no folders of the plugin\'s own');
  box.checked = true;
  st.querySelector('[data-save="grants:id.aiii.codequality"]').click();
  const sent = frames.filter(f => f.type === 'config_set').pop().config;
  assert(sent['plugins.grants.id.aiii.codequality.files'] === true && sent['plugins.grants.id.aiii.codequality.read_only'] === false, 'Save grants sends files with the boxes: ' + JSON.stringify(sent));
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/views/plugins.js", "static/pending.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
