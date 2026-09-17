//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
const providersSpeechOnlyPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings } from './views/settings.js';
import { assert, run } from './__harness.js';
run(() => {
  S.identityExists = true; S.providersLoaded = true;
  S.providers = [
    { name: 'ElevenLabs', chat: false, endpoint: 'https://api.elevenlabs.io', models: [], speech: { tts: {} } },
    { name: 'OpenAI', chat: true, api_type: 'openai', endpoint: 'https://api.openai.test', models: ['gpt'], default_model: 'gpt', speech: { tts: {} } },
    { name: 'Deepgram', chat: false, endpoint: 'https://api.deepgram.com', models: [], speech: { stt: {} } },
  ];
  S.config = { llm: { provider: 'OpenAI', model: 'gpt', resolved_provider: 'OpenAI', resolved_model: 'gpt', timeout_seconds: 120 },
    speech: { stt: { provider: '' }, tts: { provider: '' }, services: [] } };
  renderSettings();
  document.querySelector('[data-sec="providers"]').click();
  const rows = [...document.querySelectorAll('[data-prov-row]')];
  assert(rows.length === 1 && rows[0].dataset.provRow === '1' && rows[0].textContent.includes('OpenAI'),
    'the Providers card lists something other than the chat providers, or lost the entry index: ' + rows.map(r => r.dataset.provRow + ':' + r.textContent.trim().slice(0, 24)).join(' | '));
  const link = document.querySelector('#settings-stack [data-open-section="speech"]');
  const note = link ? link.parentElement.textContent : 'no link';
  assert(link && note.includes('2 speech-only services (ElevenLabs, Deepgram) are on Settings'), 'the speech-only services are not pointed to Settings → Speech: ' + note);
  rows[0].click();
  const name = document.getElementById('pv-name-1');
  assert(name && name.value === 'OpenAI', 'the editor opened for the wrong entry');
  link.click();
  assert(document.getElementById('sp-provider-tts'), 'the link did not open Settings → Speech');
});
</script>`

func TestSpeechOnlyServicesLiveOnSettingsSpeechAlone(t *testing.T) {
	runPageInEngines(t, providersSpeechOnlyPage, substrateModules(t))
}
