//go:build !windows

package dashboard

import "testing"

const voiceServiceSpeakPage = `<!doctype html>
<button class="hushb" id="hush-speaking" type="button" hidden></button>
<button class="mic" id="mic" type="button" hidden aria-pressed="false"></button>
<button class="mic-more" id="mic-more" type="button" hidden aria-expanded="false"></button>
<div class="mic-menu" id="mic-menu" hidden><div id="mic-voice" hidden></div><div id="mic-source"></div><button type="button" id="mic-settings">Speech settings</button></div>
<script type="module">
import { assert, run } from './__harness.js';
import { speak, hush, wireMic } from './voice.js';
import { S } from './state.js';
import { said } from './app.js';
run(async () => {
  const settle = () => new Promise(r => setTimeout(r, 25));
  const spoken = [];
  const ss = window.speechSynthesis;
  ss.speak = u => { spoken.push(u.text); };
  ss.cancel = () => {};
  const asked = [];
  let answer = () => Promise.reject(new Error('no answer set'));
  window.fetch = (url, opts) => {
    asked.push({ url: url, method: opts && opts.method, body: opts && opts.body ? JSON.parse(opts.body) : null });
    return answer();
  };
  const played = [];
  let playing = null;
  window.Audio = class {
    constructor(src) { this.src = src; this.going = false; played.push(src); playing = this; }
    play() { this.going = true; return Promise.resolve(); }
    pause() { this.going = false; }
    addEventListener() {}
  };
  let minted = 0;
  const wav = () => Promise.resolve({ ok: true, json: () => Promise.resolve({ id: 'say-' + (++minted) }) });
  S.voiceSpeak = true;
  wireMic();

  S.stats = { reply_voice: '' };
  speak('the browser reads this', null);
  await settle();
  assert(spoken.length === 1 && asked.length === 0, 'a page with no service asked the host to speak: ' + asked.length);

  S.stats = { reply_voice: 'ElevenLabs' };
  answer = wav;
  speak('the service speaks this', null);
  await settle();
  assert(asked.length === 1 && asked[0].url === '/speech/say' && asked[0].method === 'POST' && asked[0].body.text === 'the service speaks this',
    'the reply was not sent to be spoken: ' + JSON.stringify(asked));
  assert(played.length === 1 && played[0] === '/speech/say/say-1' && playing.going, 'the audio was not played from its own address: ' + played.join(','));

  const stop = document.getElementById('hush-speaking');
  assert(!stop.hidden, 'nothing offered to stop a reply that was being spoken');
  stop.click();
  assert(!playing.going, 'the reply kept speaking after it was stopped');
  assert(stop.hidden, 'the way to stop it outlived the speaking');
  assert(spoken.length === 1, 'the browser read a reply the service was speaking: ' + JSON.stringify(spoken));

  answer = () => Promise.resolve({ ok: false, status: 502, json: () => Promise.resolve({ error: 'this voice is not on your plan' }) });
  speak('the service refuses this', null);
  await settle();
  assert(spoken.length === 2 && spoken[1] === 'the service refuses this', 'a refused reply was lost instead of read: ' + JSON.stringify(spoken));
  assert(said.some(m => m.indexOf('not on your plan') >= 0), 'the service\'s own words were not shown: ' + said.join(' / '));

  let late = null;
  answer = () => new Promise(r => { late = r; });
  speak('the first', null);
  await settle();
  answer = wav;
  speak('the second', null);
  await settle();
  late({ ok: true, json: () => Promise.resolve({ id: 'say-late' }) });
  await settle();
  assert(played.length === 2, 'a superseded reply spoke anyway: ' + played.length);

  const going = playing;
  hush();
  assert(!going.going, 'the service kept speaking through a hush');

  const asking = asked.length;
  speak('already being spoken', { route: 'cloud', synthesis_id: 'from-the-host' });
  await settle();
  assert(asked.length === asking, 'a reply already being spoken was asked for again: ' + JSON.stringify(asked.slice(asking)));
  assert(played.at(-1) === '/speech/say/from-the-host', 'the reply was not played from the id the host gave: ' + played.at(-1));

  const before = asked.length;
  speak('the engine speaks this', { session_id: 'vs-1', route: 'plugin' });
  await settle();
  assert(asked.length === before && spoken.length === 2, 'a plugin-routed reply was spoken twice');

  S.voiceSpeak = false;
  speak('typed, and still answered aloud', null);
  await settle();
  assert(asked.length === before + 1 && asked.at(-1).body.text === 'typed, and still answered aloud',
    'a chosen voice went silent because the microphone was not used: ' + JSON.stringify(asked.slice(-1)));
  assert(spoken.length === 2, 'the browser read a reply the service was speaking');

  S.stats = { reply_voice: '' };
  const quiet = asked.length;
  speak('typed, and read by nobody', null);
  await settle();
  assert(spoken.length === 2 && asked.length === quiet, 'the browser voice read a typed reply');
  S.voiceSpeak = true;
  speak('spoken to, and read back', null);
  await settle();
  assert(spoken.length === 3 && spoken[2] === 'spoken to, and read back', 'the browser voice stopped reading to an operator who spoke');
});
</script>`

func TestTheChosenServiceSpeaksTheReplyInBrowser(t *testing.T) {
	runPageInEngines(t, voiceServiceSpeakPage, map[string][]byte{
		"/app.js": []byte("export const said = [];\nexport function toast(m) { said.push(m); }\n"),
	})
}
