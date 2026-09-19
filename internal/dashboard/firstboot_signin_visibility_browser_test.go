//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
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
  // The setting reaches the birth form too: off, a valid token still offers sign-in.
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
