//go:build !windows

package dashboard

import "testing"

// THE NOTICE ROUTE IS A SWITCH ON SETTINGS' MESSAGES CARD (spec section
// 1c and step 4; what the top of Chat's box proved, kept). The switch is
// drawn whatever any other setting says; it saves itself and nothing else,
// holding the asked value while the host answers; a refusal is said beside
// it in the door's own words; the host's word, not this browser's storage,
// decides what is checked; the line about an active route is said, and a
// route nothing can carry says so; the lines marked as you are listed, as
// text; and a save lost with the connection is said.
const noticeRoutePage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { acceptExpertConfig, rejectExpertConfig, expertStatus, expertConnectionLost, routeLine } from './views/expert.js';
import './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  const stack = document.getElementById('settings-stack');
  const q = sel => stack.querySelector(sel);
  const shown = el => !!el && !el.hidden && !el.closest('[hidden]');
  const sets = () => frames.filter(f => f.type === 'config_set');
  const status = st => { S.stats = st; expertStatus(); };
  const quinn = (channel, carried) => ({ name: 'Quinn', channel, carried });
  const route = () => q('[data-notice-route-card] [data-notice-route-switch]');
  S.identityExists = true; S.providersLoaded = true; S.providers = []; S.view = 'settings'; S.connected = true;
  S.config = { llm: { provider: 'p', model: 'm', resolved_provider: 'p', resolved_model: 'm' }, contacts: [] };
  S.stats = { name: 'Example', notices: { on: false, lines: [] } };
  S.openSettings('messages');

  assert(route() && !route().checked && shown(route()), 'the Messages card does not hold the route\'s switch, off');
  assert(route().closest('label').textContent.trim().startsWith('Send your notices to your lines when no page is open'), 'the switch is not labelled by what it does: ' + route().closest('label').textContent);
  assert(!shown(q('[data-route-line]')), 'a route that is off is said');
  assert(q('[data-notice-lines]').textContent === 'No contact is marked as you, so nothing can carry your notices off this page.', 'no line marked as you is not said');

  // The route on and carried is said; nothing can carry it now is said, never pretended.
  status({ name: 'Example', notices: { on: true, lines: [quinn('telegram', true), quinn('sms', true)] } });
  assert(!shown(q('[data-route-line]')) && q('[data-notice-lines]').textContent === 'To your lines above, in order: Telegram, then sms. This page keeps every notice either way.',
    'an active route is not said: ' + q('[data-notice-lines]').textContent);
  assert(route().checked, 'the switch does not read the host\'s route');
  status({ name: 'Example', notices: { on: true, lines: [quinn('telegram', false)], held: 'no installed channel carries a line marked as you' } });
  assert(q('[data-route-line]').textContent.includes('nothing can carry them now: no installed channel carries a line marked as you'), 'a route nothing can carry is not said: ' + q('[data-route-line]').textContent);
  assert(routeLine({ on: true, lines: [], held: 'no contact line is marked as you' }).includes('no contact line is marked as you'), 'a route with no line is not said');

  // The switch saves itself and nothing else, holding the asked value while the host answers.
  status({ name: 'Example', notices: { on: false, lines: [quinn('telegram', true)] } });
  assert(q('[data-notice-lines]').textContent.includes('Telegram'), 'the lines marked as you are not listed');
  route().click();
  assert(sets().length === 1 && JSON.stringify(sets()[0].config) === '{"dashboard.notices_off_dashboard":true}', 'the route sent more than itself: ' + JSON.stringify(sets()));
  assert(route().checked && route().disabled, 'the switch does not hold the asked value while the host answers');
  // A refusal is said beside it, in the door's words; the route stays as the host has it.
  assert(rejectExpertConfig(sets()[0].request_id, 'dashboard.notices_off_dashboard: mark a contact line as you first (Settings → Messages → Contacts, This is me) — your notices go to the lines marked as you, and none is'), 'the refusal was not taken');
  assert(shown(q('[data-notice-said]')) && q('[data-notice-said]').textContent.includes('mark a contact line as you first'), 'the refusal is not beside the switch');
  assert(!route().checked && !route().disabled, 'a refused route shows as on');
  route().click();
  assert(acceptExpertConfig(sets()[1].request_id), 'the door\'s answer to the route was not taken');
  assert(route().checked && !route().disabled, 'an acknowledged save flickered back before its status');
  status({ name: 'Example', notices: { on: true, lines: [quinn('telegram', true)] } });
  assert(route().checked && !shown(q('[data-notice-said]')), 'a kept route does not show as on');

  // A name is text.
  status({ name: 'Example', notices: { on: true, lines: [{ name: 'Quinn', channel: '<img src=x onerror="window.__owned=1">', carried: true }] } });
  assert(!stack.querySelector('[data-notice-route-card] img') && window.__owned === undefined && q('[data-notice-lines]').textContent.includes('<img src=x'), 'a channel became markup');

  // A save lost with the connection is said; the host's word stays.
  route().click();
  expertConnectionLost();
  assert(q('[data-notice-said]').textContent.includes('Connection lost') && route().checked && !route().disabled, 'a lost save is not said, or the switch shows the unsaved value');
  assert(sets().every(f => Object.keys(f.config).join() === 'dashboard.notices_off_dashboard'), 'something but the route was saved: ' + JSON.stringify(sets()));
  let stored = 0; try { stored = localStorage.length; } catch (e) {}
  assert(stored === 0, 'the page kept the route in this browser\'s storage');
});
</script>`

func TestTheNoticeRouteIsASwitchOnTheMessagesCard(t *testing.T) {
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
	runPageInEngines(t, noticeRoutePage, modules)
}
