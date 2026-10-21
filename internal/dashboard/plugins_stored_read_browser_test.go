//go:build !windows

// Runs only through the unix browser-engine rig (browserengine_test.go,
// same tag) — this file calls its helpers, so it shares its build row.

package dashboard

import "testing"

// WHAT A PLUGIN STORED IS READ ON THE PAGE BEFORE ITS REMOVAL IS ASKED,
// rendered from the shipped module. The card offered to delete what no
// page let the operator read. Each plugin's memories and keys open under
// its row: every text and value whole (a long one is not cut, markup in
// one is text), newest first, with when it was stored and whether it is
// current, replaced or temporary; the reader says how many there are and
// how many it shows, and asks for the next page by the cursor the host
// gave. An answer for another plugin is not filed here. With the content
// on the page the removal's question is asked over it; without it, the
// question says so and how to read it first. A read that failed is said.
const pluginsStoredReadPage = `<!doctype html>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { sent } from './ws.js';
import { renderPlugins, applyPluginStored } from './views/plugins.js';

run(() => {
  const stack = document.getElementById('plugins-stack');
  const id = 'org.example.notes';
  const row = () => stack.querySelector('[data-stored="' + id + '"]');
  const reader = () => stack.querySelector('[data-stored-reader]');
  const items = () => Array.from(stack.querySelectorAll('[data-stored-item]'));
  const answer = reply => { applyPluginStored(reply); renderPlugins(); };
  S.config = { plugins: { autoload: 'T2', stored: [{ id: id, installed: true, memories: 3, replaced: 1, keys: 2 }] } };
  renderPlugins();

  const asked = [];
  window.confirm = q => { asked.push({ q: q, shown: items().length }); return false; };

  assert(!reader(), 'nothing is open until the operator opens it');
  row().querySelector('[data-stored-remove]').click();
  assert(asked.length === 1 && asked[0].q.endsWith('You have not opened them on this page: Cancel, then Read its memories or Read its keys, to read them first.'),
    'asked with nothing read, the question says so and how to read first: ' + JSON.stringify(asked));

  const readMemories = row().querySelector('[data-stored-read="memories:' + id + '"]');
  assert(readMemories && readMemories.textContent === 'Read its memories' && row().querySelector('[data-stored-read="keys:' + id + '"]').textContent === 'Read its keys', 'the two reads are offered on the row');
  readMemories.click();
  assert(JSON.stringify(sent[sent.length - 1]) === JSON.stringify({ type: 'plugin_stored', stored: { plugin: id, kind: 'memories' } }), 'the read asks the host for the newest page: ' + JSON.stringify(sent));
  assert(reader() && reader().textContent.includes('reading…'), 'the reader says it is reading: ' + (reader() && reader().textContent));

  const long = 'word '.repeat(4000);
  answer({ plugin: 'org.example.other', kind: 'memories', total: 1, memories: [{ id: 'pm_x', text: 'another plugin\'s', stored_at: '2026-10-13T12:00:00Z' }] });
  assert(items().length === 0, 'an answer for another plugin is not filed here');
  answer({ plugin: id, kind: 'memories', total: 3, next: 7, memories: [
    { id: 'pm_3', text: 'line one\nline two <b>not markup</b>', stored_at: '2026-10-13T12:00:00Z' },
    { id: 'pm_2', text: long, stored_at: '2026-10-12T09:30:00Z', replaced_by: 'pm_3', replaced_at: '2026-10-13T12:00:00Z' } ] });
  assert(items().length === 2 && reader().textContent.includes('The memories ' + id + ' stored, each whole. showing 2 of 3, newest first'), 'the count and how many are shown: ' + reader().textContent);
  assert(items()[0].querySelector('pre').textContent === 'line one\nline two <b>not markup</b>' && !items()[0].querySelector('pre b'), 'a memory is shown whole, as text');
  assert(items()[0].textContent.includes('stored 2026-10-13 12:00:00 UTC') && items()[0].textContent.includes('current') && items()[0].textContent.includes('pm_3'), 'when it was stored, and that it is current: ' + items()[0].querySelector('.store-hint').textContent);
  assert(items()[1].querySelector('pre').textContent === long, 'a long memory is not cut: ' + items()[1].querySelector('pre').textContent.length + ' of ' + long.length);
  assert(items()[1].textContent.includes('replaced 2026-10-13 12:00:00 UTC by pm_3'), 'a replaced memory says when and by which: ' + items()[1].querySelector('.store-hint').textContent);

  const more = stack.querySelector('[data-stored-more]');
  assert(more && more.textContent === 'Next memories', 'the next page is offered while there is one');
  more.click();
  assert(JSON.stringify(sent[sent.length - 1]) === JSON.stringify({ type: 'plugin_stored', stored: { plugin: id, kind: 'memories', before: 7 } }), 'the next page is asked by the cursor the host gave: ' + JSON.stringify(sent[sent.length - 1]));
  const waiting = stack.querySelector('[data-stored-more]');
  assert(waiting && waiting.textContent === 'Reading…' && waiting.disabled, 'while the next page is read its button says so and waits: ' + (waiting && waiting.outerHTML));
  answer({ plugin: id, kind: 'memories', before: 7, total: 3, memories: [{ id: 'pm_1', text: 'the oldest', stored_at: '2026-10-11T08:00:00Z', temporary: true }] });
  assert(items().length === 3 && items()[2].querySelector('pre').textContent === 'the oldest' && items()[2].textContent.includes('temporary, ends with its activation'), 'the next page is added under the first: ' + reader().textContent);
  assert(reader().textContent.includes('showing 3 of 3, newest first; that is all of them') && !stack.querySelector('[data-stored-more]'), 'the last page says it is the last: ' + reader().textContent);

  row().querySelector('[data-stored-remove]').click();
  assert(asked.length === 2 && asked[1].shown === 3, 'the removal is asked with the content on the page: ' + JSON.stringify(asked[1]));
  assert(!asked[1].q.includes('You have not opened them') && asked[1].q.includes('3 memories (1 of them replaced) and 2 keys will be deleted from it. Snapshots and exports already made still hold them.'),
    'and the question does not say they were not opened: ' + asked[1].q);
  assert(!sent.some(m => m.type === 'plugin'), 'a no removes nothing: ' + JSON.stringify(sent));
  assert(items().length === 3, 'and the content stays open');

  row().querySelector('[data-stored-read="keys:' + id + '"]').click();
  assert(JSON.stringify(sent[sent.length - 1]) === JSON.stringify({ type: 'plugin_stored', stored: { plugin: id, kind: 'keys' } }), 'the keys are asked for: ' + JSON.stringify(sent[sent.length - 1]));
  answer({ plugin: id, kind: 'keys', total: 2, keys: [
    { key: 'cursor', value: '{"at":42}', stored_at: '2026-10-13T12:00:00Z' },
    { key: 'kept', value: '', deletion_marker: true, temporary: true } ] });
  assert(reader().dataset.storedReader === 'keys' && items().length === 2 && reader().textContent.includes('showing 2 of 2, newest first; that is all of them'), 'the keys open in place of the memories: ' + reader().textContent);
  assert(items()[0].textContent.includes('cursor') && items()[0].querySelector('pre').textContent === '{"at":42}' && items()[0].textContent.includes('last written 2026-10-13 12:00:00 UTC'), 'a key and its value whole: ' + items()[0].textContent);
  assert(!items()[1].querySelector('pre') && items()[1].textContent.includes('kept') && items()[1].textContent.includes('no value'), 'a deletion marker is said as what it is: ' + items()[1].textContent);
  assert(items()[1].textContent.includes('temporary, ends with its activation') && !items()[0].textContent.includes('temporary'), 'a temporary key is said to be one: ' + items()[1].textContent);

  row().querySelector('[data-stored-read="memories:' + id + '"]').click();
  answer({ plugin: id, kind: 'memories', error: 'plugins: what org.example.notes remembered could not be read: disk I/O error' });
  assert(stack.querySelector('[data-stored-read-error]').textContent === 'plugins: what org.example.notes remembered could not be read: disk I/O error' && items().length === 0, 'a read that failed is said: ' + reader().textContent);

  // A page that ends short of the count, with no next page, says the
  // record changed while it was read; a row with no time says so.
  row().querySelector('[data-stored-read="memories:' + id + '"]').click();
  answer({ plugin: id, kind: 'memories', total: 3, memories: [{ id: 'pm_9', text: 'one of three' }] });
  assert(reader().textContent.includes('showing 1 of 3, newest first; the rest changed while this was read: read again') && !stack.querySelector('[data-stored-more]'),
    'a short last page says the rest changed: ' + reader().textContent);
  assert(items()[0].textContent.includes('stored at a time that was not recorded'), 'a memory with no time says so: ' + items()[0].textContent);

  assert(stack.querySelector('[data-stored-close]').textContent === 'Close', 'the reader closes by a button that says so');
  stack.querySelector('[data-stored-close]').click();
  assert(!reader(), 'closed, the reader is gone');
});
</script>`

func TestWhatAPluginStoredIsReadOnThePageBeforeItsRemovalIsAsked(t *testing.T) {
	runPageInEngines(t, pluginsStoredReadPage, map[string][]byte{
		"/views/settings.js": []byte(`export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection() {}
export function savebarHTML() { return ''; } export function sendConfigChanges() {} export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export const sent = []; export function send(m) { sent.push(m); return true; }`),
	})
}
