import { signedFetch } from '../../pagekey.js';
import { fieldOf, recordWord } from './record-read.js';
import { SINCE_BIRTH, within, shortWhen, choice, dateFilter } from './when.js';
import WI from '../../strings/identity.js';
import WC from '../../strings/components.js';
import WS from '../../strings/status.js';

export const RECORD_WORDS = {
  showAll: bytes => bytes + ' bytes — show all',
  reading: 'reading…',
  raw: row => '#' + row.seq + ' · ' + row.time + ' · ' + row.type + (row.ring === null || row.ring === undefined ? '' : ' · ring ' + row.ring) + ' · ' + row.bytes + ' bytes',
  noWords: 'This entry carries no words of its own; As written shows it whole.',
  scope: 'Filters apply to what is loaded here; Older entries loads more.',
  noneLoaded: 'Nothing loaded here matches; Older entries loads more.',
};
const W = RECORD_WORDS;
const fill = (text, values) => text.replace(/\{(\w+)\}/g, (m, k) => (k in values ? values[k] : m));
const unreadable = e => fill(WI.recordUnreadable, { reason: e.message });

export const SENT_MOVED_TO = '#/settings/technical/sent';

function section() {
  let el = document.getElementById('view-record');
  if (el) return el;
  const views = document.getElementById('views');
  if (!views) return null;
  el = document.createElement('section');
  el.className = 'view';
  el.id = 'view-record';
  el.setAttribute('aria-hidden', 'true');
  el.inert = true;
  const stack = document.createElement('div');
  stack.className = 'stack';
  stack.id = 'record-stack';
  el.appendChild(stack);
  views.appendChild(el);
  return el;
}
section();

function make(tag, cls, ...kids) {
  const el = document.createElement(tag);
  if (cls) el.className = cls;
  el.append(...kids);
  return el;
}

const view = { words: '', kind: '', when: SINCE_BIRTH, from: '', to: '', who: '', older: '', target: '', loading: 0, built: false };
let parts = null;

let state = null;
const stateOf = () => (state ||= import('../../state.js').then(m => m.S || null, () => null));
const nameOf = S => { const n = S && S.stats && S.stats.name; return n && n !== 'Unnamed' ? n : ''; };

async function readJSON(url) {
  const res = await signedFetch(url, { cache: 'no-store', credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
  return res.json();
}

async function readRange(path, params) {
  const res = await signedFetch(path + '?' + new URLSearchParams(params), { cache: 'no-store', credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
  return { bytes: new Uint8Array(await res.arrayBuffer()), total: Number(res.headers.get('X-Total-Bytes')) };
}

export async function readRest(path, params, offset, total, rangeBytes) {
  const decoder = new TextDecoder('utf-8');
  let text = '';
  while (offset < total) {
    const got = await readRange(path, Object.assign({}, params, { offset: String(offset), length: String(rangeBytes) }));
    if (!got.bytes.length) break;
    text += decoder.decode(got.bytes, { stream: true });
    offset += got.bytes.length;
    if (got.total) total = got.total;
  }
  return text + decoder.decode();
}

export function indented(text) {
  let out = '', depth = 0, inString = false, escaped = false;
  const space = c => c === ' ' || c === '\t' || c === '\n' || c === '\r';
  const line = () => '\n' + '  '.repeat(depth);
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (inString) {
      out += c;
      if (escaped) escaped = false;
      else if (c === '\\') escaped = true;
      else if (c === '"') inString = false;
      continue;
    }
    if (space(c)) continue;
    if (c === '"') { inString = true; out += c; continue; }
    if (c === '{' || c === '[') {
      let j = i + 1;
      while (j < text.length && space(text[j])) j++;
      if (text[j] === (c === '{' ? '}' : ']')) { out += c + text[j]; i = j; continue; }
      depth++;
      out += c + line();
      continue;
    }
    if (c === '}' || c === ']') { depth = Math.max(0, depth - 1); out += line() + c; continue; }
    if (c === ',') { out += c + line(); continue; }
    if (c === ':') { out += ': '; continue; }
    out += c;
  }
  return out;
}

export function payloadBox(head, bytes, readAll, draw = (pre, text) => { pre.textContent = text; }) {
  const box = make('div', 'ledger-box');
  const pre = make('pre', 'ledger-payload');
  draw(pre, head);
  box.appendChild(pre);
  let text = head;
  const acts = make('div', 'ledger-acts');
  box.appendChild(acts);
  const note = make('p', 'ledger-note', WI.indentedHint);
  note.hidden = true;
  const indent = () => {
    const label = make('label', 'ledger-indent');
    const box2 = document.createElement('input');
    box2.type = 'checkbox';
    box2.onchange = () => {
      draw(pre, box2.checked ? indented(text) : text);
      note.hidden = !box2.checked;
    };
    label.append(box2, ' ' + WI.indented);
    acts.appendChild(label);
    box.appendChild(note);
  };
  const headBytes = new TextEncoder().encode(head).length;
  if (headBytes < bytes) {
    const more = make('button', 'btn ghost sm ledger-more', W.showAll(bytes));
    more.type = 'button';
    const said = make('span', 'ledger-said');
    more.onclick = async () => {
      more.disabled = true;
      said.textContent = W.reading;
      try {
        text = head + await readAll(headBytes);
        draw(pre, text);
        more.remove();
        said.remove();
        indent();
      } catch (e) {
        said.textContent = unreadable(e);
        more.disabled = false;
      }
    };
    acts.append(more, said);
  } else {
    indent();
  }
  return box;
}

const OS_TYPES = new Set(['system.witnessed', 'ledger.correction', 'ledger.error', 'consolidation.run', 'dream.run', 'trust.epoch_accepted', 'identity.key_rotate', 'network.name_claimed']);
const THEIRS = /^(belief|experience|intention|self_model|relationship|reflection|tension|commitment|knowledge|working_style|work|edge)\./;
export const whoWrote = type => OS_TYPES.has(type) ? 'os' : THEIRS.test(type) ? 'identity' : '';

const KINDS = [
  ['beliefs', WI.tabs.beliefs, /^belief\./],
  ['experiences', WI.tabs.experiences, /^experience\./],
  ['between', WI.tabs.between, /^relationship\./],
  ['work', WI.kindWork, /^(intention|commitment|work)\./],
  ['verification', WI.kindVerification, /^(system\.witnessed|trust\.epoch_accepted|ledger\.(correction|error))$/],
];
const kindOf = type => (KINDS.find(k => k[2].test(type)) || [''])[0];

const WORD_FIELDS = ['statement', 'content', 'synthesis_text', 'reason', 'text', 'summary', 'description', 'note', 'result'];
export function wordsOf(text) {
  let payload;
  try { payload = JSON.parse(text); } catch (e) { return ''; }
  for (const k of WORD_FIELDS) if (payload && typeof payload[k] === 'string' && payload[k].trim()) return payload[k];
  return '';
}
const headWords = head => { for (const k of WORD_FIELDS) { const w = fieldOf(head, k); if (w.trim()) return w; } return ''; };
let renderer = null;
const inWords = () => (renderer ||= import('../../components/in-words/in-words.js').catch(() => null));

function matches(el) {
  if (view.kind && el.dataset.kind !== view.kind) return false;
  if (view.who && el.dataset.who !== view.who) return false;
  return within(Date.parse(el.dataset.when || ''), view);
}
function filterLoaded() {
  if (!parts) return;
  const on = !!(view.kind || view.who || view.when !== SINCE_BIRTH);
  let shown = 0;
  for (const li of parts.rows.children) { li.hidden = on && !matches(li.firstChild); if (!li.hidden) shown++; }
  parts.scope.hidden = !on;
  parts.noneLoaded.hidden = !on || shown > 0 || !parts.rows.children.length;
}

function copying(label, text, done, cls) {
  const said = make('span', 'record__said');
  said.setAttribute('role', 'status');
  const button = make('button', 'c-button c-button--quiet ' + cls, label);
  button.type = 'button';
  button.onclick = async () => {
    try { said.textContent = (await copyOut(await text())) ? done : WC.copyFailed; } catch (e) { said.textContent = unreadable(e); }
  };
  return [button, said];
}

function line(template, values) {
  const out = [];
  for (const piece of template.split(/(\{\w+\})/)) {
    const key = /^\{(\w+)\}$/.exec(piece);
    if (!key) { if (piece) out.push(piece); continue; }
    const v = values[key[1]];
    if (v === '' || v === null || v === undefined) { if (typeof out.at(-1) === 'string') out.pop(); continue; }
    out.push(v);
  }
  return out;
}

function rowElement(row, rangeBytes, name) {
  const seq = BigInt(row.seq).toLocaleString();
  const el = make('article', 'record__item');
  el.id = 'record-' + row.seq;
  el.setAttribute('aria-label', fill(WI.entry, { seq }));
  Object.assign(el.dataset, { seq: row.seq, when: row.time, who: whoWrote(row.type), kind: kindOf(row.type) });
  const who = el.dataset.who;
  const kind = make('strong', 'record__kind-word', recordWord(row.type, WI));
  const head = make('span', 'record__line', ...line(WI.entryKind, {
    kind, who: who === 'os' ? WI.whoOs : who === 'identity' ? make('span', 'record__them', name || WI.whoThemUnnamed) : '', when: shortWhen(Date.parse(row.time)),
  }));
  const open = make('button', 'record__open', head);
  open.type = 'button';
  open.setAttribute('aria-expanded', 'false');
  open.setAttribute('aria-controls', 'record-detail-' + row.seq);
  const words = headWords(row.head);
  if (words) open.append(make('span', 'record__words', words));
  const link = location.href.split('#')[0] + '#/identity/record/' + row.seq;
  el.append(make('div', 'record__head', make('code', 'record__seq', seq), open,
    make('span', 'record__head-acts', ...copying(WI.copyLink, () => link, WC.linkCopied, 'record__link'))));

  const whole = from => readRest('/record/payload', { seq: row.seq }, from, row.bytes, rangeBytes);
  const headBytes = new TextEncoder().encode(row.head).length;
  let all = headBytes < row.bytes ? null : row.head;
  const read = async () => all ??= row.head + await whole(headBytes);
  let detail = null;
  const draw = () => {
    const said = make('p', 'record__said', W.reading);
    const body = make('div', 'record__in-words in-words');
    const [copy, copied] = copying(WI.copyEntry, read, WC.copied, 'record__copy');
    detail = make('div', 'record__detail',
      make('section', 'record__half', make('h3', 'record__h', WI.inWords), said),
      make('section', 'record__half', make('div', 'record__as-head', make('h3', 'record__h', WI.asWritten), copy, copied),
        make('p', 'record__raw', make('code', '', W.raw(row))), payloadBox(row.head, row.bytes, whole)));
    detail.id = 'record-detail-' + row.seq;
    detail.firstChild.setAttribute('aria-label', WI.inWords);
    detail.lastChild.setAttribute('aria-label', WI.asWritten);
    el.appendChild(detail);
    (async () => {
      try {
        const text = wordsOf(await read()), r = await inWords();
        if (!text) body.textContent = W.noWords; else if (r) r.draw(body, text); else body.textContent = text;
        said.replaceWith(body);
      } catch (e) { said.textContent = unreadable(e); }
    })();
  };
  const toggle = on => {
    if (on && !detail) draw();
    if (detail) detail.hidden = !on;
    el.classList.toggle('record__item--open', on);
    open.setAttribute('aria-expanded', String(on));
  };
  open.onclick = () => toggle(open.getAttribute('aria-expanded') !== 'true');
  if (row.seq === view.target) { el.setAttribute('aria-current', 'true'); toggle(true); }
  return make('li', 'record__entry', el);
}

async function copyOut(text) {
  if (navigator.clipboard && window.isSecureContext) {
    try { await navigator.clipboard.writeText(text); return true; } catch (e) { }
  }
  const ta = document.createElement('textarea');
  ta.value = text; ta.setAttribute('readonly', ''); ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta); ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
  ta.remove();
  return ok;
}

function witnessed(S) {
  const c = S && S.cont;
  if (!c || !c.witness_url || !c.witnessed_at || !c.anchored_seq) return '';
  let witness = '';
  try { witness = new URL(c.witness_url).host; } catch (e) { witness = c.witness_url; }
  return fill(WS.verifiedThrough, { seq: Number(c.anchored_seq).toLocaleString(), when: shortWhen(Date.parse(c.witnessed_at)), witness });
}

function build(stack) {
  stack.textContent = '';
  const search = make('input', 'c-filter__select record__search');
  search.type = 'search';
  search.placeholder = WI.searchRecord;
  search.setAttribute('aria-label', WI.searchRecord);
  const kind = choice(WI.filterKind, [['', WI.anyKind], ...KINDS.map(([v, text]) => [v, text])], view.kind, v => { view.kind = v; filterLoaded(); });
  const [when, dates] = dateFilter(WI, view, filterLoaded);
  const who = choice(WI.filterWho, [['', WI.anyone], ['identity', WI.whoThemUnnamed], ['os', WI.whoOs]], view.who, v => { view.who = v; filterLoaded(); });
  const verified = make('span', 'record__witnessed');
  const filters = make('div', 'record__filters', search, kind, when, dates, who, verified);
  filters.setAttribute('role', 'group');
  filters.setAttribute('aria-label', WI.filters);
  const scope = make('p', 'record__scope', W.scope);
  scope.hidden = true;
  const said = make('p', 'record__said');
  said.setAttribute('role', 'status');
  const rows = make('ol', 'record__list');
  rows.setAttribute('aria-label', WI.record);
  const noneLoaded = make('p', 'record__scope', W.noneLoaded);
  noneLoaded.hidden = true;
  const older = make('button', 'c-button record__older', WI.olderEntries);
  older.type = 'button';
  older.hidden = true;
  stack.append(make('div', 'record', filters, scope, said, rows, noneLoaded, older, make('p', 'record__hint', WI.recordHint)));
  const find = () => { view.words = search.value; view.target = ''; load(''); };
  search.onkeydown = e => { if (e.key === 'Enter') find(); };
  search.addEventListener('search', find);
  older.onclick = () => load(view.older, true);
  parts = { search, kind: kind.querySelector('select'), when: when.querySelector('select'), dates, who: who.querySelector('select'), verified, said, rows, older, scope, noneLoaded };
  view.built = true;
}

async function load(before, more) {
  const n = ++view.loading;
  const { said, rows, older } = parts;
  older.hidden = true;
  if (!more) rows.textContent = '';
  said.textContent = WI.trailLoading;
  const params = {};
  if (before) params.before = before;
  if (view.words) params.q = view.words;
  let page, S;
  try {
    [page, S] = await Promise.all([readJSON('/record?' + new URLSearchParams(params)), stateOf()]);
  } catch (e) {
    if (n === view.loading) said.textContent = unreadable(e);
    return;
  }
  if (n !== view.loading) return;
  if (S) S.recordNamed = () => rename(nameOf(S));
  const name = nameOf(S);
  parts.who.querySelector('option[value="identity"]').textContent = name || WI.whoThemUnnamed;
  parts.verified.textContent = witnessed(S);
  said.textContent = '';
  for (const row of page.rows) rows.appendChild(rowElement(row, page.range_bytes, name));
  if (!rows.children.length) said.textContent = WI.recordNoMatch;
  filterLoaded();
  view.older = page.older || '';
  older.hidden = !view.older;
  if (view.target) {
    const el = document.getElementById('record-' + view.target);
    if (el) el.scrollIntoView({ block: 'start' });
  }
}

function rename(name) {
  if (!name || !parts) return;
  for (const el of document.querySelectorAll('.record__them')) if (el.textContent !== name) el.textContent = name;
  const o = parts.who.querySelector('option[value="identity"]');
  if (o && o.textContent !== name) o.textContent = name;
}

function address() {
  const m = /^#\/(?:identity\/)?record(?:\/(.*))?$/.exec(location.hash || '');
  return m && m[1] ? m[1] : '';
}

export function renderRecord() {
  const el = section();
  const stack = el && el.querySelector('#record-stack');
  if (!stack) return;
  if (!view.built || !stack.firstChild) build(stack);
  if (!el.querySelector('[data-record-sent]')) {
    const sent = make('p', 'record__sent');
    sent.dataset.recordSent = '';
    sent.append(Object.assign(make('a'), { href: SENT_MOVED_TO, textContent: WI.recordSent }));
    stack.prepend(sent);
  }
  const at = address();
  if (at === 'sent') { location.hash = SENT_MOVED_TO; return; }
  let before = '';
  if (/^[1-9][0-9]*$/.test(at)) {
    Object.assign(view, { target: at, words: '', kind: '', who: '', when: SINCE_BIRTH, from: '', to: '' });
    parts.search.value = parts.kind.value = parts.who.value = '';
    parts.when.value = SINCE_BIRTH;
    parts.dates.hidden = true;
    for (const input of parts.dates.querySelectorAll('input')) input.value = '';
    before = (BigInt(at) + 1n).toString();
  } else {
    view.target = '';
  }
  load(before);
}
