const NAME = 'aii.signin';
const HEADER = 'Aii-Page-Key';

let held = '';
let inMemory = false;

export function pageKey() {
  for (const store of ['localStorage', 'sessionStorage']) {
    try { const key = window[store].getItem(NAME); if (key) return key; } catch (_) { }
  }
  return held;
}

function keep(key) {
  for (const store of ['localStorage', 'sessionStorage']) {
    try { window[store].setItem(NAME, key); held = ''; inMemory = false; return true; } catch (_) { }
  }
  held = key; inMemory = true;
  return false;
}

export function keptInMemoryOnly() { return inMemory; }

export const MEMORY_ONLY = 'This browser does not let the dashboard keep its sign-in, so it lasts until this tab is reloaded or closed.';

export function keepPageKey(reply) {
  const key = reply.headers.get(HEADER);
  if (!key) return false;
  keep(key);
  return true;
}

export function signedFetch(url, init) {
  const key = pageKey();
  if (!key) return fetch(url, init);
  const options = Object.assign({}, init);
  const headers = new Headers(options.headers || {});
  headers.set(HEADER, key);
  options.headers = headers;
  return fetch(url, options);
}

export async function signIn(credential) {
  const reply = await fetch('/auth/token', {
    method: 'POST', cache: 'no-store', credentials: 'same-origin',
    headers: { 'Content-Type': 'text/plain;charset=UTF-8' }, body: credential,
  });
  if (reply.ok) keepPageKey(reply);
  return reply;
}

export async function ticketed(method, address) {
  try {
    const reply = await signedFetch('/auth/ticket', {
      method: 'POST', cache: 'no-store', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ method, address }),
    });
    if (reply.status !== 200) return address;
    const { ticket } = await reply.json();
    if (!ticket) return address;
    return address + (address.includes('?') ? '&' : '?') + 'ticket=' + encodeURIComponent(ticket);
  } catch (_) {
    return address;
  }
}
