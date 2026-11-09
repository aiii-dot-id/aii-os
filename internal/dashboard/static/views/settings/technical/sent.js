import { h, fill } from '../../../util.js';
import { signedFetch } from '../../../pagekey.js';
import { readRest, payloadBox } from '../../record.js';
import W from '../../../strings/settings-technical.js';

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
    const show = h('button', { type: 'button', class: 'btn ghost sm sent-image' }, fill(W.image, { type, bytes: base64Bytes(run) }));
    show.onclick = () => show.replaceWith(run);
    pre.appendChild(show);
    at = end;
  }
  pre.append(text.slice(at));
}

export function whatFor(r) {
  const known = { dream: W.forDream, consolidate: W.forConsolidate, brief: W.forBrief, outcome: W.forOutcome, tool_outcome: W.forToolOutcome };
  if (r.facility) return known[r.facility] || r.facility;
  return r.turn ? fill(W.forTurn, { turn: r.turn }) : W.forNoTurn;
}

export function rowLine(r) {
  return fill(W.rowLine, { when: r.time, what: whatFor(r), model: r.model, entry: r.entry, bytes: r.bytes });
}

async function readJSON(url) {
  const res = await signedFetch(url, { cache: 'no-store', credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
  return res.json();
}

async function sentPart(id, part) {
  const res = await signedFetch('/sent/body?' + new URLSearchParams({ id, part, head: '1' }), { cache: 'no-store', credentials: 'same-origin' });
  if (!res.ok) throw new Error((await res.text()).trim() || String(res.status));
  return { head: await res.text(), total: Number(res.headers.get('X-Total-Bytes')) };
}

function sentRow(r, rangeBytes) {
  const el = h('details', { class: 'card ledger-row sent-row', 'data-id': r.id }, h('summary', { class: 'ledger-meta' }, rowLine(r)));
  let opened = false;
  el.addEventListener('toggle', async () => {
    if (!el.open || opened) return;
    opened = true;
    const said = h('p', { class: 'ledger-said', role: 'status' }, W.loading);
    el.appendChild(said);
    try {
      for (const [part, label] of [['body', W.asSent], ['system', W.system]]) {
        const got = await sentPart(r.id, part);
        el.appendChild(h('h4', { class: 'sent-part' }, label));
        if (part === 'system' && !got.total) { el.appendChild(h('p', { class: 'ledger-note' }, W.noSystem)); continue; }
        const box = payloadBox(got.head, got.total,
          from => readRest('/sent/body', { id: r.id, part }, from, got.total, rangeBytes),
          part === 'body' ? drawSent : undefined);
        box.dataset.part = part;
        el.appendChild(box);
      }
      said.remove();
    } catch (e) {
      said.textContent = fill(W.failed, { why: e.message });
      opened = false;
    }
  });
  return el;
}

const sent = { loading: 0 };

export function bytesSaid(n) {
  const units = ['bytes', 'KB', 'MB', 'GB'];
  let v = Number(n) || 0, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return (i ? v.toLocaleString([], { maximumFractionDigits: 1 }) : String(v)) + ' ' + units[i];
}

export async function loadSees(pane) {
  const n = ++sent.loading;
  pane.textContent = '';
  const said = h('p', { class: 'ledger-said', role: 'status' }, W.loading);
  pane.appendChild(said);
  let list;
  try {
    list = await readJSON('/sent');
  } catch (e) {
    if (n === sent.loading) said.textContent = fill(W.failed, { why: e.message });
    return false;
  }
  if (n !== sent.loading) return true;
  const head = h('div', { class: 'card ledger-head' },
    h('p', { class: 'ledger-line sees-heading' }, W.seesHeading),
    h('p', { class: 'ledger-line' }, W.seesKey),
    h('p', { class: 'ledger-line' }, W.seesPlugins),
    h('p', { class: 'ledger-line' }, W.seesUnmarked),
    h('p', { class: 'ledger-line sees-bounds' }, fill(W.seesBounds, { kept: list.kept, maxBytes: bytesSaid(list.max_bytes) })));
  if (!list.kept) head.appendChild(h('p', { class: 'ledger-warn sent-none' }, W.seesNone));
  if (list.over) head.appendChild(h('p', { class: 'ledger-warn sent-over' }, fill(W.seesOver, { maxBytes: bytesSaid(list.max_bytes) })));
  pane.insertBefore(head, said);
  said.textContent = list.kept && !list.requests.length ? W.seesEmpty : '';
  pane.appendChild(h('div', { class: 'ledger-rows' }, list.requests.map(r => sentRow(r, list.range_bytes))));
  return true;
}
