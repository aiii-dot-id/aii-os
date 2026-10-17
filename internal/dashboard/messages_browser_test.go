//go:build !windows

package dashboard

import "testing"

const messagesPage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { acceptMessages } from './views/messages.js';
import { renderSettings, acceptSettingsConfig, rejectSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';

const arrival = (id, from, address, body, ms, extra) => ({ id, channel: 'telegram', address, from, body, body_chars: [...body].length, received_ms: ms, ...extra });
const early = arrival('in_early', 'Robin', '111', 'yes, go ahead', 2050);
const unrelated = arrival('in_weather', 'Robin', '111', 'the weather is lovely today', 2500);
const later = arrival('in_later', 'Robin', '111', 'got it', 3050);
const state = {
  limit: 50, max_limit: 400, read_ms: 9000, channels: ['mail', 'telegram'],
  arrivals: [
    arrival('in_hostile', '', '<b>999</b>', '<img src=x onerror="window.__owned=1">', 5000, { woke: false, read: false }),
    { ...arrival('in_relayed', '', 'x', 'an event', 4500), channel: 'hook:org.example.relay', relayed: 'org.example.relay' },
    { ...later, woke: true, read: true },
    { ...arrival('in_long', 'ContactB', '222', 'x'.repeat(40), 1000), body_chars: 5000, read: true },
  ],
  arrivals_total: 60,
  open: [
    { id: 'w1', to: 'Kim', body: 'hello', body_chars: 5, state: 'waiting', note: 'Waiting: Kim is not in your contacts. It is sent once you add them.', created_ms: 1 },
    { id: 's1', to: 'Robin', body: 'on its way', body_chars: 10, state: 'sending', note: 'Handed to an adapter; its answer is not in yet.', created_ms: 2 },
    { id: 'p1', to: 'Robin', body: 'refused', body_chars: 7, state: 'parked', note: 'Not delivered after 8 attempt(s); parked, and tried no more.', answer: '<script>window.__owned=2<\/script>', created_ms: 3 },
    { id: 'u1', to: 'Robin', body: 'maybe', body_chars: 5, state: 'unknown', via: 'org.example.telegram', note: 'It may have left via org.example.telegram, and whether it arrived cannot be told.', answer: 'NET_EFFECT_UNKNOWN', created_ms: 4 },
  ],
  open_total: 4,
  delivered: [
    { id: 'd_b', to: 'Robin', body: 'second question', body_chars: 15, state: 'delivered', via: 'org.example.telegram', sent_ms: 3000, activity: { since_ms: 3000, count: 1, latest: [later] } },
    { id: 'd_a', to: 'Robin', body: 'first question', body_chars: 14, state: 'delivered', via: 'org.example.telegram', sent_ms: 2000, activity: { since_ms: 2000, count: 4, latest: [later, unrelated, early] } },
    { id: 'd_legacy', to: 'Robin', body: 'old', body_chars: 3, state: 'delivered', via: 'org.example.telegram', activity: { since_ms: 0, count: 0, latest: [], unavailable: 'When it left was not recorded, so what came in since cannot be told from what came before.' } },
    { id: 'd_quiet', to: 'ContactB', body: 'ping', body_chars: 4, state: 'delivered', via: 'org.example.telegram', sent_ms: 2400, activity: { since_ms: 2400, count: 0, latest: [] } },
  ],
  delivered_total: 4,
};

run(() => {
  const stack = document.getElementById('settings-stack');
  const q = sel => stack.querySelector(sel);
  const reads = () => frames.filter(f => f.type === 'query' && f.query === 'messages');
  const sets = () => frames.filter(f => f.type === 'config_set');
  S.identityExists = true; S.providersLoaded = true; S.providers = []; S.view = 'settings'; S.connected = true;
  S.config = { llm: { provider: 'p', model: 'm', resolved_provider: 'p', resolved_model: 'm' },
    contacts: [{ name: 'Robin', channel: 'telegram', address: '111', wake: true, operator: false },
               { name: 'Quinn', channel: 'telegram', address: '42', wake: false, operator: false }] };
  S.openSettings('messages');
  assert(reads().length === 1, 'opening the section did not read the messages: ' + JSON.stringify(frames));
  assert(acceptMessages({ request_id: reads()[0].request_id, messages: state }), 'the answer to the read was not taken');

  for (const [id, st] of [['w1', 'waiting'], ['s1', 'sending'], ['p1', 'parked'], ['u1', 'unknown']]) {
    const row = q('[data-send-state="' + st + '"] [data-send="' + id + '"]');
    assert(row, id + ' is not under ' + st);
  }
  assert(q('[data-send="w1"]').textContent.includes('Kim is not in your contacts'), 'a waiting send does not say why');
  assert(q('[data-send="u1"]').textContent.includes('NET_EFFECT_UNKNOWN'), 'an unknown effect does not show its answer');
  assert(q('[data-delivered-sends] [data-send="d_a"] [data-delivered]').textContent.includes('org.example.telegram'), 'a delivered send does not say its carrier');

  const act = id => q('[data-send="' + id + '"] [data-activity]');
  assert(act('d_a') && act('d_b'), 'two sends to one person do not both show what came since');
  assert(act('d_a').querySelector('[data-activity-item="in_later"]') && act('d_b').querySelector('[data-activity-item="in_later"]'), 'a message after both is not under both');
  assert(act('d_a').querySelector('[data-activity-item="in_early"]'), 'the reply that came before the acknowledgement is not counted');
  assert(act('d_a').querySelector('[data-activity-item="in_weather"]') && act('d_a').textContent.includes('This is activity, not an answer'), 'an unrelated message is not labelled activity');
  assert(act('d_a').textContent.includes('4 messages') && act('d_a').textContent.includes('1 earlier one is not quoted'), 'the count does not cover every message');
  assert(act('d_legacy').textContent.includes('was not recorded') && act('d_quiet').textContent.includes('Nothing from ContactB'), 'an unavailable or empty activity is not said');
  const said = q('[data-delivered-sends]').textContent + q('[data-open-sends]').textContent;
  assert(!/repl/i.test(said), 'the page called activity a reply: ' + said);

  assert(q('[data-arrival="in_later"] [data-woke]').textContent === 'Woke the identity.', 'a waking arrival is not said to have woken');
  assert(q('[data-arrival="in_hostile"] [data-woke]').textContent.includes('no turn has read it yet'), 'an unread arrival is not said');
  assert(q('[data-arrival="in_relayed"]').textContent.includes('not a person writing'), 'a relayed event is named as a person');
  assert(!stack.querySelector('img') && !stack.querySelector('script') && !stack.querySelector('[data-arrival="in_hostile"] b') && window.__owned === undefined, 'untrusted text became markup');
  assert(q('[data-arrival="in_hostile"]').textContent.includes('<img src=x') && q('[data-arrival="in_hostile"]').textContent.includes('<b>999</b>'), 'untrusted text was not shown as text');
  assert(q('[data-arrival="in_long"] [data-shortened]').textContent.includes('of 5000'), 'a shortened body is not said to be shortened');
  assert(q('[data-arrivals] [data-omitted]').textContent.includes('newest 4 of 60'), 'arrivals left out are not said');
  q('[data-messages-more]').click();
  assert(reads().length === 2 && reads()[1].messages.limit === 100, 'Show more did not ask for more: ' + JSON.stringify(reads()[1]));
  acceptMessages({ request_id: reads()[1].request_id, messages: state });

  const lineEl = i => q('[data-contact-line="' + i + '"]');
  const field = (i, k) => lineEl(i).querySelector('[data-contact-field="' + k + '"]');
  assert(field(0, 'name').value === 'Robin' && field(0, 'wake').checked && !field(1, 'operator').checked, 'the saved book is not drawn');
  field(1, 'address').value = '  4242 ';
  field(1, 'operator').checked = true;
  field(1, 'operator').dispatchEvent(new Event('input', { bubbles: true }));
  assert(!q('[data-contacts-draft]').hidden, 'an edit is not said to be unsaved');
  q('[data-contact-add]').click();
  field(2, 'name').value = 'Ada'; field(2, 'channel').value = 'Mail'; field(2, 'address').value = 'ada@example.test';
  field(2, 'name').dispatchEvent(new Event('input', { bubbles: true }));
  q('[data-contact-up="2"]').click();
  assert(field(1, 'name').value === 'Ada' && field(2, 'name').value === 'Quinn', 'Move up did not reorder the lines');
  q('[data-save="contacts"]').click();
  const sent = sets().at(-1).config.contacts;
  const want = [
    { name: 'Robin', channel: 'telegram', address: '111', wake: true, operator: false },
    { name: 'Ada', channel: 'mail', address: 'ada@example.test', wake: false, operator: false },
    { name: 'Quinn', channel: 'telegram', address: '4242', wake: false, operator: true },
  ];
  assert(JSON.stringify(sent) === JSON.stringify(want), 'the book was not sent whole and in order: ' + JSON.stringify(sent));
  assert(q('[data-save="contacts"]').disabled, 'a second save can start before the first is answered');
  S.config = { ...S.config, contacts: want };
  assert(acceptSettingsConfig(sets().at(-1).request_id), 'the door’s answer was not taken');
  renderSettings();
  assert(q('[data-said="contacts"]').textContent === 'Saved — the contacts are in force now.', 'a kept book is not said: ' + q('.savebar').textContent);
  assert(q('[data-contacts-draft]').hidden && field(2, 'operator').checked, 'the draft did not retire into the saved book');
  assert(reads().length === 3, 'the lists were not read again after the contacts changed');

  field(0, 'operator').checked = true;
  field(0, 'operator').dispatchEvent(new Event('input', { bubbles: true }));
  q('[data-save="contacts"]').click();
  assert(rejectSettingsConfig('contacts, line 3: line 1 marks Robin as you, the operator, and this line marks Quinn: the operator is one person', sets().at(-1).request_id), 'the refusal was not taken');
  assert(q('[data-said="contacts"]').textContent.includes('the operator is one person'), 'the refusal is not beside Save');
  assert(field(0, 'operator').checked && field(2, 'operator').checked && !q('[data-contacts-draft]').hidden, 'a refusal lost the edits');
  q('[data-contact-remove="0"]').click();
  assert(field(0, 'name').value === 'Ada' && !q('[data-contact-line="2"]'), 'Remove did not take the line out');
});
</script>`

func TestTheMessagesPageShowsStatesActivityAndEditsContacts(t *testing.T) {
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
	runPageInEngines(t, messagesPage, modules)
}

const noticesPage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { acceptMessages } from './views/messages.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';

const n = (id, extra) => ({ id, body: 'notice ' + id, body_chars: 8 + id.length, created_ms: 1000, attempts: 1, ...extra });
const state = {
  limit: 4, max_limit: 4, read_ms: 9000, channels: ['telegram'], arrivals: [], arrivals_total: 0, open: [], open_total: 0, delivered: [], delivered_total: 0,
  notices: [
    n('page', { route: 'page', state: 'shown', delivered_at: '2026-09-29T10:00:00Z', note: 'Sent to an open page. That is not a sign it was read.' }),
    n('maybe', { route: 'channel', state: 'unknown', via: 'org.example.telegram', sent_ms: 2000, answer: '<b>502</b>',
      note: 'It may have gone to you via org.example.telegram, and whether it arrived cannot be told. It is not sent again, and not pushed to a page as new.' }),
    n('flying', { state: 'sending', sent_ms: 3000, note: 'Being sent to you; its answer is not in yet. A page that opens now does not take it.' }),
    n('cut', { state: 'unknown', sent_ms: 4000, answer: 'the process stopped',
      note: 'The process stopped while it was being sent to you, so whether it reached you is not known. It is not sent again, and not pushed to a page as new.' }),
  ],
  notices_total: 5,
};

run(() => {
  const stack = document.getElementById('settings-stack');
  const q = sel => stack.querySelector(sel);
  S.identityExists = true; S.providersLoaded = true; S.providers = []; S.view = 'settings'; S.connected = true;
  S.config = { llm: { provider: 'p', model: 'm' }, contacts: [] };
  S.openSettings('messages');
  const read = frames.find(f => f.type === 'query' && f.query === 'messages');
  assert(acceptMessages({ request_id: read.request_id, messages: state }), 'the answer to the read was not taken');
  const page = q('[data-notices] [data-notice="page"]'), maybe = q('[data-notices] [data-notice="maybe"]');
  assert(page && page.dataset.route === 'page' && page.querySelector('[data-notice-route]').textContent.startsWith('Route: a page'), 'a notice the page took does not say so');
  assert(maybe && maybe.dataset.route === 'channel' && maybe.dataset.state === 'unknown', 'a notice off the dashboard of unknown effect is not listed as such');
  assert(maybe.querySelector('[data-notice-route]').textContent.includes('off the dashboard via org.example.telegram'), 'the route does not name its adapter');
  assert(maybe.textContent.includes('cannot be told') && maybe.querySelector('[data-answer]').textContent === '<b>502</b>' && !stack.querySelector('[data-answer] b'), 'the unknown effect or the adapter’s words are not said as text');
  for (const id of ['flying', 'cut']) {
    const row = q('[data-notices] [data-notice="' + id + '"]');
    assert(row && row.dataset.route === '' && row.querySelector('[data-notice-route]').textContent.startsWith('Route: not recorded, sent'), id + ' does not say its route is not recorded');
    assert(!row.textContent.includes('off the dashboard') && !row.textContent.includes('not taken yet'), id + ' names a route the row does not record');
  }
  assert(q('[data-notices] [data-omitted]').textContent.includes('newest 4 of 5 notices') && q('[data-notices] [data-omitted] [data-open-section="storage"]'), 'what the list leaves out is not said with the way to the rest');
});
</script>`

func TestTheMessagesPageListsYourNoticesWithTheirRoutes(t *testing.T) {
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
	runPageInEngines(t, noticesPage, modules)
}
