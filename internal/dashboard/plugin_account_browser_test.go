//go:build !windows

package dashboard

import "testing"

func TestAnAccountIsChosenOnThePluginCardInBrowser(t *testing.T) {
	probe := `
import { S } from '/state.js';
import { frames } from '/ws.js';
import { renderPlugins } from '/views/plugins.js';
import { acceptSettingsConfig, rejectSettingsConfig, forgetForm } from '/views/settings.js';
import { run } from '/__harness.js';
run(assert => {
  window.confirm = () => true;
  const id = 'com.aiii.examples.gmail', host = 'gmail.googleapis.com:443', key = 'plugins.settings.' + id + '.google';
  const narrow = "google-narrow's token rides only to www.googleapis.com:443, not to " + host + ", where this plugin's signed package sends its requests — every call would be refused";
  const offers = [{ name: 'google-mail', state: 'connected' }, { name: 'google-mail2', state: 'connected' }, { name: 'google-narrow', state: 'connected', not: narrow }];
  const setting = (account, more) => Object.assign({ key: 'google', type: 'secret', title: 'Google account', required: true,
    oauth: { provider: 'google', services: ['gmail'] }, key_host: host, handles: [], account: account }, more || {});
  const card = (s, grants) => ({ secrets_ok: true, llm: {}, dashboard: {}, prompt: {}, witness: {}, logs: {}, agency: {}, restart_required: [],
    plugins: { autoload: 'T2', catalog: [], catalog_url: '', auth_profiles: [],
      providers: [{ name: 'google', services: [{ name: 'gmail', read: ['gmail.readonly'], modify: ['gmail.modify'] }], hosts: ['www.googleapis.com:443', host] }],
      installed: [{ id: id, version: '0.1.0', tier: 'T2', mode: 'wasm', variant: 'linux-x86_64-wasm', capabilities: ['net.outbound:' + host], grants: grants || {}, settings: [s] }] } });
  S.view = 'plugins'; S.providers = []; S.providersLoaded = true;
  S.config = card(setting({ hosts: [host], offers: offers }));
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  const pick = () => st.querySelector('select[data-pset-key="google"]');
  const said = () => st.querySelector('[data-pacct-grants]').textContent;
  const state = () => st.querySelector('[data-pacct-state]').textContent;
  const bar = () => st.querySelector('[data-said="plugin:' + id + '"]')?.textContent || '';
  const save = () => st.querySelector('[data-save="plugin:' + id + '"]').click();

  const options = [...pick().options].map(o => o.value + (o.disabled ? ' (disabled)' : ''));
  assert(options.join(',') === ',google-mail,google-mail2,google-narrow (disabled)', 'the accounts offered: ' + options);
  assert(st.textContent.includes('google-narrow: ' + narrow), 'an account that cannot be chosen says why');
  assert(st.querySelector('[data-pkey-input]') === null, 'a setting that asks for an account takes no pasted key');
  assert(state() === 'no account chosen yet', 'state: ' + state());
  assert(said() === 'Saving grants ' + id + ' the handle of the account you choose and the host ' + host + ', the one its signed package declares — nothing else.', 'before a choice: ' + said());

  pick().value = 'google-mail';
  pick().dispatchEvent(new Event('change', { bubbles: true }));
  const disclosure = 'Saving grants ' + id + ' the handle google-mail and the host ' + host + ', the one its signed package declares: ' + id + ' will call ' + host + ' as google-mail.';
  assert(said() === disclosure, 'the disclosure: ' + said());
  renderPlugins();
  assert(pick().value === 'google-mail' && said() === disclosure, 'a re-render lost the choice or its disclosure');

  let before = frames.length;
  save();
  assert(frames.length === before + 1, 'the Save sent ' + (frames.length - before) + ' messages');
  const sent = frames.at(-1);
  assert(sent.type === 'config_set' && JSON.stringify(sent.config) === JSON.stringify({ [key]: 'google-mail' }), 'the message: ' + JSON.stringify(sent));

  const refusal = key + ': ' + narrow;
  assert(rejectSettingsConfig(refusal, sent.request_id), 'the refusal was not claimed by the save that asked');
  renderPlugins();
  assert(bar().includes(refusal), 'the refusal beside Save: ' + bar());
  assert(pick().value === 'google-mail', 'a refusal lost the choice');

  save();
  const again = frames.at(-1);
  assert(again.config[key] === 'google-mail', 'the retried save carries the choice');
  S.config = card(setting({ hosts: [host], offers: offers, granted: true }, { value: 'google-mail', effective: 'google-mail', handles: ['google-mail'] }), { listed: true, hosts: [host], handles: ['google-mail'] });
  assert(acceptSettingsConfig(again.request_id), 'the save was not correlated');
  renderPlugins();
  assert(bar().includes('Saved'), 'the save is confirmed beside Save: ' + bar());
  assert(state() === 'google-mail — connected', 'state: ' + state());
  assert(said() === 'Granted: the handle google-mail and the host ' + host + ' — ' + id + ' calls ' + host + ' as google-mail. Saving it again changes nothing.', 'granted: ' + said());
  save();
  const same = frames.at(-1);
  assert(same.type === 'config_set' && !(key in same.config), 'saving the same choice again sent the account: ' + JSON.stringify(same));
  assert(acceptSettingsConfig(same.request_id), 'the second save was not correlated');

  pick().value = 'google-mail2';
  pick().dispatchEvent(new Event('change', { bubbles: true }));
  assert(said() === 'Saving grants ' + id + ' the handle google-mail2 and the host ' + host + ', the one its signed package declares: ' + id + ' will call ' + host + ' as google-mail2, in place of google-mail.', 'replacing: ' + said());

  pick().value = 'google-mail';
  forgetForm(st);
  S.config = card(setting({ hosts: [host], offers: [{ name: 'google-mail', state: 'disconnected' }], granted: true }, { value: 'google-mail', handles: ['google-mail'] }));
  renderPlugins();
  assert(state().startsWith('google-mail is not connected: every call is refused'), 'disconnected: ' + state());
  S.config = card(setting({ hosts: [host], offers: [], granted: true }, { value: 'google-gone', handles: ['google-gone'] }));
  renderPlugins();
  assert(state() === 'google-gone no longer exists on this identity: choose another account, or Remove', 'gone: ' + state());
  const gone = [...pick().options].find(o => o.value === 'google-gone');
  assert(gone && gone.disabled && gone.selected, 'a gone account stays shown as chosen, and cannot be saved');
  assert(said().startsWith('Saving grants ' + id + ' the handle of the account you choose'), 'a gone account is not said to be granted: ' + said());

  before = frames.length;
  st.querySelector('[data-pkey-remove]').click();
  const cleared = frames.at(-1);
  assert(frames.length === before + 1 && cleared.type === 'plugin_key_clear' && cleared.plugin_key.plugin === id && cleared.plugin_key.key === 'google' && !('secret' in cleared.plugin_key), 'Remove: ' + JSON.stringify(cleared));

  S.config = card(setting({ hosts: [host], offers: [] }));
  renderPlugins();
  const connect = st.querySelector('[data-pacct-connect]');
  assert(connect && connect.textContent === 'Connect a google account…', 'with nothing to choose the card offers to connect one');
  connect.click();
  assert(document.getElementById('pf-provider') && document.getElementById('pf-provider').value === 'google', 'the New profile form opens on the hint\'s authority');
  const read = st.querySelector('.profile-form input[name="svc-gmail"][value="read"]');
  assert(read && read.checked, 'the hint\'s service is prefilled');
  assert(st.querySelector('.profile-form').textContent.includes('then choose it for Google account on ' + id + '\'s card'), 'the form says where the account is chosen');

  S.config = card(setting({ not: 'plugin ' + id + ' declares no outbound host in its signed package' }));
  renderPlugins();
  assert(st.textContent.includes('an account cannot be connected here: plugin ' + id + ' declares no outbound host'), 'the card says why no account can be connected');
  assert(pick() === null && st.querySelector('[data-pacct-connect]') === null && st.querySelector('[data-pkey-remove]') === null, 'and offers no choice');
  S.config = card(setting({ not: 'plugin ' + id + ' declares no outbound host in its signed package' }, { value: 'google-old' }));
  renderPlugins();
  assert(st.querySelector('[data-pacct-not]').textContent.endsWith('— google-old is stored for it'), 'the stored choice is named: ' + st.querySelector('[data-pacct-not]').textContent);
  st.querySelector('[data-pkey-remove]').click();
  assert(frames.at(-1).type === 'plugin_key_clear' && frames.at(-1).plugin_key.key === 'google', 'a stored choice can still be removed');

  const second = 'pubsub.googleapis.com:443';
  S.config = card(setting({ hosts: [host, second], offers: [{ name: 'google-mail', state: 'connected' }] }));
  renderPlugins();
  assert(said() === 'Saving grants ' + id + ' the handle of the account you choose and the hosts ' + host + ', ' + second + ', the ones its signed package declares — nothing else.', 'several hosts, before a choice: ' + said());
  pick().value = 'google-mail';
  pick().dispatchEvent(new Event('change', { bubbles: true }));
  assert(said() === 'Saving grants ' + id + ' the handle google-mail and the hosts ' + host + ', ' + second + ', the ones its signed package declares: ' + id + ' will call ' + host + ', ' + second + ' as google-mail.', 'several hosts, chosen: ' + said());
});`
	modules := formRecoveryModules()
	modules["/probe.js"] = []byte(probe)
	runPageInEnginesWithHeaders(t, formRecoveryMarkup+`<script type="module" src="/probe.js"></script>`, modules,
		map[string]string{"Content-Security-Policy": uiCSP})
}
