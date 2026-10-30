//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestSettingsNeverShowsOrAsksForTheDashboardToken(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { asked } from '/ws.js';
import { renderSettings, settingsConnectionLost } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  S.view = 'settings'; S.providersLoaded = true; S.providers = []; S.connected = true;
  S.config = { llm: {}, speech: {}, dashboard: { host: '192.0.2.10', port: 8181, tls: true, require_token: true },
    prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], secrets_ok: true };
  renderSettings();
  document.querySelector('[data-sec="dashboard"]').click();
  const none = (when) => {
    assert(!document.getElementById('cfg-dtoken') && !document.querySelector('[data-token-reveal]') && !document.querySelector('[data-token-copy]'),
      when + ': the card draws a field for the token');
    const where = document.querySelector('[data-token-where]');
    assert(where && where.textContent.includes('aii dashboard-token') && where.textContent.includes('Open on another device'),
      when + ': the card does not say where the token is read: ' + (where && where.textContent));
    assert(!asked.includes('dashboard_token'), when + ': the page asked the host for the token');
  };
  none('signed in');
  S.connected = false;
  settingsConnectionLost();
  S.connected = true;
  renderSettings();
  none('after a reconnection');
  let finish;
  S.rotateAccessToken = () => new Promise(resolve => { finish = resolve; });
  window.confirm = () => true;
  document.querySelector('[data-token-rotate]').click();
  finish({ ok: true });
  await new Promise(resolve => setTimeout(resolve, 0));
  const said = document.querySelector('[data-token-rotate-said]');
  assert(said && said.textContent.includes('Rotated') && !said.textContent.includes('eye'), 'the rotation was not said, or pointed at an eye: ' + (said && said.textContent));
  none('after a rotation');
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
	modules["/ws.js"] = []byte(`export const asked = []; export function send() { return 'send-1'; } export function query(name) { asked.push(name); return 'req-' + asked.length; }`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}

func TestSettingsMakesALocalAccessCodeForAnotherBrowser(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { sent } from '/ws.js';
import { renderSettings, acceptAccessCode, rejectAccessCode } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  S.view = 'settings'; S.providersLoaded = true; S.providers = []; S.connected = true;
  const cfg = secretsOK => ({ llm: {}, speech: {}, dashboard: { host: '127.0.0.1', port: 8181, tls: false, require_token: true, access_code_seconds: 120 },
    prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], secrets_ok: secretsOK, public_name: { status: 'none', can_claim: true } });
  const last = type => sent.filter(m => m.type === type).pop();
  const act = which => document.querySelector('[data-local-code-act="' + which + '"]');
  const shown = () => document.querySelector('[data-local-code]').textContent;
  S.config = cfg(true);
  renderSettings();
  document.querySelector('[data-sec="dashboard"]').click();
  assert(act('make') && act('make').textContent === 'Make a sign-in code for another browser', 'the token card offers no code for another browser');
  act('make').click();
  const ask = last('access_code');
  assert(ask && ask.access_code_local === true, 'a local code was not asked for: ' + JSON.stringify(ask));
  assert(acceptAccessCode(ask.request_id, { id: 'c1', code: 'ABCD-EFGH-JKMN', link: 'http://127.0.0.1:8181/auth/link#ABCDEFGHJKMN', seconds: 120 }), 'the local code was not claimed');
  assert(document.querySelector('[data-local-code-text]').textContent === 'ABCD-EFGH-JKMN' && document.querySelector('[data-local-code-link]').textContent === 'http://127.0.0.1:8181/auth/link#ABCDEFGHJKMN',
    'the code and its link were not shown: ' + shown());
  assert(shown().includes('It works once, for 2 minutes, and only for a browser on this machine or its own network.'), 'the code\'s life and where it works were not said: ' + shown());
  assert(!document.querySelector('[data-open-code]'), 'the local code was shown as a code for another device through the name');
  act('done').click();
  assert(last('access_code_cancel') && last('access_code_cancel').access_code === 'c1' && !document.querySelector('[data-local-code-text]'), 'Done did not end the code it showed');
  act('make').click();
  assert(rejectAccessCode('the access token changed while the code was being made', last('access_code').request_id), 'the refusal was not claimed');
  const said = document.querySelector('[data-local-code-said]');
  assert(said && said.textContent.includes('the access token changed') && act('make'), 'the refusal was not said beside the button');
  act('make').click();
  acceptAccessCode(last('access_code').request_id, { id: 'c2', code: 'WXYZ-WXYZ-WXYZ', link: 'http://127.0.0.1:8181/auth/link#WXYZWXYZWXYZ', seconds: 90 });
  assert(shown().includes('90 seconds') && act('make').textContent === 'New code', 'a second code was not shown with its life');
  let finish;
  S.rotateAccessToken = () => new Promise(resolve => { finish = resolve; });
  window.confirm = () => true;
  document.querySelector('[data-token-rotate]').click();
  finish({ ok: true });
  await new Promise(resolve => setTimeout(resolve, 0));
  assert(!document.querySelector('[data-local-code-text]'), 'a code the rotation ended is still shown');
  S.config = cfg(false);
  renderSettings();
  assert(!act('make') && document.querySelector('[data-local-code-private]'), 'a code is offered over a connection that is not private, or the card does not say why');
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
