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
import { renderSettings, acceptDashboardToken } from '/views/settings.js';
import { run } from '/__harness.js';
run(async (assert) => {
  const token = 'configured-dashboard-token';
  S.view = 'settings'; S.providersLoaded = true; S.providers = [];
  S.config = { llm: {}, speech: {}, dashboard: { host: '192.0.2.10', port: 8181, tls: true, require_token: true },
    prompt: {}, witness: {}, logs: {}, agency: {}, plugins: {}, updates: {}, restart_required: [] };
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
  // THE TOKEN IS NOT IN THE PAGE UNTIL IT IS ASKED FOR: the config frame
  // reaches every socket, the answer reaches the screen that pressed the eye.
  assert(field && field.type === 'password' && field.readOnly && field.value === '', 'the token was carried in the configuration frame');
  assert(copy.disabled, 'copy was offered before there was anything to copy');
  reveal.click();
  assert(asked.length === 1 && asked[0] === 'dashboard_token', 'the eye did not ask the host: ' + JSON.stringify(asked));
  assert(acceptDashboardToken('req-1', token), 'the answer was not claimed');
  assert(field.type === 'text' && field.value === token && reveal.getAttribute('aria-pressed') === 'true', 'the answer did not show the token');
  reveal.click();
  assert(field.type === 'password' && reveal.getAttribute('aria-pressed') === 'false', 'the reveal icon did not hide the token again');
  copy.click();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert(copied === token, 'the copy icon copied something other than the configured token');

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
	modules["/ws.js"] = []byte(`export const asked = []; export function send() { return 'req-1'; } export function query(name) { asked.push(name); return 'req-1'; }`)
	modules["/pending.js"] = []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; }`)
	modules["/sandbox.js"] = []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`)
	runPageInEngines(t, page, modules)
}
