import { S } from '../../state.js';
import { $, fill } from '../../util.js';
import { send } from '../../ws.js';
import { announceWords } from '../../announce.js';
import { pendingKeyed, notTaken } from '../../pending.js';
import * as ask from '../../components/ask/ask.js';
import W from '../../strings/home.js';
import { appendThread, renderJump } from './thread.js';

const askCards = new Map();
const askWaits = pendingKeyed();
const closedAsks = new Map();
const askNotes = new Map();
const askOf = new WeakMap();

function reopenAsk(id, note) {
  const d = askCards.get(id);
  if (!d) return false;
  ask.update(d, { state: 'refused', note });
  return true;
}
S.askRefused = (requestID, text) => {
  const id = askWaits.refused(requestID);
  if (id === null) return false;
  const note = notTaken(text);
  if (reopenAsk(id, note)) return true;
  const closed = closedAsks.get(id);
  if (closed) ask.update(closed, { state: 'answered', note });
  askNotes.set(id, note);
  return true;
};
export function asksConnectionLost() {
  askWaits.lost().forEach(id => { const d = askCards.get(id); if (d) { d.dataset.askUnsure = '1'; reopenAsk(id, W.askLost); } });
}

const timeNow = () => new Date().toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
function draw(a, into) {
  const card = ask.render({ ask: a, name: S.stats?.name && S.stats.name !== 'Unnamed' ? S.stats.name : '', onAnswer: press => answer(card, a, press), into });
  card.classList.add('msg', 'ask');
  card.dataset.askId = a.id;
  askOf.set(card, a);
  return card;
}
function answer(card, a, press) {
  const frame = press.act
    ? { type: 'plugin', plugin: { action: press.act, id: a.plugin, act: a.id } }
    : { type: 'ask', ask: Object.assign({ id: a.id }, press) };
  if (!askWaits.sent(a.id, send(frame))) { ask.update(card, { state: card.dataset.state, note: W.askOffline }); return false; }
  delete card.dataset.askUnsure;
  ask.update(card, { state: 'answering', note: W.askSending });
  if (press.answer === 'connect') {
    S.connectRequest = { connector: a.connector || '', scope: press.scope };
    if (S.go) S.go('plugins');
  }
  return true;
}

export function renderAsks(asks) {
  const inner = $('thread-inner');
  if (!inner) return;
  const seen = new Set();
  (asks || []).forEach(a => {
    seen.add(a.id);
    if (askCards.has(a.id)) return;
    const back = closedAsks.get(a.id);
    closedAsks.delete(a.id);
    const again = !!(back && back.isConnected);
    const typed = again ? ask.typed(back) : '';
    const d = draw(a, again ? back : null);
    ask.type(d, typed);
    if (askNotes.has(a.id)) { ask.update(d, { state: 'refused', note: askNotes.get(a.id) }); askNotes.delete(a.id); }
    askCards.set(a.id, d);
    if (!again) appendThread(d);
    announceWords(d.querySelector('.c-ask__label').textContent, d.querySelector('.c-ask__text').textContent);
  });
  askCards.forEach((d, id) => {
    if (seen.has(id)) return;
    const ours = d.dataset.state === 'answering' && !d.dataset.askUnsure;
    ask.update(d, { state: 'answered', note: fill(W.askAnswered, { when: ours ? timeNow() : '' }).trim() });
    askCards.delete(id);
    closedAsks.set(id, d);
  });
  placeAsks();
  S.interactionPositioned?.();
  renderJump();
}

export function placeAsks(batch) {
  askCards.forEach(card => {
    if (batch) batch.push(card);
    else if (!card.closest('#thread-inner')) appendThread(card);
  });
}
S.placeAsks = () => placeAsks();
