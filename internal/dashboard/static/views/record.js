export const RECORD_WORDS = {
  title: 'Record',
  heading: dir => ['The identity\'s record, newest first, as the database\'s copy holds it. The signed file itself is read with ',
    'aii ledger show -dir ' + (dir || '<this identity\'s directory>'), '.'],
  warning: 'Every row is shown whole: anything typed into a chat or a note, a password included, is here as it was recorded.',
  type: 'Type',
  everyType: 'Every type',
  words: 'Words in the record',
  older: 'Older',
  empty: 'Nothing in the record matches.',
  loading: 'loading the record…',
  ring: ring => ring === null || ring === undefined ? 'no ring' : 'ring ' + ring,
  showAll: bytes => bytes + ' bytes — show all',
  reading: 'reading…',
  failed: why => 'The record could not be read: ' + why,
  indented: 'Indented',
  indentedNote: 'An indented copy made by this page: only spaces and line breaks are added. The record holds the text as it is shown without Indented.',
  sentTab: 'What the model is sent',
};

export const SENT_WORDS = {
  heading: n => 'Exactly what this identity\'s last ' + n + ' model calls were sent, kept in this program\'s memory only and gone when it stops. The key that signs a call in to its provider is never kept.',
  plugins: 'A plugin that calls a model through its own connection is not seen here: only the calls this program makes are.',
  unmarked: 'The system text is shown whole, without marks for where Ring 0 and each Ring 3 section begin.',
  none: 'Nothing is kept: dashboard.sent_requests.kept is 0.',
  over: n => 'The newest call is larger than dashboard.sent_requests.max_bytes (' + n + ' bytes), so it is kept alone.',
  empty: 'No model call has been sent since this program started.',
  line: r => r.time + ' · ' + (r.facility || (r.turn ? 'turn ' + r.turn : 'no turn')) + ' · ' + r.model + ' on ' + r.entry + ' · ' + r.bytes + ' bytes',
  asSent: 'As sent',
  system: 'System text',
  noSystem: 'This call was sent no system or instructions member.',
  image: (type, n) => 'image, ' + type + ', ' + n + ' bytes — show',
  loading: 'loading what the model is sent…',
  failed: why => 'What the model is sent could not be read: ' + why,
};

const W = RECORD_WORDS;

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

function make(tag, cls, text) {
  const el = document.createElement(tag);
  if (cls) el.className = cls;
  if (text !== undefined) el.textContent = text;
  return el;
}

const view = { type: '', words: '', types: null, older: '', target: '', loading: 0, built: false };
let parts = null;

async function readJSON(url) {
  const res = await fetch(url, { cache: 'no-store', credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
  return res.json();
}

async function readRange(path, params) {
  const res = await fetch(path + '?' + new URLSearchParams(params), { cache: 'no-store', credentials: 'same-origin' });
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
  const note = make('p', 'ledger-note', W.indentedNote);
  note.hidden = true;
  const indent = () => {
    const label = make('label', 'ledger-indent');
    const box2 = document.createElement('input');
    box2.type = 'checkbox';
    box2.onchange = () => {
      draw(pre, box2.checked ? indented(text) : text);
      note.hidden = !box2.checked;
    };
    label.append(box2, ' ' + W.indented);
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
        said.textContent = W.failed(e.message);
        more.disabled = false;
      }
    };
    acts.append(more, said);
  } else {
    indent();
  }
  return box;
}

function rowElement(row, rangeBytes) {
  const el = make('article', 'card ledger-row');
  el.id = 'record-' + row.seq;
  el.dataset.seq = row.seq;
  const meta = make('div', 'ledger-meta');
  const link = make('a', '', '#' + row.seq);
  link.href = '#/record/' + row.seq;
  meta.append(link, ' · ' + row.time + ' · ' + row.type + ' · ' + W.ring(row.ring));
  el.appendChild(meta);
  el.appendChild(payloadBox(row.head, row.bytes,
    from => readRest('/record/payload', { seq: row.seq }, from, row.bytes, rangeBytes)));
  if (row.seq === view.target) el.classList.add('ledger-target');
  return el;
}

function build(stack) {
  stack.textContent = '';
  const tabs = make('div', 'tabbar ledger-tabs');
  tabs.setAttribute('role', 'tablist');
  const tab = (label, href) => {
    const a = make('a', 'tab', label);
    a.href = href;
    a.setAttribute('role', 'tab');
    tabs.appendChild(a);
    return a;
  };
  const recordTab = tab(W.title, '#/record'), sentTab = tab(W.sentTab, '#/record/sent');
  const recordPane = make('div', 'ledger-pane'), sentPane = make('div', 'ledger-pane');
  stack.append(tabs, recordPane, sentPane);
  const head = make('div', 'card ledger-head');
  const line = make('p', 'ledger-line');
  const code = make('code');
  line.append(W.heading('')[0], code, W.heading('')[2]);
  const warn = make('p', 'ledger-warn', W.warning);
  const filters = make('div', 'ledger-filters');
  const typeLabel = make('label', '', W.type + ' ');
  const type = document.createElement('select');
  type.dataset.recordType = '';
  typeLabel.appendChild(type);
  const wordsLabel = make('label', '', W.words + ' ');
  const words = document.createElement('input');
  words.type = 'search';
  words.dataset.recordWords = '';
  wordsLabel.appendChild(words);
  filters.append(typeLabel, wordsLabel);
  head.append(line, warn, filters);
  const said = make('p', 'ledger-said');
  said.setAttribute('role', 'status');
  const rows = make('div', 'ledger-rows');
  const older = make('button', 'btn ghost ledger-older', W.older);
  older.type = 'button';
  older.hidden = true;
  recordPane.append(head, said, rows, older);
  type.onchange = () => { view.type = type.value; view.target = ''; load(''); };
  words.onkeydown = e => { if (e.key === 'Enter') { view.words = words.value; view.target = ''; load(''); } };
  words.addEventListener('search', () => { view.words = words.value; view.target = ''; load(''); });
  older.onclick = () => load(view.older, true);
  parts = { code, type, words, said, rows, older, recordTab, sentTab, recordPane, sentPane };
  view.built = true;
}

function drawTypes() {
  const { type } = parts;
  type.textContent = '';
  const every = make('option', '', W.everyType);
  every.value = '';
  type.appendChild(every);
  for (const t of view.types || []) {
    const o = make('option', '', t);
    o.value = t;
    type.appendChild(o);
  }
  type.value = view.type;
}

async function load(before, more) {
  const n = ++view.loading;
  const { said, rows, older } = parts;
  older.hidden = true;
  if (!more) rows.textContent = '';
  said.textContent = W.loading;
  const params = { };
  if (before) params.before = before;
  if (view.type) params.type = view.type;
  if (view.words) params.q = view.words;
  if (!view.types) params.types = '1';
  let page;
  try {
    page = await readJSON('/record?' + new URLSearchParams(params));
  } catch (e) {
    if (n === view.loading) said.textContent = W.failed(e.message);
    return;
  }
  if (n !== view.loading) return;
  parts.code.textContent = W.heading(page.dir)[1];
  if (page.types) { view.types = page.types; drawTypes(); }
  said.textContent = '';
  for (const row of page.rows) rows.appendChild(rowElement(row, page.range_bytes));
  if (!rows.children.length) said.textContent = W.empty;
  view.older = page.older || '';
  older.hidden = !view.older;
  if (view.target) {
    const el = document.getElementById('record-' + view.target);
    if (el) el.scrollIntoView({ block: 'start' });
  }
}

function address() {
  const m = /^#\/record(?:\/(.*))?$/.exec(location.hash || '');
  return m && m[1] ? m[1] : '';
}

export function renderRecord() {
  const el = section();
  const stack = el && el.querySelector('#record-stack');
  if (!stack) return;
  if (!view.built || !stack.firstChild) build(stack);
  const at = address();
  const onSent = at === 'sent';
  parts.recordPane.hidden = onSent;
  parts.sentPane.hidden = !onSent;
  for (const [t, on] of [[parts.recordTab, !onSent], [parts.sentTab, onSent]]) {
    t.classList.toggle('sel', on);
    t.setAttribute('aria-selected', String(on));
  }
  if (onSent) { loadSent(); return; }
  let before = '';
  if (/^[1-9][0-9]*$/.test(at)) {
    view.target = at;
    view.type = '';
    view.words = '';
    parts.words.value = '';
    if (view.types) parts.type.value = '';
    before = (BigInt(at) + 1n).toString();
  } else {
    view.target = '';
  }
  load(before);
}

function imageRuns(text) {
  const runs = [];
  const dataURL = /data:(image\/[A-Za-z0-9.+-]+);base64,([A-Za-z0-9+/=]+)/g;
  const anthropic = /"media_type"\s*:\s*"(image\/[A-Za-z0-9.+-]+)"\s*,\s*"data"\s*:\s*"([A-Za-z0-9+/=]+)"/g;
  for (const re of [dataURL, anthropic]) {
    let m;
    while ((m = re.exec(text))) {
      const end = m.index + m[0].length - (re === anthropic ? 1 : 0);
      runs.push([end - m[2].length, end, m[1]]);
    }
  }
  return runs.sort((a, b) => a[0] - b[0]);
}

function base64Bytes(run) {
  const pad = run.endsWith('==') ? 2 : run.endsWith('=') ? 1 : 0;
  return Math.floor(run.length * 3 / 4) - pad;
}

export function drawSent(pre, text) {
  pre.textContent = '';
  let at = 0;
  for (const [start, end, type] of imageRuns(text)) {
    if (start < at) continue;
    pre.append(text.slice(at, start));
    const run = text.slice(start, end);
    const show = make('button', 'btn ghost sm sent-image', SENT_WORDS.image(type, base64Bytes(run)));
    show.type = 'button';
    show.onclick = () => show.replaceWith(run);
    pre.appendChild(show);
    at = end;
  }
  pre.append(text.slice(at));
}

const sent = { loading: 0 };

async function sentPart(id, part) {
  const res = await fetch('/sent/body?' + new URLSearchParams({ id, part, head: '1' }), { cache: 'no-store', credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
  return { head: await res.text(), total: Number(res.headers.get('X-Total-Bytes')) };
}

function sentRow(r, rangeBytes) {
  const el = make('details', 'card ledger-row sent-row');
  el.dataset.id = r.id;
  const line = make('summary', 'ledger-meta', SENT_WORDS.line(r));
  el.appendChild(line);
  let opened = false;
  el.addEventListener('toggle', async () => {
    if (!el.open || opened) return;
    opened = true;
    const said = make('p', 'ledger-said', SENT_WORDS.loading);
    said.setAttribute('role', 'status');
    el.appendChild(said);
    try {
      for (const [part, label] of [['body', SENT_WORDS.asSent], ['system', SENT_WORDS.system]]) {
        const got = await sentPart(r.id, part);
        el.appendChild(make('h4', 'sent-part', label));
        if (part === 'system' && !got.total) { el.appendChild(make('p', 'ledger-note', SENT_WORDS.noSystem)); continue; }
        const box = payloadBox(got.head, got.total,
          from => readRest('/sent/body', { id: r.id, part }, from, got.total, rangeBytes),
          part === 'body' ? drawSent : undefined);
        box.dataset.part = part;
        el.appendChild(box);
      }
      said.remove();
    } catch (e) {
      said.textContent = SENT_WORDS.failed(e.message);
      opened = false;
    }
  });
  return el;
}

async function loadSent() {
  const n = ++sent.loading;
  const pane = parts.sentPane;
  pane.textContent = '';
  const said = make('p', 'ledger-said', SENT_WORDS.loading);
  said.setAttribute('role', 'status');
  pane.appendChild(said);
  let list;
  try {
    list = await readJSON('/sent');
  } catch (e) {
    if (n === sent.loading) said.textContent = SENT_WORDS.failed(e.message);
    return;
  }
  if (n !== sent.loading) return;
  const head = make('div', 'card ledger-head');
  head.append(make('p', 'ledger-line', SENT_WORDS.heading(list.kept)),
    make('p', 'ledger-line', SENT_WORDS.plugins), make('p', 'ledger-line', SENT_WORDS.unmarked));
  if (!list.kept) head.appendChild(make('p', 'ledger-warn sent-none', SENT_WORDS.none));
  if (list.over) head.appendChild(make('p', 'ledger-warn sent-over', SENT_WORDS.over(list.max_bytes)));
  pane.insertBefore(head, said);
  said.textContent = list.kept && !list.requests.length ? SENT_WORDS.empty : '';
  const rows = make('div', 'ledger-rows');
  for (const r of list.requests) rows.appendChild(sentRow(r, list.range_bytes));
  pane.appendChild(rows);
}
