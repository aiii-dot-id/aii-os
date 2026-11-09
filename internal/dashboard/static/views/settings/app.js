import { S } from '../../state.js';
import { esc, fill } from '../../util.js';
import { send } from '../../ws.js';
import { notifyState, turnNotify } from '../../waiting.js';
import { markNow } from '../../shell/mark-place.js';
import { LOOK, lookOf } from '../../components/mark/look.js';
import { limitRowsHTML, savebarHTML, sendConfigChanges, disableOwing, renderSettings, configBusy, configWaitingFor, configSaidHTML } from '../settings.js';
import W from '../../strings/settings-app.js';
import C from '../../strings/components.js';

if (!document.querySelector('link[data-app-css]')) {
  document.head.append(Object.assign(document.createElement('link'), { rel: 'stylesheet', href: new URL('./app.css', import.meta.url).href }));
  document.head.lastElementChild.setAttribute('data-app-css', '');
}

const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
const named = (withName, unnamed) => { const n = nameOf(); return n ? fill(withName, { name: n }) : unnamed; };

const relative = new Intl.RelativeTimeFormat([], { numeric: 'auto' });
function ago(when) {
  const t = Date.parse(when || '');
  if (isNaN(t)) return String(when || '');
  const s = Math.round((t - Date.now()) / 1000);
  for (const [unit, size] of [['day', 86400], ['hour', 3600], ['minute', 60]]) {
    if (Math.abs(s) >= size) return relative.format(Math.round(s / size), unit);
  }
  return relative.format(0, 'second');
}

const shellWord = (s) => "'" + String(s).replace(/'/g, "'\\''") + "'";

const UPDATE_HIGHEST_LABEL = 'newest signed release seen';
const UPDATE_HIGHEST_PUBLISHED = 'published {day}, as GitHub dates it';
const UPDATE_HIGHEST_VERIFIED = 'verified here {day}';
const updateHighestDays = (u) => {
  const days = [];
  if (u.highest_published) days.push(UPDATE_HIGHEST_PUBLISHED.replace('{day}', esc(u.highest_published)));
  if (u.highest_verified) days.push(UPDATE_HIGHEST_VERIFIED.replace('{day}', esc(u.highest_verified)));
  return days.length ? ' (' + days.join('; ') + ')' : '';
};

const UPDATE_EVERY_HOUR = 'Checks run once an hour.';
const atDefault = u => { const m = u && u.check_interval_minutes; return !m || m === 60; };
function updateCadence(u) {
  return atDefault(u) ? UPDATE_EVERY_HOUR : 'Checks run every ' + u.check_interval_minutes + ' minutes.';
}

function stateLine(u, c) {
  if (!u || !u.enabled) return esc(W.noVersion);
  const managed = !!u.stage_refusal;
  let state;
  if (u.checking) state = esc(W.checking);
  else if (u.error) state = 'Last check failed: ' + esc(u.error);
  else if (u.needs_restart) state = 'Update ' + esc(u.installed_version) + ' is installed — relaunch to run it.';
  else if (u.available_version && managed && u.package && u.package.path) state = 'Update ' + esc(u.available_version) + ' is downloaded and verified against the release\'s signature. Install it with <code>sudo dpkg -i ' + esc(shellWord(u.package.path)) + '</code>; an identity running as a service restarts on the new build.';
  else if (u.available_version && managed && u.package && u.package.refusal) state = 'Update ' + esc(u.available_version) + ' is announced, and its package is not offered: ' + esc(String(u.package.refusal).replace(/[.\s]+$/, '')) + '. Nothing was verified, so there is nothing to install.';
  else if (u.available_version && managed) state = 'Update available: ' + esc(u.available_version) + ' — this program cannot replace itself where it is installed. Update it the way it was installed' + (u.release_url ? '; <a href="' + esc(u.release_url) + '" target="_blank" rel="noopener">the release</a> is here' : '') + '.';
  else if (u.available_version) state = 'Update available: ' + esc(u.available_version) + (automatic(u, c) ? ' — it is downloaded, verified and installed on the next check.' : ' — automatic apply is off; turn it on and check again to install it.');
  else if (u.checked_at) state = esc(fill(W.upToDate, { ago: ago(u.checked_at) }));
  else state = 'Not checked yet.';
  return esc(fill(W.version, { version: u.current_version || 'dev' })) + ' · ' + state;
}

function warningsHTML(u) {
  if (!u) return '';
  return (u.running_withdrawn ? '<p class="settings-app__row" data-update-withdrawn><b>The release this program came from (' + esc(u.current_version) + ') has been withdrawn by its publisher.</b>' +
      (u.available_version ? ' ' + esc(u.available_version) + ' is offered here: install it.' : ' No replacement is offered yet.') + '</p>' : '') +
    (u.running_unknown ? '<p class="settings-app__row" data-update-withdrawn>The withdrawal list this machine holds names releases, and whether this program\'s own release is one of them could not be told: ' + esc(String(u.running_unknown).replace(/[.\s]+$/, '')) + '.</p>' : '') +
    (u.available_version && u.stranded && u.stranded.length ? '<div class="settings-app__row" data-update-stranded>Updating to ' + esc(u.available_version) + ' leaves ' + u.stranded.length + ' installed plugin' + (u.stranded.length === 1 ? '' : 's') + ' without a version for it:' +
      u.stranded.map(s => '<div class="settings-app__stranded">' + esc(s.id) + ' ' + esc(s.version) + ': ' + esc(s.reason) + '</div>').join('') + '</div>' : '') +
    (u.available_version && u.stranded_note ? '<p class="settings-app__row" data-update-stranded-note>' + esc(u.stranded_note) + '</p>' : '');
}

function detailsHTML(u, c) {
  const announced = !!(u && u.announced_version);
  const managed = !!(u && u.stage_refusal);
  let heard = '';
  if (announced) heard = ' The newest release is announced in the signed plugin catalog' + (u.announced_from ? ' at ' + esc(u.announced_from) : '') + ', fetched every hour. Nothing marks an announcement as old: if announcements stop reaching this machine, its date stops moving.';
  else if (u && u.enabled) heard = ' No release announcement has reached this machine yet.';
  return '<details class="settings-app__details"><summary>' + esc(C.details) + '</summary>' +
    '<div class="sp-kv"><span>running</span><b>' + esc((u && u.current_version) || 'dev') + '</b></div>' +
    (announced ? '<div class="sp-kv" data-update-announced><span>newest announced</span><b>' + esc(u.announced_version) + (u.announced_on ? ' (' + esc(u.announced_on) + ')' : '') + '</b></div>' : '') +
    (u && u.highest_version ? '<div class="sp-kv" data-update-highest><span>' + UPDATE_HIGHEST_LABEL + '</span><b>' + esc(u.highest_version) + updateHighestDays(u) + '</b></div>' : '') +
    (u && u.previous_kept ? '<p class="settings-app__row" data-update-previous>The app this one replaced is kept beside it, at <code>' + esc(u.previous_kept) + '</code>, until the next update replaces it. To go back to it, quit AII OS and put that app in this one\'s place.</p>' : '') +
    (announced && u.announced_not_offered ? '<p class="settings-app__row" data-update-unoffered>' + esc(u.announced_version) + ' is announced, and the release source this machine checks offers ' +
      (u.available_version ? esc(u.available_version) : 'nothing newer than ' + esc(u.current_version) + ', which is running') + '. A newer release may be held back on the way here.</p>' : '') +
    (announced && u.announced_behind ? '<p class="settings-app__row" data-update-behind>This program (' + esc(u.current_version) + ') is newer than the newest release announced (' + esc(u.announced_version) + '). That is expected on a test channel; otherwise the announcement is behind, or this release was withdrawn.</p>' : '') +
    (u && u.lists ? '<p class="settings-app__row" data-update-lists>' + esc(u.lists).replace(/\n/g, '<br>') + '</p>' : '') +
    (u && u.lists_note ? '<p class="settings-app__row settings-app__hint" data-update-lists-note>' + esc(u.lists_note).replace(/\n/g, '<br>') + '</p>' : '') +
    '<p class="settings-app__row settings-app__hint" data-update-cadence>' + updateCadence(u) + heard +
    (managed ? ' This install is managed by your package manager (' + esc(u.stage_refusal) + '): updates are reported here and installed with it.' : '') +
    ' Mobile builds only report.</p>' +
    (c ? '<div class="settings-app__form" data-edit-form>' + limitRowsHTML('updates', c) + savebarHTML('updates', '') + '</div>' : '') +
    '</details>';
}

const AUTO = 'updates-auto';
function automatic(u, c) {
  const sent = configWaitingFor(AUTO);
  if (sent) return !!sent.changes['updates.automatic'];
  const set = c && c.updates && c.updates.automatic;
  return typeof set === 'boolean' ? set : !!(u && u.automatic);
}

export function updatesHTML(u, c) {
  const canCheck = !!(u && u.enabled && !u.checking);
  const managedOffer = !!(u && u.stage_refusal && u.available_version);
  const shown = u && (u.available_version || u.installed_version || u.current_version);
  const hint = (atDefault(u) ? W.checksHourly : updateCadence(u)) + ' ' + W.installReadyHint;
  return '<section class="settings-app__part" aria-labelledby="app-updates" aria-describedby="app-updates-hint">' +
    '<h3 id="app-updates" class="settings-app__h3">' + esc(W.updates) + '</h3>' +
    '<p class="settings-app__line" data-update-state data-announce>' + stateLine(u, c) + '</p>' +
    warningsHTML(u) +
    (u && u.enabled ? '<label class="settings-app__switch"><span class="settings-app__switch-words"><span>' + esc(W.installReady) + '</span>' +
      '<span id="app-updates-hint" class="settings-app__hint">' + esc(hint) + '</span></span>' +
      '<input type="checkbox" role="switch" class="settings-app__sw" id="cfg-uauto" data-update-auto' + (automatic(u, c) ? ' checked' : '') + (configBusy() ? ' disabled' : '') + '></label>'
      : '<p id="app-updates-hint" class="settings-app__hint">' + esc(W.installReadyHint) + '</p>') +
    '<p class="settings-app__said" data-update-auto-said role="status">' + configSaidHTML(AUTO) + '</p>' +
    '<div class="settings-app__acts">' +
      '<button type="button" class="c-button c-button--secondary" data-update-check' + (canCheck ? '' : ' disabled') + '>' + esc(W.checkNow) + '</button>' +
      (u && u.needs_restart ? '<button type="button" class="c-button c-button--primary" data-update-restart>Relaunch now</button>' : '') +
      (u && u.release_url && shown && !managedOffer ? '<a class="settings-app__link" href="' + esc(u.release_url) + '" target="_blank" rel="noopener">' + esc(fill(W.whatChanged, { version: shown })) + '</a>' : '') +
    '</div>' +
    detailsHTML(u, c) +
    '</section>';
}

function lookHTML() {
  const choice = (typeof S.themeChoice === 'function') ? S.themeChoice() : 'system';
  const radio = (value, label) => '<label class="settings-app__radio"><input type="radio" name="app-theme" value="' + value + '" data-theme-choice' + (choice === value ? ' checked' : '') + '>' + esc(label) + '</label>';
  return '<section class="settings-app__part" aria-labelledby="app-look" aria-describedby="app-look-hint">' +
    '<h3 id="app-look" class="settings-app__h3">' + esc(W.appearance) + '</h3>' +
    '<fieldset class="settings-app__choices"><legend class="settings-app__legend">' + esc(W.theme) + '</legend>' +
      radio('system', W.themeSystem) + radio('light', W.themeLight) + radio('dark', W.themeDark) + '</fieldset>' +
    '<p id="app-look-hint" class="settings-app__hint">' + esc(named(W.themeHint, W.themeHintUnnamed)) + '</p>' +
    '</section>';
}

const MARK = [['shape', 'markShape', 0.05], ['cx', 'markAcross', 1], ['cy', 'markDown', 1], ['folds', 'markFolds', 1]];
let markAsked = '', markSaid = '', markHeld = false;
function markHTML() {
  const look = lookOf(getComputedStyle(document.documentElement));
  const slider = ([k, label, step]) => { const [, lo, hi] = LOOK[k];
    return '<label class="settings-app__slider">' + esc(W[label]) + '<input type="range" data-mark-look="' + k + '" min="' + lo + '" max="' + hi + '" step="' + step + '" value="' + look[k] + '"' + (k === 'folds' && !look.shape ? ' disabled' : '') + '></label>'; };
  return '<section class="settings-app__part" data-mark-part aria-labelledby="app-mark" aria-describedby="app-mark-hint">' +
    '<h3 id="app-mark" class="settings-app__h3">' + esc(named(W.mark, W.markUnnamed)) + '</h3>' +
    '<div class="settings-app__mark"><div class="settings-app__preview" data-mark-preview></div><div class="settings-app__sliders">' + MARK.map(slider).join('') + '</div></div>' +
    '<p id="app-mark-hint" class="settings-app__hint">' + esc(named(W.markHint, W.markHintUnnamed)) + '</p>' +
    '<p class="settings-app__said" data-mark-said role="status">' + esc(markSaid) + '</p>' +
    '</section>';
}

const sayMark = text => { markSaid = text; const el = document.querySelector('[data-mark-said]'); if (el) el.textContent = text; };
S.themeAnswered = id => { if (id && id === markAsked) { markAsked = ''; sayMark(W.markSaved); } };
S.themeRefused = (id, why) => { if (!id || id !== markAsked) return false; markAsked = ''; sayMark(fill(W.markNotSaved, { why })); return true; };

function wireMark(root) {
  const part = root.querySelector('[data-mark-part]');
  if (!part) return;
  const inputs = [...part.querySelectorAll('[data-mark-look]')], look = () => Object.fromEntries(inputs.map(i => [i.dataset.markLook, Number(i.value)]));
  const draw = () => { inputs[3].disabled = !Number(inputs[0].value); const n = markNow(); if (n) part.querySelector('[data-mark-preview]').replaceChildren(n.mark.render({ born: n.born, hours: n.hours, state: n.state, size: 160, look: look() })); };
  S.markPreview = () => {
    if (!part.isConnected) { S.markPreview = null; return; }
    if (!markHeld && !markAsked) { const l = lookOf(getComputedStyle(document.documentElement)); for (const i of inputs) i.value = l[i.dataset.markLook]; }
    draw();
  };
  S.markPreview();
  for (const i of inputs) {
    i.oninput = () => { markHeld = true; draw(); };
    i.onchange = () => {
      markHeld = false;
      markAsked = send({ type: 'theme_set', tokens: { [LOOK[i.dataset.markLook][0]]: String(i.value) } });
      sayMark(markAsked ? W.markSaving : C.notConnected);
    };
  }
}

export function notifyHTML() {
  const state = notifyState();
  const said = state === 'unavailable' ? W.notifyUnsupported : state === 'blocked' ? W.notifyBlocked : '';
  return '<section class="settings-app__part" data-notify-card aria-labelledby="app-notify" aria-describedby="app-notify-hint">' +
    '<h3 id="app-notify" class="settings-app__h3">' + esc(W.notifications) + '</h3>' +
    '<label class="settings-app__switch"><span class="settings-app__switch-words"><span>' + esc(named(W.notifySwitch, W.notifySwitchUnnamed)) + '</span>' +
      '<span id="app-notify-hint" class="settings-app__hint">' + esc(W.notifyHint) + '</span></span>' +
      '<input type="checkbox" role="switch" class="settings-app__sw" data-notify-asks' + (state === 'on' ? ' checked' : '') + (state === 'unavailable' ? ' disabled' : '') + '></label>' +
    '<p class="settings-app__said" data-notify-said role="status"' + (said ? '' : ' hidden') + '>' + esc(said) + '</p>' +
    '</section>';
}

export function removeHTML(offered) {
  if (!offered) return '';
  return '<section class="settings-app__part" aria-labelledby="app-remove" aria-describedby="app-remove-hint">' +
    '<h3 id="app-remove" class="settings-app__h3">' + esc(W.remove) + '</h3>' +
    '<p id="app-remove-hint" class="settings-app__hint">' + esc(named(W.removeHint, W.removeHintUnnamed)) + '</p>' +
    '<div class="settings-app__acts"><button type="button" class="c-button c-button--quiet" data-uninstall>' + esc(W.uninstall) + '</button>' +
    '<span class="settings-app__said" data-uninstall-note role="status"></span></div>' +
    '</section>';
}

export function appHTML(c) {
  return '<div class="settings-app" data-app>' +
    '<header class="settings-app__head"><h2 class="settings-app__title">' + esc(W.title) + '</h2><p class="settings-app__intro">' + esc(W.intro) + '</p></header>' +
    lookHTML() + markHTML() + notifyHTML() + updatesHTML(S.update || null, c) + removeHTML(!!(S.stats && S.stats.uninstall_offered)) +
    '</div>';
}

function wireNotify(root) {
  root.querySelectorAll('[data-notify-asks]').forEach(box => {
    box.onchange = () => {
      const part = box.closest('[data-notify-card]');
      box.disabled = true;
      turnNotify(box.checked).then(() => {
        if (!part.isConnected) return;
        const fresh = document.createElement('div');
        fresh.innerHTML = notifyHTML();
        const next = fresh.firstElementChild;
        part.replaceWith(next);
        wireNotify(next);
        next.querySelector('[data-notify-asks]')?.focus();
      });
    };
  });
}

export function wireApp(root) {
  root.querySelectorAll('[data-theme-choice]').forEach(r => { r.onchange = () => { if (r.checked && typeof S.setThemeChoice === 'function') S.setThemeChoice(r.value); }; });
  wireNotify(root);
  wireMark(root);
  root.querySelectorAll('[data-update-auto]').forEach(box => {
    box.onchange = () => {
      const busy = configBusy();
      disableOwing(box);
      sendConfigChanges(AUTO, { 'updates.automatic': box.checked });
      renderSettings();
      if (busy) { const said = document.querySelector('[data-update-auto-said]'); if (said) said.textContent = C.waiting; }
    };
  });
  root.querySelectorAll('[data-update-check]').forEach(btn => { btn.onclick = () => { disableOwing(btn); send({ type: 'update_check' }); }; });
  root.querySelectorAll('[data-update-restart]').forEach(btn => { btn.onclick = () => { disableOwing(btn); btn.textContent = 'Relaunching…'; send({ type: 'restart' }); }; });
  root.querySelectorAll('[data-uninstall]').forEach(btn => { btn.onclick = () => {
    disableOwing(btn);
    const note = root.querySelector('[data-uninstall-note]');
    const asked = send({ type: 'uninstall' });
    if (!asked) btn.disabled = false;
    if (note) note.textContent = asked ? W.uninstallOpens : C.notConnected;
  }; });
}
