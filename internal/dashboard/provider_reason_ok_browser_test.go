//go:build !windows

package dashboard

import "testing"

const providerReasonOKPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { renderProviderOptions } from './firstboot.js';
import { assert, run } from './__harness.js';
run(() => {
  const why = 'no key was sent: OPENAI_API_KEY is OpenAI\'s own key variable and goes only to https://api.openai.com, and this entry is at https://relay.test';
  const relay = { name: 'OpenAI', api_type: 'openai', endpoint: 'https://relay.test/v1', models: ['m1'], default_model: 'm1', chat: true, status: 'ok', status_reason: why };
  const plain = { name: 'Gateway', api_type: 'openai', endpoint: 'https://gateway.test/v1', models: ['m1'], default_model: 'm1', chat: true, status: 'ok' };
  S.providers = [relay, plain];
  S.identityExists = true; S.providersLoaded = true;
  S.config = { llm: { provider: relay.name, model: 'm1', resolved_provider: relay.name, resolved_model: 'm1', timeout_seconds: 120 }, credential_kinds: [] };
  renderSettings();
  document.querySelector('[data-sec="providers"]').click();
  const stack = () => document.querySelector('#settings-stack').textContent;
  assert(stack().includes(why), 'a row whose status is ok does not say the reason sent with it: ' + stack().slice(0, 400));
  delete relay.status_reason;
  renderSettings();
  assert(!stack().includes('no key was sent'), 'a row with no reason shows one: ' + stack().slice(0, 400));

  relay.status_reason = why;
  S.identityExists = false;
  const sel = document.getElementById('fb-provider');
  const line = () => document.getElementById('fb-cred-why').textContent;
  const pick = v => { sel.value = v; sel.onchange(); };
  renderProviderOptions();
  pick('0');
  assert(line() === why, 'the birth form does not say the reason of an entry whose status is ok: ' + line());
  pick('1');
  assert(line() === '', 'an entry with no reason carries another\'s: ' + line());
});
</script>`

func TestARowThatAnswersStillSaysWhyNoKeyWasSent(t *testing.T) {
	runPageInEngines(t, providerReasonOKPage, substrateModules(t))
}
