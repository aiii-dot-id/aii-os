//go:build !windows

// .
// .

package dashboard

import "testing"

// .
// .
// .
// .
// .
const speechSettingsPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptProviderSave, rejectProviderSave, acceptSettingsConfig, rejectSettingsConfig } from './views/settings.js';
import { assert, run } from './__harness.js';

run(() => {
  const byId = id => document.getElementById(id);
  const stack = () => byId('settings-stack').textContent;
  const save = dir => document.querySelector('[data-save="speech_' + dir + '"]').click();
  const choose = (id, value) => { const el = byId(id); el.value = value; el.dispatchEvent(new Event('change')); return el; };
  const offered = el => [...el.options].map(o => o.value).join('|');
  const sent = type => frames.filter(f => f.type === type).length;
  const svc = (name, speech, extra) => Object.assign({ name, endpoint: 'https://' + name.toLowerCase() + '.test', speech, added: false, has_key: false, chats: false }, extra || {});
  const heard = { model_required: true, lists_models: true };
  const spoken = { model_required: true, voice_required: true, lists_models: true, lists_voices: true };
  const own8000 = 'OpenAI-compatible · localhost:8000', own9000 = 'OpenAI-compatible · 127.0.0.1:9000';
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [
    { name: 'OpenAI', chat: true, models: ['gpt-5.5'], default_model: 'gpt-5.5', has_key: false },
    { name: 'Anthropic', chat: true, models: ['m'], default_model: 'm', has_key: true },
    { name: 'Hume', chat: false, has_key: true },
    { name: own8000, chat: false, has_key: false },
  ];
  S.config = { llm: { provider: 'OpenAI', model: 'gpt-5.5', resolved_provider: 'OpenAI', resolved_model: 'gpt-5.5', timeout_seconds: 120 },
    speech: {
      stt: { provider: '', model: '', language: '', monthly_minutes: 0 },
      tts: { provider: 'OpenAI', model: 'gpt-4o-mini-tts', voice: 'marin', endpoint: 'https://api.openai.com/v1/audio/speech', monthly_characters: 0 },
      services: [
        svc('OpenAI', { stt: heard, tts: spoken }, { added: true, chats: true, api_key_env: 'OPENAI_API_KEY', endpoint: 'https://api.openai.com/v1' }),
        svc('Deepgram', { stt: heard, tts: heard }, { api_key_env: 'DEEPGRAM_API_KEY' }),
        svc('Hume', { tts: { voice_required: true, lists_voices: true } }, { added: true, has_key: true }),
        svc(own8000, { stt: heard, tts: spoken }, { added: true, custom: true, endpoint: 'http://localhost:8000/v1' }),
      ],
    } };
  S.openSettings('speech', 'sp-provider-stt');
  assert(document.querySelector('[data-sec="speech"]'), 'Settings does not list Speech');
  assert(document.activeElement === byId('sp-provider-stt'), 'the way in from the microphone does not land on the engine field');

  // ONE PICKER PER DIRECTION, THE SERVICES BY NAME.
  assert(offered(byId('sp-provider-stt')) === '|OpenAI|Deepgram|' + own8000 + '|own-server:', 'input engines: ' + offered(byId('sp-provider-stt')));
  assert(offered(byId('sp-provider-tts')) === '|OpenAI|Deepgram|Hume|' + own8000 + '|own-server:', 'reply engines: ' + offered(byId('sp-provider-tts')));
  assert(byId('sp-provider-stt').options[0].textContent === 'Off' && byId('sp-provider-tts').options[0].textContent === 'Browser voice', 'the first engines are not Off and Browser voice');
  assert([...byId('sp-provider-tts').options].at(-1).textContent === 'OpenAI-compatible…', 'no way to name an OpenAI-compatible server');
  assert(byId('sp-provider-stt').title === 'No speech-to-text engine. The microphone opens this section until one is chosen.', 'a blank engine does not explain itself: ' + byId('sp-provider-stt').title);
  assert(byId('sp-readback-stt').textContent === 'Voice input is off.' && byId('sp-key-row-stt').hidden, 'input that is off asks for something');
  const replies = byId('sp-readback-tts').textContent;
  assert(replies.includes('https://api.openai.com/v1/audio/speech') && replies.includes('key none'), 'the readback does not say what is in force: ' + replies);

  // THE KEY IS SHARED WITH A CHAT PROVIDER OF THE SAME NAME, AND SAYS SO.
  assert(!byId('sp-key-row-tts').hidden && byId('sp-key-label-tts').textContent === 'API KEY (NONE STORED)', 'no place for the chosen engine\'s key: ' + byId('sp-key-label-tts').textContent);
  assert(byId('sp-key-tts').title === 'No key is stored for OpenAI — cloud speech services refuse a request without one.', 'a missing key does not explain itself: ' + byId('sp-key-tts').title);
  assert(byId('sp-key-note-tts').textContent.indexOf('Shared with the OpenAI chat provider') === 0 && byId('sp-key-note-tts').textContent.includes('OPENAI_API_KEY'), 'the shared key is not named as shared: ' + byId('sp-key-note-tts').textContent);
  assert(byId('sp-base-row-tts').hidden, 'a vendor engine asks for a base URL');

  // ONLY THE FIELDS THIS ENGINE'S API READS. Hume names a voice and no
  // model; the card drawn for it says so, and the readback still says what
  // is in force until the choice is saved.
  choose('sp-provider-tts', 'Hume');
  assert(byId('sp-voice-tts') && !byId('sp-model-tts'), 'Hume was asked for a model it never reads');
  assert(byId('sp-key-label-tts').textContent === 'API KEY (ONE STORED — ENTER TO REPLACE)' && byId('sp-key-tts').title === 'Blank keeps the key stored for Hume.', 'a stored key is not said to be kept');
  assert(byId('sp-readback-tts').textContent.includes('https://api.openai.com/v1/audio/speech'), 'the readback stopped saying what is still in force');
  choose('sp-provider-tts', 'OpenAI');
  assert(byId('sp-model-tts') && byId('sp-voice-tts'), 'OpenAI asks for both a model and a voice');

  // THE OPERATOR'S OWN SERVER IS NAMED BY WHERE IT ANSWERS.
  choose('sp-provider-stt', own8000);
  assert(!byId('sp-base-row-stt').hidden && byId('sp-base-stt').value === 'http://localhost:8000/v1', 'the own server does not show where it answers');
  assert(byId('sp-key-note-stt').textContent === 'Kept with ' + own8000 + ' in providers.json.', 'the own server\'s key note: ' + byId('sp-key-note-stt').textContent);
  const settingsBefore = sent('config_set');
  save('stt');
  const straight = frames.at(-1);
  assert(straight.type === 'config_set' && straight.config['speech.stt.provider'] === own8000 && sent('config_set') === settingsBefore + 1,
    'a server at the address it already has did not go straight to its settings: ' + JSON.stringify(straight));
  // THE BUTTON THAT WAS PRESSED SAYS IT IS WORKING, and what comes back
  // stands beside it — not at the top of a page the operator scrolled past.
  const saveButton = () => document.querySelector('[data-save="speech_stt"]');
  assert(saveButton().disabled && saveButton().textContent === 'Checking…', 'the button does not say it is working: ' + saveButton().textContent);
  assert(!document.querySelector('#settings-stack > .config-result'), 'the answer was put at the top of the page');
  assert(rejectSettingsConfig('voice input refused: ' + own8000 + ' did not transcribe a moment of silence: connection refused', straight.request_id), 'the refusal was not claimed');
  const said = document.querySelector('[data-said="speech_stt"]');
  assert(said && said.textContent.includes('connection refused'), 'the refusal is not beside the button that asked: ' + stack());
  assert(saveButton().textContent === 'Save' && !saveButton().disabled, 'the button stayed busy after the answer');
  assert(!document.querySelector('#settings-stack > .config-result'), 'the answer was also put at the top of the page');
  assert(!document.querySelector('[data-said="speech_tts"]'), 'the other card was told about a save it did not make');

  // A NEW ADDRESS IS THE ENTRY NAMED FOR IT, never the old name pointed elsewhere.
  choose('sp-provider-stt', own8000);
  byId('sp-base-stt').value = 'http://127.0.0.1:9000/v1';
  save('stt');
  const moved = frames.at(-1);
  assert(moved.type === 'speech_service' && moved.provider === own9000 && moved.base_url === 'http://127.0.0.1:9000/v1' && moved.api_key === '',
    'a new address was not made ready first, as the entry named for it: ' + JSON.stringify(moved));
  S.providers.push({ name: own9000, chat: false, has_key: false });
  assert(acceptProviderSave(moved.request_id), 'the own server was not accepted');
  const ownSettings = frames.at(-1);
  assert(ownSettings.type === 'config_set' && ownSettings.config['speech.stt.provider'] === own9000, 'the settings did not follow the own server: ' + JSON.stringify(ownSettings));
  assert(rejectSettingsConfig('voice input refused: connection refused', ownSettings.request_id), 'the refusal was not claimed');

  // A NEW SERVER NEEDS AN ADDRESS THAT NAMES IT.
  ['', 'localhost:8000', 'ftp://localhost:8000'].forEach(address => {
    choose('sp-provider-stt', 'own-server:');
    assert(!byId('sp-base-row-stt').hidden && byId('sp-key-label-stt').textContent === 'API KEY (OPTIONAL)', 'a new server does not ask for its address with an optional key');
    byId('sp-base-stt').value = address;
    const before = frames.length;
    save('stt');
    assert(frames.length === before && stack().includes('needs the address its server answers at'), 'a server at "' + address + '" was sent: ' + stack());
  });

  // A SHIPPED SERVICE NOT YET ADDED IS ADDED, WITH THE KEY, BEFORE ITS SETTINGS.
  const settingsBeforeAdd = sent('config_set');
  choose('sp-provider-stt', 'Deepgram');
  byId('sp-key-stt').value = 'typed-key';
  save('stt');
  const add = frames.at(-1);
  assert(add.type === 'speech_service' && add.provider === 'Deepgram' && add.api_key === 'typed-key' && !add.base_url, 'the service was not added first: ' + JSON.stringify(add));
  assert(rejectProviderSave('providers: that key is not the shape Deepgram issues', add.request_id) && stack().includes('not the shape Deepgram issues'), 'a refused add hid why');
  assert(sent('config_set') === settingsBeforeAdd, 'a refused add sent settings anyway');

  choose('sp-provider-stt', 'Deepgram');
  byId('sp-key-stt').value = 'typed-key';
  save('stt');
  const again = frames.at(-1);
  S.providers.push({ name: 'Deepgram', chat: false, has_key: false });
  assert(acceptProviderSave(again.request_id), 'the add reply was not claimed');
  renderSettings();
  assert(sent('config_set') === settingsBeforeAdd && stack().includes('came back with no key stored'), 'settings followed a key that did not take: ' + stack());

  choose('sp-provider-stt', 'Deepgram');
  byId('sp-key-stt').value = 'typed-key';
  choose('sp-model-stt', 'by-id:');
  byId('sp-model-stt-typed').value = 'nova-3-general';
  save('stt');
  const keyedAdd = frames.at(-1);
  S.providers.at(-1).has_key = true;
  assert(acceptProviderSave(keyedAdd.request_id), 'the keyed add was not claimed');
  const set = frames.at(-1);
  assert(set.type === 'config_set' && set.config['speech.stt.provider'] === 'Deepgram' && set.config['speech.stt.model'] === 'nova-3-general',
    'the settings did not follow the add: ' + JSON.stringify(set));
  S.config.speech.stt = { provider: 'Deepgram', model: 'nova-3-general', language: '', monthly_minutes: 0, endpoint: 'https://deepgram.test/v1/listen', api_key_masked: '••••-key' };
  assert(acceptSettingsConfig(set.request_id), 'the checked save was not accepted');
  renderSettings();
  assert(stack().includes('Active — Deepgram transcribed the check.'), 'the save does not say the service answered: ' + stack());

  // AN ADDED ENGINE WITH NOTHING TO STORE GOES STRAIGHT TO ITS SETTINGS.
  choose('sp-voice-tts', 'by-id:');
  byId('sp-voice-tts-typed').value = 'nobody';
  save('tts');
  const direct = frames.at(-1);
  assert(direct.type === 'config_set' && direct.config['speech.tts.provider'] === 'OpenAI' && direct.config['speech.tts.voice'] === 'nobody',
    'an added engine with nothing to store did not go straight to its settings: ' + JSON.stringify(direct));
  assert(rejectSettingsConfig('voice replies refused: OpenAI did not speak one word: voice not found', direct.request_id) && stack().includes('voice not found'), 'the vendor\'s words are not shown');

  // A POINTER AT A PROVIDER THAT IS NOT A SPEECH SERVICE STAYS VISIBLE, AS WHAT IT IS.
  S.config.speech.stt = { provider: 'Anthropic', model: '', language: '' };
  renderSettings();
  const stale = [...byId('sp-provider-stt').options].find(o => o.value === 'Anthropic');
  assert(stale && stale.selected && stale.textContent === 'Anthropic (not a speech service)', 'the pointer in force vanished from its list: ' + offered(byId('sp-provider-stt')));
  assert(byId('sp-key-label-stt').textContent === 'API KEY (ONE STORED — ENTER TO REPLACE)' && byId('sp-key-note-stt').textContent === 'Shared with the Anthropic chat provider.',
    'the pointer in force lost its entry\'s key facts: ' + byId('sp-key-label-stt').textContent + ' / ' + byId('sp-key-note-stt').textContent);
});
</script>`

func TestSettingsSpeechOffersEnginesByName(t *testing.T) {
	runPageInEngines(t, speechSettingsPage, substrateModules(t))
}
