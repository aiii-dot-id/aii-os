const SHIPPED = '/shipped/';
const onShipped = location.pathname.startsWith(SHIPPED);
const SHIPPED_NOTE_DISMISSED = 'aii.shipped-note.dismissed';

function remembered(key) {
  try { return sessionStorage.getItem(key) === '1'; } catch (e) { return false; }
}

function remember(key) {
  try { sessionStorage.setItem(key, '1'); } catch (e) { }
}

function bar(id, role, onDismiss) {
  const box = document.createElement('div');
  box.id = id;
  box.setAttribute('role', role);
  const alarm = role === 'alert';
  box.style.cssText = 'display:flex;align-items:flex-start;gap:8px;flex:0 0 auto;margin:0;padding:6px 6px 6px 14px;' +
    'font:14px/1.4 system-ui,sans-serif;border-bottom:1px solid;' +
    (alarm ? 'border-color:#b3261e;background:#fff7f6;color:#2b0b09' : 'border-color:#5a6b7d;background:#f4f7fa;color:#14202b');
  const words = document.createElement('div');
  words.style.cssText = 'flex:1 1 auto;min-width:0;padding:4px 0;overflow-wrap:anywhere';
  box.appendChild(words);
  const close = document.createElement('button');
  close.type = 'button';
  close.textContent = '×';
  close.setAttribute('aria-label', 'Dismiss this notice');
  close.title = 'Dismiss';
  close.style.cssText = 'flex:0 0 auto;width:32px;height:32px;padding:0;border:0;border-radius:6px;background:transparent;' +
    'color:inherit;font:20px/32px system-ui,sans-serif;cursor:pointer';
  close.addEventListener('click', () => {
    box.remove();
    if (onDismiss) onDismiss();
  });
  box.appendChild(close);
  const column = document.querySelector('.stage');
  const host = column || document.body || document.documentElement;
  host.insertBefore(box, host.firstChild);
  return words;
}

function line(into, text, strong) {
  const p = document.createElement('p');
  p.style.margin = '0 0 4px';
  if (strong) { const b = document.createElement('strong'); b.textContent = text; p.appendChild(b); }
  else p.textContent = text;
  into.appendChild(p);
  return p;
}

function actions(into, list) {
  const row = document.createElement('p');
  row.style.cssText = 'margin:6px 0 0;display:flex;flex-wrap:wrap;gap:4px 18px';
  for (const [label, act] of list) {
    const a = document.createElement('a');
    a.textContent = label;
    a.style.cssText = 'color:inherit;text-decoration:underline';
    if (typeof act === 'string') a.href = act;
    else { a.href = '#'; a.addEventListener('click', e => { e.preventDefault(); act(); }); }
    row.appendChild(a);
  }
  into.appendChild(row);
}

function button(label) {
  const b = document.createElement('button');
  b.type = 'button';
  b.textContent = label;
  b.style.cssText = 'margin:4px 0 0;padding:6px 12px;border:1px solid currentColor;border-radius:6px;' +
    'background:transparent;color:inherit;font:inherit;font-weight:600;cursor:pointer';
  return b;
}

async function refusal(r) {
  try { const said = await r.json(); if (said && said.error) return said.error; } catch (e) { }
  return 'the dashboard answered ' + r.status;
}

async function nameTheCause(into) {
  let listed;
  try {
    const r = await fetch('/overlay/replacements', { cache: 'no-store', credentials: 'same-origin' });
    if (!r.ok) return;
    listed = await r.json();
  } catch (e) { return; }
  const files = (listed && Array.isArray(listed.files) ? listed.files : [])
    .map(f => f && f.path).filter(p => typeof p === 'string' && /\.(js|html)$/.test(p));
  if (!files.length) return;
  for (const p of files) line(into, 'Your ' + p + ' replaces a shipped dashboard file and may not match this version.');
  const act = button('Set aside and reload');
  act.title = 'Renames ' + (files.length === 1 ? 'it' : 'them') + ' to <name>.set-aside-<version> (nothing is deleted) and reloads; your other customizations stay';
  into.appendChild(act);
  const said = line(into, '');
  act.addEventListener('click', async () => {
    act.disabled = true;
    said.textContent = '';
    try {
      const r = await fetch('/overlay/set-aside', {
        method: 'POST', credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ paths: files }),
      });
      if (r.ok) { location.reload(); return; }
      said.textContent = 'Not set aside: ' + await refusal(r);
    } catch (e) {
      said.textContent = 'Not set aside: ' + ((e && e.message) || String(e));
    }
    act.disabled = false;
  });
}

function started() {
  if (!onShipped || remembered(SHIPPED_NOTE_DISMISSED)) return;
  const words = bar('boot-shipped', 'status', () => remember(SHIPPED_NOTE_DISMISSED));
  words.append('As shipped: your scripts are not loaded here; your styles are. ');
  const back = document.createElement('a');
  back.textContent = 'Back to your dashboard';
  back.href = '/' + location.hash;
  back.style.cssText = 'color:inherit;text-decoration:underline;white-space:nowrap';
  words.appendChild(back);
}

function failed(err) {
  const why = (err && err.message) ? err.message : String(err);
  const words = bar('boot-failure', 'alert');
  if (onShipped) {
    line(words, 'The dashboard as shipped did not start.', true);
    line(words, why);
    actions(words, [['Try again', () => location.reload()]]);
    return;
  }
  line(words, 'The dashboard did not start.', true);
  line(words, why);
  const cause = document.createElement('div');
  words.appendChild(cause);
  line(words, 'The dashboard as shipped runs none of your scripts (your styles still apply), with your identity as it is.');
  actions(words, [['Open the dashboard as shipped', SHIPPED + location.hash], ['Try again', () => location.reload()]]);
  return nameTheCause(cause);
}

export const ready = import('./app.js').then(started, failed);
