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
    prompt: {}, witness: {}, logs: {}, plugins: {}, agency: {}, restart_required: [] }`

// THE SWITCH ON SETTINGS → APP (step 18; it was a card on Dashboard). This
// browser's, off at first; checking it asks the browser once, inside the
// click, and the part then reads on, kept in this browser; unchecking turns
// it off without asking. Blocked, it says how to allow it; with no
// Notification it is disabled and says so. Its heading is explained by the
// hint the part names (aria-describedby), and the switch names the identity.
func TestTheNotificationSwitchOnSettingsApp(t *testing.T) {
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
  S.settingsSection('updates'); S.requestSettingsSection(); renderSettings();
  const part = () => document.querySelector('[data-notify-card]');
  const box = () => part().querySelector('[data-notify-asks]');
  const said = () => part().querySelector('[data-notify-said]');
  assert(part(), 'no notification part on Settings → App');
  const head = part().querySelector('h3');
  const hint = document.getElementById(part().getAttribute('aria-describedby'));
  assert(head.textContent === 'Notifications' && hint && hint.textContent === "In this browser, while the page is in another tab or window. The page's title also counts open questions.", 'the heading and its hint: ' + head.textContent + ' / ' + (hint && hint.textContent));
  assert(part().textContent.includes('Get a notification when they ask you something'), 'the switch’s words, unnamed: ' + part().textContent);
  assert(box().getAttribute('role') === 'switch' && !box().checked && !box().disabled && said().hidden, 'the switch is not off at first');

  // On: the browser is asked once, from the click.
  const press = async () => { const old = box(); old.click(); await until(() => box() !== old); };
  await press();
  assert(box().checked && StubNotification.asked === 1, 'on: checked ' + box().checked + ', asked ' + StubNotification.asked);
  assert(localStorage.getItem('aii.notify.asks') === 'on', 'the switch is not kept in this browser');
  // Off: nothing asked.
  await press();
  assert(!box().checked && StubNotification.asked === 1 && localStorage.getItem('aii.notify.asks') === null, 'off asked the browser or kept the switch');

  // Named: the switch says who asks.
  S.stats = { name: 'Taylor' };
  renderSettings();
  assert(part().textContent.includes('Get a notification when Taylor asks you something'), 'the switch’s words, named: ' + part().textContent);

  // Blocked: the sentence, and the switch stays off.
  StubNotification.permission = 'denied';
  renderSettings();
  assert(!said().hidden && said().textContent === 'This browser has blocked notifications for this page. Allow them in its site settings, then turn this on again.', 'blocked: ' + said().textContent);
  await press();
  assert(!box().checked && StubNotification.asked === 1, 'a blocked switch turned on or asked');

  // Not available: no Notification in this browser.
  delete window.Notification;
  renderSettings();
  assert(box().disabled && said().textContent === "This browser cannot show notifications for this page. The page's title counts the open questions.", 'not available: ' + said().textContent);
});
</script>`
	runPageInEngines(t, page, settingsPageModules())
}
