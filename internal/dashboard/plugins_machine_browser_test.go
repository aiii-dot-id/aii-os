//go:build !windows

package dashboard

import "testing"

const pluginsMachinePage = `<!doctype html>
<div class="app"><div class="stack" id="plugins-stack"></div></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { sent } from './ws.js';
import { renderPlugins } from './views/plugins.js';

run(() => {
  const stack = document.getElementById('plugins-stack');
  const card = () => stack.querySelector('[data-machine]');
  const show = machine => { S.config = { plugins: { autoload: 'T2', machine: machine } }; renderPlugins(); return card(); };

  let c = show({ state: 'linked', build: '0.1.16, a1b2c3d4e5f6', linked: 2 });
  assert(c && c.dataset.machine === 'linked' && c.textContent.includes('machine service: running, build 0.1.16, a1b2c3d4e5f6') && c.textContent.includes('2 identities linked'),
    'the running line: ' + (c && c.textContent));
  assert(!c.querySelector('[data-machine-update]'), 'no Update now when nothing waits');

  c = show({ state: 'linked', build: '0.1.16, a1b2c3d4e5f6', linked: 1, update: 'build 9f8e7d6c5b4a', can_force: true });
  assert(c.textContent.includes('1 identity linked'), 'one identity, singular: ' + c.textContent);
  assert(c.textContent.includes("the machine service's update waits for idle (build 9f8e7d6c5b4a)"), 'the waiting update: ' + c.textContent);
  const force = c.querySelector('[data-machine-update]');
  assert(force && force.textContent === 'Update now', 'Update now is beside the waiting update');
  force.click();
  assert(sent.some(m => m.type === 'plugin' && m.plugin.action === 'machine_update'), 'Update now sends the force: ' + JSON.stringify(sent));
  assert(force.disabled && force.textContent === 'Updating…' && c.querySelector('[data-machine-note]').textContent.includes('hands over now'), 'Update now answers beside itself: ' + c.textContent);

  c = show({ state: 'linked', build: '0.1.16, a1b2c3d4e5f6', linked: 1, update: 'build 9f8e7d6c5b4a', can_force: false,
    missing: ["the machine service (0.1.16, a1b2c3d4e5f6) does not speak update.force, which the Plugins page's Update now needs; this identity is 0.1.17, 0123456789ab"] });
  assert(!c.querySelector('[data-machine-update]'), 'no Update now where the service does not speak it');
  assert(c.querySelector('.machine-missing') && c.querySelector('.machine-missing').textContent.includes('does not speak update.force') && c.textContent.includes('0.1.17, 0123456789ab'),
    'the uncovered line names the message and both builds: ' + c.textContent);

  c = show({ state: 'linked', build: '0.1.16, a1b2c3d4e5f6', linked: 2, store: '/home/a/.aii/plugins' });
  assert(c.querySelector('.machine-store') && c.querySelector('.machine-store').textContent === "plugins' models and runtimes: the account's plugin store at /home/a/.aii/plugins",
    'the card names the plugin store: ' + c.textContent);

  c = show({ state: 'not running', store: '/home/a/.aii/plugins', reason: 'dial unix /run/user/0/aii-machine/link: connect: no such file or directory', remedy: 'its service manager restarts it; to start it now: systemctl --user start aii-os-machine.service' });
  assert(c.querySelector('.machine-store'), 'the store is named while the service is not running too: ' + c.textContent);
  assert(c.dataset.machine === 'not running' && c.textContent.includes('machine service: not running — dial unix') && c.querySelector('.machine-remedy').textContent.includes('systemctl --user start aii-os-machine.service'),
    'not running says why and what to do: ' + c.textContent);

  c = show({ state: 'refused', reason: 'the slot /home/a/.aii/identity-4 is not recorded, and this account\'s startup registrations do not name it', remedy: 'register it: aii register identity-4' });
  assert(c.textContent.includes('refused this identity — the slot') && c.querySelector('.machine-remedy').textContent === 'register it: aii register identity-4',
    'a refusal says why and what to do: ' + c.textContent);

  c = show({ state: 'not running', failing: true, reason: 'the machine service keeps failing: started 5 times in the last 10m0s and never answered; no identity starts it again until that window passes. Its last failure: state.json does not read',
    remedy: 'run aii machine at a prompt to see it fail', warning: "the machine service's sign-in entry is switched off in Task Manager → Startup apps, so it does not start when you sign in; switch it on there" });
  assert(c.querySelector('.machine-failing') && c.textContent.includes('keeps failing') && c.textContent.includes('state.json does not read'), 'a service that keeps failing says so and why: ' + c.textContent);
  assert(c.querySelector('.machine-warning') && c.querySelector('.machine-warning').textContent.includes('switched off in Task Manager'), 'a switched-off sign-in entry is said with its remedy: ' + c.textContent);

  S.config = { plugins: { autoload: 'T2' } }; renderPlugins();
  assert(!card(), 'no line where there is no machine service');
});
</script>`

func TestTheMachineServiceIsOnThePluginsPageInBrowser(t *testing.T) {
	runPageInEngines(t, pluginsMachinePage, map[string][]byte{
		"/views/settings.js": []byte(`export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection() {}
export function savebarHTML() { return ''; } export function sendConfigChanges() {} export function configFeedbackHTML() { return ''; }`),
		"/ws.js": []byte(`export const sent = []; export function send(m) { sent.push(m); return true; }`),
	})
}
