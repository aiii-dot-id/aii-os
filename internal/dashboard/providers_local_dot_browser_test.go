//go:build !windows

package dashboard

import "testing"

// A SERVER ON THIS COMPUTER THAT IS NOT RUNNING IS NOT A FAULT. AII OS
// ships the usual local addresses so first run can find a model there;
// most people run none of them, and a red dot would say each is broken.
// It is grey and says it is not running; a service elsewhere that does not
// answer stays red.
const providersLocalDotPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true; S.providersLoaded = true;
  S.providers = [
    { name: 'Ollama', chat: true, local: true, api_type: 'openai', endpoint: 'http://127.0.0.1:11434/v1', models: [], status: 'unreachable' },
    { name: 'Acme', chat: true, api_type: 'openai', endpoint: 'https://api.acme.test/v1', models: [], status: 'unreachable' },
  ];
  S.config = { llm: { provider: 'Acme', model: 'm', resolved_provider: 'Acme', resolved_model: 'm', timeout_seconds: 120 },
    speech: { stt: { provider: '' }, tts: { provider: '' }, services: [] } };
  renderSettings();
  S.settingsSection('providers'); S.requestSettingsSection(); renderSettings();
  const dotOf = i => document.querySelector('[data-prov-row="' + i + '"] span[title]');
  const local = dotOf(0), away = dotOf(1);
  assert(local && local.title === 'not running on this computer' && getComputedStyle(local).backgroundColor === 'rgb(119, 119, 119)',
    'a local server that is not running is not drawn grey and said to be not running: ' + (local ? local.title + ' ' + getComputedStyle(local).backgroundColor : 'no dot'));
  assert(away && away.title === 'did not list its models' && getComputedStyle(away).backgroundColor === 'rgb(192, 57, 43)',
    'a service elsewhere that does not answer is no longer red: ' + (away ? away.title + ' ' + getComputedStyle(away).backgroundColor : 'no dot'));
});
</script>`

func TestALocalServerThatIsNotRunningIsGreyNotRed(t *testing.T) {
	runPageInEngines(t, providersLocalDotPage, substrateModules(t))
}
