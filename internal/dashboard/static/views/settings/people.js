import { cleanContacts } from '../settings.js';
import { renderSettings } from './parts.js';
import { configSaidHTML, savebarHTML, sendConfigChanges } from './save.js';
import { S } from '../../state.js';
import { esc, fill, loadingCard } from '../../util.js';
import { installedChannels, messagesHTML, requestMessages } from '../messages.js';
import { mailRepairHTML } from '../mail-repair.js';
import { noticeRouteDrawn } from '../expert.js';
import W from '../../strings/settings-people.js';
import C from '../../strings/components.js';
import { named, say } from './people-say.js';

const thenList = xs => xs.length < 2 ? xs.join('') : xs.slice(0, -1).join(', ') + ', then ' + xs[xs.length - 1];
const capital = s => s ? s.charAt(0).toUpperCase() + s.slice(1) : s;

let book = null;
let editing = null;
export const contactLine = c => ({ name: c.name || '', channel: c.channel || '', address: c.address || '', wake: !!c.wake, operator: !!c.operator });
function groupsOf(lines) {
  const groups = [], at = new Map();
  lines.map(contactLine).forEach(l => {
    if (!at.has(l.name)) { at.set(l.name, groups.length); groups.push({ name: l.name, lines: [] }); }
    groups[at.get(l.name)].lines.push(l);
  });
  return groups;
}
const saved = () => groupsOf((S.config && S.config.contacts) || []);
const current = () => book || saved();
const flatten = groups => groups.flatMap(g => g.lines.map(l => ({ ...l, name: g.name })));
const ensureBook = () => { if (!book) book = saved(); return book; };
const markOf = (g, key) => g.lines.every(l => l[key]) ? true : g.lines.some(l => l[key]) ? null : false;

export function openContactFor(channel, address) {
  ensureBook().push({ name: '', lines: [contactLine({ channel, address })] });
  editing = book.length - 1;
  renderSettings();
  document.querySelector('#settings-stack [data-contact-name]')?.focus();
}

function rowHTML(g, i) {
  const channels = g.lines.map(l => l.channel).filter(Boolean);
  const wake = markOf(g, 'wake') === false ? named(W.doesNotWake) : named(W.mayWake);
  const bits = [capital(thenList(channels)), wake];
  if (markOf(g, 'operator') !== false) bits.push(W.noticesGoHere);
  return '<li class="settings-people__row" data-contact-row="' + i + '"><div class="settings-people__row-words"><span><span class="settings-people__who">' + esc(g.name) + '</span>' +
    (markOf(g, 'operator') !== false ? ' · ' + esc(W.you) : '') + '</span><span class="settings-people__hint">' + esc(bits.filter(Boolean).join(' · ')) + '</span></div>' +
    '<button type="button" class="c-button c-button--secondary" data-contact-edit="' + i + '">' + esc(W.edit) + '</button></li>';
}
function editorHTML(g, i) {
  const line = (l, n) => '<div class="settings-people__line" data-contact-line="' + n + '">' +
    '<input class="settings-people__input" type="text" data-contact-field="channel" aria-label="' + esc(W.contactLines) + ': channel" placeholder="Channel" list="contact-channels" value="' + esc(l.channel) + '">' +
    '<input class="settings-people__input" type="text" data-contact-field="address" aria-label="' + esc(W.contactLines) + ': address" placeholder="Address" value="' + esc(l.address) + '">' +
    (n ? '<button type="button" class="c-button c-button--quiet" data-contact-up="' + n + '">' + esc(W.moveUp) + '</button>' : '') +
    '<button type="button" class="c-button c-button--quiet" data-contact-remove-line="' + n + '" aria-label="' + esc(W.removeContact + ' ' + (l.channel || W.contactLines)) + '">' + esc(W.removeContact) + '</button></div>';
  const mark = (key, label, hint) => {
    const m = markOf(g, key);
    return '<label class="settings-people__check"><input type="checkbox" data-contact-field="' + key + '"' + (m === true ? ' checked' : '') + (m === null ? ' data-mixed' : '') + '><span class="settings-people__check-words"><span>' + esc(label) + '</span><span class="settings-people__hint">' + esc(hint) + '</span></span></label>';
  };
  return '<div class="settings-people__editor" data-contact-editor="' + i + '">' +
    '<label class="settings-people__field">' + esc(W.contactName) + '<input class="settings-people__input" type="text" data-contact-name value="' + esc(g.name) + '"></label>' +
    '<div class="settings-people__field"><span>' + esc(W.contactLines) + '</span><span class="settings-people__hint">' + esc(W.contactLinesHint) + '</span>' + g.lines.map(line).join('') + '</div>' +
    '<div class="settings-people__acts"><button type="button" class="c-button c-button--quiet" data-contact-add-line>' + esc(W.addLine) + '</button></div>' +
    mark('operator', W.thisIsYou, W.thisIsYouHint) + mark('wake', named(W.canWake), W.canWakeHint) +
    '<div class="settings-people__acts"><button type="button" class="c-button c-button--quiet" data-contact-remove="' + i + '">' + esc(W.removeContact) + '</button></div></div>';
}
function contactsHTML(c) {
  if (!c) return loadingCard('configuration');
  const groups = current();
  return '<section class="settings-people__part" data-contacts aria-describedby="contacts-hint"><h3 class="settings-people__h3">' + esc(W.contacts) + '</h3>' +
    '<p class="settings-people__hint" id="contacts-hint">' + esc(named(W.contactsHint)) + '</p>' +
    '<datalist id="contact-channels">' + installedChannels().map(n => '<option value="' + esc(n) + '"></option>').join('') + '</datalist>' +
    (groups.length ? '<ul class="settings-people__list">' + groups.map((g, i) => i === editing ? '<li>' + editorHTML(g, i) + '</li>' : rowHTML(g, i)).join('') + '</ul>'
      : '<p class="settings-people__hint" data-contacts-none>' + esc(say(W.contactsNone)) + '</p>') +
    '<div class="settings-people__acts"><button type="button" class="c-button c-button--quiet" data-contact-add>' + esc(W.addContact) + '</button></div>' +
    '<p class="settings-people__warn" data-contacts-draft' + (book ? '' : ' hidden') + '>' + esc(W.contactsDraft) + '</p>' +
    (book ? savebarHTML('contacts', '', { label: W.saveContacts }) + '<div class="settings-people__acts"><button type="button" class="c-button c-button--quiet" data-contacts-cancel>' + esc(C.cancel) + '</button></div>'
      : '<p class="settings-people__said">' + configSaidHTML('contacts') + '</p>') + '</section>';
}
export function wireContacts(st) {
  const card = st.querySelector('[data-contacts]');
  if (!card) return;
  const open = () => book && editing != null ? book[editing] : null;
  card.querySelectorAll('[data-mixed]').forEach(b => { b.indeterminate = true; });
  card.addEventListener('input', e => {
    const g = open();
    if (!g) return;
    if (e.target.hasAttribute('data-contact-name')) g.name = e.target.value;
    const row = e.target.closest('[data-contact-line]');
    if (row && e.target.dataset.contactField) g.lines[Number(row.dataset.contactLine)][e.target.dataset.contactField] = e.target.value;
  });
  card.querySelectorAll('input[type="checkbox"][data-contact-field]').forEach(box => {
    box.onchange = () => {
      const g = open(), key = box.dataset.contactField;
      if (!g) return;
      g.lines.forEach(l => { l[key] = box.checked; });
      if (key === 'operator' && box.checked) book.forEach(o => { if (o !== g) o.lines.forEach(l => { l.operator = false; }); });
      box.indeterminate = false;
      renderSettings();
    };
  });
  const change = edit => () => { ensureBook(); edit(); renderSettings(); };
  card.querySelectorAll('[data-contacts-cancel]').forEach(b => { b.onclick = () => { book = null; editing = null; renderSettings(); }; });
  card.querySelectorAll('[data-contact-edit]').forEach(b => { b.onclick = change(() => { editing = Number(b.dataset.contactEdit); }); });
  const add = card.querySelector('[data-contact-add]');
  if (add) add.onclick = () => { ensureBook().push({ name: '', lines: [contactLine({})] }); editing = book.length - 1; renderSettings(); card.ownerDocument.querySelector('#settings-stack [data-contact-name]')?.focus(); };
  const addLine = card.querySelector('[data-contact-add-line]');
  if (addLine) addLine.onclick = change(() => { open().lines.push(contactLine({ wake: markOf(open(), 'wake') === true, operator: markOf(open(), 'operator') === true })); });
  card.querySelectorAll('[data-contact-remove-line]').forEach(b => { b.onclick = change(() => {
    const g = open();
    g.lines.splice(Number(b.dataset.contactRemoveLine), 1);
    if (!g.lines.length) { book.splice(editing, 1); editing = null; }
  }); });
  card.querySelectorAll('[data-contact-up]').forEach(b => { b.onclick = change(() => { const l = open().lines, n = Number(b.dataset.contactUp); l.splice(n - 1, 2, l[n], l[n - 1]); }); });
  card.querySelectorAll('[data-contact-remove]').forEach(b => { b.onclick = change(() => { book.splice(Number(b.dataset.contactRemove), 1); editing = null; }); });
}
export function saveContacts() {
  const sent = cleanContacts(flatten(current()));
  sendConfigChanges('contacts', { contacts: sent }, { fields: new Map(), saved() {
    const newer = !!book && JSON.stringify(cleanContacts(flatten(book))) !== JSON.stringify(sent);
    if (!newer) { book = null; editing = null; }
    requestMessages();
    return newer;
  } });
}

function noticesHTML() {
  const { on, busy, said: saidNow, notices: n } = noticeRouteDrawn();
  const lines = (n && n.lines) || [], carried = lines.filter(l => l.carried);
  const held = on && (n?.held || !carried.length);
  const channels = lines.map(l => l.channel);
  return '<section class="settings-people__part" data-notice-route-card aria-describedby="notices-hint"><h3 class="settings-people__h3">' + esc(W.notices) + '</h3>' +
    '<label class="settings-people__switch"><span class="settings-people__switch-words"><span>' + esc(W.noticesSwitch) + '</span>' +
    '<span class="settings-people__hint" id="notices-hint" data-notice-lines>' + esc(lines.length ? fill(W.noticesHint, { lines: capital(thenList(channels)) }) : W.noticesNoLines) + '</span></span>' +
    '<input class="settings-people__sw" type="checkbox" data-notice-route-switch' + (on ? ' checked' : '') + (busy ? ' disabled' : '') + '></label>' +
    '<p class="settings-people__warn" data-route-line data-announce' + (held ? '' : ' hidden') + '>' + (held ? esc(fill(W.noticesNothingNow, { reason: n?.held || 'no installed channel carries a line marked as you' })) : '') + '</p>' +
    '<p class="settings-people__bad" data-notice-said data-announce' + (saidNow ? '' : ' hidden') + '>' + esc(saidNow) + '</p></section>';
}

export function peopleHTML(c) {
  return '<div class="settings-people" data-people><header class="settings-people__head"><h2 class="settings-people__title">' + esc(W.title) + '</h2><p class="settings-people__intro">' + esc(named(W.intro)) + '</p></header>' +
    contactsHTML(c) + noticesHTML() + messagesHTML() + mailRepairHTML() + '</div>';
}
