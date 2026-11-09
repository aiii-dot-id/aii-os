import { hinted, limitOverridesHTML, limitValueText, moreOpen, LIMIT_WORD } from '../parts.js';
import { cognitionApplies } from '../applies.js';
import { alike, captureForm, configValue, focusOf, restoreForm, wireLimits } from '../save.js';
import { drawBar, formDrawn, formEdited } from './savebar.js';
import { renderInto } from '../../../announce.js';
import { ticketed } from '../../../pagekey.js';
import { S } from '../../../state.js';
import { esc, fill } from '../../../util.js';
import W from '../../../strings/settings-technical-forms.js';
import { recordStateHTML } from './record-state.js';
export { readRecordState } from './record-state.js';

export const nameOf = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
export const named = (text, slots) => fill(text, { name: nameOf() || W.unnamed, ...slots });
export const say = (text, slots) => { const out = named(text, slots); return out.charAt(0).toUpperCase() + out.slice(1); };

export const hint = text => text ? '<p class="technical-form__hint">' + esc(text) + '</p>' : '';
export const fieldHTML = ({ id, label, about = '', value = '', type = 'text', attrs = '', unit = '', after = '', short = false, described = '' }) => {
  const by = [about && id + '-about', described].filter(Boolean).join(' ');
  const input = '<input class="technical-form__input' + (short ? ' technical-form__input--short' : '') + '" type="' + type + '" id="' + id + '" value="' + esc(value == null ? '' : value) + '"' + (by ? ' aria-describedby="' + by + '"' : '') + attrs + '>';
  return '<div class="technical-form__field' + (short ? ' technical-form__field--line' : '') + '"><label class="technical-form__label" for="' + id + '">' + esc(label) + '</label>' +
    (short ? '<span class="technical-form__entry">' + input + (unit ? '<span class="technical-form__unit">' + esc(unit) + '</span>' : '') + after + '</span>' : input) +
    '<p class="technical-form__warn" data-range-said hidden></p>' +
    (about ? '<p class="technical-form__hint" id="' + id + '-about">' + esc(about) + '</p>' : '') + '</div>';
};
export const checkHTML = ({ id, label, about = '', checked = false, attrs = '' }) =>
  '<label class="technical-form__check"><input type="checkbox" id="' + id + '"' + (checked ? ' checked' : '') + attrs + '><span class="technical-form__check-words"><span>' + esc(label) + '</span>' +
  (about ? '<span class="technical-form__hint">' + esc(about) + '</span>' : '') + '</span></label>';
export const formHTML = (section, heading, body, note = '') =>
  '<div class="technical-form" data-edit-form data-technical-form="' + section + '"><p class="technical-form__heading">' + esc(heading) + '</p>' + hint(note) + body + '</div>';
export const moreHTML = (title, rows, c, wordOf) => {
  const sets = rows.map(([key, word]) => [settingOf(c, key), word]).filter(([set]) => set && set.per_model);
  return sets.length ? '<details class="technical-form__more" data-lim-more="' + esc(title) + '"' + moreOpen(title) + '><summary>' + esc(W.more) + '</summary>' + hint(W.moreHint) +
    sets.map(([set, word]) => '<div class="technical-form__override"><p class="technical-form__label">' + esc(named(wordOf(word))) + '</p>' + limitOverridesHTML(set, c) + '</div>').join('') + '</details>' : '';
};
export const groupHTML = (title, body) => '<section class="technical-form__group"><h3 class="technical-form__group-title">' + esc(title) + '</h3>' + body + '</section>';

const MEGA = bytes => (bytes % 1048576 === 0 ? 1048576 : 1000000);
export const shownUnit = set => set.unit === 'seconds' && set.default >= 300 ? ['minutes', 60]
  : set.unit === 'milliseconds' && set.default >= 1000 ? ['seconds', 1000]
  : set.unit === 'bytes' && set.default >= 1000000 ? ['MB', MEGA(set.default)]
  : [W.units[set.unit] ?? set.unit, 1];
export function settingHTML(set, c, label, about, usualApplies = '') {
  const stated = configValue(c, set.key), configured = stated === 0 && set.zero_is_default ? null : stated, lifted = configured === LIMIT_WORD || (configured == null && !!set.default_unlimited);
  const hasNone = set.key === 'llm.retries', none = hasNone && configured === -1;
  const id = 'tech-' + set.key.replace(/[^a-z0-9]+/g, '-');
  const [unit, per] = shownUnit(set);
  const inUnit = v => +(v / per).toFixed(3);
  const usual = fill(W.usual, { value: set.default_unlimited ? W.noLimit : inUnit(set.default).toLocaleString() });
  const applies = cognitionApplies(set.key);
  const line = [about, applies === usualApplies ? '' : applies.charAt(0).toUpperCase() + applies.slice(1) + '.'].filter(Boolean).join(' ');
  const shown = configured == null || lifted || none ? '' : inUnit(configured);
  const eff = set.effective || {};
  return '<div class="technical-form__setting" data-technical-setting="' + esc(set.key) + '">' +
    fieldHTML({ id, label, short: true, unit: label.toLowerCase().includes(unit.replace(/s$/, '').toLowerCase()) ? '' : unit, described: line ? id + '-applies' : '', value: shown, type: 'number',
      after: (set.unlimited ? checkHTML({ id: id + '-unl', label: W.unlimited, checked: lifted, attrs: ' data-cog-unl="' + esc(set.key) + '"' }) : '') +
        (hasNone ? checkHTML({ id: id + '-none', label: W.limits.retriesNone, checked: none, attrs: ' data-cog-none="' + esc(set.key) + '"' }) : ''),
      attrs: ' data-cog-key="' + esc(set.key) + '" data-cog-per="' + per + '" data-cog-shown="' + esc(shown) + '" min="' + esc(hasNone ? 0 : inUnit(set.min)) + '" max="' + esc(inUnit(set.max)) + '" step="' + (per > 1 ? 'any' : '1') + '" data-usual="' + esc(usual) + '" placeholder="' + (lifted || none ? '' : esc(usual)) + '"' + (lifted || none ? ' disabled' : '') + hinted(lifted || none ? null : configured, usual) }) +
    (set.held ? '<p class="technical-form__warn" data-cog-held="' + esc(set.key) + '">' + esc(set.held) + '</p>' : '') +
    (line ? '<p class="technical-form__hint" id="' + id + '-applies" data-cog-applies="' + esc(set.key) + '">' + esc(line) + '</p>' : '') +
    (eff.provider ? '<p class="technical-form__hint" data-lim-effective="' + esc(set.key) + '">' + esc(fill(W.overrideSaid, { value: limitValueText(eff.value), where: eff.provider + (eff.model ? ' · ' + eff.model : '') })) + '</p>' : '') + '</div>';
}
const settingsOf = c => (c && c.cognition && c.cognition.settings) || [];
export const settingOf = (c, key) => settingsOf(c).find(s => s.key === key) || null;

let owed = null;
function checkRange(el) {
  const said = el.closest('.technical-form__field')?.querySelector('[data-range-said]');
  if (!said) return;
  const n = el.disabled || el.value === '' ? null : Number(el.value), low = el.min === '' ? -Infinity : Number(el.min), high = el.max === '' ? Infinity : Number(el.max);
  const out = n !== null && (n < low || n > high), broken = !el.disabled && (el.validity.badInput || el.validity.stepMismatch);
  said.hidden = !out && !broken;
  said.textContent = broken ? fill(W.wholeNumber, { label: el.closest('.technical-form__field').querySelector('label').textContent })
    : !out ? '' : high === Infinity ? fill(W.atLeast, { min: low.toLocaleString() }) : fill(W.outOfRange, { min: low.toLocaleString(), max: high.toLocaleString() });
  if (out || broken) el.setAttribute('aria-invalid', 'true'); else el.removeAttribute('aria-invalid');
}
const checkRanges = root => root.querySelectorAll('input[type="number"]').forEach(checkRange);
export function drawForm(root, section, html, wire) {
  if (root.technicalComposing) { root.technicalDue = true; return; }
  if (root.technicalDrawn === html && root.dataset.form === section) { drawBar(); return; }
  root.technicalDrawn = html;
  const focused = document.activeElement;
  const had = focused === document.body ? owed : focusOf(root, focused);
  const active = captureForm(root);
  renderInto(root, html, root.dataset.form !== section);
  root.dataset.form = section;
  restoreForm(root, active);
  root.oninput = root.onchange = e => { if (e.target.matches('input[type="number"]')) checkRange(e.target); formEdited(); };
  if (!root.technicalListening) {
    root.technicalListening = true;
    root.addEventListener('compositionstart', () => { root.technicalComposing = true; });
    root.addEventListener('compositionend', () => {
      root.technicalComposing = false;
      if (root.technicalDue) { root.technicalDue = false; queueMicrotask(() => S.renderTechnical?.()); }
    });
  }
  wireLimits(root);
  if (wire) wire(root);
  checkRanges(root);
  formDrawn(root, section);
  owed = null;
  if (had && !root.contains(document.activeElement)) {
    const again = alike(root, had.tag, had.name)[had.nth];
    if (again) {
      again.focus({ preventScroll: true });
      if (document.activeElement !== again) owed = had;
      else if (had.start !== null) again.setSelectionRange?.(had.start, had.end, had.direction);
    }
  }
}
const loading = root => { root.dataset.form = ''; root.textContent = W.loading; };

const relative = new Intl.RelativeTimeFormat([], { numeric: 'auto' });
function ago(iso) {
  const s = Math.round((Date.parse(iso) - Date.now()) / 1000);
  for (const [unit, size] of [['day', 86400], ['hour', 3600], ['minute', 60]]) {
    if (Math.abs(s) >= size) return relative.format(Math.round(s / size), unit);
  }
  return relative.format(0, 'second');
}
function receiptLine() {
  const k = S.cont;
  if (!k) return '';
  const V = W.verification;
  if (!S.config?.witness?.url) return V.noServer;
  if (k.mode === 'degraded_witness' && k.degraded_since) return fill(V.unreachable, { time: new Date(k.degraded_since).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) });
  return k.witnessed_at && !isNaN(Date.parse(k.witnessed_at)) ? fill(V.last, { ago: ago(k.witnessed_at), seq: Number(k.anchored_seq || 0).toLocaleString() }) : V.none;
}
export function drawVerification(root) {
  const c = S.config, V = W.verification;
  if (!c) return loading(root);
  drawForm(root, 'witness', formHTML('witness', named(V.heading),
    '<p class="technical-form__fact" data-receipt-line>' + esc(receiptLine()) + '</p>' +
    '<div class="technical-form__group" data-record-state>' + recordStateHTML() + '</div>' +
    fieldHTML({ id: 'cfg-wurl', label: V.server, about: V.serverHint, value: c.witness.url }) +
    fieldHTML({ id: 'cfg-wint', label: V.interval, about: V.intervalHint, value: c.witness.interval_events, short: true, type: 'number' }), W.appliesAtStart));
}

export function drawHelpers(root) {
  const c = S.config, H = W.helpers;
  if (!c) return loading(root);
  const roles = (c.agency && c.agency.roles) || [];
  const list = '<ul class="technical-form__list" data-roles>' + roles.map(r =>
    '<li class="technical-form__row" data-role="' + esc(r.name) + '"><span class="technical-form__row-title">' + esc(fill(H.row, { role: r.name, provider: r.provider, model: r.model })) + '</span>' +
    '<span class="technical-form__hint">' + esc(r.description || say(H.noDescription)) + '</span></li>').join('') + '</ul>' + hint(H.changeNote);
  drawForm(root, 'agency', formHTML('agency', say(H.heading), list));
}

const toggles = (box, input) => { box.onchange = () => { input.disabled = box.checked; }; };
export function drawLogsForm(root) {
  const c = S.config, L = W.logsForm;
  if (!c) return loading(root);
  const cfg = c.logs || {}, keepAll = cfg.max_backups === -1, never = cfg.compress_days === -1;
  const mb = v => (v ? +(v / 1048576).toFixed(3) : '');
  const size = (id, label, about, value, usual) => fieldHTML({ id, label, about, short: true, unit: 'MB', type: 'number', value: mb(value),
    attrs: ' min="0" step="any" data-cog-shown="' + mb(value) + '"' + (usual ? ' placeholder="' + esc(fill(W.usual, { value: mb(usual).toLocaleString() })) + '"' : '') });
  drawForm(root, 'logs', formHTML('logs', L.heading,
    fieldHTML({ id: 'cfg-ldir', label: L.folder, about: L.folderHint, value: cfg.dir }) +
    fieldHTML({ id: 'cfg-lbackups', label: L.keep, short: true, type: 'number', value: keepAll ? '' : cfg.max_backups, attrs: ' min="0" step="1"' + (keepAll ? ' disabled' : ''),
      after: checkHTML({ id: 'cfg-lbackups-all', label: L.keepAll, checked: keepAll }) }) +
    fieldHTML({ id: 'cfg-lcomp', label: L.compressAfter, short: true, unit: L.days, type: 'number', value: never ? '' : cfg.compress_days, attrs: ' min="0" step="1"' + (never ? ' disabled' : ''),
      after: checkHTML({ id: 'cfg-lcomp-never', label: L.never, checked: never }) }) +
    size('cfg-lbytes', L.rollAt, L.rollAtHint, cfg.max_bytes, cfg.max_bytes_default) +
    size('cfg-ltotal', L.keepUnder, L.keepUnderHint, cfg.max_total_bytes, cfg.max_total_bytes_default), W.appliesAtStart), form => {
    toggles(form.querySelector('#cfg-lbackups-all'), form.querySelector('#cfg-lbackups'));
    toggles(form.querySelector('#cfg-lcomp-never'), form.querySelector('#cfg-lcomp'));
  });
}

function wireStorage(root) {
  const form = root.querySelector('form[action="/database/export"]');
  if (!form) return;
  form.onsubmit = async event => {
    event.preventDefault();
    form.action = await ticketed('POST', '/database/export');
    form.submit();
    form.setAttribute('action', '/database/export');
  };
}
export function drawStorage(root) {
  const c = S.config, D = W.storage;
  if (!c) return loading(root);
  const db = c.database || {};
  const formats = [['', D.formatKeep], ['sqlite', D.formatPlain], ['zstd', D.formatCompressed]];
  const bytes = db.dictionary_bytes == null ? D.activeUnknown : Number(db.dictionary_bytes).toLocaleString() + ' bytes';
  const activeName = ({ sqlite: D.formatPlain, zstd: D.formatCompressed })[db.active] || db.active || D.activeUnknown;
  drawForm(root, 'storage', formHTML('storage', named(D.heading),
    '<p class="technical-form__fact">' + fill(esc(D.active), { format: '<strong id="db-active">' + esc(activeName) + '</strong>' }) + '</p>' +
    '<div class="technical-form__field"><label class="technical-form__label" for="cfg-db-format">' + esc(D.format) + '</label><select class="technical-form__input" id="cfg-db-format">' +
    formats.map(([value, label]) => '<option value="' + value + '"' + ((db.preferred || '') === value ? ' selected' : '') + '>' + esc(label) + '</option>').join('') + '</select>' + hint(D.formatHint) + '</div>' +
    (db.notice ? '<p class="technical-form__warn" data-announce>' + esc(db.notice) + '</p>' : '') +
    ((db.recovery || []).length ? '<p class="technical-form__hint">' + esc(D.recovery) + '</p><ul class="technical-form__paths">' + db.recovery.map(path => '<li>' + esc(path) + '</li>').join('') + '</ul>' : '') +
    '<div class="technical-form__part">' + (db.can_export
      ? '<form method="post" action="/database/export" class="technical-form__export"><button class="c-button c-button--secondary" type="submit" data-export-db>' + esc(D.export) + '</button></form>'
      : '<button type="button" class="c-button c-button--secondary" disabled>' + esc(D.unavailableNow) + '</button>' + hint(D.exportUnavailable)) + hint(named(D.exportHint)) + '</div>' +
    '<details class="technical-form__more" data-lim-more="storage"' + moreOpen('storage') + '><summary>' + esc(D.advanced) + '</summary>' +
    '<p class="technical-form__label">' + esc(D.optimize) + '</p>' + hint(D.optimizeHint + ' ' + D.optimizeCost) +
    checkHTML({ id: 'cfg-db-levels', label: D.optimizeLevels, checked: !!db.optimize_levels }) +
    checkHTML({ id: 'cfg-db-dictionary', label: D.optimizeDictionary, checked: !!db.learn_dictionary }) +
    '<p class="technical-form__hint">' + fill(esc(D.dictionary), { bytes: '<strong id="db-dictionary-bytes">' + esc(bytes) + '</strong>' }) + '</p></details>'), wireStorage);
}
