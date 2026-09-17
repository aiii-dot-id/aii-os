//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestAgencyCopyStatesEachLifecycle(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings, savedText } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.config = { llm: { provider: 'p', model: 'm', endpoint: 'https://x.test', api_key_masked: 'none', context_length: 1000, max_output_tokens: 100 },
    dashboard: { host: '127.0.0.1', port: 1, tls: false }, prompt: {}, witness: {}, logs: {}, plugins: {},
    agency: { prefer_local_for_roles: false, heuristic_nudges: false }, restart_required: [] };
  S.providers = []; S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  document.querySelector('[data-sec="agency"]').click();
  const st = document.getElementById('settings-stack');
  const routing = st.querySelector('[data-lifecycle="routing"]').textContent;
  const coaching = st.querySelector('[data-lifecycle="coaching"]').textContent;
  assert(/applies live/i.test(routing) && /role-tagged spawn/i.test(routing), 'role routing is stated as live: ' + routing);
  assert(/after restart/i.test(coaching), 'coaching is stated as restart-bound: ' + coaching);
  assert(!/applies live/i.test(coaching), 'no live claim covers the coaching field');
  const note = st.querySelector('.savesay').textContent;
  assert(/live/.test(note) && /restart/.test(note), 'the savebar names both lifecycles: ' + note);
  assert(/after restart/.test(savedText('agency', ['agency.heuristic_nudges'])), 'a save that changed coaching says it waits for a restart');
  assert(/applies live/.test(savedText('agency', [])), 'a save that changed only routing says it applied live');
  assert(!/live/.test(savedText('dashboard', ['dashboard.port'])) && /after restart: dashboard.port/.test(savedText('dashboard', ['dashboard.port'])), 'other sections name their restart-bound fields');
});
</script>`
	// .
	// .
	// .
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = []byte(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? ''); export const hueOf = () => 0; export const copyText = async () => true;`)
	modules["/ws.js"] = []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
