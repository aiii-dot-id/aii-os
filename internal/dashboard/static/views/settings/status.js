import { S } from '../../state.js';
import { $, h, fill } from '../../util.js';
import { query } from '../../ws.js';
import * as button from '../../components/button/button.js';
import { pageNotes } from '../../shell/page-note.js';
import { clockOf, passAsking, passWords, pressPass } from './pass-now.js';
import W from '../../strings/status.js';
import C from '../../strings/components.js';

const BLOCKS = [['trust', W.trust], ['continuity', W.continuity], ['tools', W.tools], ['modelCost', W.modelCost], ['flow', W.flow]];

const num = n => Number(n || 0).toLocaleString();
const upper = s => s.charAt(0).toUpperCase() + s.slice(1);
const relative = new Intl.RelativeTimeFormat([], { numeric: 'auto' });
function ago(iso) {
  const t = Date.parse(iso || '');
  if (isNaN(t)) return '';
  const s = Math.round((t - Date.now()) / 1000);
  for (const [unit, size] of [['day', 86400], ['hour', 3600], ['minute', 60]]) {
    if (Math.abs(s) >= size) return relative.format(Math.round(s / size), unit);
  }
  return relative.format(0, 'second');
}
const many = (n, one, other, slots) => fill(Number(n) === 1 ? one : other, Object.assign({ n: num(n) }, slots));
function runningFor(uptime) {
  const part = unit => Number((String(uptime || '').match(new RegExp('(\\d+)' + unit + '(?![a-z])')) || [0, 0])[1]);
  const seconds = part('h') * 3600 + part('m') * 60 + part('s');
  if (!uptime || !seconds && !/0s$/.test(uptime)) return '';
  const [unit, size] = seconds >= 172800 ? ['day', 86400] : seconds >= 3600 ? ['hour', 3600] : seconds >= 60 ? ['minute', 60] : ['second', 1];
  return new Intl.NumberFormat([], { style: 'unit', unit, unitDisplay: 'long' }).format(Math.floor(seconds / size));
}
function hostOf(url) {
  try { return new URL(url).host; } catch (err) { return url || ''; }
}

function heading() {
  if (!S.connected) return { text: pageNotes()[0].text, tone: 'warn', more: [], read: false };
  const read = S.pageFrames || {};
  if (!read.status || !read.continuity) return { text: C.loading, tone: '', more: [], read: false };
  const notes = pageNotes();
  if (!notes.length) return { text: W.allWell, tone: 'good', more: [], read: true };
  return { text: notes[0].text, tone: notes[0].tone, more: notes.slice(1).map(n => n.text), read: true };
}

let spendAsked = '', accountedSeen = null, spendKept = null;
function monthTokens() {
  if (S.spend && spendAsked && S.spendRequest === spendAsked) spendKept = S.spend;
  const sp = spendKept;
  if (!sp) return null;
  let month;
  try { month = new Intl.DateTimeFormat('en-CA', { timeZone: sp.zone, year: 'numeric', month: '2-digit' }).format(new Date()); }
  catch (err) { month = new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit' }).format(new Date()); }
  const these = (sp.days || []).filter(d => d.day.startsWith(month));
  return { tokens: these.reduce((sum, d) => sum + (d.tokens || 0), 0), floor: these.some(d => d.floor || d.unknown) };
}

function blocks() {
  const st = S.stats || {}, c = S.cont || {}, l = (S.config && S.config.llm) || {};
  const name = st.name && st.name !== 'Unnamed' ? st.name : upper(W.unnamed);
  const model = l.resolved_model || l.model || '', provider = l.resolved_provider || l.provider || '';
  const p = (S.providers || []).find(x => x.name === provider);
  const window = (p && p.context_length) || 0;
  const live = (S.work && S.work.live) || [];
  const out = {};

  let trust = '';
  if (model && provider) {
    const slots = { name, model, provider, reason: c.safe_reason || '' };
    trust = fill(c.mode === 'safe' ? W.trustSafe : c.mode === 'degraded_ring5' ? W.trustWaiting : W.trustLine, slots);
  }
  out.trust = { line: trust, rows: [
    st.version && fill(W.version, { version: st.version + (st.build ? ' · ' + st.build : '') }),
    st.credential_warning && fill(W.credentialWarning, { what: st.credential_warning }),
  ] };

  const work = (S.activeProject && (S.activeProject.name || S.activeProject.id)) || (live[0] && (live[0].description || live[0].id)) || '';
  const entries = num(c.ledger_seq ?? st.ledger_seq);
  const record = c.witnessed_at ? fill(W.continuityVerified, { entries, ago: ago(c.witnessed_at) }) : fill(W.continuityEntries, { entries });
  const pulse = S.config && S.config.cognition && S.config.cognition.pulse_seconds;
  const witness = hostOf(c.witness_url);
  const verification = !c.witness_url ? '' : !c.witnessed_at ? W.notYetVerified
    : c.anchored_seq ? fill(W.verifiedThrough, { seq: num(c.anchored_seq), when: ago(c.witnessed_at), witness })
    : fill(W.verified, { ago: ago(c.witnessed_at), witness });
  out.continuity = { line: [work && fill(W.continuityWork, { work }), S.stats || S.cont ? record : ''].filter(Boolean).join(' '), rows: [
    fill(W.entries, { n: entries }),
    verification,
    pulse && st.lifetime_ticks != null && (hours => many(hours, W.lifetimeOne, W.lifetime, { hours: num(hours) }))(Math.round(st.lifetime_ticks * pulse / 3600)),
    S.stats && many(st.belief_count, W.beliefOne, W.beliefs),
    S.stats && many(st.experience_count, W.experienceOne, W.experiences),
    S.stats && many(st.intention_count, W.intentionOne, W.intentions),
    S.stats && many(st.reflection_count, W.reflectionOne, W.reflections),
  ] };

  const tools = S.tools || [];
  const plugins = (S.config && S.config.plugins && S.config.plugins.installed) || [];
  const updates = st.plugin_updates || 0;
  const odd = (st.malformed_calls || 0) + (st.suspicious_paths || 0) + (st.duplicate_arg_keys || 0);
  const off = tools.filter(t => !t.enabled).length;
  out.tools = { line: tools.length ? [fill(plugins.length === 1 ? W.toolsCountOnePlugin : W.toolsCount, { tools: num(tools.length), plugins: num(plugins.length) }),
    off > 0 && fill(W.toolsOff, { off: num(off) }),
    updates === 1 ? W.toolsUpdate : updates > 1 ? fill(W.toolsUpdates, { n: num(updates) }) : ''].filter(Boolean).join(' ') : '',
  rows: [odd > 0 && many(odd, W.oddCallOne, W.oddCalls),
    ...(S.overlays || []).map(e => ({ code: e.path, text: e.outcome }))] };

  const month = monthTokens();
  out.modelCost = { line: month ? fill(month.floor ? W.modelCostTokensFloor : W.modelCostTokens, { tokens: num(month.tokens) }) : '',
    rows: [window && fill(W.windowTokens, { n: num(window) }), st.last_turn && fill(W.lastTurn, { when: st.last_turn })] };

  const asks = S.asks || [];
  const state = S.thinking ? W.stateAnswering : live.length ? fill(W.stateWorking, { work: live[0].description || live[0].id }) : W.stateWaiting;
  const waiting = asks.length === 0 ? W.flowAsksNone : asks.length === 1 ? W.flowAsksOne : fill(W.flowAsks, { n: num(asks.length) });
  out.flow = { line: S.stats ? fill(W.flowLineNow, { name, state, asks: upper(waiting) }) : '', press: { asking: passAsking(), said: passWords(name) }, rows: [
    runningFor(st.uptime) && fill(W.uptime, { duration: runningFor(st.uptime) }),
    st.foreground_holds > 0 && many(st.foreground_holds, W.foregroundHoldOne, W.foregroundHolds),
    ...asks.map(a => a.text || ''),
  ] };

  for (const b of Object.values(out)) b.rows = b.rows.filter(Boolean);
  return out;
}

let drawn = null, said = '';
function skeleton(host) {
  const title = h('h1', { class: 'status__heading', id: 'status-heading' });
  const lede = h('p', { class: 'status__lede' }, W.readAt);
  const more = h('div', { class: 'status__more' });
  const parts = {};
  const grid = h('div', { class: 'status__blocks' }, BLOCKS.map(([id, label]) => {
    const line = h('p', { class: 'status__line' });
    const rows = h('ul', { class: 'status__rows' });
    const press = id === 'flow' ? pressPart() : null;
    const details = h('details', { class: 'status__details' }, h('summary', null, W.details), rows, press && press.el);
    parts[id] = { line, rows, details, press };
    return h('section', { class: 'status__block', 'data-block': id, 'aria-labelledby': 'status-' + id },
      h('h2', { class: 'status__title', id: 'status-' + id }, label), line, details);
  }));
  const root = h('div', { class: 'status', id: 'status' }, title, lede, more, grid);
  host.replaceChildren(root);
  return { root, title, lede, more, grid, parts };
}

function pressPart() {
  const control = button.render({ label: W.flowRunSooner, onPress: pressPass });
  control.setAttribute('aria-describedby', 'status-pass-hint');
  const answer = h('p', { class: 'status__answer', 'aria-live': 'polite' });
  return { control, answer, el: h('div', { class: 'status__press' }, control,
    h('p', { class: 'status__hint', id: 'status-pass-hint' }, W.flowRunSoonerHint), answer) };
}

function setText(el, text) { if (el.textContent !== text) el.textContent = text; }

function glance(head) {
  const entry = document.querySelector('#place-tabs [data-item="status"]');
  if (!entry) return;
  let said = entry.querySelector('.status__glance');
  if (!head.read && S.connected) { said?.remove(); return; }
  if (!said) said = entry.appendChild(h('span', { class: 'status__glance' }));
  setText(said, head.text);
  if (head.tone) said.dataset.tone = head.tone; else delete said.dataset.tone;
}

export function renderStatus() {
  const host = $('status-stack');
  if (!host) return;
  readSpendAgainOnAccounting();
  const head = heading();
  glance(head);
  const now = { head, blocks: head.read ? blocks() : null };
  const sig = JSON.stringify(now);
  if (drawn && host.contains(drawn.root) && sig === said) return;
  said = sig;
  if (!drawn || !host.contains(drawn.root)) drawn = skeleton(host);
  setText(drawn.title, head.text);
  if (head.tone) drawn.title.dataset.tone = head.tone; else delete drawn.title.dataset.tone;
  drawn.lede.hidden = !head.read;
  drawn.grid.hidden = !head.read;
  const moreSig = JSON.stringify(head.more);
  if (drawn.more.dataset.said !== moreSig) {
    drawn.more.dataset.said = moreSig;
    drawn.more.replaceChildren(...head.more.map(t => h('p', { class: 'status__note' }, t)));
  }
  if (!now.blocks) return;
  for (const [id] of BLOCKS) {
    const b = now.blocks[id], part = drawn.parts[id];
    setText(part.line, b.line);
    part.line.hidden = !b.line;
    part.details.hidden = !b.rows.length && !part.press;
    if (part.press) {
      button.update(part.press.control, { busy: b.press.asking, disabled: !S.connected });
      setText(part.press.answer, b.press.said);
    }
    const rowsSig = JSON.stringify(b.rows);
    if (part.rows.dataset.said === rowsSig) continue;
    part.rows.dataset.said = rowsSig;
    part.rows.replaceChildren(...b.rows.map(r => typeof r === 'string' ? h('li', null, r)
      : h('li', null, h('code', null, r.code), ' ', r.text)));
  }
}

export function readStatus(read = query) {
  read('status'); read('continuity'); read('config'); read('providers'); read('tools'); read('work'); read('asks');
  accountedSeen = S.stats ? S.stats.accounted || 0 : null;
  spendAsked = read('spend', { days: 32 }) || '';
}

function readSpendAgain() {
  if (S.view === 'status' && accountedSeen !== null) spendAsked = query('spend', { days: 32 }) || spendAsked;
}
function readSpendAgainOnAccounting() {
  const now = S.stats ? S.stats.accounted || 0 : null;
  if (now === null || now === accountedSeen) return;
  const known = accountedSeen !== null;
  accountedSeen = now;
  if (known) readSpendAgain();
}
S.turnEnded = readSpendAgain;

S.renderStatus = renderStatus;
