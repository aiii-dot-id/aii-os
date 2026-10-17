//go:build !windows

package dashboard

import "testing"

const firstbootSignInVisibilityPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderProviderOptions } from './firstboot.js';
import { assert, run } from './__harness.js';
run(() => {
  const claude = { name: 'Claude', endpoint: 'https://example.test', models: ['c1'], default_model: 'c1', can_sign_in: true, credential: 'claude-code',
    credential_info: { kind: 'claude-code', plan: 'max', expires_at: '2099-01-01T00:00:00Z' } };
  S.providers = [claude];
  renderProviderOptions();
  const sel = document.getElementById('fb-provider');
  sel.value = '0'; sel.onchange();
  const start = () => document.getElementById('fb-signin-start');
  const row = () => document.getElementById('fb-signin').textContent;
  assert(!start(), 'a valid adopted token still offers sign-in at birth');
  claude.credential_info = { kind: 'claude-code', plan: 'max', expires_at: '2026-01-01T00:00:00Z', expired: true };
  renderProviderOptions();
  assert(start(), 'an expired token does not offer sign-in at birth');
  claude.credential_info = { kind: 'claude-code', plan: 'max', expires_at: '2099-01-01T00:00:00Z' };
  claude.signin = { status: 'failed' };
  renderProviderOptions();
  assert(!start() && row().includes('Sign-in failed.'), 'a failed sign-in beside a valid token lost its notice or offered the button: ' + row());
  claude.signin = { status: 'pending', device: { user_code: 'ABCD', verification_uri: 'https://auth.example/device' } };
  renderProviderOptions();
  assert(start() && document.getElementById('fb-signin-cancel') && row().includes('ABCD'), 'a sign-in under way lost its code, its button or its cancel: ' + row());
  delete claude.signin;
  renderProviderOptions();
  assert(!start(), 'a valid token offered sign-in at birth with the setting on');
  S.skipSignInWithValidToken = false;
  renderProviderOptions();
  assert(start(), 'with the setting off, a valid token still hides sign-in at birth');
});
</script>`

func TestBirthOffersSignInOnlyWithoutAValidToken(t *testing.T) {
	runPageInEngines(t, firstbootSignInVisibilityPage, substrateModules(t))
}

const firstbootWhyPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderProviderOptions } from './firstboot.js';
import { assert, run } from './__harness.js';
run(() => {
  const preWhy = 'a working claude-code credential is on this machine — no key needed';
  S.providers = [
    { name: 'Claude', models: ['c1'], default_model: 'c1', credential: 'claude-code', status: 'ok', preselect: true, preselect_why: preWhy },
    { name: 'ChatGPT', models: ['g1'], default_model: 'g1', status: 'ok' },
    { name: 'Acme', models: [], status: 'auth_required', status_reason: 'needs an API key to list models' },
    { name: 'Local server', endpoint: 'http://127.0.0.1:8080/v1', local: true, models: [], chat: true, status: 'unreachable', status_reason: 'the stored address did not answer' },
  ];
  const sel = document.getElementById('fb-provider');
  const why = () => document.getElementById('fb-cred-why').textContent;
  const pick = v => { sel.value = v; sel.onchange(); };
  renderProviderOptions();
  assert(sel.value === '0' && why() === preWhy, 'the preselected provider lost its reason: ' + why());
  pick('1');
  assert(why() === '', 'a working provider the operator chose carries another provider\'s reason: ' + why());
  renderProviderOptions();
  assert(sel.value === '1' && why() === '', 'a re-render put the preselected provider\'s reason back under the chosen one: ' + why());
  pick('2');
  renderProviderOptions();
  assert(why() === 'needs an API key to list models', 'a provider that cannot work does not say why: ' + why());
  pick('3');
  renderProviderOptions();
  assert(why() === '', 'a local server shows the probe\'s verdict on its stored address, not the answer its typed address gives: ' + why());
  pick('0');
  assert(why() === preWhy, 'returning to the preselected provider lost its reason: ' + why());
  pick('');
  assert(why() === '', 'no provider chosen, yet a reason is shown: ' + why());
});
</script>`

func TestTheBirthHintSpeaksForTheSelectedProvider(t *testing.T) {
	runPageInEngines(t, firstbootWhyPage, substrateModules(t))
}
