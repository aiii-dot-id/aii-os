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
const speechPickersPage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { frames } from './ws.js';
import { renderSettings, acceptProviderSave, acceptSpeechLists, rejectSpeechLists } from './views/settings.js';
import { assert, run } from './__harness.js';

run(async () => {
  const byId = id => document.getElementById(id);
  const choose = (id, value) => { const el = byId(id); el.value = value; el.dispatchEvent(new Event('change')); return el; };
  const options = id => [...byId(id).options].map(o => o.textContent).join(' | ');
  const values = id => [...byId(id).options].map(o => o.value).join('|');
  const asks = () => frames.filter(f => f.type === 'query' && f.query === 'speech_lists');
  const save = dir => document.querySelector('[data-save="speech_' + dir + '"]').click();
  const svc = (name, speech, extra) => Object.assign({ name, endpoint: 'https://' + name.toLowerCase() + '.test', speech, added: true, has_key: true, chats: false }, extra || {});
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [{ name: 'ElevenLabs', chat: false, has_key: true }, { name: 'Cartesia', chat: false, has_key: false }];
  S.config = { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 },
    speech: {
      stt: { provider: '', model: '', language: '' },
      tts: { provider: 'ElevenLabs', model: '', voice: '', endpoint: 'https://elevenlabs.test/v1/text-to-speech' },
      services: [
        svc('ElevenLabs', { tts: { model_required: true, voice_required: true, lists_models: true, lists_voices: true, searches_voices: true } }),
        svc('Cartesia', { tts: { voice_required: true, lists_voices: true, searches_voices: true } }, { has_key: false }),
      ],
    } };
  S.openSettings('speech');

  // THE SERVICE IS ASKED THE MOMENT IT IS THE CHOSEN ONE, and the wait says so.
  assert(asks().length === 1 && asks()[0].provider === 'ElevenLabs' && asks()[0].direction === 'tts', 'the chosen service was not asked: ' + JSON.stringify(asks()));
  assert(byId('sp-voice-state-tts').textContent === 'Asking ElevenLabs…', 'the wait is not said: ' + byId('sp-voice-state-tts').textContent);
  assert(byId('sp-language-row-tts') && byId('sp-language-row-tts').hidden, 'a language field shown before the service named any language');

  // WHAT IT ANSWERS IS WHAT THE OPERATOR PICKS FROM — by name, never an id.
  assert(acceptSpeechLists({ provider: 'ElevenLabs', direction: 'tts',
    models: [{ id: 'eleven_flash_v2_5', name: 'Eleven Ember v2.5', languages: ['en', 'de'] }],
    voices: [{ id: 'v-1', name: 'Rachel', detail: 'american · female · young', languages: ['en'] },
             { id: 'v-2', name: 'Adam', detail: 'british · male', languages: ['de'] }],
    languages: [{ id: 'de', name: 'German' }, { id: 'en', name: 'English' }],
    models_listed: true, voices_listed: true, models_complete: true, voices_complete: true }), 'the answer was not taken');
  assert(options('sp-voice-tts').includes('Rachel — american · female · young'), 'the voices do not read as names: ' + options('sp-voice-tts'));
  assert(values('sp-voice-tts').includes('v-1'), 'the id is not what the field sends: ' + values('sp-voice-tts'));
  assert(options('sp-model-tts').includes('Eleven Ember v2.5'), 'the models do not read as names: ' + options('sp-model-tts'));
  assert(byId('sp-voice-state-tts').textContent === '2 voices from ElevenLabs.', 'the voices are not counted: ' + byId('sp-voice-state-tts').textContent);
  assert(byId('sp-language-tts') && options('sp-language-tts') === 'Any language the service hears | German (de) | English (en)',
    'the languages the service named are not offered: ' + (byId('sp-language-tts') ? options('sp-language-tts') : 'no field'));

  // THE LANGUAGE NARROWS THE VOICES UNDER IT.
  choose('sp-language-tts', 'de');
  assert(values('sp-voice-tts').split('|').includes('v-2') && !values('sp-voice-tts').split('|').includes('v-1'),
    'the language did not narrow the voices: ' + values('sp-voice-tts'));
  choose('sp-language-tts', '');
  assert(values('sp-voice-tts').split('|').includes('v-1'), 'clearing the language did not bring the voices back');

  // THE VENDOR DOES THE SEARCHING, after the operator stops typing.
  const before = asks().length;
  byId('sp-find-tts').value = 'adam';
  byId('sp-find-tts').dispatchEvent(new Event('input'));
  assert(asks().length === before, 'the vendor was asked on every keystroke');
  await new Promise(r => setTimeout(r, 400));
  const searched = asks().at(-1);
  assert(asks().length === before + 1 && searched.search === 'adam' && searched.provider === 'ElevenLabs',
    'the search did not go to the vendor: ' + JSON.stringify(asks()));

  // A CHOICE ALREADY MADE SURVIVES A LIST THAT NARROWED UNDER IT.
  byId('sp-voice-tts').value = 'v-1';
  acceptSpeechLists({ provider: 'ElevenLabs', direction: 'tts', search: 'adam',
    voices: [{ id: 'v-2', name: 'Adam' }], voices_listed: true, voices_complete: false });
  assert(byId('sp-voice-tts').value === 'v-1' && values('sp-voice-tts').split('|').includes('v-1'),
    'a search threw away the voice already chosen: ' + values('sp-voice-tts'));
  assert(byId('sp-voice-state-tts').textContent === '1 voice from ElevenLabs — narrow the search to see the rest.',
    'a list cut short did not say so: ' + byId('sp-voice-state-tts').textContent);

  // AN ID CAN ALWAYS BE TYPED: a voice minted a minute ago is reachable
  // before any list has heard of it.
  choose('sp-voice-tts', 'by-id:');
  assert(!byId('sp-voice-tts-typed').hidden, 'there is no way to enter an id the list does not hold');
  byId('sp-voice-tts-typed').value = 'v-minted-now';
  save('tts');
  const sent = frames.filter(f => f.type === 'config_set').at(-1);
  assert(sent && sent.config['speech.tts.voice'] === 'v-minted-now', 'the id typed by hand was not what was saved: ' + JSON.stringify(sent));

  // A SERVICE WITH NO KEY IS NOT ASKED — that is a next step, not an error
  // — and a field its API never reads is not shown at all.
  choose('sp-provider-tts', 'Cartesia');
  assert(!byId('sp-model-tts'), 'a model was asked for by a service that never reads one');
  const asked = asks().length;
  acceptSpeechLists({ provider: 'Cartesia', direction: 'tts', needs_key: true });
  assert(byId('sp-voice-state-tts').textContent === 'Cartesia lists its voices once a key is stored above.',
    'a service with no key does not say what it needs: ' + byId('sp-voice-state-tts').textContent);

  // A SERVICE THAT REFUSES SAYS SO IN ITS OWN WORDS.
  acceptSpeechLists({ provider: 'Cartesia', direction: 'tts', voices_error: 'Cartesia refused the list (401): invalid api key' });
  assert(byId('sp-voice-state-tts').textContent === 'Cartesia refused the list (401): invalid api key — check the key.',
    'a refusal is not carried in the service\'s words: ' + byId('sp-voice-state-tts').textContent);

  // A KEY JUST TYPED FILLS THE LISTS — no save, no round trip through
  // the file, no proving the key twice.
  const beforeKey = asks().length, savesBefore = frames.filter(f => f.type === 'config_set' || f.type === 'speech_service').length;
  byId('sp-key-tts').value = 'ck-typed-now';
  byId('sp-key-tts').dispatchEvent(new Event('input'));
  await new Promise(r => setTimeout(r, 500));
  const keyed = asks().at(-1);
  assert(asks().length === beforeKey + 1 && keyed.api_key === 'ck-typed-now' && keyed.provider === 'Cartesia',
    'the typed key was not asked with: ' + JSON.stringify(asks().slice(-1)));
  assert(frames.filter(f => f.type === 'config_set' || f.type === 'speech_service').length === savesBefore,
    'asking with a typed key saved something');
  acceptSpeechLists({ provider: 'Cartesia', direction: 'tts', voices: [{ id: 'c-2', name: 'Barbershop Man' }], voices_listed: true, voices_complete: true });
  assert(values('sp-voice-tts').split('|').includes('c-2'), 'the lists did not fill from the typed key: ' + values('sp-voice-tts'));

  // THE FIRST ANSWER THAT NAMES LANGUAGES DOES NOT EMPTY THE KEY. It used
  // to redraw the whole card — the typed key vanished, the pickers were
  // asked again without it and emptied.
  byId('sp-key-tts').value = 'ck-typed-and-kept';
  byId('sp-key-tts').dispatchEvent(new Event('input'));
  await new Promise(r => setTimeout(r, 500));
  const askedWithKey = asks().length;
  assert(acceptSpeechLists({ provider: 'Cartesia', direction: 'tts',
    voices: [{ id: 'c-3', name: 'Newsreader', languages: ['fr'] }], languages: [{ id: 'fr', name: 'French' }],
    voices_listed: true, voices_complete: true }), 'the answer with languages was not taken');
  assert(byId('sp-key-tts').value === 'ck-typed-and-kept', 'the typed key was emptied by the answer: ' + JSON.stringify(byId('sp-key-tts').value));
  assert(values('sp-voice-tts').split('|').includes('c-3'), 'the pickers did not fill from the answer: ' + values('sp-voice-tts'));
  assert(!byId('sp-language-row-tts').hidden && options('sp-language-tts').includes('French (fr)'), 'the languages were not offered in place');
  assert(asks().length === askedWithKey, 'the answer caused another question: ' + JSON.stringify(asks().slice(askedWithKey)));

  // CHANGING THE ENGINE NEVER SENDS THE OLD ENGINE'S KEY TO THE NEW ONE.
  choose('sp-provider-tts', 'ElevenLabs');
  const afterChange = asks().filter(a => a.provider === 'ElevenLabs').at(-1);
  assert(afterChange && !afterChange.api_key, 'another service\'s key was sent to the new engine: ' + JSON.stringify(afterChange));
  choose('sp-provider-tts', 'Cartesia');

  // A REFUSED QUESTION IS SAID ON THE CARD, and can be asked again.
  byId('sp-find-tts').value = 'zzz';
  byId('sp-find-tts').dispatchEvent(new Event('input'));
  await new Promise(r => setTimeout(r, 400));
  const refused = asks().at(-1);
  assert(rejectSpeechLists(refused.request_id, 'Cartesia is not answering'), 'the refusal was not claimed');
  assert(byId('sp-voice-state-tts').textContent.includes('not answering'), 'the refusal is not on the card: ' + byId('sp-voice-state-tts').textContent);

  // AN ANSWER TO AN OLDER QUESTION CHANGES NOTHING: the narrow search was
  // asked last; the broad one arriving late must not fill the picker.
  byId('sp-find-tts').value = 'ad';
  byId('sp-find-tts').dispatchEvent(new Event('input'));
  await new Promise(r => setTimeout(r, 400));
  byId('sp-find-tts').value = 'adam';
  byId('sp-find-tts').dispatchEvent(new Event('input'));
  await new Promise(r => setTimeout(r, 400));
  acceptSpeechLists({ provider: 'Cartesia', direction: 'tts', search: 'ad', voices: [{ id: 'c-broad', name: 'Broad' }], voices_listed: true, voices_complete: true });
  assert(!values('sp-voice-tts').split('|').includes('c-broad'), 'an older question\'s answer filled the picker');
  acceptSpeechLists({ provider: 'Cartesia', direction: 'tts', search: 'adam', voices: [{ id: 'c-adam', name: 'Adam' }], voices_listed: true, voices_complete: true });
  assert(values('sp-voice-tts').split('|').includes('c-adam'), 'the current question\'s answer did not fill the picker');
  // ...AND STILL NOTHING ONCE THE CURRENT ONE WAS ANSWERED: with no question
  // pending, an older answer landing last must not take the picker back
  // (review of 0.1.5, finding 6).
  acceptSpeechLists({ provider: 'Cartesia', direction: 'tts', search: 'ad', voices: [{ id: 'c-stale', name: 'Stale' }], voices_listed: true, voices_complete: true });
  assert(!values('sp-voice-tts').split('|').includes('c-stale') && values('sp-voice-tts').split('|').includes('c-adam'),
    'an older answer landing after the current one took the picker back: ' + values('sp-voice-tts'));

  // A LATE ANSWER FOR AN ENGINE NO LONGER CHOSEN CHANGES NOTHING ON SCREEN.
  acceptSpeechLists({ provider: 'ElevenLabs', direction: 'tts', voices: [{ id: 'v-9', name: 'Late' }], voices_listed: true });
  assert(!values('sp-voice-tts').split('|').includes('v-9'), 'a late answer for another engine filled this one: ' + values('sp-voice-tts'));
  assert(asks().length >= asked, 'asking went backwards');
});
</script>`

func TestSettingsSpeechPickersAreTheServicesOwnAnswers(t *testing.T) {
	runPageInEngines(t, speechPickersPage, substrateModules(t))
}
