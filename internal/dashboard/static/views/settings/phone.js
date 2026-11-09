import { claimDeclined, howItWorksHTML, openElsewhereEnd, openElsewhereHTML, openFlow, readyForOtherDevices, startOpenElsewhere, wireOpenElsewhere } from './access-routes.js';
import { endLocalCode, forgetLocalCode, localCodeAct, localCodeHTML } from './access.js';
import { renderInto } from '../../announce.js';
import { S } from '../../state.js';
import { esc, fill, h, loadingCard } from '../../util.js';
import W from '../../strings/settings-phone.js';

function section() {
  let el = document.getElementById('view-phone');
  if (el) return el;
  const views = document.getElementById('views');
  if (!views) return null;
  el = h('section', { class: 'view', id: 'view-phone', 'aria-hidden': 'true' }, h('div', { class: 'stack', id: 'phone-stack' }));
  el.inert = true;
  views.appendChild(el);
  return el;
}
section();

let outBusy = false, outSaid = null;
function lostHTML(c) {
  if (!c.secrets_ok || !c.dashboard || !c.dashboard.require_token) return '';
  return '<section class="settings-access__part" data-lost aria-labelledby="lost-title"><h3 class="settings-access__h3" id="lost-title">' + esc(W.lost) + '</h3>' +
    '<p class="settings-access__hint">' + esc(W.lostHint) + '</p>' +
    '<div class="settings-access__acts"><button type="button" class="c-button c-button--secondary" data-sign-out-all' + (outBusy ? ' disabled' : '') + '>' + esc(W.signOutAll) + '</button></div>' +
    (outSaid ? '<p class="' + (outSaid.bad ? 'settings-access__bad' : 'settings-access__said') + '" data-sign-out-said data-announce>' + esc(outSaid.text) + '</p>' : '') + '</section>';
}
async function signOutAll() {
  if (outBusy || (window.confirm && !window.confirm(W.signOutConfirm))) return;
  outBusy = true; outSaid = null; renderPhone();
  const res = await S.rotateAccessToken();
  outBusy = false;
  outSaid = res.ok ? { bad: false, text: W.signedOut } : { bad: true, text: res.text };
  if (res.ok) { forgetLocalCode(); if (openFlow) openElsewhereEnd(); }
  renderPhone();
}

let renewal = null;
function renewWhenOut() {
  const code = openFlow && openFlow.code;
  if (!code) return;
  if (!code.until) code.until = Date.now() + code.seconds * 1000;
  if (code.until <= Date.now()) {
    if (document.visibilityState !== 'hidden') { startOpenElsewhere('auto'); renderPhone(); }
    return;
  }
  if (renewal && renewal.code === code) return;
  clearTimeout(renewal && renewal.timer);
  renewal = { code, timer: setTimeout(() => { renewal = null; if (S.view === 'phone' && openFlow && openFlow.code === code) renderPhone(); }, code.until - Date.now()) };
}
document.addEventListener('visibilitychange', () => { if (S.view === 'phone' && document.visibilityState !== 'hidden') renderPhone(); });

window.addEventListener('hashchange', () => {
  if (S.view === 'phone') return;
  if (openFlow) openElsewhereEnd();
  endLocalCode();
  outSaid = null;
});

export function renderPhone() {
  if (S.view !== 'phone') return;
  const st = section() && document.getElementById('phone-stack');
  if (!st) return;
  const c = S.config;
  if (!c) { renderInto(st, loadingCard('configuration'), false); return; }
  const p = c.public_name || {};
  if (!openFlow && readyForOtherDevices(p, c)) startOpenElsewhere('auto');
  const name = (S.stats && S.stats.name) || '';
  renderInto(st, '<div class="settings-access" data-phone><header class="settings-access__head"><h2 class="settings-access__title">' +
    esc(name && name !== 'Unnamed' ? fill(W.title, { name }) : W.titleNoName) + '</h2></header>' +
    openElsewhereHTML(p, c) + (claimDeclined(p) && c.secrets_ok ? localCodeHTML(true) : '') + lostHTML(c) + howItWorksHTML(p, c) + '</div>', false);
  wireOpenElsewhere(st);
  st.querySelectorAll('[data-local-code-act]').forEach(b => { b.onclick = () => localCodeAct(b.dataset.localCodeAct); });
  st.querySelectorAll('[data-sign-out-all]').forEach(b => { b.onclick = signOutAll; });
  renewWhenOut();
}
