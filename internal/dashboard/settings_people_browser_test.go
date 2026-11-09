//go:build !windows

package dashboard

import "testing"

// PEOPLE AND MESSAGES, as the board draws it: contacts by contact, a contact whose
// lines differ keeping each line's own mark, a message's sender added as a contact,
// messages under tabs that stay as the reader left them, and Held mail's address
// opening the disclosure at the end.
const peopleBoardPage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { acceptMessages } from './views/messages.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';

const reads = () => frames.filter(f => f.type === 'query' && f.query === 'messages');
const sets = () => frames.filter(f => f.type === 'config_set');
const heldReads = () => frames.filter(f => f.type === 'query' && f.query === 'held_mail');
const arrival = (id, from, address) => ({ id, channel: 'signal', address, from, body: 'words of ' + id, body_chars: ('words of ' + id).length, received_ms: 5000, woke: !!from, read: false });
const state = { limit: 50, max_limit: 400, read_ms: 9000, channels: ['signal', 'mail'],
  arrivals: [arrival('a_known', 'Robin', '1'), arrival('a_stranger', '', '+1 415 555 0110')], arrivals_total: 2,
  open: [{ id: 'o1', to: 'Kim', body: 'hello', body_chars: 5, created_ms: 4000, state: 'waiting', note: 'Kim is not in your contacts' }], open_total: 1,
  delivered: [], delivered_total: 0, notices: [], notices_total: 0 };
const q = sel => document.querySelector('#settings-stack ' + sel);
const type = (el, value) => { el.value = value; el.dispatchEvent(new Event('input', { bubbles: true })); };

run(async () => {
  S.identityExists = true; S.providersLoaded = true; S.providers = []; S.view = 'settings'; S.connected = true;
  S.stats = { name: 'Unnamed', notices: { on: false, lines: [] } };
  S.config = { llm: { provider: 'p', model: 'm', resolved_provider: 'p', resolved_model: 'm' }, contacts: [
    { name: 'Robin', channel: 'signal', address: '1', wake: true, operator: false },
    { name: 'Robin', channel: 'mail', address: 's@example.test', wake: false, operator: false } ] };
  // Held mail's address is the disclosure at the end of this place, and it reads both.
  S.settingsSection('held-mail');
  assert(S.settingsAddress() === '#/settings/people', 'Held mail did not lead to People and messages: ' + S.settingsAddress());
  S.requestSettingsSection();
  renderSettings();
  assert(reads().length === 1 && heldReads().length === 1, 'the place did not read what it shows');
  assert(q('[data-unsent]').open, 'Held mail\'s address did not open Unsent messages');
  acceptMessages({ request_id: reads()[0].request_id, messages: state });

  // A name not yet given reads as a sentence.
  assert(q('[data-contacts-none]') === null && document.querySelector('#contacts-hint').textContent.includes('may wake your identity'), 'an unnamed identity is not said: ' + document.querySelector('#contacts-hint').textContent);

  // Contacts by contact: one row for one person's two lines.
  const rows = () => [...document.querySelectorAll('#settings-stack [data-contact-row]')];
  assert(rows().length === 1 && rows()[0].textContent.includes('Signal, then mail') && rows()[0].textContent.includes('may wake'), 'a person\'s lines are not one row: ' + rows().map(r => r.textContent));
  q('[data-contact-edit="0"]').click();
  const box = key => q('[data-contact-editor] input[type="checkbox"][data-contact-field="' + key + '"]');
  assert(box('wake').indeterminate && !box('wake').checked, 'lines that differ are not said to differ');
  type(q('[data-contact-line="1"] [data-contact-field="address"]'), 'sam@example.test');
  q('[data-save="contacts"]').click();
  const kept = sets().at(-1).config.contacts;
  assert(kept[0].wake === true && kept[1].wake === false && kept[1].address === 'sam@example.test', 'an edit changed a mark no one changed: ' + JSON.stringify(kept));
  S.config = { ...S.config, contacts: kept };
  acceptSettingsConfig(sets().at(-1).request_id);
  renderSettings();
  q('[data-contact-edit="0"]').click();
  box('wake').click();
  assert(!box('wake').indeterminate && box('wake').checked, 'changing the box did not settle it');
  q('[data-save="contacts"]').click();
  const both = sets().at(-1).config.contacts;
  assert(both[0].wake === true && both[1].wake === true, 'the box did not set every line: ' + JSON.stringify(both));
  S.config = { ...S.config, contacts: both };
  acceptSettingsConfig(sets().at(-1).request_id);
  renderSettings();

  // A stranger's message offers them as a contact, filled in; a contact's does not.
  assert(!q('[data-arrival="a_known"] [data-add-contact]'), 'a contact is offered as a new one');
  q('[data-arrival="a_stranger"] [data-add-contact]').click();
  for (let i = 0; i < 200 && !q('[data-contact-editor]'); i++) await new Promise(r => setTimeout(r, 25));
  const editor = q('[data-contact-editor]');
  assert(editor && editor.querySelector('[data-contact-field="channel"]').value === 'signal' && editor.querySelector('[data-contact-field="address"]').value === '+1 415 555 0110', 'Add as a contact did not fill the line');
  assert(document.activeElement === editor.querySelector('[data-contact-name]'), 'Add as a contact did not go to the name');
  assert(!q('[data-contacts-draft]').hidden && q('[data-save="contacts"]'), 'a new contact is not said to be unsaved');
  // Cancel lays the draft down, and the saved book stands.
  q('[data-contacts-cancel]').click();
  assert(!q('[data-contact-editor]') && q('[data-contacts-draft]').hidden && !q('[data-save="contacts"]') && rows().length === 1, 'Cancel did not lay the draft down');
  assert(sets().length === 2, 'Cancel sent something: ' + sets().length);

  // Tabs: one list shown, the one with something wrong marked, as left across a redraw.
  const tab = id => q('[data-messages-tabs] [data-tab="' + id + '"]');
  const shown = () => [...document.querySelectorAll('#settings-stack [data-panel]')].filter(p => !p.hidden).map(p => p.dataset.panel).join();
  assert(tab('arrived').textContent.includes('2') && tab('open').textContent.includes('1') && tab('open').hasAttribute('data-any') && !tab('sent').hasAttribute('data-any'), 'the tabs do not say their counts');
  assert(shown() === 'arrived', 'the first list is not the one shown: ' + shown());
  tab('open').click();
  assert(shown() === 'open' && q('[data-send="o1"]').textContent.includes('Kim is not in your contacts'), 'a tab did not show its list: ' + shown());
  renderSettings();
  assert(shown() === 'open' && tab('open').getAttribute('aria-selected') === 'true', 'a redraw lost the tab');
  tab('open').focus();
  tab('open').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
  assert(shown() === 'notices', 'the arrow keys do not move the tabs: ' + shown());

  // A message's words stand open across a redraw.
  tab('arrived').click();
  const read = () => q('[data-row-read="a_known"]');
  assert(read() && !read().open && read().textContent.includes('words of a_known'), 'a message\'s words are not under Read it');
  read().open = true;
  await new Promise(r => read().addEventListener('toggle', r, { once: true }));
  renderSettings();
  assert(read().open, 'a redraw closed what the reader opened');
});
</script>`

func TestPeopleAndMessagesAsTheBoardDrawsIt(t *testing.T) {
	modules := map[string][]byte{
		"/state.js": []byte(`export const S = { providers: [], config: null, identityExists: false, providersLoaded: false };`),
		"/ws.js": []byte(`let seq = 0;
export const frames = [];
export function send(frame) { frame.request_id ||= String(++seq); frames.push(structuredClone(frame)); return frame.request_id; }
export function query(name, extra) { return send(Object.assign({ type: 'query', query: name }, extra || {})); }`),
		"/app.js":      []byte(`export function toast() {}`),
		"/presence.js": []byte(`export function setThinking() {} export function toolPulse() {}`),
		"/sandbox.js":  []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`),
	}
	runPageInEngines(t, peopleBoardPage, modules)
}
