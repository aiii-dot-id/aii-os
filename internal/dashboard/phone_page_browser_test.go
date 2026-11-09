//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// PHONE AND OTHER DEVICES: linking a phone is found where a person looks
// and shown at once, as a messenger links a device. Where
// another device can already reach this computer, the page asks the host for
// a code on arrival and shows it with its picture, with no press; a code that
// runs out is replaced while the page is open. The first time, one press sets
// up what a code needs, and what it claims is said beside it. Off a private
// connection nothing is offered, and the page says why. With the name service
// declined, a code for this computer's own network is offered instead, and a
// rotation ends it. Signing every other device out is asked once, said, and
// refused in the host's words. How it works is folded, and taking the page
// off the relay is one press there. No token is shown or named anywhere.
const phonePage = `<!doctype html><div id="views"></div>
<script type="module">
import { S } from '/state.js';
import { sent } from '/ws.js';
import { renderPhone, acceptAccessCode, rejectAccessCode } from '/views/settings.js';
import { TABS } from '/registry.js';
import { assert, run } from '/__harness.js';

const tick = () => new Promise(r => setTimeout(r, 0));
const q = sel => document.querySelector('#phone-stack ' + sel);
const text = () => document.getElementById('phone-stack').textContent;
const last = type => sent.filter(m => m.type === type).pop();
const count = type => sent.filter(m => m.type === type).length;
// The page in view or hidden, as the browser says it.
const seen = v => { Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => v }); document.dispatchEvent(new Event('visibilitychange')); };
const leave = () => { S.view = 'settings'; window.dispatchEvent(new HashChangeEvent('hashchange')); };
const config = (over, name) => Object.assign({ llm: {}, speech: {}, prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [],
  dashboard: { host: '127.0.0.1', port: 8181, tls: true, require_token: true, access_code_seconds: 120 }, secrets_ok: true,
  public_name: Object.assign({ status: 'issued', name: 'ui.vega.example.test', origin: 'https://ui.vega.example.test', route_mode: 'relay', relay_endpoint: 'relay.example.test:443', relay_connected: true }, name) }, over || {});

run(async () => {
  S.view = 'phone'; S.connected = true; S.stats = { name: 'Vega' };
  // Its own entry in Settings' list, directly after Status.
  assert(TABS.settings[0][0] === 'status' && TABS.settings[1].join() === 'phone,phone', 'Phone and other devices is not the entry after Status: ' + JSON.stringify(TABS.settings));

  // Ready: the code is asked for on arrival, with no press, and shown.
  S.config = config();
  renderPhone();
  const ask = last('access_code');
  assert(ask && !ask.access_code_local, 'a page ready for a code did not ask for one on arrival: ' + JSON.stringify(sent));
  assert(q('h2').textContent === 'Use Vega on your phone', 'the page is not headed by what it is for: ' + (q('h2') && q('h2').textContent));
  assert(!q('[data-open-elsewhere-act="start"]'), 'a page ready for a code asked for a press');
  assert(acceptAccessCode(ask.request_id, { id: 'c1', code: 'ABCD EFGH JKMN', qr: '<svg data-qr></svg>', seconds: 1 }), 'the code was not taken');
  assert(q('[data-open-qr] svg') && q('[data-open-code-text]').textContent === 'ABCD EFGH JKMN', 'the code and its picture are not shown');
  assert(text().includes("Point your phone's camera at this code.") && text().includes('Or open https://ui.vega.example.test on any device and type:') && text().includes('Works once. A new code appears in '),
    'the code is not said as approved: ' + text());
  assert(!/token/i.test(text()), 'the page names a token: ' + text());
  // A code that runs out is replaced while the page is open.
  const asked = sent.filter(m => m.type === 'access_code').length;
  // waits by design: the code's own life (one second) runs out
  await new Promise(r => setTimeout(r, 1300));
  assert(last('access_code_cancel') && last('access_code_cancel').access_code === 'c1' && sent.filter(m => m.type === 'access_code').length === asked + 1, 'a code that ran out was not replaced');
  acceptAccessCode(last('access_code').request_id, { id: 'c2', code: 'MNPQ RSTV WXYZ', qr: '<svg data-qr></svg>', seconds: 1 });
  assert(q('[data-open-code-text]').textContent === 'MNPQ RSTV WXYZ', 'the new code is not shown');
  // One that runs out while the page is hidden is replaced when it is seen again, not before.
  seen('hidden');
  const hiddenAsks = count('access_code');
  // waits by design: the code's life runs out while the page is hidden
  await new Promise(r => setTimeout(r, 1300));
  assert(count('access_code') === hiddenAsks, 'a code was asked for while the page was hidden');
  seen('visible');
  assert(last('access_code_cancel').access_code === 'c2' && count('access_code') === hiddenAsks + 1, 'a code that ran out while hidden was not replaced when seen again');
  acceptAccessCode(last('access_code').request_id, { id: 'c2b', code: 'NPQR STVW XYZA', qr: '<svg data-qr></svg>', seconds: 600 });

  // How it works, folded; off the relay in one press there.
  const how = q('[data-open-how]');
  assert(how && !how.open && how.textContent.includes('A relay brings the other device to this one'), 'how it works is not folded under its name');
  how.querySelector('[data-open-elsewhere-act="off"]').click();
  const off = last('config_set');
  assert(off && JSON.stringify(off.config) === '{"certificate.route_mode":"direct"}', 'taking the page off the relay did not save the direct route: ' + JSON.stringify(off));
  acceptAccessCode(last('access_code').request_id, { id: 'c3', code: 'C3C3 C3C3 C3C3', qr: '<svg data-qr></svg>', seconds: 600 });
  assert(q('[data-open-code-text]').textContent === 'C3C3 C3C3 C3C3', 'the code before signing out is not shown');

  // Signing every other device out: asked once, said, refused in the host's words.
  let answer = { ok: true }, rotations = 0, confirmed = false;
  S.rotateAccessToken = async () => { rotations++; await tick(); return answer; };
  window.confirm = () => confirmed;
  q('[data-sign-out-all]').click();
  await tick(); await tick();
  assert(rotations === 0, 'devices were signed out although the person said no');
  confirmed = true;
  const asksBeforeOut = count('access_code');
  q('[data-sign-out-all]').click();
  assert(q('[data-sign-out-all]').disabled, 'the press was taken twice');
  await tick(); await tick(); await tick();
  assert(rotations === 1 && q('[data-sign-out-said]').textContent === 'Every other device is signed out.', 'signing out was not done once and said');
  // The code shown was made under the old sign-in: it goes, and a new one is asked for.
  assert(last('access_code_cancel').access_code === 'c3' && !text().includes('C3C3') && count('access_code') === asksBeforeOut + 1, 'a code the rotation ended is still shown, or no new one was asked for');
  answer = { ok: false, text: 'This connection is not private, so the access token was not rotated' };
  q('[data-sign-out-all]').click();
  await tick(); await tick(); await tick();
  assert(q('[data-sign-out-said]').className.includes('bad') && q('[data-sign-out-said]').textContent.includes('not private'), 'a refusal was not said in the host\'s words');
  // An answer that comes after the page was left starts nothing there.
  let release;
  S.rotateAccessToken = () => new Promise(r => { release = r; });
  q('[data-sign-out-all]').click();
  await tick();
  leave();
  const asksAway = count('access_code');
  release({ ok: false, text: 'late' });
  await tick(); await tick(); await tick();
  assert(count('access_code') === asksAway, 'an answer after the page was left started the page again');
  S.rotateAccessToken = async () => { rotations++; await tick(); return answer; };
  S.view = 'phone';

  // The first time: one press, and what it claims said beside it.
  S.stats = { name: 'Unnamed' };
  S.config = config({}, { status: 'none', name: '', origin: '', route_mode: 'direct', relay_endpoint: '', can_claim: true });
  renderPhone();
  assert(q('h2').textContent === 'Use them on your phone', 'an unnamed identity is named: ' + q('h2').textContent);
  const claims = q('[data-open-claims]').textContent;
  assert(claims === "This gives your identity a web address your phone can reach. To make it, your identity's public key goes to the name service, and the claim is written in their record.",
    'what the press claims is not said before it: ' + claims);
  const setUp = q('[data-open-elsewhere-act="start"]');
  assert(setUp && setUp.textContent === 'Set up', 'the first time is not one press');

  // Off a private connection: nothing offered, and why.
  S.config = config({ secrets_ok: false });
  renderPhone();
  assert(q('[data-open-private]') && !q('[data-open-elsewhere-act]') && !q('[data-sign-out-all]') && !q('[data-open-how]'), 'something is offered off a private connection');

  // The name service declined: a code for this computer's network instead.
  // Its link, its life and where it works are said; Done ends it by its id;
  // a refusal is said beside the button; a second code replaces the first.
  S.config = config({}, { status: 'none', name: '', origin: '', route_mode: 'direct', service_declined: true });
  renderPhone();
  const act = which => q('[data-local-code-act="' + which + '"]'), make = () => act('make');
  const shownLocal = () => (q('[data-local-code]') || { textContent: '' }).textContent;
  assert(q('[data-open-claims]') && make() && make().textContent === 'Make a sign-in code for another browser', 'with the name service declined no code for this network is offered');
  make().click();
  const local = last('access_code');
  assert(local && local.access_code_local === true, 'a code for this network was not asked for');
  acceptAccessCode(local.request_id, { id: 'c9', code: 'WXYZ-WXYZ-WXYZ', link: 'http://127.0.0.1:8181/auth/link#WXYZWXYZWXYZ', seconds: 120 });
  assert(q('[data-local-code-text]').textContent === 'WXYZ-WXYZ-WXYZ' && q('[data-local-code-link]').textContent === 'http://127.0.0.1:8181/auth/link#WXYZWXYZWXYZ', 'the code for this network and its link were not shown');
  assert(shownLocal().includes('It works once, for 2 minutes, and only for a browser on this machine or its own network.'), 'its life and where it works were not said: ' + shownLocal());
  act('done').click();
  assert(last('access_code_cancel').access_code === 'c9' && !q('[data-local-code-text]'), 'Done did not end the code it showed');
  make().click();
  assert(rejectAccessCode('your devices were signed out while the code was being made', last('access_code').request_id), 'the refusal was not claimed');
  assert(q('[data-local-code-said]') && q('[data-local-code-said]').textContent.includes('your devices were signed out') && make(), 'the refusal was not said beside the button');
  make().click();
  acceptAccessCode(last('access_code').request_id, { id: 'c10', code: 'MMMM-MMMM-MMMM', link: 'http://127.0.0.1:8181/auth/link#MMMMMMMMMMMM', seconds: 90 });
  assert(shownLocal().includes('90 seconds') && make().textContent === 'New code', 'a second code was not shown with its life');
  // Leaving the page ends it by its id, and coming back shows none.
  leave();
  assert(last('access_code_cancel').access_code === 'c10', 'leaving the page did not end the code for this network');
  S.view = 'phone'; renderPhone();
  assert(!q('[data-local-code-text]'), 'the code for this network came back after the page was left');
  // An answer that comes after the page was left is taken by nobody.
  make().click();
  const owed = last('access_code');
  leave();
  // Taken or thrown over, a late answer that reaches the page is the same fault.
  let late; try { late = acceptAccessCode(owed.request_id, { id: 'c11', code: 'LATE-LATE-LATE', link: '', seconds: 90 }); } catch (e) { late = 'threw: ' + e.message; }
  assert(late === false, 'an answer that came after the page was left was taken: ' + late);
  S.view = 'phone'; renderPhone();
  assert(!q('[data-local-code-text]'), 'a late code was shown');
  // A rotation ends a code for this network.
  make().click();
  acceptAccessCode(last('access_code').request_id, { id: 'c12', code: 'ROTA-ROTA-ROTA', link: '', seconds: 90 });
  answer = { ok: true };
  q('[data-sign-out-all]').click();
  await tick(); await tick(); await tick();
  assert(!q('[data-local-code-text]'), 'a code the rotation ended is still shown');

  // A START WITH NO PRESS ONLY EVER ASKS FOR A CODE. If the name stops
  // leading here while its code is still being made, it ends and the press
  // is offered again: it claims nothing by itself, and the late code is
  // taken by nobody.
  leave();
  S.config = config();
  S.view = 'phone'; renderPhone();
  const auto = last('access_code');
  assert(auto && !auto.access_code_local, 'a page ready for a code did not ask for one on arrival again');
  const claimsBefore = count('public_name_claim');
  S.config = config({}, { status: 'none', name: '', origin: '' });
  renderPhone();
  await tick();
  assert(count('public_name_claim') === claimsBefore, 'a start with no press went on to claim a name');
  assert(q('[data-open-elsewhere-act="start"]'), 'the press was not offered once the name no longer led here');
  assert(!acceptAccessCode(auto.request_id, { id: 'c13', code: 'LATE LATE LATE', qr: '<svg data-qr></svg>', seconds: 600 }), 'the ended start took its late code');
});
</script>`

func TestPhoneAndOtherDevicesShowsTheCodeAtOnce(t *testing.T) {
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
	runPageInEngines(t, phonePage, modules)
}
