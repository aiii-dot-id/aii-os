//go:build !windows

package dashboard

import "testing"

// ACCESS AND DEVICES, as the board draws it, the code for another device being
// Phone and other devices' (phone_page_browser_test.go): who can open this page, saved as it is chosen, a concrete address
// standing as it is; the route and addresses under a disclosure that stays as the
// reader left it, and opens itself for a route not confirmed.
const accessBoardPage = `<!doctype html><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { sent } from '/ws.js';
import { renderSettings, acceptSettingsConfig } from '/views/settings.js';
import { assert, run } from '/__harness.js';

const q = sel => document.querySelector('#settings-stack ' + sel);
const config = (dash, over, name) => Object.assign({ llm: {}, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [],
  dashboard: Object.assign({ host: '127.0.0.1', port: 8181, tls: true, require_token: true }, dash), secrets_ok: true,
  public_name: Object.assign({ status: 'issued', name: 'ui.abc.example.test', origin: 'https://ui.abc.example.test', direct_origin: 'https://ui.abc.example.test', route_mode: 'direct', not_after: '2026-12-20T00:00:00Z', renew_at: '2026-12-01T00:00:00Z' }, name) }, over);
const heads = () => [...document.querySelectorAll('#settings-stack [data-access] h3')].map(h => h.textContent);
const sets = () => sent.filter(m => m.type === 'config_set');
const who = v => q('[data-who="' + v + '"]');

run(async () => {
  S.view = 'settings'; S.providersLoaded = true; S.providers = []; S.connected = true; S.stats = { name: 'Vega' };
  S.config = config();
  S.settingsSection('dashboard');
  renderSettings();

  // The board's order, and the technical controls in the disclosure, closed.
  assert(heads().join() === 'Who can open this page,Address for other devices', 'the place is not in the board\'s order: ' + heads());
  const more = () => q('[data-route-more]');
  assert(!more().open && more().contains(q('#cfg-route')) && more().contains(q('#cfg-dhost')), 'the route and addresses are not in their disclosure');
  assert(q('[data-public-name-state]').textContent.includes('its certificate renews around') && q('[data-public-name-state] b').textContent === 'https://ui.abc.example.test', 'the address is not said');

  // The disclosure stays as the reader left it.
  more().open = true;
  await new Promise(r => more().addEventListener('toggle', r, { once: true }));
  renderSettings();
  assert(more().open, 'a redraw closed the disclosure');
  more().open = false;
  await new Promise(r => more().addEventListener('toggle', r, { once: true }));
  renderSettings();
  assert(!more().open, 'a redraw opened the disclosure');
  // A route not confirmed opens it, since the reason is in it.
  S.config = config({}, {}, { route_error: 'the service asked the host to wait' });
  renderSettings();
  assert(more().open && q('[data-route-error]').textContent.includes('asked the host to wait'), 'a route not confirmed stayed closed');
  S.config = config();
  renderSettings();

  // Who can open this page: saved as it is chosen, with its answer beside it.
  assert(who('here').checked && !who('devices').checked && !q('[data-who-exposes]'), 'this computer only is not what a loopback address reads as');
  who('devices').click();
  assert(sets().length === 1 && JSON.stringify(sets().at(-1).config) === '{"dashboard.host":"0.0.0.0"}', 'leaving this computer did not ask for every address: ' + JSON.stringify(sets()));
  S.config = config({ host: '0.0.0.0' });
  assert(acceptSettingsConfig(sets().at(-1).request_id), 'the answer was not taken');
  renderSettings();
  assert(q('[data-who-said]').textContent.includes('Takes effect when AII OS restarts'), 'the save is not said beside the choice: ' + q('[data-who-said]').textContent);
  assert(who('devices').checked && q('[data-who-exposes]'), 'what it exposes is not said');
  // A concrete address stands as it is: choosing what it already is sends nothing.
  S.config = config({ host: '10.8.0.2' });
  renderSettings();
  const before = sets().length;
  assert(who('devices').checked, 'a VPN address did not read as other devices');
  who('devices').dispatchEvent(new Event('change', { bubbles: true }));
  assert(sets().length === before, 'choosing what is already so sent a change');
  who('here').click();
  assert(JSON.stringify(sets().at(-1).config) === '{"dashboard.host":"127.0.0.1"}', 'back to this computer did not say so: ' + JSON.stringify(sets().at(-1)));
  S.config = config();
  acceptSettingsConfig(sets().at(-1).request_id);
  renderSettings();

  // The code for another device and signing devices out are not drawn here.
  assert(!q('[data-open-elsewhere]') && !q('[data-token-rotate]') && !q('[data-local-code]'), 'the Access card still draws a code for another device, or a rotation');
});
</script>`

func TestAccessAndDevicesAsTheBoardDrawsIt(t *testing.T) {
	modules := map[string][]byte{
		"/state.js":   []byte(`export const S = { providers: [], config: null, providersLoaded: false };`),
		"/ws.js":      []byte(`export const sent = []; let n = 0; export function send(m) { m.request_id = 'r' + (++n); sent.push(m); return m.request_id; } export function query() { return 'req-1'; }`),
		"/sandbox.js": []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`),
	}
	runPageInEngines(t, accessBoardPage, modules)
}

// AT A PHONE'S WIDTH BOTH PLACES FIT, whatever long words they hold: nothing moves the
// page sideways, and a press is a thumb's size.
const peopleAccessPhonePage = `<!doctype html><link rel="stylesheet" href="/tokens/tokens.css"><link rel="stylesheet" href="/layout.css"><link rel="stylesheet" href="/views/settings/access.css"><link rel="stylesheet" href="/views/settings/people.css"><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { renderSettings } from '/views/settings.js';
import { requestHeldMail, acceptMailRepair } from '/views/mail-repair.js';
import { assert, run } from '/__harness.js';

const long = 'https://a-very-long-name.of.another.device.example.test:8443/with/a/long/path';
run(async () => {
  S.view = 'settings'; S.providersLoaded = true; S.providers = []; S.connected = true; S.stats = { name: 'Vega', notices: { on: false, lines: [{ name: 'Robin', channel: 'org.example.a-long-channel-name', carried: true }] } };
  S.config = { llm: {}, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], secrets_ok: true,
    dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: true },
    contacts: [{ name: 'A very long name of a person who has one', channel: 'org.example.a-long-channel-name', address: 'someone.with.a.long.address@example.test', wake: true, operator: true }],
    public_name: { status: 'issued', name: long, origin: long, direct_origin: long, route_mode: 'relay', relay: 'relay.example.test', relay_connected: true, renew_at: '2026-12-01T00:00:00Z', machine_addresses: ['2001:db8:85a3:8d3:1319:8a2e:370:7348'] } };
  // The sheets this page links, loaded before anything is measured.
  const sheets = () => [...document.querySelectorAll('link[rel="stylesheet"]')];
  for (const section of ['dashboard', 'messages']) {
    S.settingsSection(section);
    renderSettings();
    await Promise.all(sheets().map(l => l.sheet ? 0 : new Promise(r => { l.addEventListener('load', r, { once: true }); l.addEventListener('error', r, { once: true }); })));
    // The editor and an unsent message's route hold People's own controls.
    if (section === 'messages') {
      requestHeldMail();
      acceptMailRepair({ request_id: 'r1', mail_repair: { messages: [{ recipient: 'the printer', reason: 'no channel', preview: 'words' }], routes: [{ name: 'Robin', channel: 'signal' }] } });
      document.querySelector('#settings-stack [data-contact-edit]').click();
    }
    document.querySelectorAll('#settings-stack details').forEach(d => { d.open = true; });
    const root = document.documentElement;
    assert(root.scrollWidth <= root.clientWidth + 1, section + ' moves the page sideways: ' + root.scrollWidth + ' in ' + root.clientWidth);
    const small = [...document.querySelectorAll('#settings-stack .settings-access__radio, #settings-stack .settings-access__check, #settings-stack .settings-access__input, #settings-stack .settings-access__select, #settings-stack .settings-people__switch, #settings-stack .settings-people__input, #settings-stack .settings-people__select, #settings-stack .settings-people__check, #settings-stack [data-who]')].filter(el => { const h = el.getBoundingClientRect().height; return !el.matches('[data-who]') && h > 0 && h < 44; });
    if (section === 'messages') assert(document.querySelector('#settings-stack .settings-people__select') && document.querySelector('#settings-stack .settings-people__input') && document.querySelector('#settings-stack .settings-people__check'), "People's own controls are not drawn to be measured");
    assert(!small.length, section + ': a control too small to press: ' + small.map(e => Math.round(e.getBoundingClientRect().height) + ' ' + e.textContent.slice(0, 30) + ' css=' + getComputedStyle(e).minHeight + ' display=' + getComputedStyle(e).display));
  }
});
</script>`

func TestPeopleAndAccessFitAPhone(t *testing.T) {
	modules := map[string][]byte{
		"/state.js":   []byte(`export const S = { providers: [], config: null, providersLoaded: false };`),
		"/ws.js":      []byte(`export function send() { return 'r1'; } export function query() { return 'req-1'; }`),
		"/sandbox.js": []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`),
	}
	css, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatal(err)
	}
	modules["/layout.css"] = css
	runPageInEnginesAtSize(t, peopleAccessPhonePage, modules, 390, 844)
}
