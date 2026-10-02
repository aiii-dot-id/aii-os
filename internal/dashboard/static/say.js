let saying = null;
let turn = 0;
let watcher = null;

export function whenSpeaking(fn) { watcher = fn; }

function tell(on) {
  if (!watcher) return;
  try { watcher(on); } catch (e) { }
}

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

function why(url) {
  return fetch(url).then(
    r => r.ok
      ? 'this browser would not play the audio the service sent'
      : r.json().then(e => (e && e.error) || ('the service answered ' + r.status), () => 'the service answered ' + r.status),
    () => 'the audio could not be fetched');
}

export function hushSpoken() {
  turn++;
  stopAudio();
}

function stopAudio() {
  if (!saying) return;
  const el = saying;
  saying = null;
  try { el.pause(); el.removeAttribute('src'); el.load(); } catch (e) { }
  tell(false);
}
