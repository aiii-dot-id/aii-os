//go:build !windows

package dashboard

import "testing"

const pluginsChecksPage = `<!doctype html>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  const base = { tier: 'T3', mode: 'native', version: '1.0.0', grants: { listed: true, read_only: false, auto_confirm: [] } };
  const serving = (checks, selection) => ({ state: 'active', activations: [{ gen: 3, role: 'active', version: '1.0.0', checks: checks, selection: selection }] });
  S.config = { plugins: { autoload: 'T2', skips: [], runtime: { max_files: 32768, max_depth: 24, roots_kept: 2 }, catalog: [], pending: [],
    installed: [
      Object.assign({ id: 'org.example.passed', variant: 'large', lifecycle: serving({ state: 'passed', variant: 'large', at: '2026-10-09T03:00:00Z' }) }, base),
      Object.assign({ id: 'org.example.late', variant: 'large', lifecycle: serving({ state: 'pending', variant: 'large', check: 'embeds the memory', outcome: 'timed_out',
        words: 'answered as its publisher says in 812 ms, past its bound of 500 ms', timeouts: 2 }) }, base),
      Object.assign({ id: 'org.example.plain', variant: 'only', lifecycle: serving({ state: 'none', variant: 'only' }) }, base),
      Object.assign({ id: 'org.example.probed', variant: 'only', lifecycle: serving({ state: 'none', variant: 'only' }),
        probes: { state: 'passed', at: '2026-10-09T03:03:00Z' } }, base),
      Object.assign({ id: 'org.example.disagrees', variant: 'only', lifecycle: serving({ state: 'none', variant: 'only' }),
        probes: { state: 'failed', at: '2026-10-09T03:04:00Z', words: 'plugin org.example.disagrees: probe 2 (a memory) came back at 0.800 of the vector its publisher signed for it, under the 0.95 the package asks of itself' } }, base),
      Object.assign({ id: 'org.example.unasked', variant: 'only', lifecycle: serving({ state: 'none', variant: 'only' }),
        probes: { state: 'pending', words: 'plugin org.example.unasked: probe 1 (a cue) was not answered' } }, base),
      Object.assign({ id: 'org.example.demoted', variant: 'small', lifecycle: serving({ state: 'passed', variant: 'small', at: '2026-10-09T03:01:00Z' }, { variant: 'small', sets: [
        { variant: 'large', reasons: [{ words: 'its publisher\'s checks failed on this machine: "embeds the memory": came back at 0.800 of its reference, under the 0.950 it asks', remedy: 'Try again, or install a release whose checks pass here' }] },
        { variant: 'small', chosen: true, why: 'chosen: the first in the package\'s order that this machine can run' }] }) }, base),
      Object.assign({ id: 'org.example.failed', variant: 'large', lifecycle: { state: 'refused', activations: [{ gen: 4, role: 'refused', version: '1.0.0',
        checks: { state: 'failed', variant: 'large', at: '2026-10-09T03:02:00Z', check: 'answers its question', outcome: 'wrong', words: 'its answer lacks "harbor": {"text":"none"}' } }] } }, base),
    ] } };
  renderPlugins();
  const stack = document.getElementById('plugins-stack');
  const card = id => [...stack.querySelectorAll('.card')].find(c => c.querySelector('h3') && c.querySelector('h3').textContent.includes(id));
  const line = id => { const c = card(id); return c && c.querySelector('[data-checks]'); };
  const passed = line('org.example.passed');
  assert(passed && passed.textContent === "its publisher's checks passed on this machine 2026-10-09T03:00:00Z", 'a pass and when: ' + (passed && passed.textContent));
  assert(!card('org.example.passed').textContent.includes('confirmed') && !card('org.example.passed').textContent.includes('verified'), 'a pass claims no more than its publisher\'s word');
  const late = line('org.example.late');
  assert(late && late.dataset.checks === 'pending' && late.textContent.startsWith('checks not yet passed: timed out') &&
    late.textContent.includes('“embeds the memory”') && late.textContent.includes('past its bound of 500 ms'), 'a timeout: ' + (late && late.textContent));
  const plain = line('org.example.plain');
  assert(plain && plain.textContent === 'the plugin declares no checks; it is used on its readiness alone', 'no checks declared: ' + (plain && plain.textContent));
  const probed = line('org.example.probed');
  assert(probed && probed.dataset.checks === 'probes' && probed.textContent.includes('declares no checks of its own; its signed probes are checked before memory uses its vectors') &&
    probed.textContent.includes('they passed on this machine 2026-10-09T03:03:00Z') && !probed.textContent.includes('readiness alone'), 'a probe-gated source: ' + (probed && probed.textContent));
  const disagrees = line('org.example.disagrees');
  assert(disagrees && disagrees.textContent.includes('they failed on this machine') && disagrees.textContent.includes('probe 2 (a memory) came back at 0.800') &&
    disagrees.textContent.includes('memory does not use its vectors until it starts again'), 'a source whose probe disagreed: ' + (disagrees && disagrees.textContent));
  const unasked = line('org.example.unasked');
  assert(unasked && unasked.textContent.includes('not yet passed') && unasked.textContent.includes('probe 1 (a cue) was not answered'), 'a source whose probe was not answered: ' + (unasked && unasked.textContent));
  const demoted = card('org.example.demoted');
  const large = demoted && demoted.querySelector('[data-set="large"]');
  assert(large && large.textContent.includes('passed over') && large.textContent.includes('its publisher\'s checks failed on this machine: "embeds the memory"') &&
    large.textContent.includes('to admit it: Try again'), 'the set passed over says why: ' + (large && large.textContent));
  const failed = line('org.example.failed');
  assert(failed && failed.dataset.checks === 'failed' && failed.textContent.includes('its publisher\'s checks failed on this machine') &&
    failed.textContent.includes('“answers its question”') && failed.textContent.includes('its answer lacks "harbor"'), 'a failure names its check in its publisher\'s words: ' + (failed && failed.textContent));
});
</script>`

func TestThePublishersChecksAreOnThePluginsPageInBrowser(t *testing.T) {
	runPageInEngines(t, pluginsChecksPage, map[string][]byte{
		"/views/settings.js": []byte(`export const saved = []; export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection(s) { saved.push(s); }
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; }
export const sent = []; export function sendConfigChanges(s, ch) { saved.push(s); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export const sent = []; export function send(m) { sent.push(m); }`),
	})
}
