//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
// .
// .
// .
const providerSignInVisibilityPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const claude = { name: 'Claude (Max/Pro)', api_type: 'anthropic', endpoint: 'https://api.anthropic.test', models: ['m1'], default_model: 'm1',
    can_sign_in: true, credential: 'claude-code',
    credential_info: { kind: 'claude-code', plan: 'max', expires_at: '2099-01-01T00:00:00Z' } };
  S.providers = [claude];
  S.identityExists = true; S.providersLoaded = true;
  S.config = { llm: { provider: claude.name, model: 'm1', resolved_provider: claude.name, resolved_model: 'm1', timeout_seconds: 120 }, credential_kinds: ['claude-code', 'codex'] };
  renderSettings();
  document.querySelector('[data-sec="providers"]').click();
  document.querySelector('[data-prov-row="0"]').click();
  const button = () => document.querySelector('#settings-stack [data-prov-signin]');
  const stack = () => document.querySelector('#settings-stack').textContent;
  assert(!button(), 'a valid adopted token still offers sign-in');
  // The editor's own line, not the row note above it (which prints the minute, not the second).
  assert(stack().includes('usable to 2099-01-01 00:00:00 UTC'), 'a valid token is not announced by the editor: ' + stack().slice(0, 300));
  claude.credential_info = { kind: 'claude-code', plan: 'max', expires_at: '2026-01-01T00:00:00Z', expired: true };
  renderSettings();
  assert(button(), 'an expired token does not offer sign-in');
  claude.credential_info = { kind: 'claude-code', error: 'no credential file' };
  renderSettings();
  assert(button(), 'an unreadable or missing token does not offer sign-in');
  delete claude.credential_info;
  renderSettings();
  assert(button(), 'no credential fact at all does not offer sign-in');
  claude.credential_info = { kind: 'claude-code', plan: 'max', expires_at: '2099-01-01T00:00:00Z' };
  claude.signin = { status: 'pending', manual: true, url: 'https://authority.example/authorize' };
  renderSettings();
  assert(document.querySelector('#settings-stack [data-signin-complete]') && document.querySelector('#settings-stack [data-prov-signin-cancel]'),
    'a sign-in under way lost its progress or its cancel beside a valid token');
  // THE RUNTIME'S OWN VERDICT COUNTS FIRST: the file vanished after it was
  // read, the probe says no_credential, the description still says usable.
  delete claude.signin;
  claude.status = 'no_credential'; claude.status_reason = 'no claude-code credentials at ~/.claude/.credentials.json — sign in to this provider from Settings';
  renderSettings();
  assert(button(), 'a credential the runtime refused does not offer sign-in');
  claude.status = 'ok'; delete claude.status_reason;
  // A BOUNDARY THAT PASSED WHILE THE PAGE WAS OPEN: no expired flag, a usable time already behind the clock.
  const past = new Date(Date.now() - 60000).toISOString();
  claude.credential_info = { kind: 'claude-code', plan: 'max', expires_at: past };
  renderSettings();
  assert(button(), 'a usable time already past does not offer sign-in');
  assert(stack().includes('EXPIRED ' + past.replace('T', ' ').replace('Z', ' UTC')), 'the editor still announces a usable time already past as usable: ' + stack().slice(0, 300));
  // A SIGN-IN THAT ENDED beside a valid token still says how it ended, and offers nothing to press.
  claude.credential_info = { kind: 'claude-code', plan: 'max', expires_at: '2099-01-01T00:00:00Z' };
  claude.signin = { status: 'failed' };
  renderSettings();
  assert(!button() && stack().includes('Sign-in failed.'), 'a failed sign-in beside a valid token lost its notice or offered the button: ' + stack().slice(0, 300));
  // THE OPERATOR MAY ASK FOR SIGN-IN BESIDE A VALID TOKEN: with
  // dashboard.skip_signin_with_valid_token off, the button is offered anyway.
  delete claude.signin;
  S.skipSignInWithValidToken = false;
  renderSettings();
  assert(button(), 'with the setting off, a valid token still hides sign-in');
  S.skipSignInWithValidToken = true;
  renderSettings();
  assert(!button(), 'with the setting back on, a valid token still offers sign-in');
});
</script>`

func TestSignInControlsAppearOnlyWithoutAValidToken(t *testing.T) {
	runPageInEngines(t, providerSignInVisibilityPage, substrateModules(t))
}
