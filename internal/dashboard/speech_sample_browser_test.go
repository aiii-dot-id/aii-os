//go:build !windows

package dashboard

import "testing"

// .
// .
// .
// .
// .
// .
const speechSamplePage = substratePageMarkup + `<script type="module">
import { S } from './state.js';
import { renderSettings, acceptSpeechLists } from './views/settings.js';
import { assert, run } from './__harness.js';

run(async () => {
  const byId = id => document.getElementById(id);
  const settle = () => new Promise(r => setTimeout(r, 25));
  const asks = [];
  let minted = 0;
  const wav = () => Promise.resolve({ ok: true, json: () => Promise.resolve({ id: 'sample-' + (++minted) }) });
  let answer = wav;
  window.fetch = (url, opts) => {
    asks.push({ url: url, body: opts && opts.body ? JSON.parse(opts.body) : null });
    return answer();
  };
  const played = [];
  window.Audio = class {
    constructor(src) { played.push(src); }
    play() { return Promise.resolve(); }
    pause() {}
    addEventListener() {}
  };
  const spoken = [];
  const ss = window.speechSynthesis;
  ss.speak = u => { spoken.push(u.text); };
  ss.cancel = () => {};

  const svc = (name, speech) => ({ name: name, endpoint: 'https://' + name.toLowerCase() + '.test', speech: speech, added: false, has_key: false, chats: false });
  S.identityExists = true;
  S.providersLoaded = true;
  S.providers = [];
  S.config = { llm: { provider: 'OpenAI', model: 'm', resolved_provider: 'OpenAI', resolved_model: 'm', timeout_seconds: 120 },
    speech: { stt: { provider: '', model: '', language: '' }, tts: { provider: '', model: '', voice: '' },
      services: [svc('Cartesia', { tts: { voice_required: true, lists_voices: true, searches_voices: true } })] } };
  S.openSettings('speech');

  // NOTHING PICKED, NOTHING TO HEAR.
  assert(byId('sp-sample-tts') && byId('sp-sample-tts').disabled, 'a sample was offered with no engine chosen');

  const engine = byId('sp-provider-tts');
  engine.value = 'Cartesia';
  engine.dispatchEvent(new Event('change'));
  assert(acceptSpeechLists({ provider: 'Cartesia', direction: 'tts',
    voices: [{ id: 'c-2', name: 'Barbershop Man' }], voices_listed: true, voices_complete: true }), 'the voices were not taken');
  byId('sp-voice-tts').value = 'c-2';
  byId('sp-key-tts').value = 'only-typed-1234';

  // WHAT IS IN FORCE AND WHAT IS ONLY CHOSEN ARE DIFFERENT THINGS, and
  // the card must not leave the operator to guess which one it means
  // (reported live: a sample played while the line below named the
  // engine before it).
  const said = byId('sp-readback-tts');
  assert(said && said.textContent.includes('Cartesia is chosen and not saved'),
    'the card did not say the engine is not in force yet: ' + (said ? said.textContent : 'no readback'));

  const button = byId('sp-sample-tts');
  assert(!button.disabled, 'a sample was not offered for a chosen engine');
  button.click();
  assert(button.textContent === 'Playing…' && button.disabled, 'the button did not say it was working: ' + button.textContent);
  await settle();
  assert(asks.length === 1 && asks[0].url === '/speech/say', 'the sample was not asked for: ' + JSON.stringify(asks));
  const ask = asks[0].body;
  assert(ask.sample === true && ask.provider === 'Cartesia' && ask.voice === 'c-2' && ask.api_key === 'only-typed-1234',
    'the card is not what was asked with: ' + JSON.stringify(ask));
  assert(played.length === 1 && played[0] === '/speech/say/sample-1', 'the sample was not played from its own address: ' + played.join(','));
  assert(spoken.length === 0, 'the browser read the sample instead of the service');
  assert(button.textContent === 'Play sample' && !button.disabled, 'the button stayed busy after the sample');
  assert(byId('sp-sample-state').textContent === 'Playing Cartesia.', 'the sample did not say whose voice it was: ' + byId('sp-sample-state').textContent);

  // A REFUSAL STANDS BESIDE THE BUTTON, IN THE SERVICE'S OWN WORDS, and
  // the browser does not step in to read it.
  answer = () => Promise.resolve({ ok: false, status: 502, json: () => Promise.resolve({ error: 'no API key is stored for Cartesia' }) });
  button.click();
  await settle();
  assert(byId('sp-sample-state').textContent === 'no API key is stored for Cartesia',
    'the refusal is not beside the button: ' + byId('sp-sample-state').textContent);
  assert(spoken.length === 0 && played.length === 1, 'a refused sample was read by the browser');
  assert(!button.disabled && button.textContent === 'Play sample', 'the button did not come back after a refusal');

  // AND A NEW PRESS CLEARS WHAT THE LAST ONE SAID.
  answer = wav;
  button.click();
  await settle();
  assert(byId('sp-sample-state').textContent === 'Playing Cartesia.' && played.length === 2, 'the old refusal outlived the sample that worked');

  // A SOURCE THE BROWSER WILL NOT PLAY SAYS SO. Refused by a policy or
  // broken on the wire, the element errors — and some engines resolve
  // play() all the same, which is how a button came to do nothing and
  // say nothing at all.
  // A SERVICE THAT REFUSED AFTER THE ID WAS MINTED IS HEARD IN ITS OWN
  // WORDS. The element errors and play() rejects in the same breath with
  // the browser's words — "no supported source" — and the page asks the
  // host why instead of repeating the browser.
  answer = () => Promise.resolve({ ok: false, status: 502, json: () => Promise.resolve({ error: 'this voice is not on your plan' }) });
  const mintedFirst = wav;
  window.fetch = (url, opts) => { asks.push({ url: url, body: opts && opts.body ? JSON.parse(opts.body) : null }); return opts && opts.method === 'POST' ? mintedFirst() : answer(); };
  window.Audio = class {
    constructor(src) { played.push(src); this.on = {}; this.error = { code: 4 }; setTimeout(() => this.on.error && this.on.error(), 0); }
    play() { const e = new Error('Failed to load because no supported source was found.'); e.name = 'NotSupportedError'; return Promise.reject(e); }
    pause() {}
    addEventListener(name, fn) { this.on[name] = fn; }
  };
  button.click();
  await settle();
  assert(byId('sp-sample-state').textContent === 'this voice is not on your plan',
    'the service\'s own words were not what the operator read: ' + byId('sp-sample-state').textContent);
  window.fetch = (url, opts) => { asks.push({ url: url, body: opts && opts.body ? JSON.parse(opts.body) : null }); return answer(); };
  answer = wav;
  // And the engine that errors at the element while play() never settles
  // — which is how this looked to the operator: nothing, and no reason.
  window.Audio = class {
    constructor(src) { played.push(src); this.on = {}; setTimeout(() => this.on.error && this.on.error(), 0); }
    play() { return new Promise(() => {}); }
    pause() {}
    addEventListener(name, fn) { this.on[name] = fn; }
  };
  button.click();
  await settle();
  assert(byId('sp-sample-state').textContent.includes('would not play'),
    'a sample the browser refused to play said nothing: ' + byId('sp-sample-state').textContent);
  assert(!button.disabled && button.textContent === 'Play sample', 'the button stayed busy after a source that would not play');
});
</script>`

func TestPlaySampleAsksWithTheCardInBrowser(t *testing.T) {
	runPageInEngines(t, speechSamplePage, substrateModules(t))
}
