const PAGE_KEY = 'aii.signin';
const keepPageKey = reply => {
  const key = reply.headers.get('Aii-Page-Key');
  if (!key) return;
  for (const store of ['localStorage', 'sessionStorage']) {
    try { window[store].setItem(PAGE_KEY, key); return; } catch (_) { }
  }
};
const note = document.getElementById('note');
const open = document.getElementById('open');
let code = '';
try { code = decodeURIComponent(location.hash.slice(1)); } catch (_) { code = ''; }
history.replaceState(null, '', location.pathname);
const say = text => { note.textContent = text; open.hidden = false; };
if (!code) {
  say('This link carries no access code. Open the dashboard and sign in there.');
} else {
  fetch('/auth/token', {
    method: 'POST', cache: 'no-store', credentials: 'same-origin',
    headers: { 'Content-Type': 'text/plain;charset=UTF-8' }, body: code,
  }).then(async reply => {
    if (reply.ok) { keepPageKey(reply); location.replace('/'); return; }
    const why = reply.status === 403 ? (await reply.text()).trim() : '';
    say(why ? 'That code was not accepted: ' + why + '.' : 'That code was not accepted. A code works once and for a short time: ask for a new one on the device that is signed in, or sign in with the access token.');
  }).catch(() => say('Sign-in could not be confirmed. Check the connection, then scan the code again.'));
}
