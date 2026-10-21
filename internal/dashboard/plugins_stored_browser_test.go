//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

// WHAT PLUGINS HAVE STORED, ON THE PLUGINS PAGE, rendered from the shipped
// module: each plugin that holds a memory or a key in the identity's
// database, installed or not, with what it holds; the removal asked once
// with what will go, sent only on a yes; what the host says went; a
// listing that could not be read said in its place; and beside a
// plugin's Uninstall, that its package goes and what it stored stays.
const pluginsStoredPage = `<!doctype html>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { sent } from './ws.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  const stack = document.getElementById('plugins-stack');
  const card = () => stack.querySelector('[data-stored-card]');
  const show = plugins => { S.config = { plugins: Object.assign({ autoload: 'T2' }, plugins) }; renderPlugins(); return card(); };
  const row = id => stack.querySelector('[data-stored="' + id + '"]');

  assert(!show({}), 'no card where no plugin holds anything and nothing was removed');

  const notes = { id: 'org.example.notes', installed: true, memories: 12, replaced: 3, keys: 2 };
  const gone = { id: 'org.example.gone', memories: 1 };
  let c = show({ stored: [gone, notes], stored_replaced_days: 90,
    catalog: [{ id: 'org.example.notes', version: '1.0.0', tier: 'T1', installed: true, installed_version: '1.0.0', installed_tier: 'T1' }] });
  assert(c && c.querySelector('h3').textContent === 'STORED BY PLUGINS', 'the card is drawn: ' + (c && c.textContent));
  assert(c.textContent.includes("What each plugin has stored in this identity's database: its memories and its keys. Uninstalling a plugin removes its package and nothing it stored. A replaced memory is removed on its own 90 days after it was replaced (plugins.runtime.replaced_memory_days in the config file); a plugin may hold as many memories as plugins.runtime.max_memories allows."),
    'the card says what it lists and what an uninstall leaves: ' + c.textContent);
  assert(row('org.example.notes').textContent.includes('installed') && !row('org.example.notes').textContent.includes('not installed') &&
    row('org.example.notes').textContent.includes('12 memories (3 of them replaced) and 2 keys'), 'an installed plugin and what it holds: ' + row('org.example.notes').textContent);
  assert(row('org.example.gone').textContent.includes('not installed; what it stored is still here') &&
    row('org.example.gone').textContent.includes('1 memory and 0 keys'), 'a plugin that is gone is listed with what it left: ' + row('org.example.gone').textContent);
  const kept = stack.querySelector('[data-stored-kept="org.example.notes"]');
  assert(kept && kept.textContent === 'Uninstall removes its package. What it stored here (12 memories (3 of them replaced) and 2 keys) stays until you remove it under STORED BY PLUGINS.',
    'beside Uninstall, what stays: ' + (kept && kept.textContent));

  const asked = [];
  window.confirm = q => { asked.push(q); return false; };
  let btn = row('org.example.notes').querySelector('[data-stored-remove]');
  assert(btn && btn.textContent === 'Remove what it stored', 'the removal is offered on the row');
  btn.click();
  assert(asked.length === 1 && asked[0] === "Remove what org.example.notes stored in this identity's database? 12 memories (3 of them replaced) and 2 keys will be deleted from it. Snapshots and exports already made still hold them. The plugin stays installed, with an empty record. You have not opened them on this page: Cancel, then Read its memories or Read its keys, to read them first.",
    'the question says what will go: ' + JSON.stringify(asked));
  assert(sent.length === 0 && !btn.disabled, 'a no sends nothing: ' + JSON.stringify(sent));

  window.confirm = q => { asked.push(q); return true; };
  btn = row('org.example.gone').querySelector('[data-stored-remove]');
  btn.click();
  assert(asked[1] === "Remove what org.example.gone stored in this identity's database? 1 memory and 0 keys will be deleted from it. Snapshots and exports already made still hold them. You have not opened them on this page: Cancel, then Read its memories or Read its keys, to read them first.",
    'a plugin that is gone is asked about without the line on staying installed: ' + asked[1]);
  assert(sent.length === 1 && sent[0].type === 'plugin' && sent[0].plugin.action === 'remove_stored' && sent[0].plugin.id === 'org.example.gone',
    'a yes sends the act for that plugin: ' + JSON.stringify(sent));
  assert(btn.disabled && btn.textContent === 'Removing…', 'the button answers beside itself');

  // Installed is the catalog row's own mark, and a package on its way or
  // refused is said as that row says it.
  c = show({ stored: [{ id: 'org.example.held', pending: 'refused', keys: 1 }] });
  assert(row('org.example.held').textContent.includes('installed, refused') && !row('org.example.held').textContent.includes('not installed'), 'a refused package is not called gone: ' + row('org.example.held').textContent);
  c = show({ stored: ['starting', 'retiring', 'held', 'fetching', 'waiting', 'ready'].map(p => ({ id: 'org.example.' + p, pending: p, keys: 1 })) });
  for (const [p, said] of [['starting', 'installed, starting'], ['retiring', 'installed, stopping'], ['held', 'installed, held'],
    ['fetching', 'installed, preparing'], ['waiting', 'installed, preparing'], ['ready', 'installed, preparing']]) {
    const text = row('org.example.' + p).querySelector('.store-hint').textContent;
    assert(text === said, 'a package ' + p + ' is said as "' + said + '": ' + text);
  }

  const said = "Removed what org.example.gone stored in this identity's database: 1 memory and 0 keys.";
  c = show({ stored: [notes], stored_removed: said });
  assert(!row('org.example.gone') && c.querySelector('[data-stored-removed]').textContent === said, 'what the host says went is shown: ' + c.textContent);
  assert(!c.textContent.includes('removed on its own'), 'no window is named where the host sent none: ' + c.textContent);

  c = show({ stored_removed: said });
  assert(c && !c.querySelector('[data-stored]') && c.querySelector('[data-stored-removed]').textContent === said, 'the last removal is still said when nothing is left');

  c = show({ stored_error: 'what plugins have stored here could not be read: disk I/O error' });
  assert(c && c.querySelector('[data-stored-error]').textContent === 'what plugins have stored here could not be read: disk I/O error', 'a listing that could not be read is said, not shown as nothing: ' + (c && c.textContent));
});
</script>`

func TestWhatPluginsStoredIsOnThePluginsPageInBrowser(t *testing.T) {
	runPageInEngines(t, pluginsStoredPage, map[string][]byte{
		"/views/settings.js": []byte(`export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection() {}
export function savebarHTML() { return ''; } export function sendConfigChanges() {} export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export const sent = []; export function send(m) { sent.push(m); return true; }`),
	})
}
