//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
const providersBrokenPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptProviderSave } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true; S.providersLoaded = true;
  S.providers = [{ name: 'A', chat: true, api_type: 'openai', endpoint: 'https://a.test', models: ['m'], default_model: 'm' }];
  S.brokenProviders = [
    { position: 1, sha256: 'aa', name: 'B', reason: 'the entry is not a provider: json: unknown field "api_kye"' },
    { position: 2, sha256: 'bb', name: 'X', reason: 'providers "A" and "X" are both default', repair: 'clear its default flag' },
    { position: 3, sha256: 'cc', reason: 'provider name is empty' },
  ];
  S.config = { llm: { provider: 'A', model: 'm', resolved_provider: 'A', resolved_model: 'm', timeout_seconds: 120 } };
  renderSettings();
  document.querySelector('[data-sec="providers"]').click();
  const stack = () => document.querySelector('#settings-stack').textContent;
  const rows = [...document.querySelectorAll('[data-prov-broken]')];
  assert(rows.length === 3, 'broken entries are not listed: ' + rows.length);
  assert(stack().includes('3 broken entries in providers.json') && stack().includes('B · broken') && stack().includes('unnamed entry · broken'),
    'the broken entries are not marked: ' + stack().slice(0, 400));
  assert(stack().includes('unknown field "api_kye"') && stack().includes('entry 2 in providers.json'), 'a broken entry lost its reason or its place: ' + stack().slice(0, 400));
  const repairs = [...document.querySelectorAll('[data-prov-repair]')];
  const removes = [...document.querySelectorAll('[data-prov-remove-broken]')];
  assert(repairs.length === 1 && repairs[0].dataset.provRepair === '2', 'Repair offered where no repair was named: ' + repairs.map(b => b.dataset.provRepair));
  assert(removes.length === 3, 'Remove is not offered for every broken entry');
  assert(stack().includes('clear its default flag'), 'Repair does not say what it changes');
  assert(!document.querySelector('[data-prov-row="1"]'), 'a broken entry was offered for editing as a provider');

  repairs[0].click();
  let sent = frames.at(-1);
  assert(sent.type === 'provider_repair' && sent.position === 2 && sent.entry_sha256 === 'bb', 'repair sent ' + JSON.stringify(sent));
  assert(stack().includes('Repairing X… waiting for the runtime to confirm.'), 'repair shows no pending state: ' + stack().slice(0, 300));
  S.brokenProviders = S.brokenProviders.filter(b => b.sha256 !== 'bb');
  assert(acceptProviderSave(sent.request_id), 'the repair answer was not claimed');
  renderSettings();
  assert(stack().includes('Repaired — X.'), 'the repair result is not reported: ' + stack().slice(0, 300));

  document.querySelector('[data-prov-remove-broken="1"]').click();
  sent = frames.at(-1);
  assert(sent.type === 'provider_remove_broken' && sent.position === 1 && sent.entry_sha256 === 'aa', 'remove sent ' + JSON.stringify(sent));
  assert(acceptProviderSave(sent.request_id), 'the removal answer was not claimed');
  renderSettings();
  assert(stack().includes('Acknowledged, but B is still listed as broken.'), 'a removal that did not take was reported as done: ' + stack().slice(0, 300));
});
</script>`

func TestBrokenProvidersAreShownWithRepairAndRemove(t *testing.T) {
	runPageInEngines(t, providersBrokenPage, substrateModules(t))
}
