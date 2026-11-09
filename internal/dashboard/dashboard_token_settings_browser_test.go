//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

// SETTINGS NEVER SHOWS OR ASKS FOR THE DASHBOARD TOKEN (row 1b of the expert
// review's decisions). Any socket the page holds could ask the host for the
// token, and the page drew it in a field with an eye and a copy button, so a
// script in the page could read it too. The card a token is asked for on
// now draws no field and no eye, asks the host for nothing, connected or
// not, and says nothing of where a token is read: a person never needs it
// Signing devices out is Phone and other devices'
// (TestPhoneAndOtherDevicesShowsTheCodeAtOnce).
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
  S.settingsSection('dashboard'); S.requestSettingsSection(); renderSettings();
  const none = (when) => {
    assert(!document.getElementById('cfg-dtoken') && !document.querySelector('[data-token-reveal]') && !document.querySelector('[data-token-copy]'),
      when + ': the card draws a field for the token');
    assert(!document.querySelector('[data-token-where]') && !document.getElementById('settings-stack').textContent.includes('dashboard-token'),
      when + ': the card sends the person to a token');
    assert(!asked.includes('dashboard_token'), when + ': the page asked the host for the token');
  };
  none('signed in');
  S.connected = false;
  settingsConnectionLost();
  S.connected = true;
  renderSettings();
  none('after a reconnection');
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
