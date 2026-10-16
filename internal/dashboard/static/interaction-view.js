import { S } from './state.js';
import { send, wsReady } from './ws.js';
import { renderInteractionPages } from './views/chat.js';

const sources = ['recorded', 'transient'], maxRows = 200, maxBytes = 1024 * 1024;
const pages = new Map(), intents = new Map(), dirty = new Set(), resetSources = new Set();
let pending = null, serial = 0, ready = false, jumping = new Set();
const visible = () => document.visibilityState !== 'hidden' && (!S.view || S.view === 'chat');
const render = (position = '', reading = null) => {
  if (!visible()) return;
  if (jumping.size) position = 'hold';
  if (!reading && restored && !Object.keys(restored.windows).length) {
    reading = restored.reading; restored = null; position = 'restore';
  }
  renderInteractionPages(pages, navigate, !!pending, position, reading);
};
const bounds = rows => ({ anchor_id: rows[0].id, end_id: rows.at(-1).id });
const sequence = row => BigInt(row.sequence);
const rowSize = row => JSON.stringify(row).length * 2;
const size = rows => rows.reduce((n, row) => n + rowSize(row), 0);
const bookmarkKey = 'aii.history.position';
let restored = null;
try {
  const raw = sessionStorage.getItem(bookmarkKey);
  const saved = raw && raw.length <= 32768 ? JSON.parse(raw) : null;
  if (saved?.version === 1 && saved.windows && Object.keys(saved.windows).length > 0 && Object.keys(saved.windows).every(source => sources.includes(source) && ['identity', 'incarnation', 'anchor_id', 'end_id'].every(k => typeof saved.windows[source]?.[k] === 'string')) && saved.reading && typeof saved.reading.key === 'string' &&
      Number.isFinite(saved.reading.offset) && Array.isArray(saved.reading.open) &&
      saved.reading.open.every(e => typeof e.key === 'string' && Array.isArray(e.indices) && e.indices.every(Number.isInteger))) restored = saved;
} catch (_) {}
function saveReading() {
  if (restored) return;
  const reading = S.interactionBookmark?.();
  if (!reading || (S.view && S.view !== 'chat')) return;
  const windows = {};
  for (const [source, page] of pages) if (page.rows.length && !page.stale && !page.error) {
    windows[source] = { identity: page.identity, incarnation: page.incarnation, ...bounds(page.rows) };
  }
  if (!Object.keys(windows).length) return;
  const raw = JSON.stringify({ version: 1, reading, windows });
  try {
    if (raw.length <= 32768) sessionStorage.setItem(bookmarkKey, raw);
    else sessionStorage.removeItem(bookmarkKey);
  } catch (_) {}
}
window.addEventListener('pagehide', saveReading);
S.saveInteractionPosition = saveReading;
function refresh(source) {
  const saved = restored?.windows[source];
  if (!pages.has(source) && saved && restored.reading.following) return { mode: 'latest' };
  if (!pages.has(source) && saved && typeof saved.incarnation === 'string' && typeof saved.anchor_id === 'string' && typeof saved.end_id === 'string') {
    return { mode: 'range', incarnation: saved.incarnation, anchor_id: saved.anchor_id, end_id: saved.end_id };
  }

  const page = pages.get(source);
  if (!page?.rows.length) return { mode: 'latest' };
  const span = S.interactionVisible?.(source) || (!S.interactionVisible ? bounds(page.rows) : null);
  return span && { mode: 'range', incarnation: page.incarnation, ...span };
}
function navigate(source, action, continuation = false) {
  if (continuation && (jumping.size || intents.has(source))) return;
  const page = pages.get(source);
  if (action === 'latest') { S.interactionLatest(); return; }
  if (!page?.rows.length || page.stale || page.error || intents.get(source)?.window.mode === action) return;
  if (action === 'older' && !page.has_older || action === 'newer' && !page.has_newer) return;
  const row = action === 'older' ? page.rows[0] : page.rows.at(-1);
  jumping.clear();
  intents.set(source, { window: { mode: action, incarnation: page.incarnation, anchor_id: row.id }, cursor: '', rows: [] });
  drain();
}
function drain() {
  if (!ready || pending || !visible() || !wsReady()) return;
  if (!intents.size) for (const source of dirty) {
    const window = refresh(source);
    if (window) { dirty.delete(source); intents.set(source, { window, cursor: '', rows: [], follow: !restored && window.mode === 'range' && !!S.interactionFollowing?.() }); break; }
  }
  if (!intents.size) return;
  const [source, intent] = intents.entries().next().value;
  const id = 'interaction-' + (++serial);
  pending = { id, source, intent };
  if (!send({ type: 'query', query: 'interaction', request_id: id,
    interaction_query: { version: 1, source, filter: {}, cursor: intent.cursor, limit: 50 }, interaction_window: intent.window })) pending = null;
  render('hold');
}
S.interactionChanged = () => {
  ready = true;
  if (restored && !Object.keys(restored.windows).length) render('restore');
  for (const source of sources) {
    const page = pages.get(source);
    if (page) page.invalidated = new Set(page.rows.map(r => r.id));
    dirty.add(source);
  }
  drain();
};
S.interactionWake = () => { if (ready) S.interactionChanged(); };
S.interactionLost = () => {
  pending = null; ready = false; intents.clear(); dirty.clear();
  for (const source of sources) { dirty.add(source); const page = pages.get(source); if (page) { page.stale = true; page.invalidated = new Set(page.rows.map(r => r.id)); } }
  if (jumping.size) S.interactionLatest();
  render();
};
S.interactionLatest = () => {
  S.cancelReadingRestore?.();
  restored = null;
  jumping = new Set(sources);
  for (const source of sources) {
    intents.set(source, { window: { mode: 'latest' }, cursor: '', rows: [] }); dirty.delete(source);
  }
  drain();
};
S.interactionHold = () => {
  S.cancelReadingRestore?.();
  restored = null;
  jumping.clear();
  for (const [source, intent] of intents) {
    if (intent.window.mode === 'latest' || intent.follow) {
      intents.delete(source); dirty.add(source);
    }
  }
};
S.interactionScroll = () => {
  for (const source of sources) {
    const page = pages.get(source);
    if (!page?.invalidated?.size) continue;
    const span = S.interactionVisible?.(source); if (!span) continue;
    const first = page.rows.find(r => r.id === span.anchor_id), last = page.rows.find(r => r.id === span.end_id);
    if (first && last && page.rows.some(r => page.invalidated.has(r.id) && sequence(r) >= sequence(first) && sequence(r) <= sequence(last))) dirty.add(source);
  }
  drain();
};
function fail(source, message) {
  const page = pages.get(source) || { rows: [] };
  page.error = message; page.stale = true; pages.set(source, page); jumping.clear();
}
S.interactionPage = (page, id, window) => {
  if (!pending || pending.id !== id) return;
  const { source, intent } = pending; pending = null;
  if (intents.get(source) !== intent) { drain(); return; }
  if (!page || page.version !== 1 || page.source !== source || (page.cursor || '') !== intent.cursor ||
      !Array.isArray(page.rows) || !page.rows.every(r => r.id && /^\d+$/.test(r.sequence)) ||
      !window || window.fingerprint !== page.fingerprint) {
    intents.delete(source); fail(source, 'The host returned an incompatible history view.'); render(); drain(); return;
  }
  const previous = pages.get(source), mode = intent.window.mode;
  const changed = previous?.incarnation && (previous.identity !== page.identity || previous.incarnation !== page.incarnation);
  if (changed && mode !== 'latest') { S.interactionError('INTERACTION_SOURCE_CHANGED', '', id, { source, intent }); return; }
  if (changed) {
    resetSources.add(source);
    for (const other of sources) if (other !== source) { const cached = pages.get(other); if (cached) cached.stale = true; dirty.add(other); }
  }
  if (source === 'transient' && mode !== 'latest' && page.lost !== previous?.lost) {
    S.interactionError('INTERACTION_ANCHOR_UNAVAILABLE', '', id, { source, intent }); return;
  }
  if (intent.rows.length && (intent.identity !== page.identity || intent.incarnation !== page.incarnation)) {
    intents.delete(source); fail(source, 'History changed during readback. Use the down arrow to refresh.'); render(); drain(); return;
  }
  if (!intent.rows.length) intent.hasNewer = window.has_newer;
  intent.identity = page.identity; intent.incarnation = page.incarnation;
  intent.rows = [...page.rows, ...intent.rows];
  if (intent.rows.length > maxRows || size(intent.rows) > maxBytes) {
    intents.delete(source); fail(source, 'Visible history exceeds the readback limit. Use the down arrow to refresh.'); render(); drain(); return;
  }
  if (mode === 'range' && page.next_cursor) {
    if (page.next_cursor === intent.cursor || !page.rows.length) { intents.delete(source); fail(source, 'History readback did not advance.'); render(); drain(); return; }
    intent.cursor = page.next_cursor; drain(); return;
  }
  intents.delete(source);
  let rows = intent.rows, older = window.has_older, newer = window.has_newer;
  if (mode === 'range' && previous?.rows.length) {
    const first = previous.rows.find(r => r.id === intent.window.anchor_id), last = previous.rows.find(r => r.id === intent.window.end_id);
    rows = [...previous.rows.filter(r => sequence(r) < sequence(first)), ...rows, ...previous.rows.filter(r => sequence(r) > sequence(last))];
    older = previous.has_older; newer = previous.has_newer || intent.hasNewer && previous.rows.at(-1).id === last.id;
  } else if (mode === 'range') {
    newer = intent.hasNewer;
  } else if (mode === 'older') {
    rows = [...rows, ...previous.rows]; newer = previous.has_newer;
  } else if (mode === 'newer') {
    rows = [...previous.rows, ...rows]; older = previous.has_older;
  }
  let bytes = size(rows);
  while (rows.length > maxRows || bytes > maxBytes) {
    if (mode === 'older') { bytes -= rowSize(rows.pop()); newer = true; }
    else { bytes -= rowSize(rows.shift()); older = true; }
  }
  page.invalidated = new Set(rows.filter(r => dirty.has(source) || previous?.invalidated?.has(r.id)).map(r => r.id));
  if (!dirty.has(source)) for (const row of intent.rows) page.invalidated.delete(row.id);
  page.rows = rows; page.has_older = !!older; page.has_newer = !!newer;
  page.reset = resetSources.delete(source); pages.set(source, page);
  if (intent.follow && S.interactionFollowing?.() && rows.length && (mode === 'range' || newer)) {
    intents.set(source, { window: { mode: 'newer', incarnation: page.incarnation, anchor_id: rows.at(-1).id }, cursor: '', rows: [], follow: true });
  }
  const jumped = jumping.delete(source) && !jumping.size;
  if (restored?.windows[source]) {
    if (restored.windows[source].identity !== page.identity) restored = null;
    else delete restored.windows[source];
  }
  const reading = visible() && restored && !Object.keys(restored.windows).length ? restored.reading : null;
  if (reading) restored = null;
  render(reading ? 'restore' : jumped ? 'latest' : mode === 'older' || mode === 'range' ? 'hold' : '', reading); drain();
};
S.interactionError = (reason, message, id, request) => {
  if (!request) { if (!pending || pending.id !== id) return; request = pending; pending = null; }
  const { source, intent } = request;
  if (intents.get(source) !== intent) { drain(); return; }
  intents.delete(source);
  if (reason === 'INTERACTION_SOURCE_CHANGED' || reason === 'INTERACTION_ANCHOR_UNAVAILABLE') {
    restored = null;
    resetSources.add(source); const page = pages.get(source); if (page) page.stale = true;
    intents.set(source, { window: { mode: 'latest' }, cursor: '', rows: [] });
  } else fail(source, message || 'History is unavailable');
  render(); drain();
};
