import { S } from '../../state.js';
import { fill } from '../../util.js';
import { send } from '../../ws.js';
import W from '../../strings/status.js';

let confirmed = null, attempt = null;
const REFUSED = { turn_holding: W.passTurn, stall: W.passStall, budget_hold: W.passBudget, safe: W.passSafe,
  in_flight: W.passInFlight, paused: W.passPaused, already_due: W.passDue, changed: W.passChanged, not_recurring: W.passNotRecurring, absent: W.passAbsent };
const redraw = () => { S.renderStatus?.(); S.renderTechnical?.(); };
export function clockOf(when) {
  const t = typeof when === 'number' ? when : Date.parse(when || '');
  return isNaN(t) ? '' : new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}
export function passWords(name) {
  const asked = confirmed ? fill(W.passAsked, { time: clockOf(confirmed.asked_at) }) : '';
  const now = !attempt || attempt.asking ? '' : attempt.lost ? W.passUnconfirmed
    : attempt.error ? attempt.error
    : REFUSED[attempt.reason] ? fill(REFUSED[attempt.reason], { name }) : '';
  return [asked, now].filter(Boolean).join(' ');
}
export const passAsking = () => !!(attempt && attempt.asking);
export function pressPass() {
  const id = send({ type: 'pass_now' });
  if (!id) return;
  attempt = { asking: id };
  redraw();
}
S.passNowAnswered = msg => {
  if (!attempt || !attempt.asking || !msg.request_id || msg.request_id !== attempt.asking || !msg.pass_now) return;
  const r = msg.pass_now;
  if (r.moved) { confirmed = { asked_at: r.asked_at }; attempt = null; }
  else attempt = { reason: r.reason || '' };
  redraw();
};
S.passNowRefused = (requestID, text) => {
  if (!attempt || !attempt.asking || !requestID || requestID !== attempt.asking) return false;
  attempt = { error: text || '' };
  redraw();
  return true;
};
S.passNowLost = () => {
  if (!attempt || !attempt.asking) return;
  attempt = { lost: true };
  redraw();
};
