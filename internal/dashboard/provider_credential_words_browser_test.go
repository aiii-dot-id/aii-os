//go:build !windows

package dashboard

import "testing"

const providerCredentialWordsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  const claude = { name: 'Claude (Max/Pro)', api_type: 'anthropic', endpoint: 'https://api.anthropic.test', models: ['m1'], default_model: 'm1',
    credential: 'claude-code', status: 'credential_expired',
    credential_info: { kind: 'claude-code', plan: 'max', expires_at: '2026-01-01T00:00:00Z', expired: true } };
  S.providers = [claude];
  S.identityExists = true; S.providersLoaded = true;
  S.config = { llm: { provider: claude.name, model: 'm1', resolved_provider: claude.name, resolved_model: 'm1', timeout_seconds: 120 }, credential_kinds: ['claude-code', 'codex'] };
  renderSettings();
  document.querySelector('[data-sec="providers"]').click();
  const stack = () => document.querySelector('#settings-stack');
  const tips = () => [...stack().querySelectorAll('[title]')].map(e => e.getAttribute('title'));
  assert(tips().includes('adopted credential expired — sign in again, or refresh it with the tool it is borrowed from'),
    'the expired dot does not name both remedies: ' + JSON.stringify(tips()));
  document.querySelector('[data-prov-row="0"]').click();
  const text = stack().textContent;
  assert(text.includes('To use the claude-code credential at another address, name that address on the entry in providers.json (credential_options.base_url).'),
    'the credential line does not say how the claude-code credential goes to another address: ' + text.slice(0, 600));
  assert(!text.includes('supplies its own endpoint'), 'the credential line still says it supplies its own endpoint');
});
</script>`

func TestTheCredentialWordsOnTheProvidersCardAreTrueOfIt(t *testing.T) {
	runPageInEngines(t, providerCredentialWordsPage, substrateModules(t))
}
