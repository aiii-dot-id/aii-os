//go:build !windows

package dashboard

import "testing"

const pluginsSetsPage = `<!doctype html>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  const base = { tier: 'T3', mode: 'native', settings: [], grants: { listed: true, read_only: false, auto_confirm: [] } };
  const chosen = { variant: 'large', sets: [
    { variant: 'large', chosen: true, why: 'chosen: the first in the package\'s order that this machine can run',
      storage: 'needs 4.09 GB more on disk at its peak (runtime archive 1.48 GB, installed runtime 2.33 GB, models 286.1 MB); 500.00 GB free on the volume holding /data',
      bandwidth: '1.76 GB to download from cdn.example.org; about 3 minutes at the 10.0 MB/s this host measured from there',
      resources: 'fits: needs 6.00 GB of memory (48.00 GB free now) and 9.00 GB of the cuda device\'s memory (11.00 GB of 12.00 GB free now)' },
    { variant: 'compact', storage: 'needs 715.3 MB more on disk at its peak', bandwidth: 'nothing to download', resources: 'fits: needs 1.00 GB of memory (48.00 GB free now)',
      reasons: [{ words: 'the package prefers large, which this machine can run' }] }] };
  const refused = { variant: '', sets: [
    { variant: 'large', storage: 'needs 4.09 GB more on disk', bandwidth: 'the speed of this link is not yet measured', resources: 'needs 9.00 GB of the cuda device\'s 12.00 GB; 4.10 GB is free now, the rest held by other processes',
      reasons: [{ words: 'needs 9.00 GB of the cuda device\'s 12.00 GB; 4.10 GB is free now, the rest held by other processes', remedy: 'it is chosen at a later activation once that memory is free' },
                { words: 'its runtime archive is 1.48 GB; the operator\'s ceiling plugins.runtime.max_compressed_bytes is 512.0 MB', remedy: 'set plugins.runtime.max_compressed_bytes to 1591128917 or more, or clear it so that free disk decides' }] }] };
  S.config = { plugins: {
    autoload: 'T2', skips: [], catalog: [], runtime: { max_files: 32768, max_depth: 24, roots_kept: 2, max_compressed_bytes: 0 },
    pending: [{ id: 'org.example.waiting', version: '2.0.0', phase: 'refused', summary: 'no component set fits this host',
                refusal: { stage: 'material', class: 'transient', cause: 'no component set fits this host' }, selection: refused }],
    installed: [
      Object.assign({ id: 'org.example.sets', version: '1.0.0', variant: 'large',
        lifecycle: { state: 'active', activations: [{ gen: 3, role: 'active', version: '1.0.0', selection: chosen }] } }, base),
      Object.assign({ id: 'org.example.module', version: '1.0.0', variant: 'any-wasm', mode: 'wasm',
        lifecycle: { state: 'active', activations: [{ gen: 1, role: 'active', version: '1.0.0' }] } }, base),
    ],
  } };
  renderPlugins();
  const stack = document.getElementById('plugins-stack');
  const text = stack.textContent;
  const card = id => [...stack.querySelectorAll('.card')].find(c => c.textContent.includes(id));
  const sets = card('org.example.sets');
  assert(sets && sets.querySelector('.sets'), 'the installed card has its component sets: ' + text);
  const large = sets.querySelector('[data-set="large"]'), compact = sets.querySelector('[data-set="compact"]');
  assert(large.classList.contains('is-chosen') && large.textContent.includes('chosen: the first in the package\'s order that this machine can run'), 'the chosen set and why: ' + large.textContent);
  assert(large.textContent.includes('disk: needs 4.09 GB more on disk at its peak'), '(a) storage: ' + large.textContent);
  assert(large.textContent.includes('download: 1.76 GB to download from cdn.example.org; about 3 minutes'), '(b) download: ' + large.textContent);
  assert(large.textContent.includes('this machine: fits: needs 6.00 GB of memory'), '(c) resources: ' + large.textContent);
  assert(compact.textContent.includes('passed over') && compact.textContent.includes('the package prefers large'), 'the set passed over and why: ' + compact.textContent);
  const waiting = card('org.example.waiting');
  assert(waiting.textContent.includes('no component set fits this machine'), 'a refusal with no set that fits says so: ' + waiting.textContent);
  const why = [...waiting.querySelectorAll('.set-why li')].map(li => li.textContent);
  assert(why.length === 2 && why[0].includes('4.10 GB is free now, the rest held by other processes — to admit it: it is chosen at a later activation once that memory is free'), 'a momentary reason and its remedy: ' + why);
  assert(why[1].includes('to admit it: set plugins.runtime.max_compressed_bytes to 1591128917 or more'), 'a ceiling and the value that admits: ' + why);
  const module = card('org.example.module');
  assert(module && !module.querySelector('.sets') && !module.textContent.includes('COMPONENT SETS'), 'a module shows nothing passed over: ' + (module && module.textContent));
  const archive = document.getElementById('cfg-rt-archive');
  assert(archive && archive.value === '' && archive.placeholder.includes('free disk decides'), 'an unset byte ceiling is an empty box that says free disk decides: ' + (archive && archive.outerHTML));
  assert(document.getElementById('cfg-rt-files').value === '32768', 'the structural ceiling shows its value');
});
</script>`

func TestEveryComponentSetIsJudgedOnTheCardInBrowser(t *testing.T) {
	runPageInEngines(t, pluginsSetsPage, map[string][]byte{
		"/views/settings.js": []byte(`export const saved = []; export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection(s) { saved.push(s); }
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; }
export const sent = []; export function sendConfigChanges(s, ch) { saved.push(s); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export function send() {}`),
	})
}

const pluginsSwapPage = `<!doctype html>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  const base = { tier: 'T3', mode: 'native', settings: [], grants: { listed: true, read_only: false, auto_confirm: [] } };
  const serving = { variant: 'small', better: 'large', sets: [
    { variant: 'large', arriving: true, why: 'arriving: its material is fetched in the background while small serves, and it takes over once that material is verified and it has started, reported ready and answered healthy',
      storage: 'needs 4.09 GB more on disk at its peak', bandwidth: '1.76 GB to download from cdn.example.org; the speed of this link is not yet measured',
      resources: 'fits: needs 6.00 GB of memory (48.00 GB free now)' },
    { variant: 'small', chosen: true, why: 'serves while large arrives: large is the first in the package\'s order that this machine can run, and it still has material to fetch; everything this set needs is on disk',
      storage: 'everything it needs is on disk already', bandwidth: 'nothing to download', resources: 'fits: needs 1.00 GB of memory (48.00 GB free now)' }] };
  S.config = { plugins: {
    autoload: 'T2', skips: [], catalog: [], runtime: { max_files: 32768, max_depth: 24, roots_kept: 2 }, pending: [],
    installed: [Object.assign({ id: 'org.example.sets', version: '1.0.0', variant: 'small',
      lifecycle: { state: 'active', retry_at: '2026-10-08T23:59:00Z',
        refusal: { stage: 'start', class: 'transient', cause: 'the large set\'s child did not report ready within its allowance' },
        activations: [{ gen: 3, role: 'active', version: '1.0.0', selection: serving }] } }, base)],
  } };
  renderPlugins();
  const stack = document.getElementById('plugins-stack');
  const card = [...stack.querySelectorAll('.card')].find(c => c.textContent.includes('org.example.sets'));
  assert(card && card.querySelector('.sets'), 'the serving card has its component sets: ' + stack.textContent);
  const large = card.querySelector('[data-set="large"]'), small = card.querySelector('[data-set="small"]');
  assert(small.classList.contains('is-chosen') && small.textContent.includes('serves while large arrives'), 'the set serving, and for which set: ' + small.textContent);
  assert(large.classList.contains('is-arriving') && !large.classList.contains('is-chosen') && !large.textContent.includes('passed over') &&
    large.textContent.includes('arriving: its material is fetched in the background while small serves'), 'the set arriving, and how it takes over: ' + large.textContent);
  assert(large.textContent.includes('download: 1.76 GB to download from cdn.example.org'), 'what the arriving set still downloads: ' + large.textContent);
  assert(card.textContent.includes('last attempt refused: the large set\'s child did not report ready') && card.textContent.includes('transient at start'),
    'the failed set\'s reason on the card of what still serves: ' + card.textContent);
  assert(card.textContent.includes('tried again at 2026-10-08T23:59:00Z'), 'when the move is asked again: ' + card.textContent);
});
</script>`

func TestTheSetArrivingIsOnTheCardOfTheSetServingInBrowser(t *testing.T) {
	runPageInEngines(t, pluginsSwapPage, map[string][]byte{
		"/views/settings.js": []byte(`export const saved = []; export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection(s) { saved.push(s); }
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; }
export const sent = []; export function sendConfigChanges(s, ch) { saved.push(s); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export function send() {}`),
	})
}
