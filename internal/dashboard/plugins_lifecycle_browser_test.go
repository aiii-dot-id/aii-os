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
const pluginsLifecyclePage = `<!doctype html>
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
    pending: [{ id: 'org.example.late', version: '0.9.0', phase: 'refused', retry_at: '2030-01-01T10:21:00Z',
                summary: 'plugin org.example.late 0.9.0: health refused: the engine never answered',
                refusal: { stage: 'health', class: 'transient', cause: 'the engine never answered', remedy: 'It is tried again on its own.', evidence: 'probe: no reply in 10s' },
                residue: ['child: child 48211 not yet reaped'] }],
    installed: [Object.assign({ id: 'org.example.engine', version: '1.2.0',
      lifecycle: { state: 'updating', since: '2030-01-01T10:20:30Z', admission: 'admitted (host 2.9 GB; 5.1 GB free after it)',
        residue: ['child: child 48211 not yet reaped'],
        activations: [{ gen: 6, role: 'active', version: '1.1.0', timings_ms: { verify: 22, start: 25500 } },
                      { gen: 7, role: 'candidate', version: '1.2.0' }] } }, base)],
  } };
  renderPlugins();
  const text = document.getElementById('plugins-stack').textContent;
  assert(text.includes('updating since 2030-01-01T10:20:30Z'), 'the state and since when: ' + text);
  assert(text.includes('generation 6 active (1.1.0) — start 25500 ms, verify 22 ms'), 'every activation with its stage timings: ' + text);
  assert(text.includes('generation 7 candidate (1.2.0)'), 'the candidate standing up: ' + text);
  assert(text.includes('admitted (host 2.9 GB'), 'the admission it was granted: ' + text);
  assert(text.includes('still held: child: child 48211 not yet reaped'), 'what a pending cleanup still holds: ' + text);
  // The refused pending card.
  assert(text.includes('transient at health — It is tried again on its own.'), 'the refusal’s class, stage and remedy: ' + text);
  assert(text.includes('probe: no reply in 10s'), 'its evidence: ' + text);
  assert(text.includes('It is tried again on its own at 2030-01-01T10:21:00Z.'), 'a transient refusal says when: ' + text);
  assert(!text.includes('nothing retries on its own'), 'a transient refusal is not described as needing a hand: ' + text);
});
</script>`

func TestPluginLifecycleReachesTheCardInBrowser(t *testing.T) {
	runPageInEngines(t, pluginsLifecyclePage, map[string][]byte{
		"/views/settings.js": []byte(`export const saved = []; export function saveConfigSection(s) { saved.push(s); }
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; }
export const sent = []; export function sendConfigChanges(s, ch) { saved.push(s); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export function send() {}`),
	})
}
