//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestTheAgencyCardOffersOnlyRoleRouting(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div><div id="technical-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { savedText } from './views/settings.js';
import { drawHelpers } from './views/settings/technical/forms.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = { llm: { provider: 'p', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none', context_length: 1000, max_output_tokens: 100 },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {},
    agency: {}, restart_required: [] };
  S.providers = []; S.providersLoaded = true; S.view = 'technical';
  const st = document.getElementById('technical-stack');
  drawHelpers(st);
  assert(!document.getElementById('cfg-hn') && !st.querySelector('[data-lifecycle="coaching"]'), 'the card still draws the coaching control');
  assert(!/coaching/i.test(st.textContent), 'the card still speaks of coaching prompts');
  assert(!document.getElementById('cfg-plr') && !st.querySelector('[data-save]'), 'the panel still offers a switch or a Save');
  assert(!/live/.test(savedText('dashboard', ['dashboard.port'])) && /after restart: dashboard.port/.test(savedText('dashboard', ['dashboard.port'])), 'other sections name their restart-bound fields');
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? ''); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
