const SHIPPED = '/shipped/';
const onShipped = location.pathname.startsWith(SHIPPED);

function panel(id, role, lines, actions) {
  const box = document.createElement('div');
  box.id = id;
  box.setAttribute('role', role);
  const alarm = role === 'alert';
  box.style.cssText = 'position:fixed;inset:auto 16px 16px 16px;z-index:2147483647;max-width:44rem;margin:0 auto;' +
    'padding:16px 20px;border-radius:10px;font:15px/1.45 system-ui,sans-serif;box-shadow:0 6px 24px rgba(0,0,0,.18);' +
    (alarm ? 'border:1px solid #b3261e;background:#fff7f6;color:#2b0b09' : 'border:1px solid #5a6b7d;background:#f4f7fa;color:#14202b');
  for (const [text, strong] of lines) {
    const p = document.createElement('p');
    p.style.margin = '0 0 8px';
    if (strong) { const b = document.createElement('strong'); b.textContent = text; p.appendChild(b); }
    else p.textContent = text;
    box.appendChild(p);
  }
  const row = document.createElement('p');
  row.style.margin = '12px 0 0';
  for (const [label, act] of actions) {
    const a = document.createElement('a');
    a.textContent = label;
    a.style.cssText = 'margin-right:18px;color:inherit;text-decoration:underline';
    if (typeof act === 'string') a.href = act;
    else { a.href = '#'; a.addEventListener('click', e => { e.preventDefault(); act(); }); }
    row.appendChild(a);
  }
  box.appendChild(row);
  (document.body || document.documentElement).appendChild(box);
}

import('./app.js').then(function () {
  if (!onShipped) return;
  panel('boot-shipped', 'status',
    [['This is the dashboard as shipped: your customizations are not loaded on this page.', false]],
    [['Back to your dashboard', '/' + location.hash]]);
}, function (err) {
  const why = (err && err.message) ? err.message : String(err);
  if (onShipped) {
    panel('boot-failure', 'alert', [['The dashboard as shipped did not start.', true], [why, false]],
      [['Try again', () => location.reload()]]);
    return;
  }
  panel('boot-failure', 'alert',
    [['The dashboard did not start.', true], [why, false],
      ['The dashboard as shipped loads none of your customizations, and runs with your identity as it is.', false]],
    [['Open the dashboard as shipped', SHIPPED + location.hash], ['Try again', () => location.reload()]]);
});
