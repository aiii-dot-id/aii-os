//go:build !windows

package dashboard

import "testing"

// THE MODEL PAGE DRAWS THE APPROVED SIGN-IN WORDS. The words lived in
// the Model strings while the page drew its own: "Cancel sign-in", and each
// adoptable credential named by the file it is read from. This reads each
// word from the page and compares it with its key, so a literal put back
// in the page fails here.
const modelSignInWordsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import WM from './strings/settings-model.js';
import { assert, run } from './__harness.js';
run(() => {
  const claude = { name: 'Claude (Max/Pro)', api_type: 'anthropic', endpoint: 'https://api.anthropic.test', models: ['m1'], default_model: 'm1',
    can_sign_in: true, credential: 'claude-code',
    signin: { status: 'pending', manual: true, url: 'https://authority.example/authorize' } };
  S.providers = [claude];
  S.identityExists = true; S.providersLoaded = true;
  S.config = { llm: { provider: claude.name, model: 'm1', resolved_provider: claude.name, resolved_model: 'm1', timeout_seconds: 120 }, credential_kinds: ['claude-code', 'codex'] };
  renderSettings();
  S.settingsSection('providers'); S.requestSettingsSection(); renderSettings();
  document.querySelector('[data-prov-row="0"]').click();
  const stack = document.querySelector('#settings-stack');
  const cancel = stack.querySelector('[data-prov-signin-cancel]');
  assert(cancel, 'a sign-in under way offers no way to stop it');
  assert(cancel.textContent === WM.signInCancel,
    'the stop button says ' + JSON.stringify(cancel.textContent) + ', not ' + JSON.stringify(WM.signInCancel));
  const said = v => { const o = stack.querySelector('option[value="' + v + '"]'); return o ? o.textContent : '(no choice)'; };
  assert(said('claude-code') === WM.adoptClaude, 'the Claude choice says ' + JSON.stringify(said('claude-code')) + ', not ' + JSON.stringify(WM.adoptClaude));
  assert(said('codex') === WM.adoptChatGPT, 'the ChatGPT choice says ' + JSON.stringify(said('codex')) + ', not ' + JSON.stringify(WM.adoptChatGPT));
  assert(stack.textContent.includes(WM.adoptHint), 'the adopted-credential hint is not drawn: ' + stack.textContent.slice(0, 400));
});
</script>`

func TestTheModelPageDrawsTheSignInWordsJamesApproved(t *testing.T) {
	runPageInEngines(t, modelSignInWordsPage, substrateModules(t))
}
