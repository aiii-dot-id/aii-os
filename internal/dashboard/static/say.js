// say.js is the one place that asks the host to speak something and plays
// what comes back.
//
// It has no imports on purpose. Settings needs it to play a sample of a
// voice being picked, and the conversation needs it to speak replies; if
// it lived in either of those, the other would drag that whole module in
// behind it.
let saying = null;
let turn = 0;
// Whoever wants to know when the identity is speaking — the page shows a
// way to stop it, and hides that again when the voice stops on its own.
let watcher = null;

export function whenSpeaking(fn) { watcher = fn; }

function tell(on) {
  if (!watcher) return;
  try { watcher(on); } catch (e) { /* a listener's trouble is not the voice's */ }
}

// spokenAudio asks for one thing to be said and plays it as it is made.
//
// Two steps, and the shape is the point: the POST settles everything that
// can be refused in words — an unknown engine, a missing key, the
// month's ceiling — and answers an id; the audio then arrives as an
// ordinary media stream the browser plays from its first bytes, so a long
// reply starts speaking at its first sentence instead of after its last.
// A reply the host already minted arrives with its id and skips the POST
// entirely: it has been speaking since the moment the words existed.
//
// A newer request supersedes an older one — late audio must never speak
// over what the identity is saying now — and a refusal rejects with
// whoever refused it, in that service's own words where there are any.
// A refusal for a request that was hushed is not a refusal: the hush
// moved the turn on, and a failure landing for the older request
// afterwards resolves to nothing, so nobody reads the reply back.
export function spokenAudio(ask) {
  const mine = ++turn;
  stopAudio();
  const minted = ask && ask.id ? Promise.resolve(ask.id) : mint(ask);
  return minted.then(id => {
    if (mine !== turn) return;
    const url = '/speech/say/' + encodeURIComponent(id);
    const el = new Audio(url);
    saying = el;
    return new Promise((resolve, reject) => {
      // A SOURCE THAT NEVER PLAYS MUST SAY SO, IN THE RIGHT WORDS. A
      // service that refused after the id was minted arrives as an error
      // at the element, and play() rejects in the same breath with the
      // browser's own words — "no supported source" — which are not the
      // service's. Whoever loses first asks the host why.
      let settled = false;
      const fail = err => { if (settled) return; settled = true; reject(err); };
      const ask = () => why(url).then(said => fail(new Error(said)));
      el.addEventListener('error', () => { if (saying === el) { saying = null; tell(false); } ask(); });
      el.addEventListener('ended', () => { if (saying === el) { saying = null; tell(false); } });
      el.play().then(() => { if (!settled) { settled = true; tell(true); resolve(); } }, err => {
        if (el.error || (err && err.name === 'NotSupportedError')) { ask(); return; }
        fail(err && err.name === 'NotAllowedError'
          ? new Error('this browser blocked playback until the page is used')
          : (err || new Error('the audio would not play')));
      });
    });
  }).catch(err => {
    // A FAILURE OF A REQUEST THE HUSH MOVED PAST IS SETTLED SILENCE — the
    // service refusing late, the fetch dying with the connection, play()
    // rejecting because pause() got there first. Passed on, it reached
    // the fallback and the browser's own voice read the cancelled reply
    // out (review of 0.1.5, finding 5).
    if (mine !== turn) return;
    throw err;
  });
}

function mint(ask) {
  return fetch('/speech/say', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(ask) })
    .then(r => r.ok
      ? r.json().then(said => said && said.id ? said.id : Promise.reject(new Error('the host did not say which reply to play')))
      : r.json().then(
        e => Promise.reject(new Error((e && e.error) || ('the service answered ' + r.status))),
        () => Promise.reject(new Error('the service answered ' + r.status))));
}

// why asks the same address the element could not play, so a refusal that
// arrived as audio-that-never-came can be read in words.
function why(url) {
  return fetch(url).then(
    r => r.ok
      ? 'this browser would not play the audio the service sent'
      : r.json().then(e => (e && e.error) || ('the service answered ' + r.status), () => 'the service answered ' + r.status),
    () => 'the audio could not be fetched');
}

// hushSpoken stops what a service is saying now, and abandons anything
// still on its way — a hush is the operator's word, so nothing that was
// already in flight may arrive and speak.
export function hushSpoken() {
  turn++;
  stopAudio();
}

function stopAudio() {
  if (!saying) return;
  const el = saying;
  saying = null;
  // Paused is not stopped: the download would go on to the end and the
  // host would go on writing a reply nobody hears. Dropping the source
  // ends both.
  try { el.pause(); el.removeAttribute('src'); el.load(); } catch (e) { /* a source that never started */ }
  tell(false);
}
