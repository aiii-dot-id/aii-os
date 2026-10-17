//go:build !windows

package dashboard

import (
	"strings"
	"testing"
)

func TestConnectedAccountsCardInBrowser(t *testing.T) {
	page := `<!doctype html>
<div id="plugins-stack"></div>
<script type="module">
import { assert, run } from './__harness.js';
import { S } from './state.js';
import { renderPlugins, connectPrefill } from './views/plugins.js';
import { frames } from './ws.js';
window.open = () => ({ closed: false, close() {} });
window.confirm = () => true;
run(() => {
  S.config = { plugins: { autoload: 'T1', installed: [], catalog: [], catalog_url: '',
    auth_profiles: [
      { name: 'gh', scheme: 'oauth2', provider: 'github', client_id: 'cid', has_client_secret: true, scopes: ['repo'], hosts: ['api.github.com:443'], state: 'disconnected', handles: ['org.example.issues'], can_device: true, revocation: 'Revocation is not available for github: disconnecting removes the credential from this host only; revoke the grant on github\'s account page.' },
      { name: 'google-cal', scheme: 'oauth2', provider: 'google', client_id: 'g', scopes: ['https://www.googleapis.com/auth/calendar.readonly'], hosts: ['www.googleapis.com:443'], state: 'connected', expires_at: '2026-09-12T21:00:00Z', signin: {status:'pending',device: { user_code: 'WXYZ-1234', verification_uri: 'https://auth.example/device', expires: '2026-09-12T20:30:00Z' } } },
      { name: 'tw', scheme: 'basic', host: 'api.twilio.com', port: 443, secret_source: 'file', state: 'static' },
      { name: 'manual', scheme: 'oauth2', provider: 'manual-service', scopes: [], hosts: ['api.example:443'], state: 'disconnected', signin:{status:'pending',manual:true,url:'https://authority.example/authorize'} },
      { name: 'lent', scheme: 'oauth2', provider: 'github', client_id: 'cid', scopes: ['repo'], hosts: ['api.github.com:443'], state: 'disconnected', can_device: true, borrowed: 'Borrowed: /home/op/tool/token.json is outside this identity\'s credentials directory, so it is read and never written. When its token expires, refresh it with its own tool. Disconnect forgets it and leaves the file.' },
    ],
    providers: [
      { name: 'github', services: [{ name: 'issues', read: ['read:user'], modify: ['public_repo'] }], hosts: ['api.github.com:443'], device: true },
      { name: 'google', services: [{ name: 'calendar', read: ['…calendar.readonly'], modify: ['…calendar.events'] }], hosts: ['www.googleapis.com:443'], device: true, device_excludes: ['calendar'] },
      { name: 'manual-service', services: [], hosts: ['api.example:443'], sign_in:'manual', redirect_uri:'https://authority.example/registered-return' },
    ] } };
  renderPlugins();
  const st = document.getElementById('plugins-stack');
  const text = st.textContent;
  assert(text.includes('CONNECTED ACCOUNTS'), 'the card is missing');
  assert(text.includes('used by org.example.issues'), 'the profile names who uses it');
  assert(text.includes('secret: set') && !text.includes('csecret'), 'a secret is said to be set, never shown');
  assert(text.includes('WXYZ-1234'), 'a pending device code is shown on its profile');
  assert(text.includes('api.twilio.com:443') && text.includes('edited in the config file'), 'a header-scheme profile is listed read-only');
  const revocation = st.querySelector('[data-profile-revocation="gh"]');
  assert(revocation && revocation.textContent.includes('not available for github') && revocation.textContent.includes('account page'), 'the profile says what revocation there is, with the route');
  assert(st.querySelector('[data-profile-revocation="google-cal"]') === null, 'a profile with nothing to say about revocation says nothing');
  const borrowed = st.querySelector('[data-profile-borrowed="lent"]');
  assert(borrowed && borrowed.textContent.includes('/home/op/tool/token.json') && borrowed.textContent.includes('its own tool'), 'a borrowed token file is said, with where it is and how it is renewed');
  assert(st.querySelector('[data-profile-borrowed="gh"]') === null, 'a profile with a token file of its own says nothing of borrowing');
  assert(st.querySelector('[data-profile-connect="lent"]') === null && st.querySelector('[data-profile-device="lent"]') === null, 'no sign-in is offered to a borrowed file');
  assert(st.querySelector('[data-profile-disconnect="lent"]') !== null, 'Disconnect is offered for a borrowed file whatever its state');
  assert(st.querySelector('[data-profile-disconnect="gh"]') === null, 'a disconnected profile of its own has nothing to disconnect');
  let asked = '';
  window.confirm = q => { asked = q; return true; };
  st.querySelector('[data-profile-delete="lent"]').click();
  assert(asked.includes('left where it is') && !asked.includes('tokens and client secret are removed'), 'deleting a borrowed profile says its token file stays: ' + asked);
  assert(frames.some(f => f.type === 'auth_profile_delete' && f.profile === 'lent'), 'Delete sends the profile');
  window.confirm = () => true;
  assert(st.querySelector('[data-profile-device="gh"]') !== null, 'the device route is offered where the authority allows it');
  assert(st.querySelector('[data-profile-device="google-cal"]') === null, 'no device route for a profile it cannot serve');
  const manual=st.querySelector('[data-signin-name="manual"]');
  assert(manual, 'a manual profile lacks its completion control');
  manual.querySelector('input').value='profile-code#profile-state'; manual.requestSubmit();
  assert(frames.at(-1).type==='profile_signin_complete' && frames.at(-1).profile==='manual' && frames.at(-1).input==='profile-code#profile-state', 'manual code did not reach its profile');
  st.querySelector('[data-profile-connect="gh"]').click();
  assert(frames.some(f => f.type === 'profile_signin' && f.profile === 'gh'), 'Connect asks the host for the authorize URL');
  st.querySelector('[data-profile-disconnect="google-cal"]').click();
  assert(frames.some(f => f.type === 'profile_disconnect' && f.profile === 'google-cal'), 'Disconnect sends the profile');
  st.querySelector('[data-profile-new]').click();
  const sel = document.getElementById('pf-provider');
  sel.value = 'github';
  sel.dispatchEvent(new Event('change'));
  const secret = document.getElementById('pf-secret');
  assert(secret !== null && secret.type === 'password' && secret.value === '', 'the secret field is a password field, empty');
  assert(document.getElementById('pf-hosts').value === 'api.github.com:443', 'the hosts are prefilled from the template');
  document.getElementById('pf-name').value = 'gh2';
  document.getElementById('pf-client').value = 'cid2';
  secret.value = 'topsecret';
  document.querySelector('.profile-form input[name="svc-issues"][value="read"]').click();
  st.querySelector('[data-profile-save]').click();
  const set = frames.find(f => f.type === 'auth_profile_set');
  assert(set.profile_edit.redirect_uri === location.origin + '/oauth/callback', 'callback uses the browser address, even when the Go host is remote');
  assert(set && set.profile_edit.name === 'gh2' && set.profile_edit.client_secret === 'topsecret' && set.profile_edit.services.issues === 'read' && set.profile_edit.hosts[0] === 'api.github.com:443', 'the form sends the profile whole: ' + JSON.stringify(set));
  assert(!document.getElementById('pf-secret') || document.getElementById('pf-secret').value === '', 'the secret is cleared from the page once sent');

  const providers = S.config.plugins.providers;
  let pre = connectPrefill({ connector: 'google-calendar', scope: 'read' }, providers);
  assert(pre.provider === 'google' && pre.services.calendar === 'read' && pre.name === 'google-calendar', 'calendar → google, read: ' + JSON.stringify(pre));
  pre = connectPrefill({ connector: 'org.example.github-issues', scope: 'modify' }, providers);
  assert(pre.provider === 'github' && pre.services.issues === 'modify', 'github → issues, modify: ' + JSON.stringify(pre));
  pre = connectPrefill({ connector: 'acme', scope: 'read' }, providers);
  assert(pre.provider === 'custom' && Object.keys(pre.services).length === 0, 'an unknown word is custom: ' + JSON.stringify(pre));
  const installed = [{ id: 'com.aiii.examples.google-calendar', settings: [{ key: 'google', type: 'secret', oauth: { provider: 'google', services: ['calendar'] } }] }];
  pre = connectPrefill({ connector: 'com.aiii.examples.google-calendar', scope: 'read' }, providers, installed);
  assert(pre.provider === 'google' && pre.services.calendar === 'read' && pre.handle === 'google' && pre.plugin === 'com.aiii.examples.google-calendar', 'the plugin\'s hint prefills: ' + JSON.stringify(pre));
  S.connectRequest = { connector: 'gmail', scope: 'read' };
  renderPlugins();
  assert(document.getElementById('pf-provider') && document.getElementById('pf-provider').value === 'google', 'the form opens on the connector\'s template');
  assert(document.getElementById('plugins-stack').textContent.includes('Your identity asked to connect gmail'), 'the form says why it opened');
  assert(S.connectRequest === null, 'the request is consumed');
  document.getElementById('pf-provider').value='manual-service';
  document.getElementById('pf-provider').dispatchEvent(new Event('change'));
  assert(document.getElementById('pf-redirect').value==='https://authority.example/registered-return', 'manual profile replaced the registered provider return with the dashboard address');
});
</script>`
	modules := map[string][]byte{}
	for _, path := range []string{"static/views/plugins.js", "static/signin.js", "static/pending.js"} {
		data, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		modules["/"+strings.TrimPrefix(path, "static/")] = data
	}
	modules["/state.js"] = []byte(`export const S = { stats: { name: 'TestIdentity' }, providers: [], config: null, providersLoaded: false };`)
	modules["/util.js"] = utilStub(`export const $ = id => document.getElementById(id); export const esc = v => String(v ?? '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;'); export const hueOf = () => 0;`)
	modules["/ws.js"] = []byte(`export const frames = []; export function send(f) { frames.push(f); return 'req-1'; } export function wsReady() { return true; } export function query(n, e) { return send(Object.assign({ type: 'query', query: n }, e || {})); }`)
	modules["/views/settings.js"] = []byte(`export const saved = []; export function captureForm(){} export function restoreForm(){} export function forgetForm(){} export function saveConfigSection(section) { saved.push(section); }
export function savebarHTML(section, note, more) { return '<div class="savebar"><button class="btn" data-save="' + section + '">' + ((more && more.label) || 'Save') + '</button><span class="savenote">' + note + '</span>' + ((more && more.also) || '') + '</div>'; } export const sent = []; export function sendConfigChanges(section, ch) { saved.push(section); sent.push(ch); } export function configFeedbackHTML() { return ''; }`)
	runPageInEngines(t, page, modules)
}
