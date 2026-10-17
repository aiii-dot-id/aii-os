//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestSettingsOffersRotationAndTheRouteOnlyOverAPrivateConnection(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { sent } from '/ws.js';
import { renderSettings, acceptDashboardToken, acceptSettingsConfig, rejectSettingsConfig } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  const tick = () => new Promise(resolve => setTimeout(resolve, 0));
  const config = (over) => Object.assign({ llm: {}, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [],
    dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: true }, secrets_ok: true,
    public_name: { status: 'issued', name: 'ui.abc.id.example.test', origin: 'https://ui.abc.id.example.test', direct_origin: 'https://ui.abc.id.example.test', route_mode: 'direct',
      moves_with: ['sign-in profile mail', 'webhooks of plug.example'],
      relays: [{ name: 'relay.example.test', port: 443, origin: 'https://ui.abc.id.example.test' }, { name: 'far.example.test', port: 9443, origin: 'https://ui.abc.id.example.test:9443' }] } }, over || {});
  S.view = 'settings'; S.providersLoaded = true; S.providers = []; S.connected = true;
  let answer = { ok: true, text: '' };
  const rotations = [];
  S.rotateAccessToken = async () => { rotations.push(1); await tick(); return answer; };
  let confirmed = true;
  window.confirm = () => confirmed;

  S.config = config();
  renderSettings();
  document.querySelector('[data-sec="dashboard"]').click();
  const rotate = () => document.querySelector('[data-token-rotate]');
  assert(rotate() && !document.querySelector('[data-token-rotate-private]'), 'a private connection was not offered the rotation');

  document.querySelector('[data-token-reveal]').click();
  assert(acceptDashboardToken('req-1', 'old-token'), 'the eye was not answered');
  assert(document.getElementById('cfg-dtoken').value === 'old-token', 'the eye did not show the token');

  confirmed = false;
  rotate().click();
  await tick(); await tick();
  assert(rotations.length === 0, 'the rotation ran although the person said no');
  confirmed = true;
  rotate().click();
  assert(rotate().disabled, 'the control accepted a second press while rotating');
  await tick(); await tick(); await tick();
  assert(rotations.length === 1, 'the control did not ask the server once: ' + rotations.length);
  const said = document.querySelector('[data-token-rotate-said]');
  assert(said && said.textContent.includes('Rotated') && said.textContent.includes('every other browser'), 'the rotation was not said: ' + (said && said.textContent));
  assert(document.getElementById('cfg-dtoken').value === '', 'the old token is still shown after the rotation');

  answer = { ok: false, text: 'This connection is not private, so the access token was not rotated' };
  rotate().click();
  await tick(); await tick(); await tick();
  const refused = document.querySelector('[data-token-rotate-said]');
  assert(refused && refused.className.includes('bad') && refused.textContent.includes('not private'), 'a refusal was not said beside the control');

  const route = document.getElementById('cfg-route');
  assert(route && route.options.length === 3, 'the route was not offered with the relays advertised');
  const moveSaid = () => document.querySelector('[data-route-move]');
  assert(moveSaid() && moveSaid().hidden, 'the route in force was said to change something');
  route.value = 'relay:far.example.test:9443';
  route.dispatchEvent(new Event('change', { bubbles: true }));
  const moving = moveSaid().textContent;
  assert(!moveSaid().hidden && moving.includes('from https://ui.abc.id.example.test to https://ui.abc.id.example.test:9443') && moving.includes('stops answering') &&
    moving.includes('signs in again') && moving.includes('sign-in profile mail, webhooks of plug.example') && moving.includes('at home too'),
    'the move was not said before it was chosen: ' + moving);
  route.value = 'relay:relay.example.test:443';
  route.dispatchEvent(new Event('change', { bubbles: true }));
  assert(!moveSaid().hidden && !moveSaid().textContent.includes('moves the address') && moveSaid().textContent.includes('at home too'),
    'a relay on the same port was said to move the address, or not said to carry home use: ' + moveSaid().textContent);
  document.querySelector('[data-save="route"]').click();
  const set = sent[sent.length - 1];
  assert(set && set.type === 'config_set' && set.config['certificate.route_mode'] === 'relay' && set.config['certificate.relay_endpoint'] === 'relay.example.test:443',
    'the route did not go out through the door keys, its port named: ' + JSON.stringify(set));
  S.config = config({ public_name: Object.assign(config().public_name, { route_mode: 'relay', relay_endpoint: 'relay.example.test:443' }) });
  assert(acceptSettingsConfig(set.request_id), 'the answer was not claimed');
  renderSettings();
  const ok = document.querySelector('[data-said="route"]');
  assert(ok && ok.className.includes('good'), 'the saved route was not confirmed: ' + (ok && ok.textContent));
  assert(document.querySelector('[data-route-now]').textContent.includes('relay.example.test'), 'the card does not say the route in force');

  S.config = config({ dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: false } });
  renderSettings();
  const relayOption = [...document.getElementById('cfg-route').options].find(o => o.value.startsWith('relay:'));
  assert(relayOption && relayOption.disabled, 'a relay was offered to a dashboard that asks for no token');
  assert(document.querySelector('[data-route-note]').textContent.includes('access token'), 'the reason a relay is not offered was not said');
  document.querySelector('[data-save="route"]').click();
  const direct = sent[sent.length - 1];
  assert(rejectSettingsConfig('certificate.route_mode: a relay carries the public internet to this dashboard', direct.request_id), 'the refusal was not claimed');
  const bad = document.querySelector('[data-said="route"]');
  assert(bad && bad.className.includes('bad') && bad.textContent.includes('public internet'), 'the refusal was not said beside the control');

  S.config = config({ public_name: Object.assign(config().public_name, { machine_addresses: ['10.8.0.2', '192.168.1.5'] }) });
  renderSettings();
  assert(!document.querySelector('[data-route-addresses]'), 'the direct addresses were shown to an operator who has not asked for the expert controls');
  S.stats = { expert: true };
  renderSettings();
  const addrRadio = v => document.querySelector('input[name="cfg-route-addr"][value="' + v + '"]');
  const addrBox = v => document.querySelector('[data-route-addr][value="' + v + '"]');
  assert(addrRadio('auto') && addrRadio('auto').checked && addrBox('10.8.0.2') && addrBox('192.168.1.5') && !addrBox('10.8.0.2').checked, 'the direct addresses were not offered as worked out, with the machine\'s own to pick');
  document.querySelector('[data-save="route"]').click();
  assert(!('certificate.direct_addresses' in sent[sent.length - 1].config), 'unchanged addresses were sent: ' + JSON.stringify(sent[sent.length - 1]));
  rejectSettingsConfig('fixture', sent[sent.length - 1].request_id);
  renderSettings();
  addrRadio('list').checked = true;
  document.querySelector('[data-save="route"]').click();
  assert(!('certificate.direct_addresses' in sent[sent.length - 1].config), 'nothing ticked was sent as an empty list, which withdraws the route: ' + JSON.stringify(sent[sent.length - 1]));
  rejectSettingsConfig('fixture', sent[sent.length - 1].request_id);
  renderSettings();
  addrRadio('list').checked = true;
  addrBox('10.8.0.2').checked = true;
  document.querySelector('[data-save="route"]').click();
  const addrSet = sent[sent.length - 1];
  assert(JSON.stringify(addrSet.config['certificate.direct_addresses']) === '["10.8.0.2"]', 'the ticked address did not go out through the door: ' + JSON.stringify(addrSet));
  S.config = config({ public_name: Object.assign(config().public_name, { machine_addresses: ['10.8.0.2', '192.168.1.5'], direct_addresses: ['10.8.0.2'] }) });
  assert(acceptSettingsConfig(addrSet.request_id), 'the answer was not claimed');
  renderSettings();
  const savedAddr = document.querySelector('[data-said="route"]');
  assert(savedAddr && savedAddr.className.includes('good'), 'the saved addresses were not confirmed by readback: ' + (savedAddr && savedAddr.textContent));
  S.config = config({ public_name: Object.assign(config().public_name, { machine_addresses: ['10.8.0.2', '192.168.1.5'], direct_addresses: ['10.8.0.2', '203.0.113.9'] }) });
  renderSettings();
  assert(addrRadio('list').checked && addrBox('10.8.0.2').checked && addrBox('203.0.113.9') && addrBox('203.0.113.9').checked &&
    addrBox('203.0.113.9').parentElement.textContent.includes('not an address this machine reports'), 'a named address the machine does not report was dropped or not marked');
  addrRadio('auto').checked = true;
  document.querySelector('[data-save="route"]').click();
  assert(sent[sent.length - 1].config['certificate.direct_addresses'] === null, 'working them out again did not go out as null: ' + JSON.stringify(sent[sent.length - 1]));
  rejectSettingsConfig('fixture', sent[sent.length - 1].request_id);
  S.stats = {};

  S.config = config({ dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: true, token_short: true } });
  renderSettings();
  const offered = [...document.getElementById('cfg-route').options].filter(o => o.value.startsWith('relay:'));
  assert(offered.length === 2 && offered.every(o => o.disabled), 'a relay was offered behind a short token');
  assert(document.querySelector('[data-route-note]').textContent.includes('short') && document.querySelector('[data-route-note]').textContent.includes('Rotate access token'), 'the short token was not said with the way out');
  assert(!document.querySelector('[data-route-token-short]'), 'a dashboard with no relay was warned about one');
  S.config = config({ dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: true, token_short: true },
    public_name: Object.assign(config().public_name, { route_mode: 'relay', relay_endpoint: 'relay.example.test:443' }) });
  renderSettings();
  const kept = [...document.getElementById('cfg-route').options].find(o => o.value === 'relay:relay.example.test:443');
  assert(kept && kept.selected && !kept.disabled, 'the relay already chosen was taken away behind a short token');
  assert(document.querySelector('[data-route-token-short]') && document.querySelector('[data-route-token-short]').textContent.includes('Rotate access token'), 'a relay behind a short token was not warned about');

  S.config = config({ public_name: Object.assign(config().public_name, { relays: undefined }) });
  renderSettings();
  assert(document.querySelector('[data-route-note]').textContent.includes('has not been asked'), 'an unknown relay list read as none');
  const asks = sent.filter(m => m.type === 'public_name_look').length;
  document.querySelector('[data-public-name="look"]').click();
  assert(sent.filter(m => m.type === 'public_name_look').length === asks + 1, 'the route control did not ask the service which relays it offers');
  S.config = config({ public_name: Object.assign(config().public_name, { relays: [] }) });
  renderSettings();
  assert(document.querySelector('[data-route-note]').textContent.includes('advertises no relay'), 'an empty relay list was not said');

  S.config = config({ secrets_ok: false });
  renderSettings();
  assert(!rotate() && document.querySelector('[data-token-rotate-private]'), 'the rotation was offered off a private connection');
  assert(!document.getElementById('cfg-route') && document.querySelector('[data-route-private]'), 'the route was offered off a private connection');
  assert(document.querySelector('[data-route-now]'), 'the route in force was not shown off a private connection');

  S.config = config({ dashboard: { host: '127.0.0.1', port: 8181, tls: false, require_token: false } });
  renderSettings();
  assert(!rotate() && !document.getElementById('cfg-dtoken'), 'an open dashboard was offered a token to rotate');
});
</script></body></html>`

	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/ws.js"] = []byte(`export const sent = []; let n = 0; export function send(m) { m.request_id = 'r' + (++n); sent.push(m); return m.request_id; } export function query() { return 'req-1'; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}

func TestTheRotatingPageWaitsForItsNewCookieInsteadOfAskingForAToken(t *testing.T) {
	runTokenPromptProbe(t, `
import { report } from '/__harness.js';
import { S } from '/state.js';
try {
  const assert = (ok, why) => { if (!ok) throw Error(why); };
  const tick = ms => new Promise(resolve => setTimeout(resolve, ms));
  let cookie = 'old', rotatedOnServer = false, answer, statusChecks = 0;
  const nativeFetch = window.fetch.bind(window);
  window.fetch = async (url, options = {}) => {
    if (url === '/auth/rotate') return new Promise(resolve => { answer = resolve; });
    if (url !== '/auth/token') return nativeFetch(url, options);
    statusChecks++;
    const ok = cookie === 'new' || !rotatedOnServer;
    return { status: ok ? 204 : 401, ok };
  };
  window.prompt = () => { throw Error('modal prompt used'); };
  const sockets = [];
  window.WebSocket = class {
    constructor() { this.readyState = 0; sockets.push(this); }
    send() {}
    open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
    fail() { this.readyState = 3; if (this.onclose) this.onclose({}); }
  };
  const { connect } = await import('/ws.js');
  connect();
  sockets[0].open();
  assert(typeof S.rotateAccessToken === 'function', 'ws.js does not offer the rotation to the page');
  const rotation = S.rotateAccessToken();
  rotatedOnServer = true;
  sockets[0].fail();
  await tick(60);
  assert(!document.getElementById('dashboard-signin'), 'the rotating page was asked for a token it is about to hold');
  assert(statusChecks === 0, 'recovery asked the server about the old cookie mid-rotation');
  cookie = 'new';
  answer({ ok: true, status: 204, text: async () => '' });
  const result = await rotation;
  assert(result.ok, 'the rotation was not reported done');
  await tick(20);
  const last = sockets[sockets.length - 1];
  assert(sockets.length >= 2 && last.readyState === 0, 'the page did not reconnect after the rotation: ' + sockets.length);
  last.open();
  assert(!document.getElementById('dashboard-signin'), 'a sign-in form was left behind');
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`)
}

func TestSettingsOffersAskingForATokenOnlyWhileNoneIsAskedAndPrivate(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { renderSettings } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  const tick = () => new Promise(resolve => setTimeout(resolve, 0));
  const config = (over) => Object.assign({ llm: {}, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [],
    dashboard: { host: '127.0.0.1', port: 8181, tls: false, require_token: false }, secrets_ok: true,
    public_name: { status: 'issued', name: 'ui.abc.id.example.test', origin: 'https://ui.abc.id.example.test', route_mode: 'direct',
      relays: [{ name: 'relay.example.test', port: 443 }] } }, over || {});
  S.view = 'settings'; S.providersLoaded = true; S.providers = [];
  let answer = { ok: true, text: '' };
  const asked = [];
  S.requireAccessToken = async () => { asked.push(1); await tick(); return answer; };
  S.rotateAccessToken = async () => { throw new Error('rotated instead of asked'); };
  let confirmed = true, question = '';
  window.confirm = q => { question = q; return confirmed; };
  const require = () => document.querySelector('[data-token-require]');

  S.config = config();
  renderSettings();
  document.querySelector('[data-sec="dashboard"]').click();
  assert(require() && !document.querySelector('[data-token-rotate]') && !document.getElementById('cfg-dtoken'), 'an open dashboard over a private connection was not offered the requirement alone');
  assert(document.querySelector('[data-token-off-where]').textContent.includes('config.json'), 'the page does not say where asking stops');
  const note = document.querySelector('[data-route-note]').textContent;
  assert(note.includes('Ask for an access token') && !note.includes('aii dashboard-token'), 'the relay note does not send the person to this control: ' + note);

  confirmed = false;
  require().click();
  await tick(); await tick();
  assert(asked.length === 0, 'the requirement was turned on although the person said no');
  assert(question.includes('every other open page is cut off and asks for the token') && question.includes('config.json'), 'the person was not told what asking does: ' + question);
  confirmed = true;
  require().click();
  assert(require().disabled, 'the control accepted a second press');
  await tick(); await tick(); await tick();
  assert(asked.length === 1, 'the control did not ask the server once: ' + asked.length);
  const said = document.querySelector('[data-token-require-said]');
  assert(said && said.className.includes('good') && said.textContent.includes('Asked for'), 'the act was not said: ' + (said && said.textContent));
  assert(!require() && document.getElementById('cfg-dtoken') && document.querySelector('[data-token-rotate]'), 'after the act the page does not show the token it now asks for');
  assert(document.querySelector('[data-token-off-where]'), 'the page stopped saying where asking stops');

  answer = { ok: false, text: 'This connection is not private, so the access token was not turned on' };
  S.config = config();
  renderSettings();
  require().click();
  await tick(); await tick(); await tick();
  const refused = document.querySelector('[data-token-require-said]');
  assert(refused && refused.className.includes('bad') && refused.textContent.includes('not private') && require() && !document.getElementById('cfg-dtoken'), 'a refusal was not said, or the page showed a token it does not ask for');

  S.config = config({ secrets_ok: false });
  renderSettings();
  assert(!require() && document.querySelector('[data-token-require-private]'), 'the requirement was offered off a private connection');

  S.config = config({ dashboard: { host: '127.0.0.1', port: 8181, tls: false, require_token: true } });
  renderSettings();
  assert(!require() && document.querySelector('[data-token-rotate]'), 'the requirement was offered while a token is already asked for');
});
</script></body></html>`

	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/ws.js"] = []byte(`export function send() { return 'r1'; } export function query() { return 'req-1'; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}

func TestAPageOpenBeforeATokenWasAskedForAsksForItAfterward(t *testing.T) {
	runTokenPromptProbe(t, `
import { report } from '/__harness.js';
import { S } from '/state.js';
try {
  const assert = (ok, why) => { if (!ok) throw Error(why); };
  const tick = ms => new Promise(resolve => setTimeout(resolve, ms));
  let asked = false, mine = false, answer, checks = 0;
  const nativeFetch = window.fetch.bind(window);
  window.fetch = async (url, options = {}) => {
    if (url === '/auth/require') return new Promise(resolve => { answer = resolve; });
    if (url !== '/auth/token') return nativeFetch(url, options);
    checks++;
    const ok = !asked || mine;
    return { status: ok ? 204 : 401, ok };
  };
  window.prompt = () => { throw Error('modal prompt used'); };
  const sockets = [];
  window.WebSocket = class {
    constructor() { this.readyState = 0; sockets.push(this); }
    send() {}
    open() { this.readyState = 1; if (this.onopen) this.onopen({}); }
    fail() { this.readyState = 3; if (this.onclose) this.onclose({}); }
  };
  const { connect } = await import('/ws.js');
  connect();
  sockets[0].open();

  assert(typeof S.requireAccessToken === 'function', 'ws.js does not offer the requirement to the page');
  const act = S.requireAccessToken();
  asked = true;
  sockets[0].fail();
  await tick(60);
  assert(!document.getElementById('dashboard-signin') && checks === 0, 'the asking page was asked for a token it is about to hold');
  mine = true;
  answer({ ok: true, status: 204, text: async () => '' });
  assert((await act).ok, 'the act was not reported done');
  await tick(20);
  const last = sockets[sockets.length - 1];
  assert(sockets.length >= 2 && last.readyState === 0, 'the asking page did not reconnect');
  last.open();
  assert(!document.getElementById('dashboard-signin'), 'the asking page was left with a sign-in form');

  mine = false;
  last.fail();
  await tick(60);
  assert(checks >= 1, 'the page did not ask the server why its socket closed');
  assert(document.getElementById('dashboard-signin'), 'a page loaded before the requirement never asked for the token');
  report('OK');
} catch (e) { report('FAIL: ' + ((e && e.message) || String(e))); }
`)
}

func TestOnePressOpensTheDashboardOnAnotherDevice(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { sent } from '/ws.js';
import { renderSettings, acceptSettingsConfig, acceptAccessCode, rejectAccessCode } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  const tick = () => new Promise(resolve => setTimeout(resolve, 0));
  const name = 'ui.abc.id.example.test';
  const relays = [{ name: 'first.example.test', port: 443, origin: 'https://' + name }, { name: 'relay.example.test', port: 443, origin: 'https://' + name, suggested: true }];
  const cfg = (dash, pn) => ({ llm: {}, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], secrets_ok: true,
    dashboard: Object.assign({ host: '127.0.0.1', port: 8181, tls: false, require_token: false, access_code_seconds: 120 }, dash), public_name: pn });
  const last = type => sent.filter(m => m.type === type).pop();
  const act = which => document.querySelector('[data-open-elsewhere-act="' + which + '"]');
  const said = () => (document.querySelector('[data-open-said]') || { textContent: '' }).textContent;
  S.view = 'settings'; S.providersLoaded = true; S.providers = [];
  let required = 0;
  S.requireAccessToken = async () => { required++; await tick(); return { ok: true, text: '' }; };

  S.config = cfg({}, { status: 'none', tls: false, can_claim: true });
  renderSettings();
  document.querySelector('[data-sec="dashboard"]').click();
  assert(act('start') && act('start').textContent === 'Open on another device', 'the one button is not offered to an ordinary install');
  const claims = () => (document.querySelector('[data-open-claims]') || { textContent: '' }).textContent;
  assert(/claims one for it/.test(claims()) && /its public key is sent to the name service\b/.test(claims()), 'an identity with no name is not told, before the press, that the press claims one and sends its public key: ' + claims());
  const does = () => (document.querySelector('[data-open-does]') || { textContent: '' }).textContent;
  const relaySees = 'can see that you connected, from which address, when and how much';
  const saysAllItDoes = text => /turns on the access token if it is off/.test(text) && /certificate made for the name \(certificates are listed in public logs\)/.test(text) &&
    /makes a one-time access code/.test(text) && /asks the name service which relays it offers, saves one as the route and connects through it/.test(text) && text.includes(relaySees);
  assert(/^The same press /.test(does()) && saysAllItDoes(does()), 'before the press, the card does not say everything the press does: ' + does());
  S.config = cfg({ settings: [{ key: 'certificate.server_url', value: 'https://names.example.test', set: true }] }, { status: 'none', tls: false, can_claim: true });
  renderSettings();
  assert(/its public key is sent to the name service at https:\/\/names\.example\.test,/.test(claims()), 'the name service in force is not named beside the button: ' + claims());
  assert(/public key to the name service at https:\/\/names\.example\.test\./.test(document.querySelector('[data-public-name-state]').textContent), 'the Claim button\'s card does not say where the public key goes');
  assert(!sent.some(m => m.type === 'public_name_claim' || m.type === 'public_name_look'), 'the page asked the host for a name or a look before any press: ' + sent.map(m => m.type));
  S.config = cfg({}, { status: 'prepared', name: name, tls: false });
  renderSettings();
  assert(claims() === '', 'an identity that holds a name is told the press claims one: ' + claims());
  assert(/^Pressing this /.test(does()) && saysAllItDoes(does()), 'an identity whose name is prepared is not told, before the press, everything the press does: ' + does());
  S.config = cfg({ settings: [{ key: 'certificate.server_url', value: 'none: the certificate service is declined', set: true }] }, { status: 'none', tls: false, can_claim: false, service_declined: true });
  renderSettings();
  const declined = /The certificate service is declined in this identity.s configuration, so no name can be claimed\./;
  const claimCard = () => document.querySelector('[data-public-name-state]').textContent;
  assert(declined.test(claims()) && !/public key|is sent|Pressing this claims/.test(claims()), 'beside a declined service the press is said to send a key, or not said to claim nothing: ' + claims());
  assert(declined.test(claimCard()) && !/public key|sends/.test(claimCard()), 'beside a declined service the Claim card says a key is sent, or not that no name can be claimed: ' + claimCard());
  assert(does() === '', 'beside a declined service the press is said to do what it will not: ' + does());
  const claimPress = document.querySelector('[data-public-name="claim"]');
  assert(claimPress && claimPress.disabled && act('start') && act('start').disabled, 'a press that would claim is offered though the service is declined');
  const quiet = () => sent.filter(m => ['public_name_claim', 'public_name_look', 'config_set', 'access_code'].includes(m.type)).length;
  const quietBefore = quiet();
  claimPress.disabled = false; claimPress.click();
  act('start').disabled = false; act('start').click();
  await tick(); await tick(); await tick();
  assert(quiet() === quietBefore && required === 0, 'a press beside a declined service sent ' + sent.slice(-3).map(m => m.type) + ' or turned the token on (' + required + ')');
  assert(said() === '' && !act('done'), 'the Open press beside a declined service started its flow: ' + said());
  S.config = cfg({}, { status: 'none', tls: false, can_claim: true });
  renderSettings();
  act('start').click();
  await tick(); await tick(); await tick();
  assert(required === 1, 'the press did not turn the access token on: ' + required);
  assert(last('public_name_claim'), 'the press did not claim the name: ' + sent.map(m => m.type));
  assert(act('start').disabled, 'a second press was offered while the first is working');

  S.config = cfg({ require_token: true }, { status: 'claiming', name: name });
  renderSettings();
  assert(/certificate/.test(said()), 'the wait for the certificate was not said: ' + said());
  S.config = cfg({ require_token: true }, { status: 'issued', name: name, origin: 'https://' + name + ':8181', direct_origin: 'https://' + name + ':8181', route_mode: 'direct', relays: relays });
  renderSettings();
  const route = last('config_set');
  assert(route && route.config['certificate.route_mode'] === 'relay' && route.config['certificate.relay_endpoint'] === 'relay.example.test:443',
    'the press did not choose the relay the host marked, with its port named: ' + JSON.stringify(route));
  assert(document.querySelector('[data-route-expert]').hidden, 'the route controls were shown to an operator who did not ask for them');

  const carried = { status: 'issued', name: name, origin: 'https://' + name, direct_origin: 'https://' + name + ':8181', route_mode: 'relay', relay_endpoint: 'relay.example.test:443', relay: 'relay.example.test:443', relay_connected: true, relays: relays };
  S.config = cfg({ require_token: true }, carried);
  assert(acceptSettingsConfig(route.request_id), 'the route answer was not claimed');
  renderSettings();
  const ask = last('access_code');
  assert(ask && ask.access_code === '', 'a generated code was not asked for: ' + JSON.stringify(ask));
  assert(acceptAccessCode(ask.request_id, { id: 'c1', code: 'ABCD-EFGH-JKMN', link: 'https://' + name + '/auth/link#ABCDEFGHJKMN', qr: '<svg data-fixture="1"></svg>', seconds: 120 }), 'the code was not claimed');
  const shown = () => document.querySelector('[data-open-code]');
  assert(shown() && shown().textContent.includes('ABCD-EFGH-JKMN') && shown().querySelector('svg[data-fixture]') && shown().textContent.includes('2 minutes') && shown().textContent.includes('https://' + name),
    'the code was not shown as a picture, as text, with its life and the address to type it at: ' + (shown() && shown().textContent));
  const panel = () => document.querySelector('[data-open-elsewhere]').textContent;
  assert(panel().includes('cannot read any of it') && panel().includes('from which address') && panel().includes('moves off the relay') && panel().includes('Rotate access token'), 'what the relay sees and what a lost device means were not said: ' + panel());
  assert(panel().includes(relaySees), 'what the relay sees is not said in the words said before the press: ' + panel());

  document.getElementById('cfg-open-own').value = 'my own sign-in phrase';
  act('own').click();
  assert(last('access_code_cancel') && last('access_code_cancel').access_code === 'c1', 'the replaced code was not ended by its id');
  const own = last('access_code');
  assert(own.access_code === 'my own sign-in phrase', 'their own code was not sent: ' + JSON.stringify(own));
  assert(rejectAccessCode('your own access code is 9 characters long and must be at least 12', own.request_id), 'the refusal was not claimed');
  assert(said().includes('at least 12') && document.querySelector('[data-open-said]').className.includes('bad') && !act('start').disabled, 'the refusal was not said beside the button: ' + said());
  act('start').click();
  const again = last('access_code');
  assert(acceptAccessCode(again.request_id, { id: 'c2', code: 'my own sign-in phrase', own: true, seconds: 90 }), 'the second code was not claimed');
  assert(shown().textContent.includes('as strong as what you typed') && shown().textContent.includes('90 seconds'), 'an own code was not said to be the person\'s own, with its life: ' + shown().textContent);

  act('done').click();
  assert(last('access_code_cancel').access_code === 'c2' && !shown() && act('start').textContent === 'Open on another device', 'Done did not end the code it showed');

  const before = sent.filter(m => m.type === 'config_set').length;
  S.config = cfg({ require_token: true }, { status: 'issued', name: name, origin: 'https://' + name + ':8181', direct_origin: 'https://' + name + ':8181', route_mode: 'direct', relays: relays, moves_with: ['sign-in profile mail'] });
  renderSettings();
  act('start').click();
  assert(sent.filter(m => m.type === 'config_set').length === before, 'the address was moved without asking, with a sign-in profile registered at the old one');
  const move = document.querySelector('[data-open-move]');
  assert(move && move.textContent.includes('sign-in profile mail') && move.textContent.includes('from https://' + name + ':8181 to https://' + name) && act('move'), 'what the move breaks was not said before it: ' + (move && move.textContent));
  act('move').click();
  assert(sent.filter(m => m.type === 'config_set').length === before + 1 && last('config_set').config['certificate.relay_endpoint'] === 'relay.example.test:443', 'the move was not made on the second press');

  act('done').click();
  S.config = cfg({ require_token: true }, { status: 'issued', name: name, origin: 'https://' + name + ':8181', route_mode: 'direct', relays: [] });
  renderSettings();
  act('start').click();
  assert(said().includes('No relay is offered') && !act('start').disabled, 'the missing relay was not said: ' + said());

  S.config = cfg({ host: '10.8.0.2', tls: true, require_token: true }, { status: 'issued', name: name, origin: 'https://' + name + ':8181', route_mode: 'direct', address: '10.8.0.2', relays: relays });
  renderSettings();
  const asks = sent.filter(m => m.type === 'access_code').length, sets = sent.filter(m => m.type === 'config_set').length;
  act('start').click();
  assert(sent.filter(m => m.type === 'access_code').length === asks + 1 && sent.filter(m => m.type === 'config_set').length === sets, 'a dashboard already reached on its own network was moved to a relay, or given no code');

  act('done').click();
  acceptSettingsConfig(last('config_set').request_id); // the host answered the move's save: nothing is pending
  S.config = cfg({ require_token: true }, { status: 'prepared', name: name, origin: 'https://' + name + ':8181', direct_origin: 'https://' + name + ':8181', route_mode: 'direct', relays: relays });
  renderSettings();
  assert(document.querySelector('[data-public-name-state]').textContent.includes('held and not in use') && !document.querySelector('[data-public-name="retry"]'),
    'a held name was shown as a failed certificate: ' + document.querySelector('[data-public-name-state]').textContent);
  const held = sent.filter(m => m.type === 'config_set').length, retries = sent.filter(m => m.type === 'public_name_retry').length;
  act('start').click();
  assert(sent.filter(m => m.type === 'config_set').length === held + 1 && last('config_set').config['certificate.relay_endpoint'] === 'relay.example.test:443' && sent.filter(m => m.type === 'public_name_retry').length === retries,
    'a held name was not given its route first, or its certificate was asked for before anything led to it');
  const leads = last('config_set');
  S.config = cfg({ require_token: true }, { status: 'prepared', name: name, origin: 'https://' + name, direct_origin: 'https://' + name + ':8181', route_mode: 'relay', relay_endpoint: 'relay.example.test:443', relay: 'relay.example.test:443', relays: relays });
  assert(acceptSettingsConfig(leads.request_id), 'the held name\'s route answer was not claimed');
  renderSettings(); renderSettings();
  assert(sent.filter(m => m.type === 'public_name_retry').length === retries + 1 && /certificate/.test(said()), 'something leads to a held name and its certificate was not asked for exactly once: ' + said());
  act('done').click();

  acceptSettingsConfig((last('config_set') || {}).request_id);
  const unasked = { status: 'prepared', name: name, origin: 'https://' + name + ':8181', direct_origin: 'https://' + name + ':8181', route_mode: 'direct' };
  S.config = cfg({ require_token: true }, unasked);
  renderSettings();
  const looks = () => sent.filter(m => m.type === 'public_name_look').length, saves = () => sent.filter(m => m.type === 'config_set').length;
  const lookedBefore = looks(), savesAtLook = saves();
  act('start').click();
  assert(looks() === lookedBefore + 1 && saves() === savesAtLook && /relays/.test(said()), 'a press with no relays known did not ask the service for them, or chose a route without them: ' + said());
  renderSettings(); renderSettings();
  assert(looks() === lookedBefore + 1, 'the service was asked again at every drawing of the page');
  S.config = cfg({ require_token: true }, Object.assign({}, unasked, { relays: relays }));
  renderSettings();
  assert(saves() === savesAtLook + 1 && last('config_set').config['certificate.relay_endpoint'] === 'relay.example.test:443', 'the relays arrived and the press did not choose the one marked');
  act('done').click();
  acceptSettingsConfig(last('config_set').request_id);

  S.config = cfg({ require_token: true, settings: [{ key: 'dashboard.channel_sessions', value: '16' }, { key: 'certificate.relay_carried_max', value: '7', set: true }] }, carried);
  renderSettings();
  const readBack = document.querySelector('[data-settings-read]');
  const readRow = key => readBack && readBack.querySelector('[data-setting="' + key + '"]');
  assert(readBack && readRow('dashboard.channel_sessions') && readRow('dashboard.channel_sessions').textContent.includes('16') && readRow('dashboard.channel_sessions').textContent.includes('the default'),
    'a setting with no control is not read on the card with its default: ' + (readBack && readBack.textContent));
  assert(readRow('certificate.relay_carried_max').textContent.includes('7') && readRow('certificate.relay_carried_max').textContent.includes('config.json'), 'a setting the file states is not read as stated');

  S.stats = {};
  S.config = cfg({ require_token: true }, carried);
  renderSettings();
  assert(act('off') && act('off').textContent === 'Stop using the relay' && document.querySelector('[data-route-expert]').hidden, 'a dashboard behind a relay offers no way off it without Expert');
  const savedBefore = saves();
  act('off').click();
  const offSave = last('config_set');
  assert(saves() === savedBefore + 1 && offSave.config['certificate.route_mode'] === 'direct' && !('certificate.relay_endpoint' in offSave.config), 'Stop using the relay did not save the direct route: ' + JSON.stringify(offSave && offSave.config));
  S.config = cfg({ require_token: true }, { status: 'issued', name: name, origin: 'https://' + name + ':8181', direct_origin: 'https://' + name + ':8181', route_mode: 'direct', relays: relays });
  assert(acceptSettingsConfig(offSave.request_id), 'the answer to Stop using the relay was not claimed');
  renderSettings();
  const offTold = document.querySelector('[data-open-off-said]');
  assert(offTold && offTold.textContent === 'This dashboard is off the relay.' && !act('off'), 'taking the dashboard off the relay was not said, or the button stayed: ' + (offTold && offTold.textContent));

  S.stats = { expert: true };
  renderSettings();
  assert(!document.querySelector('[data-route-expert]').hidden && document.getElementById('cfg-route'), 'Expert did not show the route controls');

  S.config = cfg({ require_token: true }, { status: 'none', tls: false, can_claim: true });
  renderSettings();
  const claimed = () => sent.filter(m => m.type === 'public_name_claim').length;
  const claimedBefore = claimed();
  act('start').click();
  assert(claimed() === claimedBefore + 1 && /Claiming/.test(said()), 'the press with the service in force did not claim: ' + said());
  S.config = cfg({ require_token: true }, { status: 'none', tls: false, can_claim: false, service_declined: true });
  renderSettings();
  assert(declined.test(said()) && !act('done') && claimed() === claimedBefore + 1, 'a running flow did not stop when the service was declined: ' + said());
});
</script></body></html>`

	modules := map[string][]byte{}
	for _, path := range []string{"static/views/settings.js", "static/views/model-picker.js", "static/util.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { providers: [], config: null, providersLoaded: false };`)
	modules["/ws.js"] = []byte(`export const sent = []; let n = 0; export function send(m) { m.request_id = 'r' + (++n); sent.push(m); return m.request_id; } export function query() { return 'req-1'; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
