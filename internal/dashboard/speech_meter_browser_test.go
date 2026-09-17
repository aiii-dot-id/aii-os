//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
const speechMeterPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  const byId = id => document.getElementById(id);
  const svc = (name, speech) => ({ name: name, endpoint: 'https://' + name.toLowerCase() + '.test', speech: speech, added: true, has_key: true, chats: false });
  const sets = () => frames.filter(f => f.type === 'config_set').map(f => f.config);
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [];
  S.config = { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 },
    speech: {
      stt: { provider: 'Deepgram', model: 'nova-3', language: '', monthly_minutes: 0, endpoint: 'https://deepgram.test/v1/listen' },
      tts: { provider: 'ElevenLabs', model: 'eleven_flash_v2_5', voice: 'v-1', monthly_characters: 30000, endpoint: 'https://elevenlabs.test/v1/text-to-speech' },
      services: [
        svc('ElevenLabs', { tts: { model_required: true, voice_required: true, lists_models: true, lists_voices: true } }),
        svc('Deepgram', { stt: { model_required: true, lists_models: true } }),
      ],
      spent: [
        { provider: 'ElevenLabs', direction: 'tts', requests: 37, characters: 12480 },
        { provider: 'Cartesia', direction: 'tts', requests: 1, characters: 400 },
        { provider: 'Deepgram', direction: 'stt', requests: 22, seconds: 252 },
      ],
      resets: '1 October',
    } };
  S.openSettings('speech');

  // SPOKEN: every service that spent, counted in full, with the ceiling.
  const tts = byId('sp-spent-tts').textContent;
  assert(tts.includes('ElevenLabs 12,480 characters over 37 replies'), 'the service that spent is not named: ' + tts);
  assert(tts.includes('Cartesia 400 characters over 1 reply'), 'a service tried and left is not shown: ' + tts);
  assert(tts.includes('17,120 characters of the ceiling left'), 'what is left of the ceiling is not said: ' + tts);
  assert(tts.includes('resets 1 October'), 'the day it lifts is not said: ' + tts);

  // HEARD: the same meter, in the clock its bill uses.
  const stt = byId('sp-spent-stt').textContent;
  assert(stt.includes('Deepgram 4m 12s over 22 utterances'), 'listening is not read as a clock: ' + stt);
  assert(!stt.includes('of the ceiling left'), 'a ceiling nobody set was accounted for: ' + stt);
  // MINUTES ARE NOT SECONDS: a listening ceiling of 60 minutes with 4m 12s
  // used has 55m 48s left, not nothing.
  S.config.speech.stt.monthly_minutes = 60;
  renderSettings();
  assert(byId('sp-spent-stt').textContent.includes('55m 48s of the ceiling left'), 'the listening ceiling was measured in the wrong unit: ' + byId('sp-spent-stt').textContent);
  S.config.speech.stt.monthly_minutes = 0;
  renderSettings();
  assert(!stt.includes('ElevenLabs') && !stt.includes('Cartesia'), 'what was spoken was billed to the microphone: ' + stt);
  assert(!tts.includes('Deepgram'), 'what was heard was billed to the replies: ' + tts);

  // THE CEILING IS THE CARD'S OWN FIELD, and it is saved with it.
  const ceiling = byId('sp-ceiling-tts');
  assert(ceiling && ceiling.value === '30000', 'the ceiling in force is not shown: ' + (ceiling && ceiling.value));
  assert(byId('sp-ceiling-stt').value === '' && byId('sp-ceiling-stt').placeholder === 'no ceiling', 'no ceiling does not read as none');
  ceiling.value = '50000';
  document.querySelector('[data-save="speech_tts"]').click();
  const saved = sets().pop();
  assert(saved && saved['speech.tts.monthly_characters'] === 50000, 'the ceiling was not saved with the card: ' + JSON.stringify(saved));

  // AND EMPTYING IT IS A DECISION, not a field left behind. (The button
  // that asked is busy until its answer arrives, so the answer comes
  // first — the card says so beside it.)
  S.config.speech.tts.monthly_characters = 50000;
  acceptSettingsConfig(frames.filter(f => f.type === 'config_set').pop().request_id);
  renderSettings();
  byId('sp-ceiling-tts').value = '';
  document.querySelector('[data-save="speech_tts"]').click();
  assert(sets().pop()['speech.tts.monthly_characters'] === 0, 'an emptied ceiling did not clear: ' + JSON.stringify(sets().pop()));

  // NOTHING SPENT IS AN ANSWER.
  S.config.speech.spent = [];
  renderSettings();
  assert(byId('sp-spent-tts').textContent.includes('nothing yet'), 'a month with no spending reads as an empty space: ' + byId('sp-spent-tts').textContent);
});
</script>`

func TestSettingsSpeechShowsWhatTheVoiceCostInBrowser(t *testing.T) {
	runPageInEngines(t, speechMeterPage, substrateModules(t))
}
