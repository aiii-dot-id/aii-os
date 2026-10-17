//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestAuthenticatedSettingsCanRevealAndCopyDashboardToken(t *testing.T) {
	page := `<!doctype html><html><body><div id="settings-stack"></div>
<script type="module">
import { S } from '/state.js';
import { asked } from '/ws.js';
import { renderSettings, acceptDashboardToken, settingsConnectionLost } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  const token = 'configured-dashboard-token';
  S.view = 'settings'; S.providersLoaded = true; S.providers = []; S.connected = true;
  S.config = { llm: {}, speech: {}, dashboard: { host: '192.0.2.10', port: 8181, tls: true, require_token: true },
    prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [], secrets_ok: true };
  let copied = '';
  try { Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async text => { copied = text; } } }); } catch (e) {}
  document.execCommand = command => {
    if (command === 'copy') copied = document.querySelector('textarea').value;
    return command === 'copy';
  };
  renderSettings();
  document.querySelector('[data-sec="dashboard"]').click();
  const field = document.getElementById('cfg-dtoken');
  const reveal = document.querySelector('[data-token-reveal]');
  const copy = document.querySelector('[data-token-copy]');
  assert(field && field.type === 'password' && field.readOnly && field.value === '', 'the token was carried in the configuration frame');
  assert(copy.disabled, 'copy was offered before there was anything to copy');
  S.connected = false;
  settingsConnectionLost();
  assert(reveal.disabled && reveal.title.includes('Reconnect'), 'the disconnected eye offered an unavailable query');
  reveal.onclick();
  assert(asked.length === 0, 'a stale eye handler asked while disconnected');
  S.connected = true;
  renderSettings();
  assert(!document.querySelector('[data-token-reveal]').disabled, 'reconnection did not enable the eye');
  document.querySelector('[data-token-reveal]').click();
  assert(asked.length === 1, 'the connected eye did not ask');
  S.connected = false;
  settingsConnectionLost();
  assert(!acceptDashboardToken('req-1', 'stale-token'), 'a reply from the lost connection was accepted');
  S.connected = true;
  renderSettings();
  assert(asked.length === 1, 'reconnection retried a secret query without a press');
  const currentField = document.getElementById('cfg-dtoken');
  const currentReveal = document.querySelector('[data-token-reveal]');
  const currentCopy = document.querySelector('[data-token-copy]');
  currentReveal.click();
  assert(asked.length === 2 && asked[1] === 'dashboard_token', 'the eye did not ask on the new connection');
  assert(acceptDashboardToken('req-2', token), 'the new answer was not claimed');
  assert(currentField.type === 'text' && currentField.value === token && currentReveal.getAttribute('aria-pressed') === 'true', 'the answer did not show the token');
  S.connected = false;
  settingsConnectionLost();
  assert(!currentReveal.disabled, 'an already shown token could not be hidden while disconnected');
  currentReveal.click();
  assert(currentField.type === 'password' && currentReveal.getAttribute('aria-pressed') === 'false', 'the reveal icon did not hide the token again');
  currentCopy.click();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert(copied === token, 'the copy icon copied something other than the configured token');

  S.connected = true;
  let finishRotation;
  S.rotateAccessToken = () => new Promise(resolve => { finishRotation = resolve; });
  window.confirm = () => true;
  document.querySelector('[data-token-rotate]').click();
  finishRotation({ ok: true });
  S.connected = false;
  await new Promise(resolve => setTimeout(resolve, 0));
  assert(document.getElementById('cfg-dtoken').value === '', 'rotation retained the old token');
  assert(document.querySelector('[data-token-reveal]').disabled, 'rotation success enabled reveal before reconnecting');
  S.connected = true;
  renderSettings();
  document.querySelector('[data-token-rotate]').click();
  const busyEye = document.querySelector('[data-token-reveal]');
  assert(busyEye.disabled && busyEye.title.includes('Wait'), 'the empty eye was offered during rotation');
  busyEye.onclick();
  assert(asked.length === 2, 'the eye asked during rotation');
  finishRotation({ ok: true });
  await new Promise(resolve => setTimeout(resolve, 0));
  const reconnectedEye = document.querySelector('[data-token-reveal]');
  assert(!reconnectedEye.disabled, 'the connected eye stayed disabled after rotation');
  reconnectedEye.click();
  assert(acceptDashboardToken('req-3', 'rotated-token'), 'the rotated token was not accepted on the new connection');

  S.config.dashboard.require_token = false;
  renderSettings();
  assert(!document.getElementById('cfg-dtoken'), 'an open dashboard was given a token field');
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
