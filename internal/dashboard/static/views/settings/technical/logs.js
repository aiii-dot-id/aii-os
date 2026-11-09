import { S } from '../../../state.js';
import { h, fill } from '../../../util.js';
import * as button from '../../../components/button/button.js';
import { bytesSaid } from './sent.js';
import W from '../../../strings/settings-technical.js';
import WF from '../../../strings/settings-technical-forms.js';

const asks = { list: '', tail: '' };
const failed = { list: '', tail: '' };
let lost = false;
async function ask(which, extra) {
  failed[which] = '';
  const { query } = await import('../../../ws.js');
  asks[which] = query('logs', extra) || '';
  lost = !asks[which];
  S.renderTechnical?.();
}

export function readLogs() {
  S.logsList = null;
  S.logFile = '';
  S.logTail = null;
  failed.tail = '';
  ask('list');
}

S.logsConnectionLost = () => {
  if (S.logFile && !(S.logTail && S.logTail.name === S.logFile)) lost = true;
  S.renderTechnical?.();
};

S.logsAskFailed = (requestID, text) => {
  if (!requestID) return false;
  if (requestID === asks.list) failed.list = text || 'refused';
  else if (requestID === asks.tail) failed.tail = text || 'refused';
  else return false;
  S.renderTechnical?.();
  return true;
};

let olderOpen = false;
function when(iso) {
  const t = Date.parse(iso || '');
  return isNaN(t) ? '' : new Date(t).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
}

export function drawLogs(pane) {
  let root = pane.querySelector('.technical-logs');
  if (!root) {
    root = h('div', { class: 'technical-logs' },
      h('h3', { class: 'technical-form__group-title' }, WF.logsForm.reader), h('p', { class: 'technical-form__hint' }, WF.logsForm.readerHint),
      h('p', { class: 'ledger-said', role: 'status' }),
      h('div', { class: 'technical-logs__files' }),
      h('div', { class: 'technical-logs__tail' }));
    pane.replaceChildren(root);
  }
  const said = root.querySelector('.ledger-said');
  const files = root.querySelector('.technical-logs__files');
  const tail = root.querySelector('.technical-logs__tail');
  const list = S.logsList;
  const off = !!(S.config && S.config.logs && S.config.logs.dir === '');
  const line = failed.list ? fill(W.logsListFailed, { why: failed.list })
    : list === null || list === undefined ? (S.connected ? W.logsLoading : W.logsOffline)
    : off ? W.logsOff : list.length ? '' : W.logsNone;
  if (said.textContent !== line) said.textContent = line;
  const sig = JSON.stringify([list, S.logFile]);
  if (files.dataset.said !== sig) {
    files.dataset.said = sig;
    const row = (f, words) => {
      const el = h('div', { class: 'technical-logs__file', 'data-logfile': f.name });
      const control = button.render({ label: fill(words, { size: bytesSaid(f.size), modified: when(f.mod_at) }), kind: 'ghost',
        onPress: () => { S.logFile = f.name; S.logTail = null; ask('tail', { name: f.name }); S.renderTechnical?.(); } });
      if (f.name === S.logFile) el.setAttribute('aria-current', 'true');
      el.appendChild(control);
      return el;
    };
    const [current, ...older] = list || [];
    const shelf = older.length ? h('details', { class: 'technical-logs__older' }, h('summary', {}, fill(WF.logsForm.older, { n: older.length })), ...older.map(f => row(f, WF.logsForm.row))) : null;
    if (shelf) {
      shelf.open = olderOpen || older.some(f => f.name === S.logFile);
      shelf.ontoggle = () => { olderOpen = shelf.open; };
    }
    files.replaceChildren(...[current && row(current, WF.logsForm.current), shelf].filter(Boolean));
  }
  const own = S.logTail && S.logTail.name === S.logFile ? S.logTail : null;
  const tailSig = JSON.stringify([S.logFile, own, failed.tail, lost]);
  if (tail.dataset.said === tailSig) return true;
  tail.dataset.said = tailSig;
  if (!S.logFile) { tail.replaceChildren(); return true; }
  const name = S.logFile;
  if (failed.tail) { tail.replaceChildren(h('p', { class: 'ledger-said technical-logs__failed', role: 'status' }, fill(W.logsTailFailed, { name, why: failed.tail }))); return true; }
  if (lost && !own) { tail.replaceChildren(h('p', { class: 'ledger-said technical-logs__lost', role: 'status' }, W.logsOffline)); return true; }
  if (!own) { tail.replaceChildren(h('p', { class: 'ledger-said', role: 'status' }, fill(W.logsTailLoading, { name }))); return true; }
  const lines = own.lines || [];
  tail.replaceChildren(h('h4', { class: 'sent-part' }, fill(W.logsTail, { name })),
    lines.length ? h('pre', { class: 'technical-logs__pre' }, lines.join('\n')) : h('p', { class: 'ledger-note' }, fill(W.logsTailEmpty, { name })));
  return true;
}
