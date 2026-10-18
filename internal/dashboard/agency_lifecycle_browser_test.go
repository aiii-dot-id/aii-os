//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestTheAgencyCardOffersOnlyRoleRouting(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, savedText } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = { llm: { provider: 'p', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none', context_length: 1000, max_output_tokens: 100 },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {},
    agency: { prefer_local_for_roles: false }, restart_required: [] };
  S.providers = []; S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  document.querySelector('[data-sec="agency"]').click();
  const st = document.getElementById('settings-stack');
  assert(!document.getElementById('cfg-hn') && !st.querySelector('[data-lifecycle="coaching"]'), 'the card still draws the coaching control');
  assert(!/coaching/i.test(st.textContent), 'the card still speaks of coaching prompts');
  const routing = st.querySelector('[data-lifecycle="routing"]').textContent;
  assert(/applies live/i.test(routing) && /role-tagged spawn/i.test(routing), 'role routing is stated as live: ' + routing);
  const note = st.querySelector('.savesay').textContent;
  assert(/live/.test(note) && !/restart/.test(note), 'the savebar names the one live lifecycle: ' + note);
  assert(/applies live/.test(savedText('agency', [])) && !/coaching|restart/.test(savedText('agency', [])), 'a save of the card says it applied live, and nothing more');
  assert(!/live/.test(savedText('dashboard', ['dashboard.port'])) && /after restart: dashboard.port/.test(savedText('dashboard', ['dashboard.port'])), 'other sections name their restart-bound fields');
  document.getElementById('cfg-plr').checked = true;
  st.querySelector('[data-save="agency"]').click();
  const sent = frames.filter(f => f.type === 'config_set');
  assert(sent.length === 1, 'the save sent ' + sent.length + ' config frame(s)');
  assert(JSON.stringify(sent[0].config) === '{"agency.prefer_local_for_roles":true}', 'the save writes only role routing: ' + JSON.stringify(sent[0].config));
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
