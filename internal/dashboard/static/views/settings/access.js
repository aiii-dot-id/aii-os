import { accessCodeAsk, certificateFailedSaid, claimDeclined, daySaid, heldSaid, nameServiceSaid, openElsewhereEnd, openFlow, routeHTML, say } from './access-routes.js';
import { disableOwing, sec } from '../settings.js';
import { renderSettings } from './parts.js';
import { configBusy, configSaidHTML, savebarHTML, sendConfigChanges } from './save.js';
import { scriptsCardHTML, scriptsCardStale } from '../../scripts.js';
import { S } from '../../state.js';
import { esc, fill, loadingCard } from '../../util.js';
import { send } from '../../ws.js';
import W from '../../strings/settings-access.js';
import C from '../../strings/components.js';

function isLoopbackHost(h) {
  h = (h || '').trim();
  return h === '' || h === '127.0.0.1' || h === '::1' || h === 'localhost';
}
export function dashboardWarnHTML(host, tls) {
  if (tls || isLoopbackHost(host)) return '';
  return '<p class="settings-access__warn" id="cfg-dwarn">WITHOUT HTTPS ON THIS ADDRESS, EVERY WORD BETWEEN YOU AND THIS IDENTITY CROSSES THE NETWORK IN THE CLEAR — AND THE BROWSER WILL REFUSE THE MICROPHONE.</p>';
}

const WHO = 'who';
function whoHTML(c) {
  const here = isLoopbackHost(c.dashboard.host), busy = configBusy() ? ' disabled' : '';
  const choice = (value, on, label, hint) => '<label class="settings-access__radio"><input type="radio" name="cfg-who" data-who="' + value + '"' + (on ? ' checked' : '') + busy + '><span class="settings-access__radio-words"><span>' + esc(label) + '</span><span class="settings-access__hint"' + (on ? ' id="who-hint"' : '') + '>' + esc(hint) + '</span></span></label>';
  return '<section class="settings-access__part" data-who-part aria-describedby="who-hint"><h3 class="settings-access__h3">' + esc(W.who) + '</h3>' +
    '<fieldset class="settings-access__choices"><legend class="settings-access__legend">' + esc(W.who) + '</legend>' +
    choice('here', here, W.thisComputer, W.thisComputerHint) + choice('devices', !here, W.myDevices, W.myDevicesHint) + '</fieldset>' +
    (here ? '' : '<p class="settings-access__warn" data-who-exposes>' + esc(W.exposesWarning) + '</p>') +
    '<p class="settings-access__said" data-who-said>' + configSaidHTML(WHO) + '</p></section>';
}

const TOKEN_ACTS = {
  require: { confirm: W.turnOnConfirm, done: W.turnedOn, failed: W.notTurnedOn },
};
let tokenBusy = '', tokenSaid = null;
function tokenActHTML(act, label, secretsOK) {
  if (!secretsOK) return '<p class="settings-access__hint" data-token-' + act + '-private>' + esc(W.privateOnly) + '</p>';
  return '<button type="button" class="c-button c-button--secondary" data-token-' + act + (tokenBusy ? ' disabled' : '') + '>' + (tokenBusy === act ? esc(C.waiting) : esc(label)) + '</button>';
}
function tokenSaidHTML() {
  return tokenSaid ? '<p class="config-result ' + tokenSaid.kind + '" data-token-' + tokenSaid.act + '-said data-announce>' + esc(tokenSaid.text) + '</p>' : '';
}
function signInHTML(c) {
  const required = !!c.dashboard.require_token, secretsOK = !!c.secrets_ok;
  const offWhere = '<p class="settings-access__hint" data-token-off-where>' + esc(W.turnOnOnceOn) + '</p>';
  const row = (words, act) => '<div class="settings-access__row"><div class="settings-access__row-words">' + words + '</div>' + act + '</div>';
  if (!required) {
    return '<section class="settings-access__part" data-sign-in-part>' + row(
      '<span>' + esc(W.signIn) + '</span><span class="settings-access__hint" data-token-open>' + esc(W.signInOff) + '</span>',
      tokenActHTML('require', W.turnOn, secretsOK)) + tokenSaidHTML() + offWhere + '</section>';
  }
  return '<section class="settings-access__part" data-sign-in-part>' + row('<span>' + esc(W.signIn) + '</span>', '') + tokenSaidHTML() +
    (c.dashboard.token_short ? '<p class="settings-access__warn" data-token-short>' + esc(W.routeSignInShort) + '</p>' : '') + offWhere + '</section>';
}

let localCode = null, localSaid = '';
export function codeLifeSaid(seconds) {
  return seconds % 60 ? seconds + ' seconds' : (seconds / 60) + (seconds === 60 ? ' minute' : ' minutes');
}
export function localCodeHTML(secretsOK) {
  if (!secretsOK) return '';
  let html = '<div class="settings-access__part" data-local-code>';
  if (localCode) {
    html += '<p class="settings-access__line">In another browser on this machine or its network, open this link, or open this dashboard there and type the code where it asks you to sign in:</p>' +
      '<p class="settings-access__link-text" data-local-code-link>' + esc(localCode.link || '') + '</p>' +
      '<p class="settings-access__code" data-local-code-text>' + esc(localCode.code) + '</p>' +
      '<p class="settings-access__hint">It works once, for ' + esc(codeLifeSaid(localCode.seconds)) + ', and only for a browser on this machine or its own network.</p>';
  }
  if (localSaid) html += '<p class="settings-access__bad" data-local-code-said data-announce>' + esc(localSaid) + '</p>';
  return html + '<div class="settings-access__acts"><button type="button" class="c-button c-button--quiet" data-local-code-act="make">' + (localCode ? esc(W.newCode) : 'Make a sign-in code for another browser') + '</button>' +
    (localCode ? '<button type="button" class="c-button c-button--quiet" data-local-code-act="done">' + esc(W.done) + '</button>' : '') + '</div></div>';
}
export function forgetLocalCode() { localCode = null; localSaid = ''; }
export function endLocalCode() {
  if (localCode) send({ type: 'access_code_cancel', access_code: localCode.id });
  localCode = null; localSaid = '';
  accessCodeAsk.drop();
}
export function localCodeAct(act) {
  if (act === 'done') {
    if (localCode) send({ type: 'access_code_cancel', access_code: localCode.id });
    localCode = null; localSaid = '';
    renderSettings();
    return;
  }
  openElsewhereEnd();
  localCode = null; localSaid = '';
  if (!accessCodeAsk.arm({ local: true }, send({ type: 'access_code', access_code_local: true }))) localSaid = W.notConnected;
  renderSettings();
}

async function runTokenAct(act) {
  if (tokenBusy) return;
  const a = TOKEN_ACTS[act];
  if (window.confirm && !window.confirm(a.confirm)) return;
  tokenBusy = act; tokenSaid = null; renderSettings();
  const res = await S.requireAccessToken();
  tokenBusy = '';
  if (res.ok) {
    if (S.config && S.config.dashboard) S.config.dashboard.require_token = true;
    tokenSaid = { act, kind: 'good', text: a.done };
    localCode = null;
    scriptsCardStale();
  } else tokenSaid = { act, kind: 'bad', text: res.text || a.failed };
  if (S.view === 'settings' && sec === 'dashboard') renderSettings();
}

export function acceptAccessCode(requestID, code) {
  const held = accessCodeAsk.claim(requestID);
  if (!held) return false;
  if (held.local) localCode = code;
  else {
    localCode = null;
    if (openFlow && code) { openFlow.code = code; openFlow.own = ''; openFlow.running = false; openFlow.bad = false; openFlow.said = ''; }
  }
  renderSettings();
  return true;
}

export function rejectAccessCode(message, requestID) {
  const held = accessCodeAsk.claim(requestID);
  if (!held) return false;
  if (held.local) localSaid = message;
  else if (openFlow) { openFlow.running = false; openFlow.bad = true; openFlow.said = message; openFlow.own = ''; }
  renderSettings();
  return true;
}

export function directAddressesHTML(p) {
  const machine = p.machine_addresses || [], set = p.direct_addresses, named = set != null;
  const listed = [...new Set([...(set || []), ...machine])];
  const pick = a => '<label class="settings-access__check"><input type="checkbox" data-route-addr value="' + esc(a) + '"' + ((set || []).includes(a) ? ' checked' : '') + '> ' +
    esc(a) + (machine.includes(a) ? '' : ' (not an address this machine reports)') + '</label>';
  return '<fieldset class="settings-access__choices" data-route-addresses><legend class="settings-access__h4">' + esc(W.addresses) + '</legend>' +
    '<p class="settings-access__hint">' + esc(W.addressesHint) + '</p>' +
    '<label class="settings-access__radio"><input type="radio" name="cfg-route-addr" value="auto"' + (named ? '' : ' checked') + '> <span>Work them out from the address the dashboard is bound to</span></label>' +
    '<label class="settings-access__radio"><input type="radio" name="cfg-route-addr" value="list"' + (named ? ' checked' : '') + '> <span>Publish exactly these</span></label>' + listed.map(pick).join('') +
    (named && !set.length ? '<p class="settings-access__hint" data-route-addr-empty>config.json names an empty list: the direct route publishes nothing.</p>' : '') +
    '<p class="settings-access__hint">Used while the route is direct. For your own VPN, tick the VPN address: the name then leads there, and nothing passes through a relay.</p></fieldset>';
}

export function routeMoveText(p, c, to, relay) {
  const moves = !!to && !!p.origin && to !== p.origin;
  const same = (p.route_mode || 'direct') === (relay ? 'relay' : 'direct');
  if (!moves && (!relay || same)) return '';
  let text = '';
  if (moves) {
    text = fill(W.moveWarns, { from: p.origin, to }) + ' ' + W.moveWarnsSignIn;
    if (p.moves_with && p.moves_with.length) text += ' ' + fill(W.moveRegistered, { what: p.moves_with.join(', ') });
    const front = c && c.dashboard && c.dashboard.origin;
    if (front && front !== p.origin) text += ' ' + fill(W.moveFront, { front });
  }
  if (relay) text += (text ? ' ' : '') + W.routeAtHomeToo;
  return text;
}

function addressHTML(p, c) {
  let state, hint = '', control = '';
  const day = daySaid;
  const bold = text => esc(text).replace(esc(p.origin || p.name || ''), m => '<b>' + m + '</b>');
  const button = (act, label, primary, off) => '<button type="button" class="c-button ' + (primary ? 'c-button--primary' : 'c-button--secondary') + '" data-public-name="' + act + '"' + (off ? ' disabled' : '') + '>' + esc(label) + '</button>';
  if (!p || p.status === 'none') {
    state = esc(W.addressNone);
    hint = claimDeclined(p) ? esc(say(W.declined)) : esc(say(W.claimHint, { service: nameServiceSaid(c) }));
    control = button('claim', W.claim, false, !(p && p.can_claim && !claimDeclined(p)));
  } else if (p.status === 'prepared') {
    state = bold(fill(W.prepared, { origin: p.origin || p.name }));
  } else if (p.status === 'claiming') {
    state = '<b>' + esc(p.name) + '</b> · ' + esc(fill(W.gettingCertificate, { origin: p.origin || p.name }));
  } else if (p.status === 'issued') {
    state = bold(p.relay && p.relay_connected ? fill(W.addressLine, { origin: p.origin, relay: p.relay, date: day(p.renew_at) })
      : p.relay ? fill(W.issued, { origin: p.origin, expires: day(p.not_after), renews: day(p.renew_at) }) + ' ' + fill(W.relayNotCarryingShort, { relay: p.relay, reason: p.relay_error || 'unknown' })
      : fill(W.addressDirect, { origin: p.origin, date: day(p.renew_at) }));
    hint = esc(say(W.addressHint, { origin: p.origin })).replace(esc(p.origin), m => '<a class="settings-access__link" href="' + m + '">' + m + '</a>');
    if (p.can_move) {
      state += ' ' + esc(fill(W.zoneMoved, { zone: p.service_zone, old: p.zone }));
      control = button('move', fill(W.moveTo, { zone: p.service_zone }), false, false);
    }
  } else if (p.status === 'held') {
    state = bold(heldSaid(p));
  } else {
    state = bold(certificateFailedSaid(p));
    control = button('retry', W.retry, false, false);
  }
  const phone = S.stats && S.stats.hosted_on_phone ? '<p class="settings-access__hint" data-public-name-phone>' + esc(say(W.hostedOnPhone)) + '</p>' : '';
  return '<section class="settings-access__part" data-public-name-part aria-describedby="address-state"><h3 class="settings-access__h3">' + esc(W.address) + '</h3>' +
    '<p class="settings-access__line" id="address-state" data-public-name-state>' + state + '</p>' + phone +
    (hint ? '<p class="settings-access__hint">' + hint + '</p>' : '') +
    (control ? '<div class="settings-access__acts">' + control + '</div>' : '') + '</section>';
}

function listenHTML(c) {
  const tls = !!c.dashboard.tls;
  const field = (id, label, hint, value) => '<label class="settings-access__field" for="' + id + '">' + esc(label) + '</label><input class="settings-access__input" type="text" id="' + id + '" value="' + esc(value == null ? '' : value) + '">' + (hint ? '<p class="settings-access__hint">' + esc(hint) + '</p>' : '');
  return '<div class="settings-access__part" data-listen data-edit-form>' +
    field('cfg-dhost', W.host, W.hostHint, c.dashboard.host) + field('cfg-dport', W.port, '', c.dashboard.port) +
    '<label class="settings-access__check"><input type="checkbox" id="cfg-dtls"' + (tls ? ' checked' : '') + '> ' + esc(W.https) + '</label>' +
    '<p class="settings-access__hint">' + esc(W.httpsHint) + '</p>' +
    dashboardWarnHTML(c.dashboard.host, tls) +
    savebarHTML('dashboard', '', { label: W.saveListen }) + '</div>';
}

function settingsReadHTML(c) {
  const rows = (c && c.dashboard && c.dashboard.settings) || [];
  if (!rows.length) return '';
  return '<details data-settings-read><summary>Every setting of this card, as it is in force (' + rows.length + ')</summary>' +
    '<table class="settings-access__table"><thead><tr><th>Setting</th><th>In force</th><th>From</th></tr></thead><tbody>' +
    rows.map(r => '<tr data-setting="' + esc(r.key) + '"><td><code>' + esc(r.key) + '</code></td><td>' + esc(r.value) + '</td><td>' + (r.set ? 'config.json' : 'the default') + '</td></tr>').join('') +
    '</tbody></table><p class="settings-access__hint">Those with no control above are set in config.json.</p></details>';
}

let moreOpen = false;
function moreHTML(p, c) {
  const open = moreOpen || !!(p && p.route_error);
  return '<details class="settings-access__more" data-route-more' + (open ? ' open' : '') + '><summary>' + esc(W.route) + ' <span class="settings-access__more-hint">· ' + esc(W.routeHint) + '</span></summary>' +
    '<div class="settings-access__more-body">' + listenHTML(c) + routeHTML(p, c) + settingsReadHTML(c) + '</div></details>';
}

export function dashboardHTML(c) {
  if (!c) return loadingCard('configuration');
  const p = c.public_name || null;
  return '<div class="settings-access" data-access><header class="settings-access__head"><h2 class="settings-access__title">' + esc(W.title) + '</h2><p class="settings-access__intro">' + esc(W.intro) + '</p></header>' +
    whoHTML(c) + signInHTML(c) + addressHTML(p, c) + moreHTML(p, c) + '</div>' + scriptsCardHTML();
}

export function wirePublicName(root) {
  root.querySelectorAll('[data-token-require]').forEach(btn => { btn.onclick = () => runTokenAct('require'); });
  root.querySelectorAll('[data-route-more]').forEach(d => { d.ontoggle = () => { moreOpen = d.open; }; });
  root.querySelectorAll('[data-who]').forEach(radio => {
    radio.onchange = () => {
      if (!radio.checked) return;
      const now = (S.config && S.config.dashboard && S.config.dashboard.host) || '';
      const host = radio.dataset.who === 'here' ? '127.0.0.1' : isLoopbackHost(now) ? '0.0.0.0' : now;
      if (host === now) return;
      disableOwing(radio);
      sendConfigChanges(WHO, { 'dashboard.host': host });
      renderSettings();
    };
  });
  const route = root.querySelector('#cfg-route'), move = root.querySelector('[data-route-move]');
  if (route && move) {
    const tell = () => { const o = route.selectedOptions[0]; move.textContent = (o && o.dataset.move) || ''; move.hidden = !move.textContent; };
    route.addEventListener('change', tell);
    tell();
  }
  root.querySelectorAll('[data-public-name]').forEach(btn => { btn.onclick = () => {
    const kind = btn.dataset.publicName;
    if (kind === 'claim' && claimDeclined(S.config && S.config.public_name)) return;
    disableOwing(btn);
    send({ type: kind === 'retry' ? 'public_name_retry' : kind === 'move' ? 'public_name_move' : kind === 'look' ? 'public_name_look' : 'public_name_claim' });
  }; });
}
