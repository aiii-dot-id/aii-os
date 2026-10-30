//go:build !windows

package dashboard

import (
	"testing"
)

// settingsPageModules is the real settings.js, util.js and waiting.js over
// the stubs a Settings page needs to draw one section.
func settingsPageModules() map[string][]byte {
	return map[string][]byte{
		"/state.js": []byte(`export const S = { providers: [], config: null, providersLoaded: false, asks: [] };`),
		"/ws.js": []byte(`export const frames = [];
export function send(f) { frames.push(f); return 'req-1'; }
export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`),
		"/pending.js": []byte(`export function pendingSlot() { return { arm(){}, claim(){ return false; }, drop(){}, waiting(){ return false; } }; } export function anyPending() { return false; }`),
		"/sandbox.js": []byte(`export function sandboxCardHTML() { return ''; } export function wireSandboxCard() {}`),
	}
}

// settingsConfig is a configuration enough to draw Agency and Dashboard.
const settingsConfig = `{ llm: { provider: 'p', model: 'm' }, dashboard: { host: '127.0.0.1', port: 1, tls: false },
    prompt: {}, witness: {}, logs: {}, plugins: {}, agency: { prefer_local_for_roles: false }, restart_required: [] }`

// THE SWITCH ON SETTINGS → DASHBOARD. This browser's, off at
// first; checking it asks the browser once, inside the click, and the card
// then reads on, kept in this browser; unchecking turns it off without
// asking. Blocked, it says how to allow it; with no Notification it is
// disabled and says it is not available. The heading carries its sentence.
func TestTheNotificationSwitchOnSettingsDashboard(t *testing.T) {
	page := `<!doctype html>
<div id="settings-stack"></div>
<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run, until } from './__harness.js';
` + notificationStub + `
run(async () => {
  S.config = ` + settingsConfig + `;
  S.providersLoaded = true; S.view = 'settings';
  renderSettings();
  document.querySelector('[data-sec="dashboard"]').click();
  const card = () => document.querySelector('[data-notify-card]');
  const box = () => card().querySelector('[data-notify-asks]');
  const said = () => card().querySelector('[data-notify-said]');
  assert(card(), 'no notification card on Settings → Dashboard');
  const head = card().querySelector('h3');
  assert(head.textContent === 'NOTIFICATIONS' && head.title.length > 20 && head.title.endsWith('.'), 'the heading’s sentence: ' + head.title);
  assert(card().textContent.includes('Notify me in this browser when the identity asks me something'), 'the switch’s words');
  assert(!box().checked && !box().disabled && said().hidden, 'the switch is not off at first');

  // On: the browser is asked once, from the click.
  const press = async () => { const old = box(); old.click(); await until(() => box() !== old); };
  await press();
  assert(box().checked && StubNotification.asked === 1, 'on: checked ' + box().checked + ', asked ' + StubNotification.asked);
  assert(localStorage.getItem('aii.notify.asks') === 'on', 'the switch is not kept in this browser');
  // Off: nothing asked.
  await press();
  assert(!box().checked && StubNotification.asked === 1 && localStorage.getItem('aii.notify.asks') === null, 'off asked the browser or kept the switch');

  // Blocked: the sentence, and the switch stays off.
  StubNotification.permission = 'denied';
  renderSettings();
  assert(!said().hidden && said().textContent === 'This browser has blocked notifications for this page. Allow them in the browser’s site settings, then turn this on again.', 'blocked: ' + said().textContent);
  await press();
  assert(!box().checked && StubNotification.asked === 1, 'a blocked switch turned on or asked');

  // Not available: no Notification in this browser.
  delete window.Notification;
  renderSettings();
  assert(box().disabled && said().textContent === 'This browser cannot show this page’s notifications here; the page’s title counts the open questions.', 'not available: ' + said().textContent);
});
</script>`
	runPageInEngines(t, page, settingsPageModules())
}
