import { codeLifeSaid, directAddressesHTML, routeMoveText } from './access.js';
import { renderSettings } from './parts.js';
import { configResult, savebarHTML, sendConfigChanges } from './save.js';
import { pendingSlot } from '../../pending.js';
import { S } from '../../state.js';
import { esc, fill } from '../../util.js';
import { query, send } from '../../ws.js';
import W from '../../strings/settings-access.js';
import WP from '../../strings/settings-phone.js';

const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
export const say = (text, slots) => {
  const out = fill(text, { name: nameOf() || W.unnamed, ...slots });
  return out.charAt(0).toUpperCase() + out.slice(1);
};
const said = (text, key, value, slots) => {
  const mark = '\u0000';
  return esc(say(text, { ...slots, [key]: mark })).replace(mark, '<b>' + esc(value) + '</b>');
};
const stripEnd = s => String(s).replace(/[.\s]+$/, '');

export function routeHTML(p, c) {
  if (!p || !p.name) return '';
  const mode = p.route_mode || 'direct';
  const now = mode === 'relay' ? 'through the relay ' + esc(p.relay_endpoint || '(none named)')
    : mode === 'disabled' ? 'nowhere — the route is withdrawn (set in config.json), and the name and its certificate are kept'
    : mode === 'direct' ? 'directly at the addresses this machine is reached at' : 'as config.json says: ' + esc(mode);
  let html = '<div class="settings-access__part" data-route><label class="settings-access__h4" for="cfg-route">' + esc(W.routeHow) + '</label><p class="settings-access__line" data-route-now>The name points ' + now + '.</p>';
  if (p.address) html += '<p class="settings-access__line" data-route-published>The certificate service last answered that the route is: ' + esc(p.address) + '.</p>';
  if (p.route_error) html += '<p class="settings-access__warn" data-route-error data-announce>The route is not confirmed as chosen: ' + esc(stripEnd(p.route_error)) + '.</p>';
  if (p.resolves_to) html += '<p class="settings-access__line" data-route-resolves>' + esc(fill(W.resolvesTo, { address: p.resolves_to })) + '</p>';
  else if (p.resolve_error) html += '<p class="settings-access__warn" data-route-resolves data-announce>' + esc(fill(W.doesNotResolve, { reason: stripEnd(p.resolve_error) })) + '</p>';
  if (mode === 'direct') {
    html += (p.held_families || []).map(h => '<p class="settings-access__hint" data-route-held>' + esc(fill(W.addressFamilyHeld, { family: h.family, addresses: (h.addresses || []).join(', ') })) + '</p>').join('');
  }
  if (!c || !c.secrets_ok) {
    return html + '<p class="settings-access__hint" data-route-private>' + esc(W.notPrivate) + '</p></div>';
  }
  const relays = p.relays, token = !!(c.dashboard && c.dashboard.require_token);
  const short = !!(c.dashboard && c.dashboard.token_short);
  const relayValue = r => 'relay:' + r.name + ':' + r.port;
  const held = (relays || []).find(r => r.name === (p.relay_endpoint || '').replace(/:\d+$/, ''));
  const chosen = mode === 'relay' ? (held ? relayValue(held) : 'relay:' + (p.relay_endpoint || '')) : 'direct';
  const opt = (value, label, off, to, relay) => '<option value="' + esc(value) + '"' + (value === chosen ? ' selected' : '') + (off ? ' disabled' : '') +
    ' data-move="' + esc(routeMoveText(p, c, to, relay)) + '">' + esc(label) + '</option>';
  let options = opt('direct', W.routeDirect, false, p.direct_origin, false);
  (relays || []).forEach(r => { options += opt(relayValue(r), fill(W.routeRelay, { relay: r.name, port: r.port }), !token || (short && relayValue(r) !== chosen), r.origin, true); });
  let note = '';
  let ask = '';
  if (relays == null) {
    note = W.routeNotAsked;
    ask = '<div class="settings-access__acts"><button type="button" class="c-button c-button--secondary" data-public-name="look">' + esc(W.routeAskRelays) + '</button></div>';
  }
  else if (!relays.length) note = W.routeNoneOffered;
  else if (!token) note = W.routeNeedsSignIn;
  else if (short) note = W.routeSignInShort;
  else note = W.routeRelayPort;
  if (short && mode === 'relay') html += '<p class="settings-access__bad" data-route-token-short data-announce>' + esc(W.routeSignInShort) + '</p>';
  return html + '<div class="settings-access__part" data-route-controls data-edit-form><select class="settings-access__select" id="cfg-route">' + options + '</select>' +
    '<p class="settings-access__hint" data-route-note>' + esc(note) + '</p>' + (relays && relays.length ? '<p class="settings-access__hint">' + esc(W.routeRelayHint) + '</p>' : '') + ask +
    '<p class="settings-access__warn" data-route-move data-announce hidden></p>' +
    directAddressesHTML(p) +
    savebarHTML('route', '', { label: W.useRoute }) + '</div></div>';
}

export let openFlow = null;
export const accessCodeAsk = pendingSlot();
let offSaid = null, offAsked = false;
let ownOpen = false, howOpen = false;

function reachesOtherDevices(p) {
  return p.route_mode === 'relay' || (!!p.address && p.address !== 'withdrawn');
}

export function nameServiceSaid(c) {
  const row = ((c && c.dashboard && c.dashboard.settings) || []).find(r => r.key === 'certificate.server_url');
  return row && row.value ? 'the name service at ' + row.value : 'the name service';
}

export function claimDeclined(p) {
  return !!(p && p.service_declined) && (!p.name || p.status === 'none');
}

export const daySaid = iso => { const t = Date.parse(iso || ''); return isNaN(t) ? String(iso || '') : new Date(t).toLocaleDateString([], { day: 'numeric', month: 'long', year: 'numeric' }); };
export function heldSaid(p) {
  return fill(W.held, { origin: p.origin || p.name, expires: daySaid(p.not_after), reason: p.last_error || 'unknown' });
}
export function certificateFailedSaid(p) {
  const days = p.days_left ? Math.floor(p.days_left) : 0;
  const text = fill(W.certificateFailed, { origin: p.origin || p.name, reason: p.last_error || 'unknown', days });
  return days ? text : text.replace(/ \(0 days left on the current one\)$/, '');
}

const pressDoesHTML = () => '<p class="settings-access__hint" data-open-does>' + esc(W.pressDoes) + ' ' + esc(W.certificatesPublic) + ' ' + esc(W.pressDoesRelay) + '</p>';

function ownCodeHTML() {
  return '<details class="settings-access__own" data-open-own' + (ownOpen ? ' open' : '') + '><summary>' + esc(W.ownCode) + '</summary><div class="settings-access__own-body">' +
    '<label class="settings-access__field" for="cfg-open-own">' + esc(W.ownCodeField) + '</label>' +
    '<input class="settings-access__input" type="text" id="cfg-open-own" autocomplete="off" spellcheck="false">' +
    '<p class="settings-access__hint">' + esc(W.ownCodeHint) + '</p>' +
    '<div class="settings-access__acts"><button type="button" class="c-button c-button--secondary" data-open-elsewhere-act="own">' + esc(W.ownCodeUse) + '</button></div></div></details>';
}

export function readyForOtherDevices(p, c) {
  return !!(c && c.secrets_ok && c.dashboard && c.dashboard.require_token && !c.dashboard.token_short &&
    p && p.status === 'issued' && reachesOtherDevices(p) && !claimDeclined(p));
}

export function openElsewhereHTML(p, c) {
  const f = openFlow || {};
  let html = '<section class="settings-access__panel" data-open-elsewhere>';
  if (!c || !c.secrets_ok) return html + '<p class="settings-access__line" data-open-private>' + esc(W.notPrivate) + '</p></section>';
  if (claimDeclined(p)) return html + '<p class="settings-access__line" data-open-claims>' + esc(say(W.declined)) + '</p></section>';
  const busy = !!f.running;
  if (f.code) {
    html += '<div class="settings-access__code-row" data-open-code>' + (f.code.qr ? '<div class="settings-access__qr" data-open-qr>' + f.code.qr + '</div>' : '') +
      '<div class="settings-access__code-words">' + (f.code.qr ? '<p class="settings-access__line">' + esc(WP.scan) + '</p>' : '') +
      '<p class="settings-access__line">' + said(WP.orType, 'origin', p.origin || '') + '</p>' +
      '<p class="settings-access__code" data-open-code-text>' + esc(f.code.code) + '</p>' +
      '<p class="settings-access__hint" id="open-elsewhere-hint">' + esc(f.code.own ? fill(W.codeLifeOwn, { life: codeLifeSaid(f.code.seconds) }) : fill(WP.life, { life: codeLifeSaid(f.code.seconds) })) + '</p></div></div>';
  } else if (!busy && !f.said) {
    html += '<p class="settings-access__line" data-open-claims>' + esc(say(WP.setUpSays)) + (!p || !p.name || p.status === 'none' ? ' ' + esc(say(WP.setUpClaims, { service: nameServiceSaid(c) })) : '') + '</p>' +
      '<div class="settings-access__acts"><button type="button" class="c-button c-button--primary" data-open-elsewhere-act="start">' + esc(WP.setUp) + '</button></div>';
  }
  if (f.said) html += '<p class="' + (f.bad ? 'settings-access__bad' : 'settings-access__said') + '" data-open-said data-announce>' + esc(f.said) + '</p>';
  if (f.needsMove) {
    html += '<p class="settings-access__warn" data-open-move data-announce>' + esc(f.needsMove) + '</p>' +
      '<div class="settings-access__acts"><button type="button" class="c-button c-button--primary" data-open-elsewhere-act="move">' + esc(W.moveAddress) + '</button>' +
      '<button type="button" class="c-button c-button--quiet" data-open-elsewhere-act="done">' + esc(W.done) + '</button></div>';
  } else if (f.said && f.bad && !busy && !f.code) {
    html += '<div class="settings-access__acts"><button type="button" class="c-button c-button--secondary" data-open-elsewhere-act="start">' + esc(WP.tryAgain) + '</button></div>';
  }
  return html + '</section>';
}

export function howItWorksHTML(p, c) {
  if (!c || !c.secrets_ok || claimDeclined(p)) return '';
  let html = '<details class="settings-access__more" data-open-how' + (howOpen ? ' open' : '') + '><summary>' + esc(WP.how) + '</summary><div class="settings-access__more-body">' +
    pressDoesHTML() + '<p class="settings-access__hint" data-open-relay-words>' + esc(W.relayExplained) + ' ' + esc(W.relaySees) + ' ' + esc(W.relayMovesOff) + '</p>' + ownCodeHTML();
  if (p.route_mode === 'relay' && !(openFlow && openFlow.running && !openFlow.auto)) {
    html += '<div class="settings-access__acts"><button type="button" class="c-button c-button--secondary" data-open-elsewhere-act="off">' + esc(W.stopRelay) + '</button></div>' +
      '<p class="settings-access__hint" data-open-off-note>' + esc(W.stopRelayHint) + '</p>';
  }
  if (offSaid) html += '<p class="' + (offSaid.bad ? 'settings-access__bad' : 'settings-access__said') + '" data-open-off-said data-announce>' + esc(offSaid.text) + '</p>';
  return html + '</div></details>';
}

function openElsewhereAdvance() {
  const f = openFlow;
  if (!f || !f.running || f.busy) return;
  const c = S.config, p = (c && c.public_name) || {};
  if (f.auto && !readyForOtherDevices(p, c)) { openElsewhereEnd(); return; }
  const settle = () => { clearTimeout(f.poll); f.poll = null; };
  const stop = (text, bad) => { settle(); f.running = false; f.bad = bad !== false; f.said = text; };
  const wait = text => {
    f.bad = false; f.said = text;
    if (!f.poll) f.poll = setTimeout(() => { f.poll = null; send({ type: 'public_name_state' }); send({ type: 'query', query: 'config' }); }, 2000);
  };
  if (!c || !c.dashboard) return wait(W.reading);
  if (!c.secrets_ok) return stop(W.notPrivate);
  if (claimDeclined(p)) return stop(say(W.declined));
  if (!c.dashboard.require_token) {
    f.busy = true; f.bad = false; f.said = W.turningOnSignIn;
    S.requireAccessToken().then(res => {
      f.busy = false;
      if (!res || !res.ok) stop(fill(W.signInFailed, { reason: stripEnd((res && res.text) || W.notTurnedOn) }));
      else if (S.config && S.config.dashboard) S.config.dashboard.require_token = true;
      renderSettings();
    });
    return;
  }
  if (!p.name || p.status === 'none') {
    if (!f.claimed) { f.claimed = true; send({ type: 'public_name_claim' }); }
    return wait(say(W.claimingAddress));
  }
  if (p.status === 'claiming') return wait(fill(W.gettingCertificate, { origin: p.origin || p.name }));
  if (p.status === 'held') return stop(heldSaid(p));
  if (p.status !== 'issued' && p.status !== 'prepared') return stop(certificateFailedSaid(p));
  if (!reachesOtherDevices(p)) {
    if (c.dashboard.token_short) return stop(W.signInShort);
    if (p.relays == null) {
      if (!f.lookedAt || Date.now() - f.lookedAt > 15000) { f.lookedAt = Date.now(); send({ type: 'public_name_look' }); }
      return wait(W.askingRelays);
    }
    const r = p.relays.find(x => x.suggested);
    if (!r) return stop(fill(W.noRelay, { origin: p.direct_origin || p.origin || p.name }));
    if (r.origin && p.origin && r.origin !== p.origin && (p.moves_with || []).length && !f.acceptMove) {
      settle(); f.running = false; f.bad = false; f.said = ''; f.needsMove = routeMoveText(p, c, r.origin, true);
      return;
    }
    if (!f.routed) {
      f.routed = true;
      sendConfigChanges('route', { 'certificate.route_mode': 'relay', 'certificate.relay_endpoint': r.name + ':' + r.port });
    }
    if (configResult && configResult.section === 'route' && configResult.kind === 'bad') return stop(fill(W.routeFailed, { reason: stripEnd(configResult.text) }));
    return wait(W.choosingRelay);
  }
  if (p.status === 'prepared') {
    if (!f.ordered) { f.ordered = true; send({ type: 'public_name_retry' }); }
    return wait(fill(W.gettingCertificate, { origin: p.origin || p.name }));
  }
  if (p.route_mode === 'relay' && !p.relay_connected) return wait(p.relay_error ? fill(W.relayNotCarrying, { reason: p.relay_error }) : W.connectingRelay);
  if (!f.code && !accessCodeAsk.waiting()) {
    settle(); f.bad = false; f.said = W.makingCode;
    if (!accessCodeAsk.arm({}, send({ type: 'access_code', access_code: f.own || '' }))) stop(W.notConnected);
  }
}

export function openElsewhereEnd() {
  const f = openFlow;
  if (!f) return;
  clearTimeout(f.poll);
  accessCodeAsk.drop();
  if (f.code) send({ type: 'access_code_cancel', access_code: f.code.id });
  openFlow = null;
}

export function wireOpenElsewhere(root) {
  if (offAsked && configResult && configResult.section === 'route' && configResult.kind) {
    offAsked = false;
    offSaid = configResult.kind === 'bad' ? { bad: true, text: fill(W.routeNotChanged, { reason: stripEnd(configResult.text) }) } : { bad: false, text: W.offRelay };
    const was = root.querySelector('[data-open-how]');
    if (was && S.config) was.outerHTML = howItWorksHTML(S.config.public_name || {}, S.config);
  }
  const told = () => JSON.stringify(openFlow && [openFlow.said, openFlow.bad, openFlow.running, openFlow.needsMove, openFlow.code && openFlow.code.id]);
  const before = told();
  openElsewhereAdvance();
  const box = root.querySelector('[data-open-elsewhere]');
  if (box && told() !== before && S.config) box.outerHTML = openElsewhereHTML(S.config.public_name || {}, S.config);
  root.querySelectorAll('[data-open-own]').forEach(d => { d.ontoggle = () => { ownOpen = d.open; }; });
  root.querySelectorAll('[data-open-how]').forEach(d => { d.ontoggle = () => { howOpen = d.open; }; });
  root.querySelectorAll('[data-open-elsewhere-act]').forEach(btn => {
    btn.onclick = () => {
      const act = btn.dataset.openElsewhereAct;
      if (act === 'done') { ownOpen = false; openElsewhereEnd(); renderSettings(); return; }
      if (act === 'off') {
        openElsewhereEnd();
        offSaid = { bad: false, text: W.goingOffRelay }; offAsked = true;
        sendConfigChanges('route', { 'certificate.route_mode': 'direct' });
        renderSettings();
        return;
      }
      if (claimDeclined(S.config && S.config.public_name)) return;
      const own = act === 'own' ? ((document.getElementById('cfg-open-own') || {}).value || '').trim() : '';
      if (act === 'own' && !own) return;
      startOpenElsewhere(act, own);
      renderSettings();
    };
  });
}

export function startOpenElsewhere(act, own) {
  if (act !== 'auto') { offSaid = null; offAsked = false; }
  if (openFlow && openFlow.code) send({ type: 'access_code_cancel', access_code: openFlow.code.id });
  const kept = openFlow || {};
  clearTimeout(kept.poll);
  accessCodeAsk.drop();
  openFlow = { running: true, auto: act === 'auto', own: own || '', acceptMove: act === 'move' || !!kept.acceptMove, claimed: !!kept.claimed, routed: act === 'move' ? false : !!kept.routed };
}
