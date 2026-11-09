
import { holdUntilHand } from '../../shell/keep.js';
import { S } from '../../state.js';
import { $, copyText, esc, composing } from '../../util.js';
import { send, wsReady } from '../../ws.js';
import { ownsThread } from '../../registry.js';
import { setThinking } from '../../presence.js';
import { toast } from '../../app.js';
import { announce } from '../../announce.js';
import { pendingKeyed, notTaken, NOT_SENT, LOST } from '../../pending.js';
import { displayInteractions, foldTurns, locationKey } from '../../interaction-display.js';
import { attachmentsForSend, attachmentsSent, drawAttached } from '../attach.js';
import * as outcomeRow from '../../components/outcome-row/outcome-row.js';
import * as osNote from '../../components/os-note/os-note.js';
import W from '../../strings/chat.js';
import { setText, setData, syncChildren } from './dom.js';
import { placeAsks } from './asks.js';
import { showPendingMessage, viewReference, pendingCount } from './sends.js';
import { messageTextState, longMessage, refreshMessageText, detailReader } from './long-text.js';
import { markReceipt, receiptRow } from './receipts.js';
import { disclosure as thinkingDisclosure } from './thinking.js';
import { cited as citedBeliefs, drawWhy } from './why.js';

let thinkingEl = null;

let turnWork = new Map();
export const workOfTurn = t => turnWork.get(t);
export function appendThread(el, before = null) {
  const t = $('thread'), inner = $('thread-inner');
  const follow = t && following;
  if (before?.parentNode === inner) inner.insertBefore(el, before);
  else if (thinkingEl && thinkingEl.isConnected && el !== thinkingEl) inner.insertBefore(el, thinkingEl);
  else inner.appendChild(el);
  if (follow && t) positionThread(t, t.scrollHeight);
  S.interactionPositioned?.();
  renderJump();
}

export function renderSteering(pending) {
  const el = $('steerq');
  if (!el) return;
  if (!pending || !pending.length) { el.className = 'steerq'; el.innerHTML = ''; return; }
  el.className = 'steerq on';
  const title = pending.length + (pending.length === 1 ? ' message waiting' : ' messages waiting') + ' for the next tool call';
  let details = el.querySelector('details');
  if (!details) { details = document.createElement('details'); details.append(document.createElement('summary'), document.createElement('div')); el.appendChild(details); }
  setText(details.firstElementChild, title);
  setText(details.lastElementChild, pending.join('\n'));

}

const COPY_ICON = '<svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true"><rect x="5.5" y="5.5" width="8" height="8" rx="1.8"/><path d="M10.5 3.5v-.3A1.7 1.7 0 0 0 8.8 1.5H4.2a1.7 1.7 0 0 0-1.7 1.7v4.6a1.7 1.7 0 0 0 1.7 1.7h.3"/></svg>';
const COPIED_ICON = '<svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true"><path d="m3.5 8.5 3 3 6-7"/></svg>';
const theirName = () => { const n = (S.stats && S.stats.name) || ''; return n === 'Unnamed' ? '' : n; };
export function copyButton(text) {
  const b = document.createElement('button');
  const label = '<span>' + esc(W.copy) + '</span>';
  b.type = 'button'; b.className = 'msg-chip copyb';
  b.innerHTML = COPY_ICON + label; b.dataset.copyText = text;
  b.onclick = async () => {
    if (!(await copyText(b.dataset.copyText))) { toast('Could not copy \u2014 the browser refused the clipboard.'); return; }
    b.innerHTML = COPIED_ICON + label;
    setTimeout(() => { b.innerHTML = COPY_ICON + label; }, 1200);
  };
  return b;
}

function messageActs(text) {
  const acts = document.createElement('div');
  acts.className = 'msg-acts';
  acts.appendChild(copyButton(text));
  return acts;
}
function messageEl(role, text, whoNote, voiceRef) {
  if (!text) return null;
  const d = document.createElement('div');
  d.className = 'msg ' + role;
  const who = role === 'identity' ? theirName() || W.yourIdentity : role === 'operator' ? W.you : role === 'participant' ? 'participant' : '';
  d.innerHTML = (who ? '<div class="who">' + esc(who) + (whoNote ? ' · <span class="speaker">' + esc(whoNote) + '</span>' : '') + '</div>' : '') +
    '<div class="body">' + esc(text) + '</div>';
  if (role === 'identity' || role === 'operator') d.appendChild(messageActs(text));
  if (voiceRef) d.dataset.voiceRef = voiceRef;
  return d;
}
let liveError = '';
export function sysLine(text) {
  if (!text) return null;
  liveError = text;
  renderNotices();
  return null;
}
function renderNotices() {
  if (document.visibilityState === 'hidden') return;
  const host = chatSurface('chat-notices');
  let el = $('chat-error');
  if (!liveError) { el?.remove(); return; }
  if (!el) {
    el = document.createElement('div'); el.id = 'chat-error'; el.className = 'chat-notice error';
    const body = document.createElement('span'); body.setAttribute('role', 'alert');
    const close = document.createElement('button'); close.type = 'button'; close.textContent = 'Dismiss';
    close.onclick = () => { liveError = ''; renderNotices(); };
    el.append(body, close); host.appendChild(el);
  }
  setText(el.firstElementChild, liveError);
}
export function thinkingEvent() {}
export function toolEventLive() {}
function thinkingMarker() {
  if (!thinkingEl) {
    thinkingEl = document.createElement('div');
    thinkingEl.className = 'msg identity thinking-row';
    thinkingEl.innerHTML = '<div class="thinking"><i></i><i></i><i></i></div>';
  }
  return thinkingEl;
}
export function toggleThinkingDots(on) {
  if (document.visibilityState === 'hidden') return;
  renderNotices();
  if (on) {
    const marker = thinkingMarker();
    if ($('thread-inner').lastElementChild !== marker) appendThread(marker);
  } else if (thinkingEl) { thinkingEl.remove(); thinkingEl = null; }
  if (liveReceipt && liveReceipt.el.isConnected) markReceipt(liveReceipt.el, liveReceipt.item, !!on);
}

let following = true;
function atBottom(t) { return t.scrollHeight - t.scrollTop - t.clientHeight <= 2; }
function positionThread(t, top) {
  const target = Math.max(0, Math.min(top, t.scrollHeight - t.clientHeight));
  if (Math.abs(t.scrollTop - target) > 0.5) t.scrollTop = target;
  S.interactionPositioned?.();
}
export function scrollThread(force) {
  const t = $('thread');
  if (force) { following = true; S.cancelReadingRestore?.(); }
  if (following) positionThread(t, t.scrollHeight);
  renderJump();
}
export function renderJump() {
  const t = $('thread'), j = $('jump-latest');
  if (t && j) j.hidden = following && !historyEarlier;
}
let pendingTextReading = null;
S.cancelReadingRestore = () => { pendingTextReading = null; held?.end(); held = null; };
let held = null;
function keepReadingRow(thread, inner, reading) {
  held?.end();
  const place = () => {
    const anchor = Array.from(inner.querySelectorAll('[data-interaction-key]')).find(e => e.dataset.interactionKey === reading.key);
    if (anchor) positionThread(thread, thread.scrollTop + anchor.getBoundingClientRect().top - thread.getBoundingClientRect().top - reading.offset);
  };
  held = holdUntilHand(inner, place);
  held.watch(inner);
}
export function holdReading() { following = false; S.interactionHold?.(); renderJump(); }
export function messageChange(change) {
  const thread = $('thread'), inner = $('thread-inner');
  const anchor = readingAnchor(inner, thread.getBoundingClientRect().top), offset = anchor?.getBoundingClientRect().top;
  change();
  if (pendingTextReading) restoreReading(pendingTextReading, thread, inner);
  else if (following) positionThread(thread, thread.scrollHeight);
  else if (anchor?.isConnected) positionThread(thread, thread.scrollTop + anchor.getBoundingClientRect().top - offset);
  renderJump();
}
{
  const t = $('thread'), j = $('jump-latest');
  let lastTop = t?.scrollTop || 0, directionFromInput = 0, dragging = false, touchY = null;
  const move = direction => {
    if (!direction || !t || document.visibilityState === 'hidden' || S.view && S.view !== 'chat') return;
    S.cancelReadingRestore?.(); S.readingGivenUp?.();
    if (direction < 0) { following = false; S.interactionHold?.(); }
    else if (atBottom(t)) following = true;
    renderJump();
    const viewport = t.getBoundingClientRect(), older = direction < 0;
    let top;
    for (const source of ['recorded','transient']) {
      const page = currentPages.get(source);
      if (!(older ? page?.has_older : page?.has_newer) || !page.rows.length) continue;
      const row = older ? page.rows[0] : page.rows.at(-1);
      const key = CSS.escape(locationKey(source, page, row.id));
      const record = t.querySelector('[data-record-key="' + key + '"]')?.closest('.interaction-item');
      const edge = record?.closest('.turn-receipt') || record;
      if (!edge) continue;
      const rect = edge.getBoundingClientRect();
      if (older ? rect.top >= viewport.top - 140 && rect.top <= viewport.top + 140 :
          rect.bottom <= viewport.bottom + 140 && rect.bottom > viewport.top) {
        currentNavigate?.(source, older ? 'older' : 'newer'); top = null; break;
      }
      if (older && !(top?.y <= rect.top)) top = { source, y: rect.top };
    }
    if (top && t.scrollTop <= 140) currentNavigate?.(top.source, 'older');
    S.interactionScroll?.();
  };
  if (t) {
    t.addEventListener('wheel', e => { directionFromInput = Math.sign(e.deltaY); move(directionFromInput); }, { passive: true });
    t.addEventListener('touchstart', e => { directionFromInput = 0; touchY = e.touches[0]?.clientY; }, { passive: true });
    t.addEventListener('touchmove', e => {
      const y = e.touches[0]?.clientY;
      if (touchY != null && y != null) { directionFromInput = Math.sign(touchY-y); move(directionFromInput); } touchY = y;
    }, { passive: true });
    t.addEventListener('pointerdown', () => { dragging = true; }, { passive: true });
    t.addEventListener('click', e => {
      const summary = e.target.closest('summary');
      if (summary && !summary.parentElement.open) { following = false; S.interactionHold?.(); renderJump(); }
    });
    for (const event of ['pointerup', 'pointercancel']) window.addEventListener(event, () => { dragging = false; }, { passive: true });
    t.addEventListener('scrollend', () => { directionFromInput = 0; }, { passive: true });
    t.addEventListener('keydown', e => {
      const space = e.key === ' ' && !e.target.closest('button,summary');
      if (!e.target.closest('input,textarea,select,[contenteditable]') && (space || ['ArrowUp','PageUp','Home','ArrowDown','PageDown','End'].includes(e.key))) {
        directionFromInput = space ? (e.shiftKey ? -1 : 1) : ['ArrowUp','PageUp','Home'].includes(e.key) ? -1 : 1; move(directionFromInput);
      }
    });
    t.addEventListener('scroll', () => {
      const direction = Math.sign(t.scrollTop-lastTop); lastTop = t.scrollTop; renderJump();
      if (dragging || direction && direction === directionFromInput) move(direction);
    }, { passive: true });
    S.interactionPositioned = () => { lastTop = t.scrollTop; };
  }
  if (j) j.onclick = () => {
    S.cancelReadingRestore?.();
    if (S.interactionLatest) S.interactionLatest();
    else {
      following = true;
      const still = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
      t.scrollTo({ top: t.scrollHeight, behavior: still ? 'auto' : 'smooth' });
    }
  };
}
S.interactionFollowing = () => following;
S.interactionVisible = source => {
  const t = $('thread'), page = currentPages.get(source);
  if (!t || !page?.rows.length) return null;
  const box = t.getBoundingClientRect(), ids = new Set();
  for (const item of t.querySelectorAll('.interaction-item')) {
    const rect = item.getBoundingClientRect(); if (rect.bottom <= box.top || rect.top >= box.bottom) continue;
    for (const record of item.querySelectorAll('[data-record-key]')) {
      const [identity, origin, incarnation, id] = JSON.parse(record.dataset.recordKey);
      if (origin === source && identity === page.identity && incarnation === page.incarnation) ids.add(id);
    }
  }
  const rows = page.rows.filter(r => ids.has(r.id));
  return rows.length ? { anchor_id: rows[0].id, end_id: rows.at(-1).id } : null;
};
export function sendChat(text) {
  text = (text || '').trim();
  if (!text) { attachmentsForSend(text); return false; }
  if (!wsReady()) { toast('not connected — reconnecting'); return false; }
  if (pendingCount() >= 20) { toast('Resolve or dismiss unconfirmed messages before sending more.'); return false; }
  const attachments = attachmentsForSend(text);
  if (!attachments) return false;
  S.voiceSpeak = false;
  const request = send(attachments.length ? { type: 'chat', message: text, attachments } : { type: 'chat', message: text });
  if (!request) return false;
  attachmentsSent();
  showPendingMessage(request, text);
  operatorTurnBegins();
  setThinking(true);
  return true;
}
export function operatorTurnBegins() { liveReceipt = null; S.draftTurnBegins?.(); }

const turnFailures = new Map(), turnReaders = new Map();
export const isLocation = ref => !!ref && typeof ref === 'object' && ['identity','incarnation','id'].every(k => typeof ref[k] === 'string' && ref[k].length) && ['recorded','transient'].includes(ref.source);
S.turnFailed = (text, ref) => {
  if (!isLocation(ref)) { announce(text); return; }
  turnFailures.set(locationKey(ref.source, ref, ref.id), { ref, text });
  const inner = $('thread-inner');
  if (inner) placeTurnFailures(inner);
};
function forgetUnreachableFailures(pages) {
  for (const source of ['recorded','transient']) {
    const page = pages.get(source);
    if (!page?.incarnation) continue;
    const was = turnReaders.get(source);
    turnReaders.set(source, { identity: page.identity || '', incarnation: page.incarnation });
    if (!was || was.identity === (page.identity || '') && was.incarnation === page.incarnation) continue;
    for (const [key, { ref }] of turnFailures) {
      if (ref.source === source && ref.identity === was.identity && ref.incarnation === was.incarnation) turnFailures.delete(key);
    }
  }
}
function placeTurnFailures(inner) {
  for (const [key, { text }] of turnFailures) {
    const row = inner.querySelector(':scope > [data-interaction-key="' + CSS.escape(key) + '"]'), msg = row && row.querySelector(':scope > .msg.operator');
    if (!msg) continue;
    const had = msg.querySelector(':scope > .turn-failure');
    if (had && had.dataset.text === text) continue;
    had?.remove();
    const note = osNote.render({ tone: 'bad', text });
    note.classList.add('turn-failure');
    note.dataset.text = text;
    msg.appendChild(note);
  }
}
S.operatorTurnBegins = operatorTurnBegins;
export { composing };
export { renderComposer } from './reply.js';

export function renderChatSubstrate() {}
export function substrateConnectionLost() { return false; }
export function acceptSubstrateConfig() { return false; }
export function rejectSubstrateConfig() { return false; }

export { renderAsks } from './asks.js';

export function chatSurface(id) {
  let el = $(id);
  if (!el) {
    el = document.createElement('div'); el.id = id;
    const composer = $('composer-wrap');
    if (composer) composer.prepend(el); else $('thread').after(el);
  }
  return el;
}
export let currentPages = new Map(), currentNavigate = null;
let historyEarlier = false, beliefsCited = new Map();
function speakerLabel(p) {
  if (!p) return '';
  if (p.reason === 'speaker_profile_pending') return 'speaker identification pending';
  if (p.speaker_uuid) return p.display_label ? JSON.stringify(p.display_label) : 'anonymous speaker';
  if (p.decision === 'known') return p.speaker?.trim() || 'unknown speaker';
  if (p.decision === 'uncertain') {
    if (p.reason === 'enrollment_unavailable') return 'speaker enrollment unavailable';
    if (p.reason === 'no_enrollments') return 'no speaker is enrolled';
    const label = p.speaker || p.reason?.replaceAll('_', ' ') || '';
    return 'uncertain' + (label ? ': ' + label : '');
  }
  return 'unknown speaker';
}
function occurrence(el, entry) {
  const r = entry.row;
  if (!el) {
    el = document.createElement('div'); el.className = 'record-detail'; el.dataset.recordKey = entry.key;
    const body = document.createElement('pre'); body.className = 'record-content';
    const meta = document.createElement('div'); meta.className = 'interaction-meta';
    const context = document.createElement('details'); context.className = 'record-context';
    const title = document.createElement('summary'); title.textContent = 'Recorded context';
    const raw = document.createElement('pre'); context.append(title, raw);
    el.append(body, meta, context);
  }
  setText(el.querySelector('.record-content'), r.content || '');
  setText(el.querySelector('.interaction-meta'), [r.kind, r.recorded_at || r.created_at || 'Time unavailable', entry.source === 'transient' ? 'not saved' : ''].filter(Boolean).join(' · '));
  setText(el.querySelector('.record-context pre'), JSON.stringify(r, null, 2));
  const signature = JSON.stringify(r.content_ref || null);
  if (el.dataset.detailRef !== signature) {
    el.querySelector('.detail-read')?.remove(); el.dataset.detailRef = signature;
    if (r.content_ref) el.appendChild(detailReader(r.content_ref));
  }
  return el;
}
function recordKey(el, entry) {
  if (!el) { el = document.createElement('i'); el.dataset.recordKey = entry.key; }
  return el;
}
const cutLabel = text => { const line = String(text).split('\n')[0]; return line.length > 100 || line.length < String(text).length ? line.slice(0, 100) + ' …' : line; };
export function presentationRow(el, item) {
  const r = item.anchor.row, compact = item.tool || r.kind === 'notice' || r.kind === 'annotation' || r.kind === 'legacy' && r.role === 'system';
  if (!el) {
    el = document.createElement('div'); el.className = 'interaction-item'; el.dataset.interactionKey = item.key;
    if (compact) {
      const disclosure = document.createElement('details'); disclosure.className = 'interaction-disclosure tool-ev';
      const summary = document.createElement('summary');
      const label = document.createElement('span'); label.className = 'tname';
      const status = document.createElement('span'); status.className = 'tool-outcome';
      summary.append(label, status);
      const records = document.createElement('div'); records.className = 'interaction-records';
      disclosure.append(summary, records); el.appendChild(disclosure);
    } else {
      const msg = messageEl(r.role === 'resident' ? 'identity' : r.role, r.content || ' ');
      msg.classList.add('record-message');
      const thinking = thinkingDisclosure(el); thinking.hidden = true;
      msg.querySelector('.body').after(thinking);
      const records = document.createElement('div'); records.className = 'interaction-records'; records.hidden = true;
      el.append(msg, records);
    }
    const qualification = document.createElement('div'); qualification.className = 'record-qualification'; el.appendChild(qualification);
  }
  setData(el, 'turnId', r.id);
  setData(el, 'payload', JSON.stringify(r));
  setData(el, 'records', JSON.stringify(item.records.map(e => e.row)));
  const voice = r.annotations?.voice;
  if (voice?.session && voice?.sequence) setData(el, 'voiceRef', voice.session + '/' + voice.sequence);
  else if (el.dataset.voiceRef) delete el.dataset.voiceRef;
  if (compact) {
    const attached = !item.tool && r.kind === 'notice' && r.details?.reason_code === 'attached' && r.content;
    el.classList.toggle('attached-file', !!attached);
    if (attached) drawAttached(el.querySelector('.tname'), r.content);
    else if (!item.tool && r.kind === 'notice' && r.details?.reason_code === 'shown' && r.content) setText(el.querySelector('.tname'), 'shown to the model: ' + r.content);
    else if (!item.tool && r.kind === 'notice' && r.details?.reason_code === 'not_shown' && r.content) setText(el.querySelector('.tname'), 'not shown to the model: ' + r.content);
    else setText(el.querySelector('.tname'), item.tool ? r.details?.tool || 'Tool' : cutLabel(r.content || (r.kind === 'annotation' ? 'Recorded annotation' : 'Recorded notice')));
    const labels = { succeeded: 'Done', failed: 'Failed', cancelled: 'Cancelled', refused: 'Refused', unknown: 'Outcome unknown', conflict: 'Conflicting records' };
    setText(el.querySelector('.tool-outcome'), item.tool ? labels[item.outcome] || (item.outcome ? 'Outcome unknown' : 'Requested') : r.outcome || '');
    el.classList.toggle('record-failed', ['failed','refused','unknown','conflict'].includes(item.outcome || r.outcome));
  } else {
    const recordedReply = r.role === 'resident' && !(item.transient || item.source === 'transient') && /^[1-9]\d*$/.test(r.sequence || '');
    if (recordedReply) setData(el, 'thinkingTurn', r.sequence); else if (el.dataset.thinkingTurn) delete el.dataset.thinkingTurn;
    const thinking = el.querySelector('.record-thinking');
    if (thinking && thinking.hidden !== !recordedReply) thinking.hidden = !recordedReply;
    const msg = el.querySelector('.record-message');
    const was = { operator: 'participant', participant: 'operator' }[r.role];
    if (msg.classList.contains(was)) {
      msg.classList.replace(was, r.role);
      msg.querySelector('.msg-acts')?.remove();
      if (r.role === 'operator') msg.append(messageActs(r.content || ''));
    }
    longMessage(msg, r, item.key);
    if (r.role === 'resident') drawWhy(msg, beliefsCited.get(r.turn_id));
    const who = msg.querySelector('.who'), label = speakerLabel(r.annotations?.speaker);
    const role = r.role === 'resident' ? theirName() || W.yourIdentity : r.role === 'operator' ? W.you : r.role === 'participant' ? 'participant' : '';
    setText(who, role + (label ? ' · ' + label : ''));
    const said = (r.deliveries || []).map(outcomeRow.deliveryNote).filter(Boolean);
    let sent = el.querySelector('.delivery-notes');
    if (said.length && !sent) { sent = document.createElement('div'); sent.className = 'delivery-notes'; el.querySelector('.record-qualification').before(sent); }
    if (sent) {
      const sig = JSON.stringify(said);
      if (sent.dataset.signature !== sig) { sent.dataset.signature = sig; sent.replaceChildren(...said.map(n => osNote.render(Object.assign({ live: false }, n)))); }
    }
  }
  const notes = [];
  if (item.conflict) notes.push('Conflicting records — inspect details');
  if (item.transient || item.source === 'transient') notes.push(item.tool ? 'Completion/activity not saved' : 'Not saved');
  if (item.records.some(e => e.row.details?.recording_error)) notes.push('Recording failed');
  setText(el.querySelector('.record-qualification'), notes.join(' · '));
  const records = el.querySelector('.interaction-records');
  const old = new Map(Array.from(records.children).map(e => [e.dataset.recordKey, e]));
  const nodes = item.records.map(entry => compact ? occurrence(old.get(entry.key), entry) : recordKey(old.get(entry.key), entry));
  let missing = el.querySelector('.missing-start');
  if (item.missingStart) {
    if (!missing) { missing = document.createElement('p'); missing.className = 'missing-start';  }
    setText(missing, 'The start of this execution is outside this view or unavailable.'); nodes.push(missing);
  }
  syncChildren(records, nodes);
  return el;
}
function sourceControls(el, source, page, navigate, busy) {
  if (!el) {
    el = document.createElement('div'); el.className = 'interaction-controls'; el.dataset.source = source;
    el.appendChild(document.createElement('span'));
    const retry = document.createElement('button'); retry.type = 'button'; retry.textContent = 'Retry'; el.appendChild(retry);
  }
  const state = !page ? 'Loading conversation…' : page.error ? 'History unavailable: ' + page.error : page.stale ? 'Last known view — refreshing when connected' : page.reset ? 'History source changed — view refreshed' : '';
  const label = source === 'transient' ? 'Not saved — current session' : '';
  setText(el.firstElementChild, [label, state, page?.lost ? page.lost + ' earlier entries no longer retained' : ''].filter(Boolean).join(' · '));
  const retry = el.querySelector('button');
  if (retry.hidden !== !page?.error) retry.hidden = !page?.error;
  if (retry.disabled !== busy) retry.disabled = busy;
  retry.onclick = () => navigate?.(source, 'latest');
  const hidden = !el.firstElementChild.textContent && retry.hidden;
  if (el.hidden !== hidden) el.hidden = hidden;
  return el;
}
let liveReceipt = null;
export function renderInteractionPages(pages, navigate, busy, position = '', reading = null) {
  currentPages = pages; currentNavigate = navigate; beliefsCited = citedBeliefs(pages);
  forgetUnreachableFailures(pages);
  if (document.visibilityState === 'hidden') return;
  const thread = $('thread'), inner = $('thread-inner'); if (!thread || !inner) return;
  renderNotices();
  historyEarlier = Array.from(pages.values()).some(p => p.has_newer);
  const top = thread.scrollTop;
  const boundary = thread.getBoundingClientRect().top;
  const anchor = readingAnchor(inner, boundary);
  const offset = anchor?.getBoundingClientRect().top;
  const anchorText = anchor && messageTextState.get(anchor.querySelector('.record-message'));
  const focused = document.activeElement;
  const old = new Map(Array.from(inner.querySelectorAll('.interaction-item[data-interaction-key]')).map(el => [el.dataset.interactionKey, el]));
  const projected = displayInteractions(pages), batch = [], claimed = new Set();
  const runningSource = S.cont?.mode === 'safe' || S.cont?.mode === 'degraded_ring5' ? 'transient' : 'recorded';
  liveReceipt = null;
  const firsts = new Map(), work = new Map();
  for (const source of ['recorded','transient']) {
    const page = pages.get(source), items = projected.get(source);
    if (source === 'transient' && (!page || !page.error && !page.lost && !items.length)) continue;
    const controls = Array.from(inner.children).find(el => el.classList.contains('interaction-controls') && el.dataset.source === source);
    batch.push(sourceControls(controls, source, page, navigate, busy));
    const folded = foldTurns(items, page), newest = folded.at(-1);
    for (const item of folded) {
      let el;
      if (!item.receipt) el = presentationRow(old.get(item.key), item);
      else {
        const canRun = source === runningSource && item === newest && !page.has_newer;
        el = receiptRow(item, old, claimed, canRun && !!S.thinking);
        if (canRun) liveReceipt = { el, item };
        for (const t of item.turns) work.set(t, el);
      }
      if (!firsts.has(source)) firsts.set(source, el);
      batch.push(el);
    }
  }
  placeAsks(batch);
  turnWork = work;
  S.draftRows?.(pages, batch);
  if (S.thinking) batch.push(thinkingMarker()); else thinkingEl = null;
  syncChildren(inner, batch);
  placeTurnFailures(inner);
  if (focused && inner.contains(focused) && document.activeElement !== focused) focused.focus({ preventScroll: true });
  const changedText = anchor?.isConnected && messageTextState.get(anchor.querySelector('.record-message'));
  if (!reading && !pendingTextReading && !following && anchorText?.loaded && changedText && changedText !== anchorText && !changedText.loaded) {
    pendingTextReading = { key: anchor.dataset.interactionKey, offset: offset - boundary, following: false, open: S.interactionBookmark()?.open || [] };
  }
  if (reading || pendingTextReading) restoreReading(reading || pendingTextReading, thread, inner);
  else if (position === 'latest' || position !== 'hold' && following) {
    if (position === 'latest') following = true;
    positionThread(thread, thread.scrollHeight);
  } else if (anchor?.isConnected) positionThread(thread, thread.scrollTop + anchor.getBoundingClientRect().top - offset);
  else positionThread(thread, top);
  S.interactionPositioned?.();
  renderJump();
  continueOlder(pages, firsts, position);
  refreshMessageText();
}

const shownFirst = new Map();
function continueOlder(pages, firsts, position) {
  for (const source of ['recorded', 'transient']) {
    const page = pages.get(source), first = firsts.get(source) || null, row = page?.rows?.[0];
    const before = shownFirst.get(source);
    shownFirst.set(source, first && row ? { el: first, sequence: row.sequence } : null);
    if (position !== 'hold' || !before || !first || !row || first !== before.el) continue;
    const work = $('thread-inner').querySelector('[data-record-key="' + CSS.escape(locationKey(source, page, row.id)) + '"]')?.closest('.turn-receipt');
    if (!page.has_older || page.stale || page.error || !work || work.querySelector('.receipt').open) continue;
    let deeper = false;
    try { deeper = BigInt(row.sequence) < BigInt(before.sequence); } catch (err) { deeper = false; }
    if (deeper) queueMicrotask(() => {
      const thread = $('thread');
      if (currentPages.get(source) !== page || !work.isConnected || work.querySelector('.receipt').open ||
          document.visibilityState === 'hidden' || (S.view && S.view !== 'chat') ||
          work.getBoundingClientRect().bottom <= thread.getBoundingClientRect().top) return;
      currentNavigate?.(source, 'older', true);
    });
  }
}

function readingAnchor(inner, boundary) {
  let anchor = Array.from(inner.children).find(el => el.dataset.interactionKey && !el.hidden && el.getBoundingClientRect().bottom > boundary);
  if (anchor?.classList.contains('turn-receipt') && anchor.querySelector('.receipt').open) {
    anchor = Array.from(anchor.querySelectorAll('.receipt-steps > .interaction-item')).find(el => el.getBoundingClientRect().bottom > boundary) || anchor;
  }
  return anchor;
}
S.interactionBookmark = () => {
  if (pendingTextReading) return pendingTextReading;
  const thread = $('thread'), inner = $('thread-inner'); if (!thread || !inner) return null;
  const boundary = thread.getBoundingClientRect().top, anchor = readingAnchor(inner, boundary);
  if (!anchor) return null;
  const open = [];
  inner.querySelectorAll('.interaction-item[data-interaction-key]').forEach(el => {
    const indices = Array.from(el.querySelectorAll('details')).filter(d => d.closest('.interaction-item') === el).flatMap((d, i) => d.open ? [i] : []);
    if (indices.length) open.push({ key: el.dataset.interactionKey, indices });
  });
  return { key: anchor.dataset.interactionKey, offset: anchor.getBoundingClientRect().top - boundary, following: S.interactionFollowing(), open };
};
function restoreReading(reading, thread, inner) {
  following = !!reading.following;
  for (const entry of reading.open || []) {
    const el = Array.from(inner.querySelectorAll('[data-interaction-key]')).find(e => e.dataset.interactionKey === entry.key);
    if (el) Array.from(el.querySelectorAll('details')).filter(d => d.closest('.interaction-item') === el).forEach((d, i) => { d.open = entry.indices.includes(i); });
  }
  const anchor = Array.from(inner.querySelectorAll('[data-interaction-key]')).find(e => e.dataset.interactionKey === reading.key);
  const text = anchor && messageTextState.get(anchor.querySelector('.record-message'));
  if (!following && text && !text.loaded) {
    pendingTextReading = reading;
    positionThread(thread, thread.scrollTop + anchor.getBoundingClientRect().top - thread.getBoundingClientRect().top);
  } else {
    pendingTextReading = null;
    if (following) positionThread(thread, thread.scrollHeight);
    else if (anchor) keepReadingRow(thread, inner, reading);
  }
}
