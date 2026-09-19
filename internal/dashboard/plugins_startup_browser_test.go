//go:build !windows

// .
// .

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
// .
// .
const pluginsStartupPage = `<!doctype html>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  const base = { tier: 'T3', mode: 'native', variant: 'linux-x86_64-native', family: 'voice', settings: [],
    grants: { listed: true, read_only: false, auto_confirm: [] } };
  S.config = { plugins: {
    autoload: 'T2', skips: [], catalog: [],
    pending: [{ id: 'org.example.late', version: '0.9.0', phase: 'refused',
                summary: 'plugin org.example.late 0.9.0: start refused: no readiness mark within 200ms (you set 300ms; capped at the ceiling 200ms); killed' }],
    installed: [
      Object.assign({ id: 'org.example.engine', version: '1.0.0',
        startup: { effective_ms: 60000, requested_ms: 180000, ceiling_ms: 60000, source: 'package', capped: true } }, base),
      Object.assign({ id: 'org.example.mine', version: '1.0.0',
        startup: { effective_ms: 45000, requested_ms: 45000, ceiling_ms: 300000, source: 'operator', capped: false } }, base),
      Object.assign({ id: 'org.example.plain', version: '1.0.0' }, base),
    ],
  } };
  renderPlugins();
  const cards = Array.from(document.querySelectorAll('#plugins-stack .card'));
  const card = id => { const c = cards.find(c => c.textContent.includes(id)); assert(c, 'no card for ' + id); return c.textContent; };

  const engine = card('org.example.engine');
  assert(engine.includes('Allowed 60 s to report ready — the package asked for 180 s; capped at the 60 s ceiling.'),
    'the recorded allowance, in the operator’s words: ' + engine);
  const mine = card('org.example.mine');
  assert(mine.includes('Allowed 45 s to report ready — you set 45 s; ceiling 300 s.'),
    'an operator-set allowance says so: ' + mine);
  const plain = card('org.example.plain');
  assert(!plain.includes('report ready'), 'a plugin with no recorded allowance says nothing about one: ' + plain);

  // The refusal carries the allowance that killed the child, and the
  // pending card shows the refusal as it came.
  const late = card('org.example.late');
  assert(late.includes('no readiness mark within 200ms (you set 300ms; capped at the ceiling 200ms)'),
    'a refused start names the allowance that decided it: ' + late);
});
</script>`

func TestStartupAllowanceReachesTheCardInBrowser(t *testing.T) {
	runPageInEngines(t, pluginsStartupPage, map[string][]byte{
		"/views/settings.js": []byte(`export const saved = []; export function saveConfigSection(s) { saved.push(s); }
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; }
export const sent = []; export function sendConfigChanges(s, ch) { saved.push(s); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export function send() {}`),
	})
}
