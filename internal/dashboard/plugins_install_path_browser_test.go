//go:build !windows

package dashboard

import "testing"

// release not yet fetched says when it will be known; an update that
// changes the model says how many vectors are made again; the engine's ready
const pluginsInstallPathPage = `<!doctype html>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { sent } from './ws.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  const base = { tier: 'T3', mode: 'native', grants: { listed: true, read_only: false, auto_confirm: [] } };
  const needs = 'needs a GPU (cuda) with 1.25 GB of its memory, or runs without one (cpu-set)';
  S.config = { plugins: {
    autoload: 'T2', skips: [], runtime: { max_files: 32768, max_depth: 24, roots_kept: 2 }, catalog_refresh_minutes: 30,
    catalog: [
      { id: 'org.example.engine', version: '1.0.0', tier: 'T3', available: true, installed: true, installed_version: '1.0.0', installed_tier: 'T3', needs: needs },
      { id: 'org.example.fresh', version: '1.0.0', tier: 'T3', available: true },
      { id: 'org.example.vectors', version: '2.0.0', tier: 'T3', available: true, installed: true, installed_version: '1.0.0', installed_tier: 'T3', pending: 'fetching', pending_text: 'models arriving',
        remakes: 2312, remake_seconds: 4380 },
      { id: 'org.example.unmeasured', version: '2.0.0', tier: 'T3', available: true, installed: true, installed_version: '1.0.0', installed_tier: 'T3', pending: 'ready', remakes: 1 },
    ],
    pending: [{ id: 'org.example.stopped', version: '2.0.0', phase: 'waiting', summary: 'runtime absent (21.3 MB to fetch)', attempt: 1,
      bytes_present: 0, bytes_total: 22340309, files_present: 0, files_total: 1,
      last_error: 'the download stopped: fetch runtime archive: the server answered 404',
      remedy: 'Nothing is at the address the package declares; its publisher must correct the release. Try again once they have, or install a corrected release.' },
      { id: 'org.example.cut', version: '1.0.0', phase: 'waiting', summary: 'models 0 B of 4.9 KB present (0 of 1 files)', attempt: 5,
        bytes_present: 0, bytes_total: 5000, files_present: 0, files_total: 1, resumes: true,
        last_error: 'the download stopped after 5 attempts in a row brought nothing: dial tcp: connect: network is unreachable',
        remedy: '5 attempts in a row brought nothing; check this machine\'s connection to the address it names. It is tried again by itself when the plugin catalog is next fetched, or Try again now.' },
      { id: 'org.example.cutoff', version: '1.0.0', phase: 'refused', summary: 'material refused', resumes: true,
        refusal: { stage: 'material', class: 'permanent', cause: 'the download stopped after 5 attempts in a row brought nothing', remedy: '5 attempts in a row brought nothing; check this machine\'s connection to the address it names. It is tried again by itself when the plugin catalog is next fetched, or Try again now.' } }],
    installed: [Object.assign({ id: 'org.example.engine', version: '1.0.0', variant: 'gpu',
      readiness: { models_loaded: 2, accelerator: 'cpu_vulkan', probe_ms: 40,
        differs: 'the engine reports cpu_vulkan (on vulkan) where its set declares cuda+vulkan (on cuda and vulkan); shown, not acted on: the set is judged by its publisher\'s checks' },
      settings: [{ key: 'quality', type: 'enum', title: 'Quality', values: ['fast', 'best'], value: 'best', effective: 'best',
        models: { state: 'stopped', files_present: 0, files_total: 1, bytes_present: 0, bytes_total: 5000,
          last_error: 'the download stopped: fetch model best-model: the server answered 404', remedy: 'Nothing is at the address the package declares. Try again once it is.' } }],
      lifecycle: { state: 'active', activations: [{ gen: 2, role: 'active', version: '1.0.0', selection: { variant: 'gpu', needs: needs, sets: [
        { variant: 'gpu', chosen: true, why: 'chosen: the first in the package\'s order that this machine can run' },
        { variant: 'cpu-set', reasons: [{ words: 'the package prefers gpu, which this machine can run' }] }] } }] } }, base)],
  } };
  renderPlugins();
  const stack = document.getElementById('plugins-stack');
  const card = id => [...stack.querySelectorAll('.card')].find(c => c.querySelector('h3') && c.querySelector('h3').textContent.includes(id));
  const stopped = stack.querySelector('[data-pending="org.example.stopped"]');
  assert(stopped && stopped.textContent.includes('stopped — nothing retries it on its own') && stopped.textContent.includes('the server answered 404') &&
    stopped.textContent.includes('Nothing is at the address the package declares') && !stopped.textContent.includes('waiting to retry'), 'a stopped download: ' + (stopped && stopped.textContent));
  const retry = stopped.querySelector('[data-plugin="retry:org.example.stopped"]');
  assert(retry && retry.textContent === 'Try again', 'the stopped download offers Try again');
  // One the network's failures ended says the host asks again by itself,
  // and still offers Try again; stopped or refused, it never says nothing
  // will.
  const cut = stack.querySelector('[data-pending="org.example.cut"]');
  assert(cut && cut.textContent.includes('stopped — tried again by itself when the plugin catalog is next fetched') && !cut.textContent.includes('nothing retries') &&
    cut.querySelector('[data-plugin="retry:org.example.cut"]'), 'a download the network ended: ' + (cut && cut.textContent));
  const cutoff = stack.querySelector('[data-pending="org.example.cutoff"]');
  assert(cutoff && cutoff.textContent.includes('Nothing is downloading now; it is tried again by itself when the plugin catalog is next fetched. Try again to ask now, or remove it.') &&
    !cutoff.textContent.includes('nothing retries') && cutoff.querySelector('[data-plugin="retry:org.example.cutoff"]'), 'a refusal the network ended: ' + (cutoff && cutoff.textContent));
  const engine = card('org.example.engine');
  assert(engine && engine.querySelector('.set-needs') && engine.querySelector('.set-needs').textContent === needs, 'the needs line on the card: ' + (engine && engine.textContent));
  const row = stack.querySelector('[data-entry="org.example.engine"]');
  assert(row && row.querySelector('.store-needs') && row.querySelector('.store-needs').textContent === needs, 'the needs line on the catalog row: ' + (row && row.textContent));
  const fresh = stack.querySelector('[data-detail-of="org.example.fresh"]');
  assert(fresh && fresh.textContent.includes('said once its package is fetched'), 'a release not yet fetched says when its needs are known: ' + (fresh && fresh.textContent));
  // AN UPDATE THAT CHANGES THE MODEL SAYS SO before it activates: how
  // many vectors are made again and, where the host
  // measured the source's work, about how long; an update that keeps the
  // model says nothing.
  const remade = stack.querySelector('[data-remakes="org.example.vectors"]');
  assert(remade && remade.textContent === 'This update changes the model that makes the meaning layer\'s vectors: 2312 vectors are made again, about 73 minutes.', 'the update line: ' + (remade && remade.textContent));
  const unmeasured = stack.querySelector('[data-remakes="org.example.unmeasured"]');
  assert(unmeasured && unmeasured.textContent === 'This update changes the model that makes the meaning layer\'s vectors: 1 vector is made again.', 'with nothing measured, no time is said: ' + (unmeasured && unmeasured.textContent));
  assert(stack.querySelectorAll('[data-remakes]').length === 2, 'a row with nothing made again says nothing of it');
  const differs = engine.querySelector('.engine-differs');
  assert(differs && differs.textContent.includes('cpu_vulkan (on vulkan)') && differs.textContent.includes('cuda+vulkan (on cuda and vulkan)') && differs.textContent.includes('not acted on'),
    'the ready line\'s difference from its set: ' + engine.textContent);
  const line = engine.querySelector('[data-setting-models="stopped"]');
  assert(line && line.textContent.includes('could not be fetched') && line.textContent.includes('Try again once it is'), 'the stopped setting line: ' + (line && line.textContent));
  const again = line.querySelector('[data-models-retry="org.example.engine"]');
  assert(again, 'the stopped setting line has Try again');
  again.click();
  assert(sent.some(m => m.type === 'plugin' && m.plugin.action === 'retry' && m.plugin.id === 'org.example.engine'), 'Try again on the line sends the plugin\'s retry: ' + JSON.stringify(sent));
  // How often the index is fetched is the operator's, and says what it
  // also decides: when a download the network stopped is tried again.
  const every = stack.querySelector('#cfg-catevery');
  assert(every && every.value === '30' && every.closest('label').textContent.includes('Check every (minutes)'), 'the catalog\'s spacing: ' + (every && every.closest('label').textContent));
  // The operator's limits apply now.
  assert(stack.textContent.includes('Applies at once.'), 'the limits say they apply now: ' + stack.textContent);
});
</script>`

func TestTheInstallPathIsOnThePluginsPageInBrowser(t *testing.T) {
	runPageInEngines(t, pluginsInstallPathPage, map[string][]byte{
		"/views/settings.js": []byte(`export const saved = []; export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection(s) { saved.push(s); }
export function savebarHTML(section, note) { return '<div class="savebar"><button class="btn" data-save="' + section + '">Save</button><span class="savenote">' + note + '</span></div>'; }
export const sent = []; export function sendConfigChanges(s, ch) { saved.push(s); sent.push(ch); }
export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export const sent = []; export function send(m) { sent.push(m); }`),
	})
}
